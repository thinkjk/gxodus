package extractor

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Options struct {
	KeepZip bool
}

type Result struct {
	OutputDir  string
	FileCount  int
}

// Extract unpacks ZIP archives into organized date-stamped directories.
// Output structure: {outputDir}/{YYYY-MM-DD}/{service}/
func Extract(files []string, outputDir string, opts Options) (*Result, error) {
	dateDir := filepath.Join(outputDir, time.Now().Format("2006-01-02"))
	if err := os.MkdirAll(dateDir, 0755); err != nil {
		return nil, fmt.Errorf("creating date directory: %w", err)
	}

	totalFiles := 0

	for _, file := range files {
		count, err := extractZip(file, dateDir)
		if err != nil {
			return nil, fmt.Errorf("extracting %s: %w", filepath.Base(file), err)
		}
		totalFiles += count

		if !opts.KeepZip {
			if err := os.Remove(file); err != nil {
				fmt.Printf("Warning: could not remove %s: %v\n", file, err)
			}
		}
	}

	return &Result{
		OutputDir: dateDir,
		FileCount: totalFiles,
	}, nil
}

func extractZip(zipPath, destDir string) (int, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, err
	}
	defer r.Close()

	count := 0
	for _, f := range r.File {
		destPath := filepath.Join(destDir, f.Name)

		// Prevent zip slip
		if !strings.HasPrefix(filepath.Clean(destPath), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return count, fmt.Errorf("illegal file path in archive: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(destPath, 0755)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return count, err
		}

		if err := extractFile(f, destPath); err != nil {
			return count, err
		}
		count++
	}

	return count, nil
}

func extractFile(f *zip.File, destPath string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	outFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
	if err != nil {
		return err
	}
	defer outFile.Close()

	_, err = io.Copy(outFile, rc)
	return err
}
