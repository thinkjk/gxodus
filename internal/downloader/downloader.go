// Package downloader fetches Google Takeout archives via a chromedp-driven
// chromium browser (cookie-only HTTP fails because Google's download URLs
// require a fresh re-authentication / "rapt" token that only a real
// browser can negotiate). See docs/superpowers/specs/2026-05-05-chromedp-downloader-design.md.
package downloader

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
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
