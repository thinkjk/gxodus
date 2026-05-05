//go:build integration

package downloader

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/thinkjk/gxodus/internal/config"
)

// makeTinyZip returns a valid ZIP byte slice with one entry.
func makeTinyZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create("hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDownload_HappyPath_OneFile(t *testing.T) {
	zipBytes := makeTinyZip(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="hello.zip"`)
		_, _ = io.Copy(w, bytes.NewReader(zipBytes))
	}))
	defer srv.Close()

	outDir := t.TempDir()
	t.Setenv("GXODUS_CONFIG_DIR", t.TempDir())

	res, err := Download(context.Background(), []string{srv.URL + "/hello.zip"}, outDir, nil, config.NotifyConfig{})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(res.Files) != 1 {
		t.Fatalf("Files len = %d, want 1", len(res.Files))
	}
	got, err := os.ReadFile(res.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, zipBytes) {
		t.Errorf("downloaded file content mismatch (got %d bytes, want %d)", len(got), len(zipBytes))
	}
	if filepath.Dir(res.Files[0]) != outDir {
		t.Errorf("file landed in %s, want %s", filepath.Dir(res.Files[0]), outDir)
	}
}
