package extractor

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractZip(t *testing.T) {
	// Create a test ZIP file
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "test.zip")

	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("creating zip: %v", err)
	}

	w := zip.NewWriter(f)

	// Add a file
	fw, err := w.Create("Takeout/Drive/test.txt")
	if err != nil {
		t.Fatalf("adding file: %v", err)
	}
	fw.Write([]byte("hello world"))

	// Add another file in a subdirectory
	fw2, err := w.Create("Takeout/Gmail/inbox.mbox")
	if err != nil {
		t.Fatalf("adding file: %v", err)
	}
	fw2.Write([]byte("email content"))

	w.Close()
	f.Close()

	// Extract
	destDir := filepath.Join(dir, "output")
	count, err := extractZip(zipPath, destDir)
	if err != nil {
		t.Fatalf("extracting: %v", err)
	}

	if count != 2 {
		t.Errorf("expected 2 files extracted, got %d", count)
	}

	// Verify files exist
	content, err := os.ReadFile(filepath.Join(destDir, "Takeout", "Drive", "test.txt"))
	if err != nil {
		t.Fatalf("reading extracted file: %v", err)
	}
	if string(content) != "hello world" {
		t.Errorf("expected 'hello world', got '%s'", string(content))
	}
}

func TestExtractWithKeepZip(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "test.zip")

	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("creating zip: %v", err)
	}
	w := zip.NewWriter(f)
	fw, _ := w.Create("file.txt")
	fw.Write([]byte("data"))
	w.Close()
	f.Close()

	outputDir := filepath.Join(dir, "output")

	// Extract with KeepZip = true
	_, err = Extract([]string{zipPath}, outputDir, Options{KeepZip: true})
	if err != nil {
		t.Fatalf("extracting: %v", err)
	}

	// ZIP should still exist
	if _, err := os.Stat(zipPath); os.IsNotExist(err) {
		t.Error("ZIP file was removed despite KeepZip=true")
	}
}

func TestExtractWithoutKeepZip(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "test.zip")

	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("creating zip: %v", err)
	}
	w := zip.NewWriter(f)
	fw, _ := w.Create("file.txt")
	fw.Write([]byte("data"))
	w.Close()
	f.Close()

	outputDir := filepath.Join(dir, "output")

	// Extract with KeepZip = false
	_, err = Extract([]string{zipPath}, outputDir, Options{KeepZip: false})
	if err != nil {
		t.Fatalf("extracting: %v", err)
	}

	// ZIP should be removed
	if _, err := os.Stat(zipPath); !os.IsNotExist(err) {
		t.Error("ZIP file was not removed despite KeepZip=false")
	}
}
