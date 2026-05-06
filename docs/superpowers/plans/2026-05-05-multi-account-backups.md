# Multi-Account Backups Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let one gxodus container back up multiple Google accounts on the same schedule, each with its own session, chromium profile, pending-export marker, and output subdir, identified by the email scraped from the chromium session after login.

**Architecture:** New `$CONFIG_DIR/accounts/<email>/` layout replaces single-rooted session/profile/marker files. Each touched module (`auth`, `browser`, `cli/pending_export`) gains an `accountDir string` parameter on its path helpers. The `gxodus export` RunE iterates over accounts sequentially; per-account failures are isolated and surfaced with `EventData.Account` populated. New `gxodus auth --new`/`--account` flags + `list-accounts` / `remove-account` commands. Docker entrypoint reads a new `.failed-accounts` file to wipe only the right session(s) on exit-1.

**Tech Stack:** Go 1.26, chromedp v0.15.1, cobra, pelletier/go-toml/v2.

**Spec:** `docs/superpowers/specs/2026-05-05-multi-account-design.md`

**Branch:** Already on `feat/multi-account-backups`.

---

## File Structure

**Create:**
- `internal/accounts/accounts.go` — `AccountDir(email)`, `ScanAccounts()`, account-discovery helpers
- `internal/accounts/accounts_test.go` — unit tests
- `internal/cli/list_accounts.go` — `gxodus list-accounts` cobra command
- `internal/cli/list_accounts_test.go` — unit test for the row-builder
- `internal/cli/remove_account.go` — `gxodus remove-account <email>` cobra command
- `internal/cli/failed_accounts.go` — read/write `.failed-accounts` helpers

**Modify:**
- `internal/auth/session.go` — `SessionPath`/`Save`/`Load`/`SessionExists`/`DeleteSession` accept `accountDir string`
- `internal/auth/session_test.go` — round-trip + isolation tests (create file if absent)
- `internal/browser/browser.go` — `ProfileDir(accountDir string)` signature change
- `internal/browser/login.go` — `InteractiveLogin` returns `(cookies, email, error)`; scrape email from takeout DOM
- `internal/cli/pending_export.go` — path helpers parameterized by accountDir
- `internal/cli/auth.go` — rewrite for `--new` / `--account` flags + email scrape integration
- `internal/cli/export.go` — iterate over accounts via `runExportForAccount`; small `takeoutClient` interface for testability
- `internal/cli/export_test.go` — new test file: per-account isolation
- `internal/cli/status.go` — accept `--account` (default to first if 1)
- `internal/cli/debug_api.go` — accept `--account` (default to first if 1)
- `internal/notify/notify.go` — `EventData.Account string`; prepend to Pushover title
- `internal/notify/pushover_test.go` — extend Fire test to verify `[email]` in title
- `docker-entrypoint.sh` — read `.failed-accounts` on exit-1; per-account auth-retry loop
- `README.md` — multi-account section + migration instructions

---

### Task 1: New `internal/accounts` package — AccountDir + ScanAccounts

**Files:**
- Create: `internal/accounts/accounts.go`
- Create: `internal/accounts/accounts_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/accounts/accounts_test.go`:

```go
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
```

- [ ] **Step 2: Run to confirm fail**

```
go test ./internal/accounts/ -v
```

Expected: build fails — package doesn't exist yet.

- [ ] **Step 3: Implement the package**

Create `internal/accounts/accounts.go`:

```go
// Package accounts manages the per-email account directories under
// $CONFIG_DIR/accounts/. Each account is one Google sign-in with its
// own session.enc, chrome-profile, and pending_export.uuid.
package accounts

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/thinkjk/gxodus/internal/config"
)

// Account describes one configured account on disk.
type Account struct {
	Email      string // the directory basename
	Dir        string // absolute path to $CONFIG_DIR/accounts/<email>
	HasSession bool   // session.enc present
}

// AccountDir returns the on-disk dir for the given email.
func AccountDir(email string) string {
	return filepath.Join(config.ConfigDir(), "accounts", email)
}

// AccountsRoot returns $CONFIG_DIR/accounts.
func AccountsRoot() string {
	return filepath.Join(config.ConfigDir(), "accounts")
}

// EnsureAccountDir creates the dir for the given email if missing.
func EnsureAccountDir(email string) error {
	return os.MkdirAll(AccountDir(email), 0700)
}

// ScanAccounts returns all account dirs found under $CONFIG_DIR/accounts/,
// sorted by email. Missing accounts dir is treated as empty (not an error).
func ScanAccounts() ([]Account, error) {
	root := AccountsRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", root, err)
	}

	out := make([]Account, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		_, err := os.Stat(filepath.Join(dir, "session.enc"))
		out = append(out, Account{
			Email:      e.Name(),
			Dir:        dir,
			HasSession: err == nil,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out, nil
}
```

- [ ] **Step 4: Run tests to confirm pass**

```
go test ./internal/accounts/ -v
```

Expected: PASS for all four tests.

- [ ] **Step 5: Commit**

```bash
git add internal/accounts/
git commit -m "$(cat <<'EOF'
Add internal/accounts: per-email AccountDir + ScanAccounts helpers

New package owns the $CONFIG_DIR/accounts/<email>/ layout. Account
struct exposes the parsed email + dir + whether a session.enc exists
(so list-accounts can flag "no session" entries without errorring).
ScanAccounts is forgiving: missing accounts/ dir returns empty.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Parameterize `auth/session.go` by accountDir

**Files:**
- Modify: `internal/auth/session.go`
- Create: `internal/auth/session_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/auth/session_test.go`:

```go
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
```

- [ ] **Step 2: Run to confirm fail**

```
go test ./internal/auth/ -run TestSession_RoundTrip -v
```

Expected: build error — `SaveSession`, `LoadSession`, etc. don't accept the new arg.

- [ ] **Step 3: Update session.go signatures**

Replace `internal/auth/session.go` (preserving the existing `encrypt`/`decrypt` helpers and the `CookieData` struct):

```go
package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const sessionFile = "session.enc"

type CookieData struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Secure   bool   `json:"secure"`
	HTTPOnly bool   `json:"http_only"`
}

// SessionPath returns the session-file path for a given account dir.
func SessionPath(accountDir string) string {
	return filepath.Join(accountDir, sessionFile)
}

func SaveSession(accountDir string, cookies []*http.Cookie) error {
	if err := os.MkdirAll(accountDir, 0700); err != nil {
		return fmt.Errorf("creating account dir: %w", err)
	}

	data := make([]CookieData, len(cookies))
	for i, c := range cookies {
		data[i] = CookieData{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HTTPOnly: c.HttpOnly,
		}
	}

	plaintext, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshaling cookies: %w", err)
	}

	key, err := getOrCreateKey()
	if err != nil {
		return fmt.Errorf("getting encryption key: %w", err)
	}

	encrypted, err := encrypt(plaintext, key)
	if err != nil {
		return fmt.Errorf("encrypting session: %w", err)
	}

	if err := os.WriteFile(SessionPath(accountDir), encrypted, 0600); err != nil {
		return fmt.Errorf("writing session file: %w", err)
	}
	return nil
}

func LoadSession(accountDir string) ([]*http.Cookie, error) {
	encrypted, err := os.ReadFile(SessionPath(accountDir))
	if err != nil {
		return nil, fmt.Errorf("reading session file: %w", err)
	}

	key, err := getOrCreateKey()
	if err != nil {
		return nil, fmt.Errorf("getting encryption key: %w", err)
	}

	plaintext, err := decrypt(encrypted, key)
	if err != nil {
		return nil, fmt.Errorf("decrypting session: %w", err)
	}

	var data []CookieData
	if err := json.Unmarshal(plaintext, &data); err != nil {
		return nil, fmt.Errorf("unmarshaling cookies: %w", err)
	}

	cookies := make([]*http.Cookie, len(data))
	for i, d := range data {
		cookies[i] = &http.Cookie{
			Name:     d.Name,
			Value:    d.Value,
			Domain:   d.Domain,
			Path:     d.Path,
			Secure:   d.Secure,
			HttpOnly: d.HTTPOnly,
		}
	}
	return cookies, nil
}

func DeleteSession(accountDir string) error {
	if err := os.Remove(SessionPath(accountDir)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing session file: %w", err)
	}
	return nil
}

func SessionExists(accountDir string) bool {
	_, err := os.Stat(SessionPath(accountDir))
	return err == nil
}

func encrypt(plaintext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func decrypt(ciphertext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}
```

Note: removed the `config` import; `accountDir` is now passed in.

- [ ] **Step 4: Run tests to confirm pass**

```
go test ./internal/auth/ -v
```

Expected: PASS for new + existing tests.

Build will be broken at every caller (`internal/cli/*`) since the API changed. Verify the breakage is only in CLI:

```
go build ./internal/auth/...
go build ./... 2>&1 | head -20
```

Expected: `internal/auth` builds clean; CLI errors at session.SessionExists/Save/Load/Delete calls (fixed in later tasks).

- [ ] **Step 5: Commit**

```bash
git add internal/auth/
git commit -m "$(cat <<'EOF'
Parameterize auth.Session* by accountDir

SessionPath, SaveSession, LoadSession, SessionExists, and DeleteSession
all gain an accountDir string argument so each Google account can have
its own encrypted session file. Build is broken at internal/cli/*
callers; fixed in subsequent tasks.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Parameterize `cli/pending_export.go` by accountDir

**Files:**
- Modify: `internal/cli/pending_export.go`
- Create: `internal/cli/pending_export_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/cli/pending_export_test.go`:

```go
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
```

- [ ] **Step 2: Run to confirm fail**

```
go test ./internal/cli/ -run TestPendingExport_Isolated -v
```

Expected: build error — functions don't take the new arg.

- [ ] **Step 3: Update pending_export.go**

Replace `internal/cli/pending_export.go`:

```go
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// pending_export.uuid lives inside each account dir
// ($CONFIG_DIR/accounts/<email>/pending_export.uuid). It marks an
// export that's been created at Google but not yet downloaded. On
// startup, gxodus export reads it and resumes polling that UUID
// instead of creating a fresh export — so a container restart
// mid-poll doesn't fire another full backup. Cleared after a
// successful download.

func pendingExportPath(accountDir string) string {
	return filepath.Join(accountDir, "pending_export.uuid")
}

func readPendingExport(accountDir string) (string, error) {
	data, err := os.ReadFile(pendingExportPath(accountDir))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading %s: %w", pendingExportPath(accountDir), err)
	}
	return strings.TrimSpace(string(data)), nil
}

func writePendingExport(accountDir, uuid string) error {
	if err := os.MkdirAll(accountDir, 0700); err != nil {
		return fmt.Errorf("ensuring account dir: %w", err)
	}
	if err := os.WriteFile(pendingExportPath(accountDir), []byte(uuid+"\n"), 0600); err != nil {
		return fmt.Errorf("writing %s: %w", pendingExportPath(accountDir), err)
	}
	return nil
}

func clearPendingExport(accountDir string) error {
	if err := os.Remove(pendingExportPath(accountDir)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing %s: %w", pendingExportPath(accountDir), err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to confirm pass**

```
go test ./internal/cli/ -run TestPendingExport_Isolated -v
```

Expected: PASS.

The CLI package as a whole won't build (export.go uses the old signatures); that's expected and fixed in Task 8.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/pending_export.go internal/cli/pending_export_test.go
git commit -m "$(cat <<'EOF'
Parameterize pending-export marker by accountDir

writePendingExport / readPendingExport / clearPendingExport now scope
to a per-account dir under $CONFIG_DIR/accounts/<email>/. Test verifies
isolation between two account dirs in the same temp config dir.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Parameterize `browser.ProfileDir()` by accountDir

**Files:**
- Modify: `internal/browser/browser.go`

- [ ] **Step 1: Update ProfileDir signature**

In `internal/browser/browser.go`, replace:

```go
func ProfileDir() string {
	return filepath.Join(config.ConfigDir(), "chrome-profile")
}
```

with:

```go
// ProfileDir returns the persistent chromium user-data-dir for the
// given account, scoped under that account's $CONFIG_DIR/accounts/<email>/
// dir. Each account has its own profile so Google sees one continuous
// "trusted device" per account across gxodus invocations.
func ProfileDir(accountDir string) string {
	return filepath.Join(accountDir, "chrome-profile")
}
```

If the file imports `"github.com/thinkjk/gxodus/internal/config"` and no other code in the file uses it, remove the import.

- [ ] **Step 2: Build to verify the package itself compiles**

```
go build ./internal/browser/...
```

Expected: success.

`go build ./...` will fail at every `browser.ProfileDir()` callsite — fixed in subsequent tasks.

- [ ] **Step 3: Commit**

```bash
git add internal/browser/browser.go
git commit -m "$(cat <<'EOF'
Parameterize browser.ProfileDir by accountDir

Each account gets its own chrome-profile so Google sees a single
trusted device per account. Callers updated in subsequent tasks.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Email scrape in `InteractiveLogin`

**Files:**
- Modify: `internal/browser/login.go`

- [ ] **Step 1: Update InteractiveLogin signature + behavior**

In `internal/browser/login.go`:

(a) Change signature from `func InteractiveLogin(ctx context.Context, _ string) ([]*http.Cookie, error)` to `func InteractiveLogin(ctx context.Context, _ string, profileDir string) ([]*http.Cookie, string, error)`.

(b) Replace `profileDir := ProfileDir()` (~line 39) with `_ = profileDir` (use the passed-in value directly later in the function).

Wait — that's ambiguous. Concretely: search for the line `profileDir := ProfileDir()` and remove it (the function now accepts profileDir as a parameter). All subsequent uses of `profileDir` reference the parameter.

(c) After the existing `Visit takeout.google.com so Google sets the takeout-specific cookies` warmup (around line 110-116), add an email-scrape block before `cookies, err := ExtractCookies(browserCtx)`:

```go
	// Scrape the active account's email from the takeout page's
	// account-chooser button (aria-label encodes "Google Account: <Name> (<email>)").
	// Falls back to "" if the scrape fails — the caller decides what to do
	// (typically: save cookies to a .pending-auth-<unix> dir for manual rescue).
	email := scrapeEmail(browserCtx)
	if email != "" {
		fmt.Printf("Detected account email: %s\n", email)
	} else {
		fmt.Fprintln(os.Stderr, "warning: could not detect account email from takeout page (DOM may have changed)")
	}
```

(d) Update the return to include email:

```go
	cookies, err := ExtractCookies(browserCtx)
	if err != nil {
		return nil, "", fmt.Errorf("extracting cookies: %w", err)
	}
	return cookies, email, nil
```

(e) Update earlier error returns from `return nil, ...` to `return nil, "", ...` (5 such sites in InteractiveLogin).

(f) Add the `scrapeEmail` helper at the bottom of the file:

```go
// emailFromAriaLabelRE pulls the email out of strings shaped like
// "Google Account: Jason Kramer (jason@example.com)".
var emailFromAriaLabelRE = regexp.MustCompile(`\(([^)]+@[^)]+)\)`)

// scrapeEmail reads the aria-label of the account-chooser button on the
// takeout page and extracts the email. Returns "" if anything goes wrong.
func scrapeEmail(ctx context.Context) string {
	var ariaLabel string
	err := chromedp.Run(ctx,
		chromedp.AttributeValue(
			`button[aria-label*="Google Account:"]`,
			"aria-label",
			&ariaLabel,
			nil,
			chromedp.ByQuery,
		),
	)
	if err != nil {
		return ""
	}
	m := emailFromAriaLabelRE.FindStringSubmatch(ariaLabel)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}
```

Add `"regexp"` and `"strings"` to the imports if not already present.

- [ ] **Step 2: Build to verify the package compiles**

```
go build ./internal/browser/...
```

Expected: success. CLI build still broken at the InteractiveLogin caller — fixed in Task 6.

- [ ] **Step 3: Commit**

```bash
git add internal/browser/login.go
git commit -m "$(cat <<'EOF'
InteractiveLogin: accept profileDir, return scraped email

The account-chooser button on the takeout page exposes the active
account's email in its aria-label (Google Account: Name (email)).
Scrape via chromedp.AttributeValue + regex; return "" if the DOM
changed so the caller can fall back to a recovery path. profileDir
is now an explicit parameter so callers can use per-account dirs OR
a fresh temp dir for "add new account" flows.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Rewrite `cli/auth.go` for `--new` / `--account` flags

**Files:**
- Modify: `internal/cli/auth.go`

- [ ] **Step 1: Rewrite auth.go**

Replace `internal/cli/auth.go`:

```go
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/thinkjk/gxodus/internal/accounts"
	"github.com/thinkjk/gxodus/internal/auth"
	"github.com/thinkjk/gxodus/internal/browser"
	"github.com/thinkjk/gxodus/internal/config"
)

var (
	authCheck   bool
	authRevoke  bool
	authNewFlag bool
	authAccount string
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authenticate with Google",
	Long: `Authenticate via interactive browser login.

  gxodus auth                       — refresh sole account, OR add first account
                                      if none exists yet. Errors if 2+ accounts.
  gxodus auth --new                 — add a new account (fresh chromium profile;
                                      email derived from the post-login DOM scrape)
  gxodus auth --account <email>     — refresh that specific account; treats as
                                      adding if the dir doesn't yet exist
                                      (uses --account email instead of scrape)
  gxodus auth --check               — validate current session(s)
  gxodus auth --revoke              — delete saved session(s)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		if authRevoke {
			return runAuthRevoke()
		}
		if authCheck {
			return runAuthCheck(ctx)
		}

		existing, err := accounts.ScanAccounts()
		if err != nil {
			return fmt.Errorf("scanning accounts: %w", err)
		}

		// Decide whether this is "add new" or "refresh existing".
		var (
			profileDir   string
			fixedEmail   string // when set, skip the scrape and use this email
			usingNewProfile bool
		)
		switch {
		case authNewFlag:
			// Always add new with a fresh temp profile.
			profileDir, err = newTempProfileDir()
			if err != nil {
				return err
			}
			usingNewProfile = true
		case authAccount != "":
			// Targeting a specific email.
			ad := accounts.AccountDir(authAccount)
			if err := os.MkdirAll(ad, 0700); err != nil {
				return fmt.Errorf("ensuring account dir: %w", err)
			}
			profileDir = browser.ProfileDir(ad)
			fixedEmail = authAccount // skip scrape; trust the flag
		case len(existing) == 0:
			// First-time setup convenience: behave as --new.
			profileDir, err = newTempProfileDir()
			if err != nil {
				return err
			}
			usingNewProfile = true
		case len(existing) == 1:
			// Refresh the sole account.
			profileDir = browser.ProfileDir(existing[0].Dir)
			fixedEmail = existing[0].Email
		default:
			return fmt.Errorf("multiple accounts configured (%d); use --account <email> to refresh a specific one or --new to add another", len(existing))
		}

		cookies, scrapedEmail, err := browser.InteractiveLogin(ctx, "", profileDir)
		if err != nil {
			return fmt.Errorf("login failed: %w", err)
		}

		email := fixedEmail
		if email == "" {
			email = scrapedEmail
		}
		if email == "" {
			// Email scrape failed AND no flag/existing account told us which one.
			// Save cookies to a recovery dir so the user can manually rescue.
			pending := filepath.Join(config.ConfigDir(), fmt.Sprintf(".pending-auth-%d", time.Now().Unix()))
			if err := os.MkdirAll(pending, 0700); err != nil {
				return fmt.Errorf("creating pending-auth dir: %w", err)
			}
			if err := auth.SaveSession(pending, cookies); err != nil {
				return fmt.Errorf("saving cookies to pending-auth dir: %w", err)
			}
			return fmt.Errorf("email scrape failed; cookies saved to %s — rename to accounts/<email>/ manually after looking up the email", pending)
		}

		dest := accounts.AccountDir(email)
		if err := accounts.EnsureAccountDir(email); err != nil {
			return fmt.Errorf("ensuring account dir for %s: %w", email, err)
		}
		if err := auth.SaveSession(dest, cookies); err != nil {
			return fmt.Errorf("saving session for %s: %w", email, err)
		}

		// If we used a temp profile (--new or first-account), move it into place.
		if usingNewProfile {
			if err := os.Rename(profileDir, browser.ProfileDir(dest)); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not move temp chrome-profile %s into %s: %v (next chromium spawn will create a fresh one)\n",
					profileDir, browser.ProfileDir(dest), err)
			}
		}

		fmt.Printf("Session saved for %s.\n", email)
		return nil
	},
}

func newTempProfileDir() (string, error) {
	dir := filepath.Join(config.ConfigDir(), fmt.Sprintf(".tmp-profile-%d", time.Now().Unix()))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("creating temp profile dir: %w", err)
	}
	return dir, nil
}

func runAuthRevoke() error {
	existing, err := accounts.ScanAccounts()
	if err != nil {
		return fmt.Errorf("scanning accounts: %w", err)
	}
	if authAccount != "" {
		// Revoke specific account
		dir := accounts.AccountDir(authAccount)
		if err := auth.DeleteSession(dir); err != nil {
			return fmt.Errorf("deleting session for %s: %w", authAccount, err)
		}
		fmt.Printf("Session for %s revoked.\n", authAccount)
		return nil
	}
	// Revoke all
	for _, a := range existing {
		if err := auth.DeleteSession(a.Dir); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not delete session for %s: %v\n", a.Email, err)
		}
	}
	if err := auth.DeleteKey(); err != nil {
		return fmt.Errorf("deleting keyring entry: %w", err)
	}
	fmt.Printf("Revoked %d session(s).\n", len(existing))
	return nil
}

func runAuthCheck(ctx context.Context) error {
	existing, err := accounts.ScanAccounts()
	if err != nil {
		return fmt.Errorf("scanning accounts: %w", err)
	}
	if len(existing) == 0 {
		fmt.Println("No accounts configured.")
		return fmt.Errorf("no accounts")
	}
	allOK := true
	for _, a := range existing {
		if !a.HasSession {
			fmt.Printf("%s: no session.enc — run 'gxodus auth --account %s'\n", a.Email, a.Email)
			allOK = false
			continue
		}
		cookies, err := auth.LoadSession(a.Dir)
		if err != nil {
			fmt.Printf("%s: load failed (%v)\n", a.Email, err)
			allOK = false
			continue
		}
		valid, err := browser.CheckSession(ctx, cookies, "")
		if err != nil {
			fmt.Printf("%s: check failed (%v)\n", a.Email, err)
			allOK = false
			continue
		}
		if valid {
			fmt.Printf("%s: ✓ valid\n", a.Email)
		} else {
			fmt.Printf("%s: ✗ expired — run 'gxodus auth --account %s'\n", a.Email, a.Email)
			allOK = false
		}
	}
	if !allOK {
		return fmt.Errorf("some accounts need re-auth")
	}
	return nil
}

func init() {
	authCmd.Flags().BoolVar(&authCheck, "check", false, "validate saved session(s) are still active")
	authCmd.Flags().BoolVar(&authRevoke, "revoke", false, "delete saved session(s)")
	authCmd.Flags().BoolVar(&authNewFlag, "new", false, "add a new account (fresh chromium profile)")
	authCmd.Flags().StringVar(&authAccount, "account", "", "target a specific account by email")
	rootCmd.AddCommand(authCmd)
}
```

You will need to add `"context"` to the imports.

- [ ] **Step 2: Build to verify cli/auth.go compiles**

```
go build ./internal/cli/auth.go internal/cli/root.go internal/cli/version.go internal/cli/pending_export.go internal/cli/failed_accounts.go 2>&1 | head -10
```

Some of those files don't exist yet (failed_accounts.go from Task 9). So a cleaner check:

```
go vet ./internal/cli/auth.go 2>&1 | head -10
```

Or check the whole package and accept that some other CLI files (export.go) are still broken — just verify auth.go has no errors specific to it:

```
go build ./internal/cli/... 2>&1 | grep -v "internal/cli/export.go\|internal/cli/status.go\|internal/cli/debug_api.go" | head -10
```

Expected: only errors related to export.go/status.go/debug_api.go which use the old auth APIs (fixed in Tasks 8, 12).

- [ ] **Step 3: Commit**

```bash
git add internal/cli/auth.go
git commit -m "$(cat <<'EOF'
Rewrite auth.go for multi-account: --new, --account, refresh-vs-add

Routing logic:
- --new always uses a fresh temp profile (then renamed into place).
- --account <email> targets that account, skipping the email scrape.
- No flag, 0 accounts → behaves as --new (initial setup convenience).
- No flag, 1 account → refreshes that one.
- No flag, 2+ accounts → errors with guidance.

Email comes from either the --account flag (trusted) or InteractiveLogin
scrape. If both are absent and the scrape fails, cookies are saved to
.pending-auth-<unix>/ for manual rescue rather than discarded.

--check now iterates over all accounts and prints per-account status.
--revoke without --account wipes everything; with --account wipes one.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: `EventData.Account` propagation

**Files:**
- Modify: `internal/notify/notify.go`
- Modify: `internal/notify/pushover_test.go`

- [ ] **Step 1: Add Account field + propagation**

In `internal/notify/notify.go`, add `Account string` to `EventData`:

```go
type EventData struct {
	Account    string  // Google account email this event relates to (empty for global events)
	Error      string
	OutputPath string
	ExportSize int64
	Duration   time.Duration
}
```

Then in `pushoverMessageFor`, prepend `[<account>]` to the title when set. Replace the function:

```go
func pushoverMessageFor(event string, data EventData) (title, message string) {
	host := os.Getenv("GXODUS_PUBLIC_HOSTNAME")
	if host == "" {
		if h, err := os.Hostname(); err == nil {
			host = h
		} else {
			host = "the gxodus container"
		}
	}
	suffix := ""
	if data.Account != "" {
		suffix = " [" + data.Account + "]"
	}
	switch event {
	case "auth_expired":
		return "gxodus: re-auth needed" + suffix,
			fmt.Sprintf("Open noVNC at %s:6080/vnc.html and complete the password challenge.", host)
	case "export_complete":
		return "gxodus: export ready" + suffix,
			fmt.Sprintf("Downloaded %d bytes to %s.", data.ExportSize, data.OutputPath)
	case "error":
		return "gxodus: error" + suffix, data.Error
	case "export_started":
		return "gxodus: export started" + suffix, "New Takeout submitted."
	}
	return "gxodus" + suffix, event
}
```

- [ ] **Step 2: Extend the existing Fire test**

In `internal/notify/pushover_test.go`, append:

```go
func TestFire_PushoverTitleIncludesAccount(t *testing.T) {
	var captured url.Values
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		captured = r.PostForm
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	pushoverEndpointOverride = srv.URL
	defer func() { pushoverEndpointOverride = "" }()

	cfg := config.NotifyConfig{
		Pushover: config.PushoverConfig{
			Token:   "tk",
			UserKey: "uk",
			Events:  []string{"auth_expired"},
		},
	}
	Fire(cfg, "auth_expired", EventData{Account: "jason@example.com"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		ok := captured.Get("title") != ""
		mu.Unlock()
		if ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	got := captured.Get("title")
	if got != "gxodus: re-auth needed [jason@example.com]" {
		t.Errorf("title = %q, want includes [jason@example.com]", got)
	}
}
```

- [ ] **Step 3: Run tests**

```
go test ./internal/notify/ -v
```

Expected: PASS for new + existing tests.

- [ ] **Step 4: Commit**

```bash
git add internal/notify/
git commit -m "$(cat <<'EOF'
Add EventData.Account; prepend to Pushover title

Multi-account events fire with EventData.Account set so notifications
can identify which account needs attention. Pushover title becomes
'gxodus: re-auth needed [jason@example.com]' (suffix omitted when
Account is empty so global events stay clean).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: Refactor `cli/export.go` to iterate accounts

**Files:**
- Modify: `internal/cli/export.go`
- Create: `internal/cli/export_test.go`

This is the big one. The pipeline logic stays mostly the same; we wrap it in `runExportForAccount(ctx, email, accountDir, cfg)` and iterate.

- [ ] **Step 1: Write the failing isolation test**

Create `internal/cli/export_test.go`:

```go
package cli

import (
	"reflect"
	"sort"
	"testing"
)

func TestSelectFailedAccountsForRetry(t *testing.T) {
	// classifyFailures returns (authFailed, otherFailed).
	type res struct{ auth, other []string }
	got := res{}
	got.auth, got.other = classifyFailures([]accountResult{
		{Email: "a@x", Err: errSessionExpiredSentinel},
		{Email: "b@x", Err: nil}, // success
		{Email: "c@x", Err: errSomeOtherFailure},
		{Email: "d@x", Err: errSessionExpiredSentinel},
	})
	sort.Strings(got.auth)
	sort.Strings(got.other)
	wantAuth := []string{"a@x", "d@x"}
	wantOther := []string{"c@x"}
	if !reflect.DeepEqual(got.auth, wantAuth) {
		t.Errorf("auth = %v, want %v", got.auth, wantAuth)
	}
	if !reflect.DeepEqual(got.other, wantOther) {
		t.Errorf("other = %v, want %v", got.other, wantOther)
	}
}
```

We're testing a small pure helper (`classifyFailures`) — that's the testable surface. We'll need sentinels for the test to use.

- [ ] **Step 2: Rewrite export.go**

Replace `internal/cli/export.go` (large change — the logic is preserved per-account, just wrapped):

```go
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thinkjk/gxodus/internal/accounts"
	"github.com/thinkjk/gxodus/internal/auth"
	"github.com/thinkjk/gxodus/internal/config"
	"github.com/thinkjk/gxodus/internal/downloader"
	"github.com/thinkjk/gxodus/internal/extractor"
	"github.com/thinkjk/gxodus/internal/notify"
	"github.com/thinkjk/gxodus/internal/poller"
	"github.com/thinkjk/gxodus/internal/takeoutapi"
)

var (
	outputDir       string
	extract         bool
	noKeepZip       bool
	pollInterval    string
	fileSize        string
	fileType        string
	frequency       string
	noActivityLogs  bool
	resumeUUID      string
	exportAccount   string
)

// Sentinel errors so classifyFailures can group results without
// re-importing takeoutapi types.
var (
	errSessionExpiredSentinel = errors.New("session expired (sentinel)")
	errSomeOtherFailure       = errors.New("other failure (sentinel)")
)

type accountResult struct {
	Email string
	Err   error // nil = success
}

// classifyFailures partitions per-account results into auth-failures
// (use ErrSessionExpired sentinel) and other failures. Successful
// results contribute to neither.
func classifyFailures(results []accountResult) (auth, other []string) {
	for _, r := range results {
		if r.Err == nil {
			continue
		}
		if errors.Is(r.Err, takeoutapi.ErrSessionExpired) || errors.Is(r.Err, errSessionExpiredSentinel) {
			auth = append(auth, r.Email)
			continue
		}
		other = append(other, r.Email)
	}
	return auth, other
}

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export data from Google Takeout (all configured accounts by default)",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		applyFlagOverrides(cfg)

		all, err := accounts.ScanAccounts()
		if err != nil {
			return fmt.Errorf("scanning accounts: %w", err)
		}
		if len(all) == 0 {
			fmt.Fprintln(os.Stderr, "No accounts configured. Run 'gxodus auth' to add one.")
			notify.Fire(cfg.Notify, "auth_expired", notify.EventData{Error: "no accounts configured"})
			os.Exit(1)
		}

		// Filter by --account if provided.
		if exportAccount != "" {
			filtered := []accounts.Account{}
			for _, a := range all {
				if a.Email == exportAccount {
					filtered = append(filtered, a)
					break
				}
			}
			if len(filtered) == 0 {
				return fmt.Errorf("--account %q not found", exportAccount)
			}
			all = filtered
		}

		var results []accountResult
		for _, a := range all {
			if !a.HasSession {
				fmt.Printf("[%s] no session.enc; skipping. Run 'gxodus auth --account %s' to set up.\n", a.Email, a.Email)
				results = append(results, accountResult{Email: a.Email, Err: errSomeOtherFailure})
				continue
			}
			err := runExportForAccount(ctx, a.Email, a.Dir, cfg)
			results = append(results, accountResult{Email: a.Email, Err: err})
		}

		authFails, otherFails := classifyFailures(results)
		if len(authFails) > 0 {
			if err := writeFailedAccounts(authFails); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not write .failed-accounts: %v\n", err)
			}
			os.Exit(1)
		}
		if len(otherFails) > 0 {
			os.Exit(3)
		}
		fmt.Println("All accounts complete.")
		return nil
	},
}

func runExportForAccount(ctx context.Context, email, accountDir string, cfg *config.Config) error {
	fmt.Printf("\n=== Account: %s ===\n", email)

	cookies, err := auth.LoadSession(accountDir)
	if err != nil {
		notify.Fire(cfg.Notify, "auth_expired", notify.EventData{Account: email, Error: err.Error()})
		return err
	}
	fmt.Printf("[%s] Loaded %d cookies from saved session.\n", email, len(cookies))

	client, err := takeoutapi.NewClient(cookies, 0)
	if err != nil {
		notify.Fire(cfg.Notify, "error", notify.EventData{Account: email, Error: err.Error()})
		return err
	}

	products := defaultProductSlugs()
	if cfg.ActivityLogs {
		products = append(products, "bond")
	}

	sizeBytes, err := parseFileSize(cfg.FileSize)
	if err != nil {
		notify.Fire(cfg.Notify, "error", notify.EventData{Account: email, Error: err.Error()})
		return err
	}

	var trackUUID string
	switch {
	case resumeUUID != "":
		fmt.Printf("[%s] Resuming export (uuid=%s, --export-uuid flag) — skipping CreateExport.\n", email, resumeUUID)
		trackUUID = resumeUUID
	default:
		if persisted, err := readPendingExport(accountDir); err != nil {
			fmt.Fprintf(os.Stderr, "[%s] warning: could not read pending-export marker: %v\n", email, err)
		} else if persisted != "" {
			fmt.Printf("[%s] Resuming export (uuid=%s) — skipping CreateExport.\n", email, persisted)
			trackUUID = persisted
		}
	}

	if trackUUID == "" {
		newExport, err := client.CreateExport(ctx, takeoutapi.CreateExportOptions{
			Products:  products,
			Format:    strings.ToUpper(cfg.FileType),
			SizeBytes: sizeBytes,
			Frequency: cfg.Frequency,
		})
		if err != nil {
			if errors.Is(err, takeoutapi.ErrSessionExpired) {
				fmt.Fprintf(os.Stderr, "[%s] Session expired — cookies are stale and need re-auth via noVNC.\n", email)
				notify.Fire(cfg.Notify, "auth_expired", notify.EventData{Account: email, Error: err.Error()})
				return err
			}
			fmt.Fprintf(os.Stderr, "[%s] CreateExport failed: %v\n", email, err)
			notify.Fire(cfg.Notify, "error", notify.EventData{Account: email, Error: err.Error()})
			return err
		}
		if newExport.UUID == "" {
			err := fmt.Errorf("CreateExport returned no UUID")
			notify.Fire(cfg.Notify, "error", notify.EventData{Account: email, Error: err.Error()})
			return err
		}
		trackUUID = newExport.UUID
		fmt.Printf("[%s] Export submitted (uuid=%s)\n", email, trackUUID)
		if err := writePendingExport(accountDir, trackUUID); err != nil {
			fmt.Fprintf(os.Stderr, "[%s] warning: could not persist pending-export marker: %v\n", email, err)
		}
	}
	notify.Fire(cfg.Notify, "export_started", notify.EventData{Account: email})

	pollDuration, err := cfg.PollDuration()
	if err != nil {
		return fmt.Errorf("invalid poll interval: %w", err)
	}

	pollResult, err := poller.Poll(ctx, poller.Config{
		Interval:   pollDuration,
		Cookies:    cookies,
		ExportUUID: trackUUID,
	})
	if err != nil {
		if errors.Is(err, takeoutapi.ErrSessionExpired) {
			fmt.Fprintf(os.Stderr, "[%s] Session expired during poll.\n", email)
			notify.Fire(cfg.Notify, "auth_expired", notify.EventData{Account: email, Error: err.Error()})
			return err
		}
		fmt.Fprintf(os.Stderr, "[%s] Poll failed: %v\n", email, err)
		notify.Fire(cfg.Notify, "error", notify.EventData{Account: email, Error: err.Error()})
		return err
	}

	resolvedOutput := filepath.Join(cfg.ResolveOutputDir(), email)
	dlResult, err := downloader.Download(ctx, pollResult.DownloadURLs, resolvedOutput, cookies, cfg.Notify, accountDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[%s] Download failed: %v\n", email, err)
		notify.Fire(cfg.Notify, "error", notify.EventData{Account: email, Error: err.Error()})
		return err
	}

	fmt.Printf("[%s] Downloaded %d file(s), total size: %s\n", email, len(dlResult.Files), formatSize(dlResult.TotalSize))

	if err := clearPendingExport(accountDir); err != nil {
		fmt.Fprintf(os.Stderr, "[%s] warning: could not clear pending-export marker: %v\n", email, err)
	}

	if cfg.Extract {
		extResult, err := extractor.Extract(dlResult.Files, resolvedOutput, extractor.Options{KeepZip: cfg.KeepZip})
		if err != nil {
			notify.Fire(cfg.Notify, "error", notify.EventData{Account: email, Error: err.Error()})
			return fmt.Errorf("extracting archives: %w", err)
		}
		fmt.Printf("[%s] Extracted %d files to %s\n", email, extResult.FileCount, extResult.OutputDir)
	}

	notify.Fire(cfg.Notify, "export_complete", notify.EventData{
		Account:    email,
		OutputPath: resolvedOutput,
		ExportSize: dlResult.TotalSize,
		Duration:   pollResult.Duration,
	})
	fmt.Printf("[%s] Done.\n", email)
	return nil
}

func applyFlagOverrides(cfg *config.Config) {
	if outputDir != "" {
		cfg.OutputDir = outputDir
	}
	if extract {
		cfg.Extract = true
	}
	if noKeepZip {
		cfg.KeepZip = false
	}
	if pollInterval != "" {
		cfg.PollInterval = pollInterval
	}
	if fileSize != "" {
		cfg.FileSize = fileSize
	}
	if fileType != "" {
		cfg.FileType = fileType
	}
	if frequency != "" {
		cfg.Frequency = frequency
	}
	if noActivityLogs {
		cfg.ActivityLogs = false
	}
}

func init() {
	exportCmd.Flags().StringVarP(&outputDir, "output", "o", "", "output directory for downloaded archives")
	exportCmd.Flags().BoolVar(&extract, "extract", false, "extract archives into organized directories")
	exportCmd.Flags().BoolVar(&noKeepZip, "no-keep-zip", false, "remove ZIP files after extraction (requires --extract)")
	exportCmd.Flags().StringVar(&pollInterval, "poll-interval", "", "poll interval for checking export status (e.g., 5m, 10m)")
	exportCmd.Flags().StringVar(&fileSize, "file-size", "", "archive split size (1GB, 2GB, 4GB, 10GB, 50GB)")
	exportCmd.Flags().StringVar(&fileType, "file-type", "", "archive type (zip, tgz)")
	exportCmd.Flags().StringVar(&frequency, "frequency", "", "export frequency (once, every_2_months)")
	exportCmd.Flags().BoolVar(&noActivityLogs, "no-activity-logs", false, "skip the Access Log Activity item")
	exportCmd.Flags().StringVar(&resumeUUID, "export-uuid", "", "skip CreateExport and resume polling an existing export by UUID")
	exportCmd.Flags().StringVar(&exportAccount, "account", "", "limit export to a single account (default: all configured accounts)")
	rootCmd.AddCommand(exportCmd)
}
```

Note the new `accountDir` parameter on `downloader.Download` — that's added in Task 9. (Without it, the downloader's `tmpDir` would still be `$CONFIG_DIR/downloads-tmp`, shared across accounts, which would race when running sequentially across accounts that complete around the same time.)

- [ ] **Step 3: Build to verify**

```
go build ./internal/cli/... 2>&1 | head -10
```

Expected: errors at `downloader.Download` callsite (Task 9 fixes), at `status.go` (Task 12), and at `debug_api.go` (Task 12). All other CLI files clean.

- [ ] **Step 4: Run the export classifyFailures test**

```
go test ./internal/cli/ -run TestSelectFailedAccountsForRetry -v
```

This will fail to compile because of the export.go build errors. We'll defer the test run until after Task 9. Skip step 4 here and continue to commit.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/export.go internal/cli/export_test.go
git commit -m "$(cat <<'EOF'
Refactor export.go to iterate over accounts via runExportForAccount

The pipeline (load session → CreateExport → poll → download → extract
→ notify) is preserved per-account; the RunE wraps it in a per-account
loop. Per-account isolation: a failure in account A logs + fires
notify(Account=A) and continues to B. Final exit code is 1 if any
account had ErrSessionExpired (writes .failed-accounts for the
entrypoint to consume), else 3 if any other failure, else 0.

The downloader.Download call now takes accountDir so its tmpDir can
scope per-account (added in next task — build is broken until then).
classifyFailures helper extracted for unit-testability.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 9: Add accountDir to downloader + add `failed_accounts.go` helper

**Files:**
- Modify: `internal/downloader/downloader.go`
- Create: `internal/cli/failed_accounts.go`

- [ ] **Step 1: Update downloader.Download signature**

In `internal/downloader/downloader.go`, change `Download(ctx, urls, outputDir, cookies, notifyCfg)` to accept an additional `accountDir string` parameter (last argument).

Find:

```go
func Download(ctx context.Context, urls []string, outputDir string, cookies []*http.Cookie, notifyCfg config.NotifyConfig) (*Result, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}

	tmpDir := filepath.Join(config.ConfigDir(), "downloads-tmp")
```

Replace with:

```go
func Download(ctx context.Context, urls []string, outputDir string, cookies []*http.Cookie, notifyCfg config.NotifyConfig, accountDir string) (*Result, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}

	// Per-account tmp dir so concurrent accounts (future) and sequential
	// retries don't fight over the same downloads-tmp.
	tmpDir := filepath.Join(accountDir, "downloads-tmp")
```

Also update `browser.NewContext` call's `UserDataDir`:

Find:

```go
	bctx, cancel, err := browser.NewContext(ctx, browser.Options{
		Headless:    false,
		UserDataDir: browser.ProfileDir(),
	})
```

Replace with:

```go
	bctx, cancel, err := browser.NewContext(ctx, browser.Options{
		Headless:    false,
		UserDataDir: browser.ProfileDir(accountDir),
	})
```

And `clearStaleProfileLock(browser.ProfileDir())` → `clearStaleProfileLock(browser.ProfileDir(accountDir))`.

- [ ] **Step 2: Update integration test signature**

In `internal/downloader/downloader_integration_test.go`, find the `TestDownload_HappyPath_OneFile` test and update both `Download(...)` calls to pass an empty/temp-dir accountDir:

```go
	res, err := Download(context.Background(), []string{srv.URL + "/hello.zip"}, outDir, nil, config.NotifyConfig{}, t.TempDir())
```

Apply the same pattern to `TestDownload_RecoversAfterChallenge` (the skipped one).

- [ ] **Step 3: Add failed_accounts.go**

Create `internal/cli/failed_accounts.go`:

```go
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thinkjk/gxodus/internal/config"
)

// failed_accounts is a newline-separated list of account emails that
// hit ErrSessionExpired during the most recent export cycle. The docker
// entrypoint reads this file on exit-1 and wipes only the listed
// sessions (rather than all of them) before re-running auth.

func failedAccountsPath() string {
	return filepath.Join(config.ConfigDir(), ".failed-accounts")
}

func writeFailedAccounts(emails []string) error {
	if err := config.EnsureConfigDir(); err != nil {
		return err
	}
	body := strings.Join(emails, "\n") + "\n"
	return os.WriteFile(failedAccountsPath(), []byte(body), 0600)
}

func readFailedAccounts() ([]string, error) {
	data, err := os.ReadFile(failedAccountsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", failedAccountsPath(), err)
	}
	out := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out, nil
}
```

- [ ] **Step 4: Build and run all tests**

```
go build ./...
go test ./...
```

Expected: builds clean. The `TestSelectFailedAccountsForRetry` test now runs (and passes — `classifyFailures` was implemented in Task 8).

- [ ] **Step 5: Commit**

```bash
git add internal/downloader/downloader.go internal/downloader/downloader_integration_test.go internal/cli/failed_accounts.go
git commit -m "$(cat <<'EOF'
downloader: per-account tmpDir + chrome-profile; add failed_accounts helper

Download() now takes accountDir so tmpDir + chromedp UserDataDir scope
per-account (avoids races and ensures each account's persistent profile
is the one used for its downloads). Integration test updated to pass
a temp dir.

Also add internal/cli/failed_accounts.go with read/write helpers for
$CONFIG_DIR/.failed-accounts, the file the docker entrypoint will
consume to wipe only the right sessions on exit-1.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 10: `gxodus list-accounts`

**Files:**
- Create: `internal/cli/list_accounts.go`
- Create: `internal/cli/list_accounts_test.go`

- [ ] **Step 1: Write the failing test for the row-builder**

Create `internal/cli/list_accounts_test.go`:

```go
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
```

- [ ] **Step 2: Run test (fails — function doesn't exist)**

```
go test ./internal/cli/ -run TestBuildAccountRows -v
```

Expected: build error.

- [ ] **Step 3: Implement list_accounts.go**

Create `internal/cli/list_accounts.go`:

```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/thinkjk/gxodus/internal/accounts"
)

var listAccountsCmd = &cobra.Command{
	Use:   "list-accounts",
	Short: "List configured Google accounts and their session/marker status",
	RunE: func(cmd *cobra.Command, args []string) error {
		all, err := accounts.ScanAccounts()
		if err != nil {
			return fmt.Errorf("scanning accounts: %w", err)
		}
		if len(all) == 0 {
			fmt.Println("No accounts configured. Run 'gxodus auth' to add one.")
			return nil
		}
		pending := map[string]string{}
		for _, a := range all {
			uuid, _ := readPendingExport(a.Dir)
			pending[a.Email] = uuid
		}
		rows := buildAccountRows(all, pending)
		fmt.Printf("%-40s %-12s %s\n", "EMAIL", "SESSION", "PENDING")
		for _, r := range rows {
			fmt.Println(r)
		}
		return nil
	},
}

// buildAccountRows produces one display row per account. Pure function
// for testability.
func buildAccountRows(all []accounts.Account, pending map[string]string) []string {
	out := make([]string, 0, len(all))
	for _, a := range all {
		sess := "✗ no session"
		if a.HasSession {
			sess = "✓ valid"
		}
		p := pending[a.Email]
		if p == "" {
			p = "-"
		}
		out = append(out, fmt.Sprintf("%-40s %-12s %s", a.Email, sess, p))
	}
	return out
}

func init() {
	rootCmd.AddCommand(listAccountsCmd)
}
```

- [ ] **Step 4: Run tests + smoke check**

```
go test ./internal/cli/ -run TestBuildAccountRows -v
go run ./cmd/gxodus list-accounts
```

Test should PASS. The smoke run will print "No accounts configured." (since you're running locally without an `accounts/` dir).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/list_accounts.go internal/cli/list_accounts_test.go
git commit -m "$(cat <<'EOF'
Add gxodus list-accounts command

Prints one row per account with session status and any in-flight
pending-export UUID. buildAccountRows is a pure function so the row
formatting is unit-tested independently of FS access.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 11: `gxodus remove-account`

**Files:**
- Create: `internal/cli/remove_account.go`

- [ ] **Step 1: Implement the command**

Create `internal/cli/remove_account.go`:

```go
package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/thinkjk/gxodus/internal/accounts"
	"github.com/thinkjk/gxodus/internal/config"
)

var (
	removeKeepExports bool
	removeForce       bool
)

var removeAccountCmd = &cobra.Command{
	Use:   "remove-account <email>",
	Short: "Remove a configured account (deletes session + chrome-profile + exports)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		email := args[0]
		dir := accounts.AccountDir(email)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return fmt.Errorf("account %s not found at %s", email, dir)
		}

		// Refuse if a download is in flight unless --force.
		if !removeForce {
			if uuid, _ := readPendingExport(dir); uuid != "" {
				return fmt.Errorf("account %s has an in-flight export (%s); pass --force to remove anyway", email, uuid)
			}
		}

		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("removing account dir: %w", err)
		}
		fmt.Printf("Removed %s\n", dir)

		if !removeKeepExports {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not load config to find output dir: %v\n", err)
			} else {
				outputDir := filepath.Join(cfg.ResolveOutputDir(), email)
				if err := os.RemoveAll(outputDir); err != nil {
					fmt.Fprintf(os.Stderr, "warning: could not remove %s: %v\n", outputDir, err)
				} else {
					fmt.Printf("Removed %s (use --keep-exports to skip next time)\n", outputDir)
				}
			}
		}
		return nil
	},
}

func init() {
	removeAccountCmd.Flags().BoolVar(&removeKeepExports, "keep-exports", false, "don't delete the per-account output dir under $OUTPUT_DIR")
	removeAccountCmd.Flags().BoolVar(&removeForce, "force", false, "remove even if an export is in flight")
	rootCmd.AddCommand(removeAccountCmd)
}
```

- [ ] **Step 2: Build + smoke**

```
go build ./...
go run ./cmd/gxodus remove-account --help
```

Expected: build OK, help shows `--keep-exports` and `--force`.

- [ ] **Step 3: Commit**

```bash
git add internal/cli/remove_account.go
git commit -m "$(cat <<'EOF'
Add gxodus remove-account command

Deletes $CONFIG_DIR/accounts/<email>/ and (by default) the matching
$OUTPUT_DIR/<email>/. Refuses if a marker file shows an in-flight
export unless --force; --keep-exports preserves the output dir.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 12: Update `status.go` and `debug_api.go` for `--account`

**Files:**
- Modify: `internal/cli/status.go`
- Modify: `internal/cli/debug_api.go`

- [ ] **Step 1: Update status.go**

Find the existing `auth.SessionExists()` and `auth.LoadSession()` calls in `internal/cli/status.go` and rewrite to pick an account:

```go
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/thinkjk/gxodus/internal/accounts"
	"github.com/thinkjk/gxodus/internal/auth"
	"github.com/thinkjk/gxodus/internal/takeoutapi"
)

var statusAccount string

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show in-progress export status (default: first configured account)",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		all, err := accounts.ScanAccounts()
		if err != nil {
			return fmt.Errorf("scanning accounts: %w", err)
		}
		acct, err := pickSingleAccount(all, statusAccount)
		if err != nil {
			return err
		}
		if !acct.HasSession {
			return fmt.Errorf("account %s has no session.enc; run 'gxodus auth --account %s'", acct.Email, acct.Email)
		}
		cookies, err := auth.LoadSession(acct.Dir)
		if err != nil {
			return fmt.Errorf("loading session: %w", err)
		}
		client, err := takeoutapi.NewClient(cookies, 0)
		if err != nil {
			return fmt.Errorf("creating client: %w", err)
		}
		exports, err := client.ListExports(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%s] list exports failed: %v\n", acct.Email, err)
			return err
		}
		fmt.Printf("[%s] %d exports:\n", acct.Email, len(exports))
		for _, e := range exports {
			fmt.Printf("  %s  status=%v  size=%d\n", e.UUID, e.Status, e.TotalBytes)
		}
		return nil
	},
}

// pickSingleAccount resolves a single account from the configured set.
// If --account flag is given, finds that one. If exactly 1 account
// exists, picks it. If 0 or 2+ and no flag, errors with guidance.
func pickSingleAccount(all []accounts.Account, flag string) (*accounts.Account, error) {
	if flag != "" {
		for i := range all {
			if all[i].Email == flag {
				return &all[i], nil
			}
		}
		return nil, fmt.Errorf("--account %q not found", flag)
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("no accounts configured; run 'gxodus auth'")
	}
	if len(all) == 1 {
		return &all[0], nil
	}
	emails := make([]string, len(all))
	for i, a := range all {
		emails[i] = a.Email
	}
	return nil, fmt.Errorf("multiple accounts configured (%v); use --account <email>", emails)
}

func init() {
	statusCmd.Flags().StringVar(&statusAccount, "account", "", "target a specific account by email")
	rootCmd.AddCommand(statusCmd)
}
```

- [ ] **Step 2: Update debug_api.go**

In `internal/cli/debug_api.go`, the existing debug commands all do something like:

```go
client, err := newDebugClient(debugUserIdx)
```

Where `newDebugClient` does `auth.SessionExists()`/`auth.LoadSession()` directly. Update `newDebugClient` and add an account flag.

Replace `newDebugClient`:

```go
func newDebugClient(userIdx int) (*takeoutapi.Client, error) {
	all, err := accounts.ScanAccounts()
	if err != nil {
		return nil, fmt.Errorf("scanning accounts: %w", err)
	}
	acct, err := pickSingleAccount(all, debugAccount)
	if err != nil {
		return nil, err
	}
	if !acct.HasSession {
		return nil, fmt.Errorf("account %s has no session.enc", acct.Email)
	}
	cookies, err := auth.LoadSession(acct.Dir)
	if err != nil {
		return nil, fmt.Errorf("loading session: %w", err)
	}
	return takeoutapi.NewClient(cookies, userIdx)
}
```

Add the package-level `debugAccount` var and bind it as a flag on each debug command (in their respective `init()` registrations or in the shared init). Simplest: add it to the shared init function for all debug commands:

```go
var debugAccount string

// In init() (or wherever debug-* flags are registered):
for _, cmd := range []*cobra.Command{
    debugAPICmd, debugTokensCmd, debugListCmd, debugCreateCmd, debugDownloadCmd,
} {
    cmd.Flags().StringVar(&debugAccount, "account", "", "target a specific account by email")
}
```

(If the existing init() does each registration separately, just add the line for each; reuse of the same variable across multiple commands is fine because they're invoked one at a time.)

The `debug-download` command also has its own session-loading logic — update it to use `pickSingleAccount` similarly. Find:

```go
if !auth.SessionExists() {
    return fmt.Errorf("no saved session — run 'gxodus auth' first")
}
cookies, err := auth.LoadSession()
```

Replace with:

```go
all, err := accounts.ScanAccounts()
if err != nil {
    return fmt.Errorf("scanning accounts: %w", err)
}
acct, err := pickSingleAccount(all, debugAccount)
if err != nil {
    return err
}
if !acct.HasSession {
    return fmt.Errorf("account %s has no session.enc", acct.Email)
}
cookies, err := auth.LoadSession(acct.Dir)
```

And update the subsequent `downloader.Download` call to pass `acct.Dir` as the new `accountDir` argument.

Add `"github.com/thinkjk/gxodus/internal/accounts"` to imports.

- [ ] **Step 3: Build and run all tests**

```
go build ./...
go test ./...
```

Expected: build clean, all tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/status.go internal/cli/debug_api.go
git commit -m "$(cat <<'EOF'
status + debug-* commands: --account flag + pickSingleAccount helper

Both status and the hidden debug-* commands now operate on a single
account at a time. Default behavior: error if 0 accounts, pick the
sole account if exactly 1, error with guidance if 2+. Pass --account
<email> to disambiguate.

pickSingleAccount lives in status.go and is reused by debug commands.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 13: Update `docker-entrypoint.sh` for `.failed-accounts`

**Files:**
- Modify: `docker-entrypoint.sh`

- [ ] **Step 1: Replace the auth-failure recovery block**

In `docker-entrypoint.sh`, find the existing block that handles exit code 1 (around the `if [ "$EXIT" -eq 1 ]; then` line). Replace it with logic that reads `.failed-accounts`:

Find:

```sh
            if run_export_once; then
                EXIT=0
            else
                EXIT=$?
            fi

            SLEEP_FOR="$GXODUS_INTERVAL"
            if [ "$EXIT" -eq 1 ]; then
                # gxodus export exits 1 on auth failure (notify hook fires).
                # Wipe session so next cycle re-auths via noVNC, and use the
                # short retry interval so the user can recover quickly.
                echo "Auth expired or failed. Wiping session — next cycle will re-auth via noVNC."
                rm -f "$SESSION_FILE"
                SLEEP_FOR="$AUTH_RETRY_INTERVAL"
                echo "Auth retry: will retry in $SLEEP_FOR (override with GXODUS_AUTH_RETRY) instead of $GXODUS_INTERVAL."
            elif [ "$EXIT" -ne 0 ]; then
                echo "Export failed with exit $EXIT — will retry next cycle."
            fi
```

Replace with:

```sh
            if run_export_once; then
                EXIT=0
            else
                EXIT=$?
            fi

            SLEEP_FOR="$GXODUS_INTERVAL"
            if [ "$EXIT" -eq 1 ]; then
                FAILED_FILE="${CONFIG_DIR}/.failed-accounts"
                if [ -f "$FAILED_FILE" ]; then
                    echo "Auth expired for the following account(s):"
                    cat "$FAILED_FILE"
                    while read -r email; do
                        [ -z "$email" ] && continue
                        ACCOUNT_DIR="${CONFIG_DIR}/accounts/${email}"
                        echo "Wiping session for $email and queuing re-auth."
                        rm -f "${ACCOUNT_DIR}/session.enc"
                    done < "$FAILED_FILE"
                    rm -f "$FAILED_FILE"
                    # Run gxodus auth for each failed account, in order.
                    # User completes the noVNC flow for each in turn.
                    while IFS= read -r email; do
                        [ -z "$email" ] && continue
                        echo "Running gxodus auth --account $email ..."
                        gxodus auth --account "$email" "$CONFIG_ARG" "$CONFIG_VAL" || \
                            echo "auth for $email did not complete; will retry next cycle"
                    done < <(cat "${FAILED_FILE}.bak" 2>/dev/null || true)
                else
                    # Defensive: no .failed-accounts file. Could be no accounts
                    # configured at all, or an exit-1 from a different code path.
                    echo "No .failed-accounts file found despite exit-1; running gxodus auth (no flag)."
                    gxodus auth "$CONFIG_ARG" "$CONFIG_VAL" || \
                        echo "auth did not complete; will retry next cycle"
                fi
                SLEEP_FOR="$AUTH_RETRY_INTERVAL"
                echo "Auth retry: will retry in $SLEEP_FOR (override with GXODUS_AUTH_RETRY) instead of $GXODUS_INTERVAL."
            elif [ "$EXIT" -ne 0 ]; then
                echo "Export failed with exit $EXIT — will retry next cycle."
            fi
```

Wait — the loop reads `.failed-accounts` then deletes it then reads `${FAILED_FILE}.bak`. That's wrong (bak doesn't exist). Fix: keep a copy in a shell var before deleting.

Replace the inner `if [ -f "$FAILED_FILE" ]` block with this cleaner version:

```sh
                FAILED_FILE="${CONFIG_DIR}/.failed-accounts"
                if [ -f "$FAILED_FILE" ]; then
                    FAILED_EMAILS=$(cat "$FAILED_FILE")
                    rm -f "$FAILED_FILE"
                    echo "Auth expired for the following account(s):"
                    echo "$FAILED_EMAILS"
                    # First wipe session.enc for each.
                    echo "$FAILED_EMAILS" | while IFS= read -r email; do
                        [ -z "$email" ] && continue
                        rm -f "${CONFIG_DIR}/accounts/${email}/session.enc"
                        echo "Wiped session for $email"
                    done
                    # Then run gxodus auth for each.
                    echo "$FAILED_EMAILS" | while IFS= read -r email; do
                        [ -z "$email" ] && continue
                        echo "Running gxodus auth --account $email"
                        gxodus auth --account "$email" "$CONFIG_ARG" "$CONFIG_VAL" || \
                            echo "auth for $email did not complete; will retry next cycle"
                    done
                else
                    echo "No .failed-accounts file found despite exit-1; running gxodus auth (no flag)."
                    gxodus auth "$CONFIG_ARG" "$CONFIG_VAL" || \
                        echo "auth did not complete; will retry next cycle"
                fi
```

Also make sure `CONFIG_DIR` is available in the entrypoint. The existing script uses `CONFIG_DIR="${GXODUS_CONFIG_DIR:-/config}"` near the top — confirm that and reuse it.

- [ ] **Step 2: Smoke test the script syntax**

```
sh -n docker-entrypoint.sh
```

Expected: no output (syntax OK).

- [ ] **Step 3: Commit**

```bash
git add docker-entrypoint.sh
git commit -m "$(cat <<'EOF'
entrypoint: read .failed-accounts and re-auth per account

On exit-1, $CONFIG_DIR/.failed-accounts (written by gxodus export)
lists the emails whose session.enc needs wiping. Iterate that file:
wipe each session, then run 'gxodus auth --account <email>' for each
in turn. If the file is missing (defensive), fall back to running
'gxodus auth' with no flag.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 14: README + migration doc

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add multi-account section**

In `README.md`, find the existing "How it works" or "Quick start" section and insert a new "Multi-account" section before "Configuration":

```markdown
## Multi-account

Each Google account is its own sign-in: separate cookies, separate
chromium profile, separate pending-export marker. All accounts share
one `config.toml` (same `poll_interval`, `file_size`, etc.) and run
sequentially on the same `GXODUS_INTERVAL` schedule.

### Adding an account

```sh
docker exec -it gxodus gxodus auth --new --config /config/config.toml
```

Chromium opens in noVNC. Log in, the email is scraped from the
account-chooser button on the takeout page, and a new
`$CONFIG_DIR/accounts/<email>/` directory is created with the cookies,
profile, and (eventually) pending marker. Repeat for each additional
account.

If an email scrape ever fails (Google changed the DOM), cookies are
saved to `$CONFIG_DIR/.pending-auth-<unix>/` with a clear log message
telling you which directory to rename to `accounts/<email>/`.

### Listing and removing accounts

```sh
docker exec gxodus gxodus list-accounts
```

```
EMAIL                                    SESSION       PENDING
jason@example.com                        ✓ valid       -
work@example.com                         ✓ valid       5430dfbb-...
spouse@example.com                       ✗ no session  -
```

```sh
# Remove (deletes session, profile, marker, AND $OUTPUT_DIR/<email>/)
docker exec gxodus gxodus remove-account spouse@example.com

# Keep the downloaded archives
docker exec gxodus gxodus remove-account spouse@example.com --keep-exports
```

### Refreshing a single account

```sh
docker exec -it gxodus gxodus auth --account jason@example.com --config /config/config.toml
```

Uses that account's existing chrome-profile so Google sees a trusted
device and (usually) skips the password challenge.

### Per-cycle behavior

`gxodus export` iterates `accounts/*` sequentially. Per-account
isolation: a failure for account A logs and continues to account B.
Pushover notifications include the account email in the title:
`gxodus: re-auth needed [jason@example.com]`. On exit-1 (any account
hit ErrSessionExpired), the entrypoint wipes only the failed
sessions and runs `gxodus auth --account <email>` for each in turn.
```

- [ ] **Step 2: Add migration block at the bottom**

Append a "Migration from single-account" section near the bottom:

```markdown
## Migration from single-account

The pre-multi-account layout had `$CONFIG_DIR/session.enc`,
`$CONFIG_DIR/chrome-profile/`, and `$CONFIG_DIR/pending_export.uuid`
at the config root. The new layout puts these inside
`$CONFIG_DIR/accounts/<email>/`. There's no auto-migration code —
move the files manually:

```sh
docker exec gxodus sh -c '
  EMAIL=jason@example.com   # whichever email the saved session belongs to
  mkdir -p /config/accounts/$EMAIL
  mv /config/session.enc        /config/accounts/$EMAIL/ 2>/dev/null || true
  mv /config/chrome-profile     /config/accounts/$EMAIL/ 2>/dev/null || true
  mv /config/pending_export.uuid /config/accounts/$EMAIL/ 2>/dev/null || true
'
```

If you don't know the email, pick any temporary value, then run
`gxodus auth --account <real-email>` once to refresh; the new
session lands in the correctly-named dir. Then `rm -rf` the
temporary dir.
```

- [ ] **Step 3: Update env-var table to mention --account-related ones**

If the env-var table in the README doesn't mention multi-account-aware behaviors, update the row for `GXODUS_INTERVAL` to note "applies to ALL accounts in the per-cycle iteration".

(This is a small cleanup — adjust the table only if it would mislead.)

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "$(cat <<'EOF'
README: document multi-account flow + manual migration

Adds a "Multi-account" section covering: gxodus auth --new for adding,
list-accounts / remove-account commands, --account targeting for
refresh, per-cycle isolation, and the email-in-Pushover-title
convention. Migration block tells single-account users how to move
session.enc + chrome-profile + pending_export.uuid into the new
accounts/<email>/ dir.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 15: Final verification + push

- [ ] **Step 1: Run the full test suite**

```
go test ./...
```

Expected: all PASS.

- [ ] **Step 2: Build**

```
go build ./...
```

Expected: clean.

- [ ] **Step 3: Push the feature branch**

```bash
git push -u origin feat/multi-account-backups
```

- [ ] **Step 4: Open a pull request**

```bash
gh pr create --base main --head feat/multi-account-backups \
  --title "Multi-account backups" \
  --body "$(cat <<'EOF'
## Summary

- Per-account directories under `\$CONFIG_DIR/accounts/<email>/` (session, chrome-profile, pending marker)
- Per-account output subdirs under `\$OUTPUT_DIR/<email>/`
- Sequential same-schedule iteration; per-account failure isolation
- New CLI: `gxodus auth --new`, `--account <email>`, `gxodus list-accounts`, `gxodus remove-account`
- Pushover/shell-hook notifications include `[<email>]` in titles
- Email auto-detected from the takeout account-chooser aria-label
- Docker entrypoint reads `.failed-accounts` to re-auth only the right accounts on exit-1

## Test plan

- [x] All unit tests pass (`go test ./...`)
- [ ] Manual: build container, run `gxodus auth --new` twice with two different Google accounts via noVNC
- [ ] Manual: confirm `list-accounts` shows both with `✓ valid`
- [ ] Manual: trigger `gxodus export`, observe sequential iteration in container logs
- [ ] Manual: verify both `\$OUTPUT_DIR/<email>/` populate
- [ ] Manual: wipe one account's session.enc, confirm only that account fires `auth_expired` Pushover

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 5: After PR merges, manually test on Unraid container**

After merging to main and ghcr publishes the new `:main` image:

```sh
docker pull ghcr.io/thinkjk/gxodus:main
docker compose up -d --force-recreate gxodus

# Migrate existing single-account data
docker exec gxodus sh -c '
  EMAIL=jason@example.com   # your real email
  mkdir -p /config/accounts/$EMAIL
  mv /config/session.enc /config/chrome-profile /config/pending_export.uuid \
     /config/accounts/$EMAIL/ 2>/dev/null
'
docker restart gxodus
docker exec gxodus gxodus list-accounts

# Add second account
docker exec -it gxodus gxodus auth --new --config /config/config.toml
# (log in via noVNC for the second Google account)

docker exec gxodus gxodus list-accounts
# Both accounts should show ✓ valid.
```

---

## Out of scope (intentionally not in this plan)

- Parallel cross-account downloads
- Per-account schedules / config overrides
- Auto-migration code from single-account layout
- Web UI for account management
