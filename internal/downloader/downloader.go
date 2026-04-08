package downloader

import (
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
func Download(urls []string, outputDir string) (*Result, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}

	var result Result

	for i, url := range urls {
		filename := extractFilename(url, i)
		destPath := filepath.Join(outputDir, filename)

		size, err := downloadFile(url, destPath)
		if err != nil {
			return &result, fmt.Errorf("downloading %s: %w", filename, err)
		}

		result.Files = append(result.Files, destPath)
		result.TotalSize += size
	}

	return &result, nil
}

func downloadFile(url, destPath string) (int64, error) {
	// Check if partial download exists for resume
	var startByte int64
	if fi, err := os.Stat(destPath); err == nil {
		startByte = fi.Size()
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
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

	written, err := io.Copy(io.MultiWriter(f, bar), resp.Body)
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
