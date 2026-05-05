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

// downloadOne is implemented in Task 6. Stub for now so Task 5's test
// can compile and (intentionally) fail at runtime.
func downloadOne(ctx context.Context, url string, index int, tmpDir, outputDir string, notifyCfg config.NotifyConfig) (string, int64, error) {
	// Suppress unused import warnings until Task 6 implements this.
	_ = sync.Mutex{}
	_ = notify.EventData{}
	_ = bytes.NewReader
	_ = io.EOF
	_ = strings.Contains
	_ = time.Now
	return "", 0, fmt.Errorf("downloadOne not implemented in Task 5 — see Task 6")
}
