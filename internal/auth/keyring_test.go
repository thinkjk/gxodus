package auth

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadKeyFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session-key")

	key := make([]byte, keyLength)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generating key: %v", err)
	}

	if err := saveKeyToFile(path, key); err != nil {
		t.Fatalf("saveKeyToFile: %v", err)
	}

	got, err := loadKeyFromFile(path)
	if err != nil {
		t.Fatalf("loadKeyFromFile: %v", err)
	}

	if hex.EncodeToString(got) != hex.EncodeToString(key) {
		t.Errorf("round-trip mismatch")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("key file permissions = %v, want 0600", info.Mode().Perm())
	}
}

func TestLoadKeyFromFileMissing(t *testing.T) {
	if _, err := loadKeyFromFile("/nonexistent/key"); err == nil {
		t.Error("expected error for missing file")
	}
}
