package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPendingExport_IsolatedPerAccountDir(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("GXODUS_CONFIG_DIR", cfg)

	dirA := filepath.Join(cfg, "accounts", "a@x.com")
	dirB := filepath.Join(cfg, "accounts", "b@x.com")
	if err := os.MkdirAll(dirA, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirB, 0700); err != nil {
		t.Fatal(err)
	}

	if err := writePendingExport(dirA, "uuid-A"); err != nil {
		t.Fatalf("write A: %v", err)
	}
	if err := writePendingExport(dirB, "uuid-B"); err != nil {
		t.Fatalf("write B: %v", err)
	}

	gotA, err := readPendingExport(dirA)
	if err != nil {
		t.Fatalf("read A: %v", err)
	}
	if gotA != "uuid-A" {
		t.Errorf("read A = %q, want uuid-A", gotA)
	}
	gotB, err := readPendingExport(dirB)
	if err != nil {
		t.Fatalf("read B: %v", err)
	}
	if gotB != "uuid-B" {
		t.Errorf("read B = %q, want uuid-B", gotB)
	}

	if err := clearPendingExport(dirA); err != nil {
		t.Fatalf("clear A: %v", err)
	}
	gotA2, _ := readPendingExport(dirA)
	if gotA2 != "" {
		t.Errorf("after clear A, read = %q, want empty", gotA2)
	}
	gotB2, _ := readPendingExport(dirB)
	if gotB2 != "uuid-B" {
		t.Errorf("after clearing A, B should still read uuid-B; got %q", gotB2)
	}
}
