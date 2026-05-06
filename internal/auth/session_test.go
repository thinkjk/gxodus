package auth

import (
	"net/http"
	"path/filepath"
	"testing"
)

func TestSession_RoundTripIsolatedPerAccountDir(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("GXODUS_CONFIG_DIR", cfg)

	dirA := filepath.Join(cfg, "accounts", "a@x.com")
	dirB := filepath.Join(cfg, "accounts", "b@x.com")

	cookiesA := []*http.Cookie{{Name: "SID", Value: "AAA"}}
	cookiesB := []*http.Cookie{{Name: "SID", Value: "BBB"}}

	if err := SaveSession(dirA, cookiesA); err != nil {
		t.Fatalf("SaveSession A: %v", err)
	}
	if err := SaveSession(dirB, cookiesB); err != nil {
		t.Fatalf("SaveSession B: %v", err)
	}

	if !SessionExists(dirA) {
		t.Errorf("SessionExists A: false, want true")
	}
	if !SessionExists(dirB) {
		t.Errorf("SessionExists B: false, want true")
	}

	gotA, err := LoadSession(dirA)
	if err != nil {
		t.Fatalf("LoadSession A: %v", err)
	}
	if len(gotA) != 1 || gotA[0].Value != "AAA" {
		t.Errorf("LoadSession A = %v, want one cookie SID=AAA", gotA)
	}
	gotB, err := LoadSession(dirB)
	if err != nil {
		t.Fatalf("LoadSession B: %v", err)
	}
	if len(gotB) != 1 || gotB[0].Value != "BBB" {
		t.Errorf("LoadSession B = %v, want one cookie SID=BBB", gotB)
	}

	if err := DeleteSession(dirA); err != nil {
		t.Fatalf("DeleteSession A: %v", err)
	}
	if SessionExists(dirA) {
		t.Errorf("SessionExists A after delete: true, want false")
	}
	if !SessionExists(dirB) {
		t.Errorf("SessionExists B after deleting A: false, want true (isolation)")
	}
}
