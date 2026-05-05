// Package downloader fetches Google Takeout archives via a chromedp-driven
// chromium browser (cookie-only HTTP fails because Google's download URLs
// require a fresh re-authentication / "rapt" token that only a real
// browser can negotiate). See docs/superpowers/specs/2026-05-05-chromedp-downloader-design.md.
package downloader

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/chromedp"
	"github.com/thinkjk/gxodus/internal/browser"
	"github.com/thinkjk/gxodus/internal/config"
	"github.com/thinkjk/gxodus/internal/notify"
)

// archive magic bytes — what real downloads should start with.
var (
	zipMagic = []byte{'P', 'K', 0x03, 0x04}
	gzMagic  = []byte{0x1f, 0x8b}
)

func looksLikeArchive(b []byte) bool {
	return bytes.HasPrefix(b, zipMagic) || bytes.HasPrefix(b, gzMagic)
}

// isLikelyHTML returns true if the file at path begins with bytes that look
// like HTML (saved auth-redirect from before this fix). Used to invalidate
// stale partial downloads.
func isLikelyHTML(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 64)
	n, _ := io.ReadFull(f, head)
	head = bytes.TrimLeft(head[:n], " \t\r\n")
	for _, prefix := range [][]byte{
		[]byte("<!doctype"), []byte("<!DOCTYPE"),
		[]byte("<html"), []byte("<HTML"),
		[]byte("<head"), []byte("<HEAD"),
		[]byte("<meta"), []byte("<META"),
	} {
		if bytes.HasPrefix(head, prefix) {
			return true
		}
	}
	return false
}

func extractFilename(url string, index int) string {
	parts := strings.Split(url, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		part := strings.Split(parts[i], "?")[0]
		if strings.Contains(part, ".zip") || strings.Contains(part, ".tgz") {
			return part
		}
	}
	if strings.Contains(url, "filename=") {
		for _, param := range strings.Split(url, "&") {
			if strings.HasPrefix(param, "filename=") {
				return strings.TrimPrefix(param, "filename=")
			}
		}
	}
	return fmt.Sprintf("takeout-%s-%03d.zip", time.Now().Format("2006-01-02"), index+1)
}

type Result struct {
	Files     []string
	TotalSize int64
}

// Download fetches Takeout archives via chromedp into outputDir.
// cookies is the authenticated Google session; notifyCfg is used to fire
// auth_expired (Pushover + shell hook) when a re-auth challenge appears.
func Download(ctx context.Context, urls []string, outputDir string, cookies []*http.Cookie, notifyCfg config.NotifyConfig) (*Result, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}

	tmpDir := filepath.Join(config.ConfigDir(), "downloads-tmp")
	if err := resetTmpDir(tmpDir); err != nil {
		return nil, err
	}

	bctx, cancel, err := browser.NewContext(ctx, browser.Options{
		Headless:    false, // headed via container's Xvfb so noVNC can show challenges
		UserDataDir: browser.ProfileDir(),
	})
	if err != nil {
		return nil, fmt.Errorf("opening chromedp context: %w", err)
	}
	defer cancel()

	// Force a navigation so the chromedp context has an active target before
	// we inject cookies (cookies are scoped per-target / browser-wide via CDP).
	if err := chromedp.Run(bctx, chromedp.Navigate("about:blank")); err != nil {
		return nil, fmt.Errorf("opening blank page: %w", err)
	}

	if err := browser.InjectCookies(bctx, cookies); err != nil {
		return nil, fmt.Errorf("injecting cookies: %w", err)
	}

	if err := chromedp.Run(bctx, cdpbrowser.SetDownloadBehavior(cdpbrowser.SetDownloadBehaviorBehaviorAllow).
		WithDownloadPath(tmpDir).
		WithEventsEnabled(true)); err != nil {
		return nil, fmt.Errorf("setting download behavior: %w", err)
	}

	var result Result
	for i, u := range urls {
		path, size, err := downloadOne(bctx, u, i, tmpDir, outputDir, notifyCfg)
		if err != nil {
			return &result, fmt.Errorf("downloading %s: %w", u, err)
		}
		result.Files = append(result.Files, path)
		result.TotalSize += size
	}
	return &result, nil
}

// resetTmpDir wipes any abandoned partials from prior runs.
func resetTmpDir(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("clearing tmp dir: %w", err)
	}
	return os.MkdirAll(dir, 0755)
}

// downloadOne navigates to one download URL and waits for the file to land
// in tmpDir, then moves it to outputDir. Detects re-auth challenges by
// watching for the URL host to drift away from takeout.google.com after a
// 10s grace period; when that happens, fires auth_expired and blocks until
// the user completes the challenge via noVNC.
func downloadOne(ctx context.Context, url string, index int, tmpDir, outputDir string, notifyCfg config.NotifyConfig) (string, int64, error) {
	type downloadResult struct {
		filename string
		size     int64
		err      error
	}
	done := make(chan downloadResult, 1)
	began := make(chan string, 1) // filename when download begins

	var once sync.Once
	chromedp.ListenBrowser(ctx, func(ev interface{}) {
		switch e := ev.(type) {
		case *cdpbrowser.EventDownloadWillBegin:
			once.Do(func() { began <- e.SuggestedFilename })
		case *cdpbrowser.EventDownloadProgress:
			switch e.State {
			case cdpbrowser.DownloadProgressStateCompleted:
				done <- downloadResult{size: int64(e.TotalBytes)}
			case cdpbrowser.DownloadProgressStateCanceled:
				done <- downloadResult{err: fmt.Errorf("download canceled by browser")}
			}
		}
	})

	// Navigate to the URL. If the server triggers a download immediately,
	// chromium aborts the page load with ERR_ABORTED — ignore that specific
	// error; we'll still receive download events.
	if err := chromedp.Run(ctx, chromedp.Navigate(url)); err != nil {
		if !strings.Contains(err.Error(), "net::ERR_ABORTED") {
			return "", 0, fmt.Errorf("navigating to download URL: %w", err)
		}
	}

	// Race: download starts within 10s, OR we landed off-takeout (challenge),
	// OR context cancels.
	var filename string
	select {
	case filename = <-began:
		// happy path
	case <-time.After(10 * time.Second):
		if challenged, currentURL := atChallengePage(ctx); challenged {
			fmt.Printf("Download blocked on re-auth challenge (current URL: %s)\n", currentURL)
			fmt.Println("Open noVNC at <container-host>:6080/vnc.html and complete the password challenge.")
			fireAuthExpired(notifyCfg)
			if err := waitForChallengeResolved(ctx); err != nil {
				return "", 0, err
			}
			// After challenge, the browser usually proceeds to the download
			// automatically. Wait again for the began event.
			select {
			case filename = <-began:
			case <-time.After(60 * time.Second):
				return "", 0, fmt.Errorf("no download began after challenge resolved")
			case <-ctx.Done():
				return "", 0, ctx.Err()
			}
		} else {
			return "", 0, fmt.Errorf("no download began within 10s and not on a challenge page")
		}
	case <-ctx.Done():
		return "", 0, ctx.Err()
	}

	// Now wait for completion. 5-minute stall timeout.
	var result downloadResult
	select {
	case result = <-done:
		if result.err != nil {
			return "", 0, result.err
		}
	case <-time.After(5 * time.Minute):
		return "", 0, fmt.Errorf("download stalled (no completion event in 5m)")
	case <-ctx.Done():
		return "", 0, ctx.Err()
	}

	// Move file from tmpDir to outputDir.
	src := filepath.Join(tmpDir, filename)
	dst := filepath.Join(outputDir, filename)
	if err := os.Rename(src, dst); err != nil {
		return "", 0, fmt.Errorf("moving %s to %s: %w", src, dst, err)
	}

	// Magic-bytes safety check — chromedp downloads should always be archives,
	// but if Google ever serves a 200 OK with HTML body, catch it.
	if !verifyArchive(dst) {
		_ = os.Remove(dst)
		return "", 0, fmt.Errorf("downloaded file %s does not have zip/gzip magic", dst)
	}

	return dst, result.size, nil
}

// verifyArchive opens path and checks the first 4 bytes against magic.
func verifyArchive(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 4)
	n, _ := io.ReadFull(f, head)
	return looksLikeArchive(head[:n])
}

// atChallengePage queries the active page URL and returns true if its host
// is anything other than takeout.google.com (i.e., we got redirected).
func atChallengePage(ctx context.Context) (bool, string) {
	var currentURL string
	if err := chromedp.Run(ctx, chromedp.Location(&currentURL)); err != nil {
		return false, ""
	}
	if strings.Contains(currentURL, "://takeout.google.com/") {
		return false, currentURL
	}
	return true, currentURL
}

// fireAuthExpired calls into the notify package without import cycle.
func fireAuthExpired(cfg config.NotifyConfig) {
	notify.Fire(cfg, "auth_expired", notify.EventData{})
}

// waitForChallengeResolved is implemented in Task 7. Stub for now.
func waitForChallengeResolved(ctx context.Context) error {
	return fmt.Errorf("waitForChallengeResolved not implemented in Task 6 — see Task 7")
}
