package cli

import (
	"strings"
	"testing"

	"github.com/thinkjk/gxodus/internal/accounts"
)

func TestBuildAccountRows(t *testing.T) {
	rows := buildAccountRows([]accounts.Account{
		{Email: "a@x.com", Dir: "/cfg/accounts/a@x.com", HasSession: true},
		{Email: "b@x.com", Dir: "/cfg/accounts/b@x.com", HasSession: false},
	}, map[string]string{
		"a@x.com": "5430dfbb-...",
		"b@x.com": "",
	})

	if len(rows) != 2 {
		t.Fatalf("rows len = %d, want 2", len(rows))
	}
	if !strings.Contains(rows[0], "a@x.com") || !strings.Contains(rows[0], "valid") {
		t.Errorf("row[0] = %q", rows[0])
	}
	if !strings.Contains(rows[0], "5430dfbb") {
		t.Errorf("row[0] should mention pending uuid: %q", rows[0])
	}
	if !strings.Contains(rows[1], "b@x.com") || !strings.Contains(rows[1], "no session") {
		t.Errorf("row[1] = %q", rows[1])
	}
}
