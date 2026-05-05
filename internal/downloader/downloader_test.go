package downloader

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLooksLikeArchive(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want bool
	}{
		{"zip magic", []byte{'P', 'K', 0x03, 0x04, 'x', 'y'}, true},
		{"gzip magic", []byte{0x1f, 0x8b, 0x08}, true},
		{"html doctype", []byte("<!DOCTYPE html>"), false},
		{"empty", []byte{}, false},
		{"three bytes only", []byte{'P', 'K', 0x03}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeArchive(tc.in); got != tc.want {
				t.Errorf("looksLikeArchive(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsLikelyHTML(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		content []byte
		want    bool
	}{
		{"doctype lower", []byte("<!doctype html>...rest..."), true},
		{"doctype upper", []byte("<!DOCTYPE HTML>...rest..."), true},
		{"leading whitespace", []byte("\n\n  <html>"), true},
		{"plain html tag", []byte("<html><body>x</body></html>"), true},
		{"zip magic", []byte{'P', 'K', 0x03, 0x04, 'x'}, false},
		{"random bytes", []byte{0x42, 0x43, 0x44, 0x45}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".bin")
			if err := os.WriteFile(path, tc.content, 0600); err != nil {
				t.Fatal(err)
			}
			if got := isLikelyHTML(path); got != tc.want {
				t.Errorf("isLikelyHTML(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestExtractFilename(t *testing.T) {
	cases := []struct {
		url   string
		index int
		want  string
	}{
		{"https://example.com/path/takeout-001.zip?j=x", 0, "takeout-001.zip"},
		{"https://takeout.google.com/takeout/download?j=abc&i=0&user=1", 0, "takeout-2026-05-05-001.zip"},
		// Note: the date in the second case will use time.Now() — we don't
		// assert it; just that the fallback shape kicks in. See the loose
		// matcher below.
	}
	if got := extractFilename(cases[0].url, cases[0].index); got != cases[0].want {
		t.Errorf("extractFilename(%q) = %q, want %q", cases[0].url, got, cases[0].want)
	}
	got := extractFilename(cases[1].url, cases[1].index)
	if !filepath.IsAbs(got) && filepath.Ext(got) == ".zip" && len(got) > 10 {
		// shape ok — looks like takeout-YYYY-MM-DD-NNN.zip
	} else {
		t.Errorf("extractFilename fallback shape wrong: %q", got)
	}
}

func TestMoveFile_SameFilesystem(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")

	want := []byte("hello world")
	if err := os.WriteFile(src, want, 0644); err != nil {
		t.Fatal(err)
	}

	if err := moveFile(src, dst); err != nil {
		t.Fatalf("moveFile: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("reading dst: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("dst content mismatch: got %q, want %q", got, want)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src should be gone, stat err = %v", err)
	}
}
