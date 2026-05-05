package downloader

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/schollz/progressbar/v3"
)

type Result struct {
	Files     []string
	TotalSize int64
}

// Download fetches files from the given URLs to the output directory.
// Supports resuming interrupted downloads via HTTP Range headers.
//
// cookies must be the authenticated Google session — without them the
// takeout download URLs redirect to the sign-in page and Google's response
// body is HTML. The magic-bytes check below also defends against this.
func Download(urls []string, outputDir string, cookies []*http.Cookie) (*Result, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}

	var result Result

	for i, url := range urls {
		filename := extractFilename(url, i)
		destPath := filepath.Join(outputDir, filename)

		size, err := downloadFile(url, destPath, cookies)
		if err != nil {
			return &result, fmt.Errorf("downloading %s: %w", filename, err)
		}

		result.Files = append(result.Files, destPath)
		result.TotalSize += size
	}

	return &result, nil
}

// archive magic bytes — what real downloads should start with.
var (
	zipMagic = []byte{'P', 'K', 0x03, 0x04}
	gzMagic  = []byte{0x1f, 0x8b}
)

func looksLikeArchive(b []byte) bool {
	return bytes.HasPrefix(b, zipMagic) || bytes.HasPrefix(b, gzMagic)
}

// isLikelyHTML returns true if the file at path begins with bytes that look
// like HTML (saved auth-redirect from before the cookie fix). Used to
// invalidate stale partial downloads so resume doesn't append archive bytes
// onto an HTML page.
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

func downloadFile(url, destPath string, cookies []*http.Cookie) (int64, error) {
	// Check if partial download exists for resume. Discard tiny HTML stubs
	// (saved auth-redirects from before the cookie fix) so we don't append
	// archive bytes onto an HTML head.
	var startByte int64
	if fi, err := os.Stat(destPath); err == nil {
		if isLikelyHTML(destPath) {
			fmt.Printf("Discarding non-archive file at %s (likely a saved auth-redirect)\n", destPath)
			_ = os.Remove(destPath)
		} else {
			startByte = fi.Size()
		}
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}
	for _, ck := range cookies {
		req.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
	}

	if startByte > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", startByte))
		fmt.Printf("Resuming download from byte %d...\n", startByte)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("unexpected status: %s", resp.Status)
	}

	// If server doesn't support Range, start fresh
	if startByte > 0 && resp.StatusCode == http.StatusOK {
		startByte = 0
	}

	// On a fresh download, peek the first 4 bytes and verify they're a known
	// archive magic. Google sometimes returns 200 OK with an HTML body when
	// auth is rejected; without this check we'd silently save HTML as a .zip.
	body := resp.Body
	if startByte == 0 {
		peek := make([]byte, 4)
		n, _ := io.ReadFull(body, peek)
		peek = peek[:n]
		if !looksLikeArchive(peek) {
			ct := resp.Header.Get("Content-Type")
			return 0, fmt.Errorf("response is not a zip/tgz archive (Content-Type=%q, first %d bytes=%q) — auth likely rejected; check session cookies", ct, n, peek)
		}
		body = io.NopCloser(io.MultiReader(bytes.NewReader(peek), body))
	}

	totalSize := resp.ContentLength
	if startByte > 0 && totalSize > 0 {
		totalSize += startByte
	}

	flags := os.O_CREATE | os.O_WRONLY
	if startByte > 0 && resp.StatusCode == http.StatusPartialContent {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}

	f, err := os.OpenFile(destPath, flags, 0644)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	bar := progressbar.NewOptions64(
		totalSize,
		progressbar.OptionSetDescription(filepath.Base(destPath)),
		progressbar.OptionShowBytes(true),
		progressbar.OptionShowCount(),
		progressbar.OptionSetWidth(40),
		progressbar.OptionOnCompletion(func() { fmt.Println() }),
	)

	if startByte > 0 {
		bar.Set64(startByte)
	}

	written, err := io.Copy(io.MultiWriter(f, bar), body)
	if err != nil {
		return startByte + written, err
	}

	return startByte + written, nil
}

func extractFilename(url string, index int) string {
	// Try to get filename from URL path
	parts := strings.Split(url, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		part := strings.Split(parts[i], "?")[0]
		if strings.Contains(part, ".zip") || strings.Contains(part, ".tgz") {
			return part
		}
	}

	// Try Content-Disposition style names from query params
	if strings.Contains(url, "filename=") {
		for _, param := range strings.Split(url, "&") {
			if strings.HasPrefix(param, "filename=") {
				return strings.TrimPrefix(param, "filename=")
			}
		}
	}

	return fmt.Sprintf("takeout-%s-%03d.zip", time.Now().Format("2006-01-02"), index+1)
}
