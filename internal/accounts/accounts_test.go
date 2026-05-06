package accounts

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestAccountDir(t *testing.T) {
	t.Setenv("GXODUS_CONFIG_DIR", "/explicit/cfg")
	got := AccountDir("jason@example.com")
	want := "/explicit/cfg/accounts/jason@example.com"
	if got != want {
		t.Errorf("AccountDir = %q, want %q", got, want)
	}
}

func TestScanAccounts_Empty(t *testing.T) {
	t.Setenv("GXODUS_CONFIG_DIR", t.TempDir())
	got, err := ScanAccounts()
	if err != nil {
		t.Fatalf("ScanAccounts: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ScanAccounts on empty config = %v, want []", got)
	}
}

func TestScanAccounts_FindsAllWithSessionFile(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("GXODUS_CONFIG_DIR", cfg)

	// Create three account dirs: two with session.enc, one without.
	for _, name := range []string{"a@x.com", "b@x.com", "c@x.com-no-session"} {
		dir := filepath.Join(cfg, "accounts", name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"a@x.com", "b@x.com"} {
		f := filepath.Join(cfg, "accounts", name, "session.enc")
		if err := os.WriteFile(f, []byte("dummy"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := ScanAccounts()
	if err != nil {
		t.Fatalf("ScanAccounts: %v", err)
	}

	emails := make([]string, len(got))
	for i, a := range got {
		emails[i] = a.Email
	}
	sort.Strings(emails)
	want := []string{"a@x.com", "b@x.com", "c@x.com-no-session"}
	if !reflect.DeepEqual(emails, want) {
		t.Errorf("emails = %v, want %v", emails, want)
	}

	// Find c@... and verify HasSession is false.
	var cAccount *Account
	for i := range got {
		if got[i].Email == "c@x.com-no-session" {
			cAccount = &got[i]
		}
	}
	if cAccount == nil {
		t.Fatal("c@x.com-no-session not in result")
	}
	if cAccount.HasSession {
		t.Error("c@x.com-no-session should have HasSession=false")
	}
}

func TestScanAccounts_NoAccountsDir(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("GXODUS_CONFIG_DIR", cfg)
	// Don't create accounts/ — confirm clean empty result, not error.
	got, err := ScanAccounts()
	if err != nil {
		t.Fatalf("ScanAccounts (no accounts dir): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ScanAccounts = %v, want []", got)
	}
}
