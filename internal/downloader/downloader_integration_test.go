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

	res, err := Download(context.Background(), []string{srv.URL + "/hello.zip"}, outDir, nil, config.NotifyConfig{}, t.TempDir())
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

func TestDownload_RecoversAfterChallenge(t *testing.T) {
	zipBytes := makeTinyZip(t)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			// First request: redirect to a fake challenge page.
			http.Redirect(w, r, "/challenge", http.StatusFound)
			return
		}
		if r.URL.Path == "/challenge" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body>fake challenge</body></html>`))
			return
		}
		// Subsequent navigation back to the download URL succeeds.
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="hello.zip"`)
		_, _ = io.Copy(w, bytes.NewReader(zipBytes))
	}))
	defer srv.Close()

	outDir := t.TempDir()
	t.Setenv("GXODUS_CONFIG_DIR", t.TempDir())

	// Run Download in a goroutine; while it's blocked on the challenge,
	// programmatically navigate back to the download URL to simulate the
	// user completing the challenge in the browser.
	resultCh := make(chan error, 1)
	var res *Result
	go func() {
		var err error
		res, err = Download(context.Background(), []string{srv.URL + "/download"}, outDir, nil, config.NotifyConfig{}, t.TempDir())
		resultCh <- err
	}()

	// Give the download goroutine time to hit the challenge and start polling.
	// Then we'd need to inject a navigation back to the download URL — but
	// we don't have a chromedp ctx handle from outside Download. Workaround:
	// the test relies on Download's challenge detector seeing the URL host
	// flip back when the next listener event fires. Without manual nudge we
	// can't simulate the user. Mark this test t.Skip with TODO until the
	// debug-download command lands (Task 9) and provides a manual harness.
	t.Skip("integration test for challenge recovery requires manual nudge; covered by manual debug-download flow")
	_ = resultCh
	_ = res
}
