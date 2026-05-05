# chromedp Downloader + Pushover Integration — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the HTTP downloader (which silently saves Google's auth-redirect HTML as `.zip`) with a chromedp-based downloader that uses the existing chromium + persistent profile, surfaces re-auth challenges via noVNC + Pushover, and downloads real archives.

**Architecture:** New chromedp-driven `downloader.Download` opens a headed chromium against the container's Xvfb display, injects session cookies, sets `Browser.setDownloadBehavior` to a temp dir, and navigates to each download URL. CDP `Browser.downloadProgress` events drive completion detection; URL host check drives challenge detection. New `[notify.pushover]` config block adds first-class Pushover notifications layered on top of the existing per-event shell-hook system.

**Tech Stack:** Go 1.26, chromedp v0.15.1, cdproto, standard library net/http, pelletier/go-toml/v2.

**Spec:** `docs/superpowers/specs/2026-05-05-chromedp-downloader-design.md`

---

## File Structure

**Create:**
- `internal/notify/pushover.go` — `sendPushover(cfg, title, message)` HTTP client
- `internal/notify/pushover_test.go` — unit test against `httptest.Server`
- `internal/downloader/downloader_integration_test.go` — chromedp + httptest tests (build tag)

**Rewrite:**
- `internal/downloader/downloader.go` — chromedp-based; existing HTTP code deleted
- `internal/downloader/downloader_test.go` — tests for helpers (magic bytes, hostname, filename)

**Modify:**
- `internal/config/config.go` — add `PushoverConfig` struct + defaults
- `internal/notify/notify.go` — fire Pushover after shell hook in `Fire`
- `internal/cli/export.go` — pass `cookies` + `cfg.Notify` to `Download`
- `internal/cli/debug_api.go` — add `debug-download` command
- `README.md` — Pushover config docs

---

### Task 1: PushoverConfig struct + defaults

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/config/config_test.go`:

```go
func TestPushoverConfigDefaults(t *testing.T) {
	cfg := DefaultConfig()
	wantEvents := []string{"auth_expired", "export_complete", "error"}
	if !reflect.DeepEqual(cfg.Notify.Pushover.Events, wantEvents) {
		t.Errorf("Pushover.Events = %v, want %v", cfg.Notify.Pushover.Events, wantEvents)
	}
	if cfg.Notify.Pushover.Token != "" {
		t.Errorf("Token should default empty, got %q", cfg.Notify.Pushover.Token)
	}
}

func TestPushoverConfigParseTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
[notify.pushover]
token = "abc123"
user_key = "uk456"
events = ["auth_expired", "error"]
`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Notify.Pushover.Token != "abc123" {
		t.Errorf("Token = %q", cfg.Notify.Pushover.Token)
	}
	if cfg.Notify.Pushover.UserKey != "uk456" {
		t.Errorf("UserKey = %q", cfg.Notify.Pushover.UserKey)
	}
	wantEvents := []string{"auth_expired", "error"}
	if !reflect.DeepEqual(cfg.Notify.Pushover.Events, wantEvents) {
		t.Errorf("Events = %v, want %v", cfg.Notify.Pushover.Events, wantEvents)
	}
}
```

If `reflect` isn't already imported in the test file, add it.

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./internal/config/ -run 'TestPushoverConfig' -v
```

Expected: FAIL with `cfg.Notify.Pushover undefined` or similar.

- [ ] **Step 3: Implement PushoverConfig**

In `internal/config/config.go`, replace the `NotifyConfig` struct and update `DefaultConfig`:

```go
type PushoverConfig struct {
	Token   string   `toml:"token"`
	UserKey string   `toml:"user_key"`
	Events  []string `toml:"events"`
}

type NotifyConfig struct {
	OnAuthExpired    string         `toml:"on_auth_expired"`
	OnExportStarted  string         `toml:"on_export_started"`
	OnExportComplete string         `toml:"on_export_complete"`
	OnError          string         `toml:"on_error"`
	Pushover         PushoverConfig `toml:"pushover"`
}
```

In `DefaultConfig()`, set the default Events list:

```go
return &Config{
    OutputDir:    filepath.Join(homeDir(), "gxodus-exports"),
    PollInterval: "1h",
    Extract:      false,
    KeepZip:      true,
    FileSize:     "2GB",
    FileType:     "zip",
    Frequency:    "once",
    ActivityLogs: true,
    Notify: NotifyConfig{
        Pushover: PushoverConfig{
            Events: []string{"auth_expired", "export_complete", "error"},
        },
    },
}
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./internal/config/ -run 'TestPushoverConfig' -v
```

Expected: PASS for both new tests, plus all existing tests in the package still pass.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "Add PushoverConfig to NotifyConfig with sensible event defaults"
```

---

### Task 2: sendPushover HTTP client

**Files:**
- Create: `internal/notify/pushover.go`
- Create: `internal/notify/pushover_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/notify/pushover_test.go`:

```go
package notify

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/thinkjk/gxodus/internal/config"
)

func TestSendPushover_PostsExpectedForm(t *testing.T) {
	var captured url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "application/x-www-form-urlencoded") {
			t.Errorf("Content-Type = %q", ct)
		}
		_ = r.ParseForm()
		captured = r.PostForm
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":1,"request":"abc"}`))
	}))
	defer srv.Close()

	cfg := config.PushoverConfig{Token: "tk", UserKey: "uk"}
	err := sendPushoverTo(srv.URL, cfg, "Hello", "World")
	if err != nil {
		t.Fatalf("sendPushoverTo: %v", err)
	}
	if got := captured.Get("token"); got != "tk" {
		t.Errorf("token = %q", got)
	}
	if got := captured.Get("user"); got != "uk" {
		t.Errorf("user = %q", got)
	}
	if got := captured.Get("title"); got != "Hello" {
		t.Errorf("title = %q", got)
	}
	if got := captured.Get("message"); got != "World" {
		t.Errorf("message = %q", got)
	}
}

func TestSendPushover_ErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":0,"errors":["invalid token"]}`))
	}))
	defer srv.Close()

	err := sendPushoverTo(srv.URL, config.PushoverConfig{Token: "tk", UserKey: "uk"}, "T", "M")
	if err == nil {
		t.Fatal("expected error for non-2xx response, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should mention status 400: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./internal/notify/ -run 'TestSendPushover' -v
```

Expected: FAIL with `undefined: sendPushoverTo`.

- [ ] **Step 3: Implement sendPushover**

Create `internal/notify/pushover.go`:

```go
package notify

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/thinkjk/gxodus/internal/config"
)

const pushoverAPIURL = "https://api.pushover.net/1/messages.json"

// sendPushover posts a message to the Pushover API. No retries; logging
// errors at the call site is sufficient. Returns nil if cfg has no token
// (caller may pass a zero-value config defensively).
func sendPushover(cfg config.PushoverConfig, title, message string) error {
	if cfg.Token == "" || cfg.UserKey == "" {
		return nil
	}
	return sendPushoverTo(pushoverAPIURL, cfg, title, message)
}

// sendPushoverTo is the testable form — caller chooses the endpoint URL.
func sendPushoverTo(endpoint string, cfg config.PushoverConfig, title, message string) error {
	form := url.Values{
		"token":   {cfg.Token},
		"user":    {cfg.UserKey},
		"title":   {title},
		"message": {message},
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(endpoint, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("posting to pushover: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("pushover returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./internal/notify/ -run 'TestSendPushover' -v
```

Expected: PASS for both tests.

- [ ] **Step 5: Commit**

```bash
git add internal/notify/pushover.go internal/notify/pushover_test.go
git commit -m "Add sendPushover HTTP client with httptest-backed unit tests"
```

---

### Task 3: Wire Pushover into notify.Fire

**Files:**
- Modify: `internal/notify/notify.go`
- Modify: `internal/notify/pushover_test.go`

- [ ] **Step 1: Write the failing test**

Append the imports `"sync"` and `"sync/atomic"` to `internal/notify/pushover_test.go` (in the existing import block — keep the imports added in Task 2).

Then append this test to the same file:

```go
func TestFire_FiresPushoverForListedEvents(t *testing.T) {
	var hits int32
	var mu sync.Mutex
	captured := map[string]url.Values{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		captured[r.PostForm.Get("title")] = r.PostForm
		mu.Unlock()
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	pushoverEndpointOverride = srv.URL
	defer func() { pushoverEndpointOverride = "" }()

	cfg := config.NotifyConfig{
		Pushover: config.PushoverConfig{
			Token:   "tk",
			UserKey: "uk",
			Events:  []string{"auth_expired", "error"},
		},
	}

	// auth_expired is in events list — should fire.
	Fire(cfg, "auth_expired", EventData{})
	// export_started is NOT in events list — should be skipped.
	Fire(cfg, "export_started", EventData{})
	// error is in events list — should fire.
	Fire(cfg, "error", EventData{Error: "boom"})

	// Fire spawns goroutines; give them a tick.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&hits) < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("Pushover hits = %d, want 2", got)
	}
	mu.Lock()
	if _, ok := captured["gxodus: re-auth needed"]; !ok {
		t.Errorf("missing auth_expired notification; captured titles: %v", keysOf(captured))
	}
	if v := captured["gxodus: error"]; v.Get("message") != "boom" {
		t.Errorf("error message = %q, want boom", v.Get("message"))
	}
	mu.Unlock()
}

func keysOf(m map[string]url.Values) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./internal/notify/ -run 'TestFire_FiresPushover' -v
```

Expected: FAIL — Fire doesn't dispatch to Pushover yet, so hits stays 0.

- [ ] **Step 3: Add Pushover dispatch to Fire**

First, add the test override hook to `internal/notify/pushover.go` (production file — production code references it; tests assign to it):

```go
// pushoverEndpointOverride redirects Fire's Pushover POST to a different
// URL. Set by tests; "" in production means use pushoverAPIURL.
var pushoverEndpointOverride string
```

Then in `internal/notify/notify.go`, replace the `Fire` function with:

```go
// Fire executes the notification hook for the given event.
// Runs the configured shell command (if any) AND fires Pushover (if
// configured and the event is in cfg.Pushover.Events). Both are
// non-blocking; errors are logged but never propagated.
func Fire(cfg config.NotifyConfig, event string, data EventData) {
	fireShellHook(cfg, event, data)
	firePushover(cfg, event, data)
}

func fireShellHook(cfg config.NotifyConfig, event string, data EventData) {
	var cmdTemplate string

	switch event {
	case "auth_expired":
		cmdTemplate = cfg.OnAuthExpired
	case "export_started":
		cmdTemplate = cfg.OnExportStarted
	case "export_complete":
		cmdTemplate = cfg.OnExportComplete
	case "error":
		cmdTemplate = cfg.OnError
	default:
		return
	}

	if cmdTemplate == "" {
		return
	}

	rendered, err := renderTemplate(cmdTemplate, data)
	if err != nil {
		fmt.Printf("Warning: failed to render notification template for %s: %v\n", event, err)
		return
	}

	go func() {
		cmd := exec.Command("sh", "-c", rendered)
		output, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Printf("Warning: notification hook '%s' failed: %v\nOutput: %s\n", event, err, string(output))
		}
	}()
}

func firePushover(cfg config.NotifyConfig, event string, data EventData) {
	if cfg.Pushover.Token == "" || cfg.Pushover.UserKey == "" {
		return
	}
	enabled := false
	for _, e := range cfg.Pushover.Events {
		if e == event {
			enabled = true
			break
		}
	}
	if !enabled {
		return
	}

	title, message := pushoverMessageFor(event, data)
	endpoint := pushoverAPIURL
	if pushoverEndpointOverride != "" {
		endpoint = pushoverEndpointOverride
	}

	go func() {
		if err := sendPushoverTo(endpoint, cfg.Pushover, title, message); err != nil {
			fmt.Printf("Warning: pushover for '%s' failed: %v\n", event, err)
		}
	}()
}

func pushoverMessageFor(event string, data EventData) (title, message string) {
	host := os.Getenv("GXODUS_PUBLIC_HOSTNAME")
	if host == "" {
		if h, err := os.Hostname(); err == nil {
			host = h
		} else {
			host = "the gxodus container"
		}
	}
	switch event {
	case "auth_expired":
		return "gxodus: re-auth needed",
			fmt.Sprintf("Open noVNC at %s:6080/vnc.html and complete the password challenge.", host)
	case "export_complete":
		return "gxodus: export ready",
			fmt.Sprintf("Downloaded %d bytes to %s.", data.ExportSize, data.OutputPath)
	case "error":
		return "gxodus: error", data.Error
	case "export_started":
		return "gxodus: export started", "New Takeout submitted."
	}
	return "gxodus", event
}
```

Add `"os"` to the imports if it's not already there.

- [ ] **Step 4: Run test to verify it passes**

```
go test ./internal/notify/ -v
```

Expected: PASS for all tests, including new and existing.

- [ ] **Step 5: Commit**

```bash
git add internal/notify/notify.go internal/notify/pushover_test.go
git commit -m "Fire Pushover notification alongside shell hook for events in opt-in list"
```

---

### Task 4: Downloader pure helpers (magic bytes, filename, hostname)

**Files:**
- Rewrite: `internal/downloader/downloader.go` (this task only adds the helpers; later tasks add Download)
- Rewrite: `internal/downloader/downloader_test.go`

- [ ] **Step 1: Write failing tests**

Replace `internal/downloader/downloader_test.go` with:

```go
package downloader

import (
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
```

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./internal/downloader/ -v
```

Expected: FAIL — `looksLikeArchive`, `isLikelyHTML`, `extractFilename` don't exist yet (the existing downloader.go file still has them but the test file rewrite may not see them; if they exist tests will pass — verify by looking at output).

If the existing downloader.go still has these functions and they pass, that's fine for this task — we'll be rewriting the file in later tasks. Move to step 3 to ensure the helpers survive the rewrite.

- [ ] **Step 3: Replace downloader.go with helpers-only stub**

Rewrite `internal/downloader/downloader.go` to contain ONLY the helpers that survive the migration. Delete every other line:

```go
// Package downloader fetches Google Takeout archives via a chromedp-driven
// chromium browser (cookie-only HTTP fails because Google's download URLs
// require a fresh re-authentication / "rapt" token that only a real
// browser can negotiate). See docs/superpowers/specs/2026-05-05-chromedp-downloader-design.md.
package downloader

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// archive magic bytes — what real downloads should start with.
var (
	zipMagic = []byte{'P', 'K', 0x03, 0x04}
	gzMagic  = []byte{0x1f, 0x8b}
)

func looksLikeArchive(b []byte) bool {
	return bytes.HasPrefix(b, zipMagic) || bytes.HasPrefix(b, gzMagic)
}

// isLikelyHTML returns true if the file at path begins with bytes that look
// like HTML (saved auth-redirect from before this fix). Used to invalidate
// stale partial downloads.
func isLikelyHTML(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 64)
	n, _ := io.ReadFull(f, head)
	head = bytes.TrimLeft(head[:n], " \t\r\n")
	for _, prefix := range [][]byte{
		[]byte("<!doctype"), []byte("<!DOCTYPE"),
		[]byte("<html"), []byte("<HTML"),
		[]byte("<head"), []byte("<HEAD"),
		[]byte("<meta"), []byte("<META"),
	} {
		if bytes.HasPrefix(head, prefix) {
			return true
		}
	}
	return false
}

func extractFilename(url string, index int) string {
	parts := strings.Split(url, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		part := strings.Split(parts[i], "?")[0]
		if strings.Contains(part, ".zip") || strings.Contains(part, ".tgz") {
			return part
		}
	}
	if strings.Contains(url, "filename=") {
		for _, param := range strings.Split(url, "&") {
			if strings.HasPrefix(param, "filename=") {
				return strings.TrimPrefix(param, "filename=")
			}
		}
	}
	return fmt.Sprintf("takeout-%s-%03d.zip", time.Now().Format("2006-01-02"), index+1)
}

type Result struct {
	Files     []string
	TotalSize int64
}
```

This deletes the broken HTTP `Download` and `downloadFile` functions. The package will not compile callers in `internal/cli/export.go` until Task 8 wires the new entry point.

- [ ] **Step 4: Run helper tests in isolation**

```
go test ./internal/downloader/ -run 'TestLooksLikeArchive|TestIsLikelyHTML|TestExtractFilename' -v
```

Expected: PASS for all three.

- [ ] **Step 5: Commit (build will be broken until Task 8 — that's expected and called out in the commit message)**

```bash
git add internal/downloader/downloader.go internal/downloader/downloader_test.go
git commit -m "Strip downloader to pure helpers; chromedp Download() to follow

The HTTP downloader silently saved Google's auth-redirect HTML as zip.
Removing the broken implementation now; chromedp-based replacement comes
in subsequent tasks. Build is broken at internal/cli/export.go (calls
removed Download function); fixed in Task 8.
"
```

Verify build is broken at the expected place:

```
go build ./... 2>&1 | head -5
```

Expected: error mentioning `downloader.Download` undefined in `internal/cli/export.go`.

---

### Task 5: chromedp Download — bootstrap + tmp dir

**Files:**
- Modify: `internal/downloader/downloader.go`
- Create: `internal/downloader/downloader_integration_test.go`

- [ ] **Step 1: Write the failing integration test**

Create `internal/downloader/downloader_integration_test.go`:

```go
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
		w.Header().Set("Content-Length", "2")
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
```

- [ ] **Step 2: Run integration test to verify it fails**

```
go test -tags integration ./internal/downloader/ -run TestDownload_HappyPath -v
```

Expected: FAIL with `undefined: Download`.

- [ ] **Step 3: Implement Download with bootstrap + per-URL stub**

Replace the imports block at the top of `internal/downloader/downloader.go` with the full final form (covers what later tasks need too — avoids re-editing imports):

```go
import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/chromedp"
	"github.com/thinkjk/gxodus/internal/browser"
	"github.com/thinkjk/gxodus/internal/config"
	"github.com/thinkjk/gxodus/internal/notify"
)
```

The alias `cdpbrowser` distinguishes Chrome DevTools Protocol's `browser` package from gxodus's `internal/browser`.

Then add:

```go
// Download fetches Takeout archives via chromedp into outputDir.
// cookies is the authenticated Google session; notifyCfg is used to fire
// auth_expired (Pushover + shell hook) when a re-auth challenge appears.
func Download(ctx context.Context, urls []string, outputDir string, cookies []*http.Cookie, notifyCfg config.NotifyConfig) (*Result, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}

	tmpDir := filepath.Join(config.ConfigDir(), "downloads-tmp")
	if err := resetTmpDir(tmpDir); err != nil {
		return nil, err
	}

	bctx, cancel, err := browser.NewContext(ctx, browser.Options{
		Headless:    false, // headed via container's Xvfb so noVNC can show challenges
		UserDataDir: browser.ProfileDir(),
	})
	if err != nil {
		return nil, fmt.Errorf("opening chromedp context: %w", err)
	}
	defer cancel()

	// Force a navigation so the chromedp context has an active target before
	// we inject cookies (cookies are scoped per-target / browser-wide via CDP).
	if err := chromedp.Run(bctx, chromedp.Navigate("about:blank")); err != nil {
		return nil, fmt.Errorf("opening blank page: %w", err)
	}

	if err := browser.InjectCookies(bctx, cookies); err != nil {
		return nil, fmt.Errorf("injecting cookies: %w", err)
	}

	if err := chromedp.Run(bctx, cdpbrowser.SetDownloadBehavior(cdpbrowser.SetDownloadBehaviorBehaviorAllow).
		WithDownloadPath(tmpDir).
		WithEventsEnabled(true)); err != nil {
		return nil, fmt.Errorf("setting download behavior: %w", err)
	}

	var result Result
	for i, u := range urls {
		path, size, err := downloadOne(bctx, u, i, tmpDir, outputDir, notifyCfg)
		if err != nil {
			return &result, fmt.Errorf("downloading %s: %w", u, err)
		}
		result.Files = append(result.Files, path)
		result.TotalSize += size
	}
	return &result, nil
}

// resetTmpDir wipes any abandoned partials from prior runs.
func resetTmpDir(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("clearing tmp dir: %w", err)
	}
	return os.MkdirAll(dir, 0755)
}

// downloadOne is implemented in Task 6. Stub for now so Task 5's test
// can compile and (intentionally) fail at runtime.
func downloadOne(ctx context.Context, url string, index int, tmpDir, outputDir string, notifyCfg config.NotifyConfig) (string, int64, error) {
	return "", 0, fmt.Errorf("downloadOne not implemented in Task 5 — see Task 6")
}
```

`sync`, `notify`, `bytes`, `io`, `strings`, and `time` are used by Task 6/7 code. If `go build` complains about unused imports for the partial Task 5 state, add a `_ = sync.Mutex{}; _ = notify.EventData{}; _ = bytes.NewReader; _ = io.EOF; _ = strings.Contains; _ = time.Now` line inside `downloadOne` until Task 6 lands. Remove these throwaway references in Task 6.

- [ ] **Step 4: Build to verify the package compiles in isolation (callers still broken)**

```
go build ./internal/downloader/...
```

Expected: success.

- [ ] **Step 5: Run integration test (will fail at downloadOne, but verifies bootstrap runs)**

```
go test -tags integration ./internal/downloader/ -run TestDownload_HappyPath -v
```

Expected: FAIL with "downloadOne not implemented in Task 5". This confirms chromedp bootstrap and cookie injection work — the failure is at the per-URL step, which is implemented next.

If chromedp fails to spawn (no chromium installed locally), skip the integration test for now; CI gates them via the `integration` build tag.

- [ ] **Step 6: Commit**

```bash
git add internal/downloader/downloader.go internal/downloader/downloader_integration_test.go
git commit -m "Wire chromedp bootstrap for downloader: ctx, cookies, setDownloadBehavior

downloadOne stub returns 'not implemented' to be filled in next task."
```

---

### Task 6: Per-URL download via CDP downloadProgress events

**Files:**
- Modify: `internal/downloader/downloader.go`

- [ ] **Step 1: Re-run integration test to confirm it still fails at downloadOne**

```
go test -tags integration ./internal/downloader/ -run TestDownload_HappyPath -v 2>&1 | tail -10
```

Expected: FAIL with `downloadOne not implemented in Task 5 — see Task 6`.

- [ ] **Step 2: Implement downloadOne**

Replace the stub `downloadOne` in `internal/downloader/downloader.go` with:

```go
// downloadOne navigates to one download URL and waits for the file to land
// in tmpDir, then moves it to outputDir. Detects re-auth challenges by
// watching for the URL host to drift away from takeout.google.com after a
// 10s grace period; when that happens, fires auth_expired and blocks until
// the user completes the challenge via noVNC.
func downloadOne(ctx context.Context, url string, index int, tmpDir, outputDir string, notifyCfg config.NotifyConfig) (string, int64, error) {
	type downloadResult struct {
		filename string
		size     int64
		err      error
	}
	done := make(chan downloadResult, 1)
	began := make(chan string, 1) // filename when download begins

	var once sync.Once
	listenerCancel := chromedp.ListenBrowser(ctx, func(ev interface{}) {
		switch e := ev.(type) {
		case *cdpbrowser.EventDownloadWillBegin:
			once.Do(func() { began <- e.SuggestedFilename })
		case *cdpbrowser.EventDownloadProgress:
			switch e.State {
			case cdpbrowser.DownloadProgressStateCompleted:
				done <- downloadResult{size: int64(e.TotalBytes)}
			case cdpbrowser.DownloadProgressStateCanceled:
				done <- downloadResult{err: fmt.Errorf("download canceled by browser")}
			}
		}
	})
	defer listenerCancel()

	if err := chromedp.Run(ctx, chromedp.Navigate(url)); err != nil {
		return "", 0, fmt.Errorf("navigating to download URL: %w", err)
	}

	// Race: download starts within 10s, OR we landed off-takeout (challenge),
	// OR context cancels.
	var filename string
	select {
	case filename = <-began:
		// happy path
	case <-time.After(10 * time.Second):
		if challenged, currentURL := atChallengePage(ctx); challenged {
			fmt.Printf("Download blocked on re-auth challenge (current URL: %s)\n", currentURL)
			fmt.Println("Open noVNC at <container-host>:6080/vnc.html and complete the password challenge.")
			fireAuthExpired(notifyCfg)
			if err := waitForChallengeResolved(ctx); err != nil {
				return "", 0, err
			}
			// After challenge, the browser usually proceeds to the download
			// automatically. Wait again for the began event.
			select {
			case filename = <-began:
			case <-time.After(60 * time.Second):
				return "", 0, fmt.Errorf("no download began after challenge resolved")
			case <-ctx.Done():
				return "", 0, ctx.Err()
			}
		} else {
			return "", 0, fmt.Errorf("no download began within 10s and not on a challenge page")
		}
	case <-ctx.Done():
		return "", 0, ctx.Err()
	}

	// Now wait for completion. 5-minute stall timeout.
	var result downloadResult
	select {
	case result = <-done:
		if result.err != nil {
			return "", 0, result.err
		}
	case <-time.After(5 * time.Minute):
		return "", 0, fmt.Errorf("download stalled (no completion event in 5m)")
	case <-ctx.Done():
		return "", 0, ctx.Err()
	}

	// Move file from tmpDir to outputDir.
	src := filepath.Join(tmpDir, filename)
	dst := filepath.Join(outputDir, filename)
	if err := os.Rename(src, dst); err != nil {
		return "", 0, fmt.Errorf("moving %s to %s: %w", src, dst, err)
	}

	// Magic-bytes safety check — chromedp downloads should always be archives,
	// but if Google ever serves a 200 OK with HTML body, catch it.
	if !verifyArchive(dst) {
		_ = os.Remove(dst)
		return "", 0, fmt.Errorf("downloaded file %s does not have zip/gzip magic", dst)
	}

	return dst, result.size, nil
}

// verifyArchive opens path and checks the first 4 bytes against magic.
func verifyArchive(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 4)
	n, _ := io.ReadFull(f, head)
	return looksLikeArchive(head[:n])
}

// atChallengePage queries the active page URL and returns true if its host
// is anything other than takeout.google.com (i.e., we got redirected).
func atChallengePage(ctx context.Context) (bool, string) {
	var currentURL string
	if err := chromedp.Run(ctx, chromedp.Location(&currentURL)); err != nil {
		return false, ""
	}
	if strings.Contains(currentURL, "://takeout.google.com/") {
		return false, currentURL
	}
	return true, currentURL
}

// fireAuthExpired calls into the notify package without import cycle.
// notify already depends on config; downloader depends on notify here.
func fireAuthExpired(cfg config.NotifyConfig) {
	notifyFireFunc(cfg, "auth_expired", notifyEventData{})
}

// notifyFireFunc + notifyEventData are wired in init() to break the
// would-be import cycle. See bottom of file for the wiring.
var notifyFireFunc func(cfg config.NotifyConfig, event string, data notifyEventData)

type notifyEventData struct{}
```

Wait — this is getting tangled. Simpler approach: import notify directly. notify imports config; downloader imports notify and config. No cycle.

Replace the trailing `notifyFireFunc` plumbing with a direct import. Update the imports block to include notify, and change `fireAuthExpired` to:

```go
func fireAuthExpired(cfg config.NotifyConfig) {
	notify.Fire(cfg, "auth_expired", notify.EventData{})
}
```

Add `"github.com/thinkjk/gxodus/internal/notify"` to imports.

Add `waitForChallengeResolved` helper (filled in next task — Task 7). For now, stub:

```go
// waitForChallengeResolved is implemented in Task 7.
func waitForChallengeResolved(ctx context.Context) error {
	return fmt.Errorf("waitForChallengeResolved not implemented in Task 6 — see Task 7")
}
```

- [ ] **Step 3: Run integration test to verify happy path passes**

```
go test -tags integration ./internal/downloader/ -run TestDownload_HappyPath -v
```

Expected: PASS. The httptest server serves a real ZIP, chromedp downloads it, the file lands in `outDir` with matching content.

If chromium isn't installed locally, this will fail to spawn. Skip locally and let it run on a chromium-equipped host (or in the container).

- [ ] **Step 4: Commit**

```bash
git add internal/downloader/downloader.go
git commit -m "Implement per-URL chromedp download via CDP downloadProgress events"
```

---

### Task 7: Challenge detector + waitForChallengeResolved

**Files:**
- Modify: `internal/downloader/downloader.go`
- Modify: `internal/downloader/downloader_integration_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/downloader/downloader_integration_test.go`:

```go
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
		res, err = Download(context.Background(), []string{srv.URL + "/download"}, outDir, nil, config.NotifyConfig{})
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
```

(The skip is honest — full automated coverage of the noVNC-completion path requires injecting the user-simulation, which is beyond our ergonomic budget. Manual end-to-end via `debug-download` covers it.)

- [ ] **Step 2: Run test to confirm it skips cleanly**

```
go test -tags integration ./internal/downloader/ -run TestDownload_RecoversAfterChallenge -v
```

Expected: SKIP with the explanatory message.

- [ ] **Step 3: Implement waitForChallengeResolved**

Replace the stub `waitForChallengeResolved` in `internal/downloader/downloader.go`:

```go
// waitForChallengeResolved polls the active page URL once per 5 seconds
// until the host returns to takeout.google.com. No timeout — the user may
// take hours/days to clear the challenge via noVNC.
func waitForChallengeResolved(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			challenged, currentURL := atChallengePage(ctx)
			if !challenged {
				fmt.Printf("Challenge resolved — back on takeout.google.com (URL: %s)\n", currentURL)
				return nil
			}
		}
	}
}
```

- [ ] **Step 4: Build and run all downloader tests**

```
go build ./internal/downloader/...
go test ./internal/downloader/ -v
go test -tags integration ./internal/downloader/ -v
```

Expected: build OK; non-integration tests PASS; integration TestDownload_HappyPath PASS; TestDownload_RecoversAfterChallenge SKIP.

- [ ] **Step 5: Commit**

```bash
git add internal/downloader/downloader.go internal/downloader/downloader_integration_test.go
git commit -m "Add challenge detector + 5s URL-poll wait for user re-auth via noVNC"
```

---

### Task 8: Wire new downloader into export.go

**Files:**
- Modify: `internal/cli/export.go`

- [ ] **Step 1: Update the call site**

In `internal/cli/export.go`, find the line:

```go
dlResult, err := downloader.Download(pollResult.DownloadURLs, resolvedOutput, cookies)
```

(The existing line still has the old signature from the previous patch series.) Replace with:

```go
dlResult, err := downloader.Download(ctx, pollResult.DownloadURLs, resolvedOutput, cookies, cfg.Notify)
```

- [ ] **Step 2: Build to verify the project compiles**

```
go build ./...
```

Expected: success — the broken build from Task 4 is now fixed.

- [ ] **Step 3: Run the full unit test suite**

```
go test ./...
```

Expected: all PASS. (Integration tests are tagged separately and not run here.)

- [ ] **Step 4: Commit**

```bash
git add internal/cli/export.go
git commit -m "Switch export.go to chromedp downloader; pass ctx + notify cfg"
```

---

### Task 9: debug-download CLI command

**Files:**
- Modify: `internal/cli/debug_api.go`

- [ ] **Step 1: Add the command**

Append to `internal/cli/debug_api.go`:

```go
var debugDownloadUUID string

var debugDownloadCmd = &cobra.Command{
	Use:    "debug-download",
	Short:  "Skip create+poll and download a known-complete export by UUID",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		if !auth.SessionExists() {
			return fmt.Errorf("no saved session — run 'gxodus auth' first")
		}
		cookies, err := auth.LoadSession()
		if err != nil {
			return fmt.Errorf("loading session: %w", err)
		}

		client, err := takeoutapi.NewClient(cookies, debugUserIdx)
		if err != nil {
			return err
		}
		exp, err := client.GetExport(ctx, debugDownloadUUID)
		if err != nil {
			return fmt.Errorf("looking up export: %w", err)
		}
		if exp == nil {
			return fmt.Errorf("export %s not found", debugDownloadUUID)
		}
		if exp.Status != takeoutapi.StatusComplete {
			return fmt.Errorf("export %s not complete (status=%v)", debugDownloadUUID, exp.Status)
		}
		if len(exp.DownloadURLs) == 0 {
			return fmt.Errorf("export %s has no download URLs", debugDownloadUUID)
		}

		fmt.Printf("Downloading %d archive(s) for %s to %s\n",
			len(exp.DownloadURLs), exp.UUID, cfg.ResolveOutputDir())

		res, err := downloader.Download(ctx, exp.DownloadURLs, cfg.ResolveOutputDir(), cookies, cfg.Notify)
		if err != nil {
			return fmt.Errorf("download failed: %w", err)
		}
		fmt.Printf("Downloaded %d file(s), total %d bytes:\n", len(res.Files), res.TotalSize)
		for _, p := range res.Files {
			fmt.Printf("  %s\n", p)
		}
		return nil
	},
}

func init() {
	debugDownloadCmd.Flags().StringVar(&debugDownloadUUID, "uuid", "", "UUID of an existing complete export")
	_ = debugDownloadCmd.MarkFlagRequired("uuid")
	rootCmd.AddCommand(debugDownloadCmd)
}
```

Add imports if missing: `"github.com/thinkjk/gxodus/internal/config"`, `"github.com/thinkjk/gxodus/internal/downloader"`.

- [ ] **Step 2: Build**

```
go build ./...
```

Expected: success.

- [ ] **Step 3: Smoke-test the command help**

```
go run ./cmd/gxodus debug-download --help
```

Expected: usage output mentioning `--uuid`.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/debug_api.go
git commit -m "Add debug-download command for end-to-end testing of download path"
```

---

### Task 10: README + sample config docs

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Find the existing notification section in README.md**

```
grep -n "on_auth_expired\|notify" README.md | head -10
```

- [ ] **Step 2: Append a Pushover subsection after the existing shell-hook example**

Locate the snippet showing `on_auth_expired = "ntfy publish ..."` and add immediately after it:

````markdown
For Pushover, you can use the built-in destination instead of (or alongside) shell hooks:

```toml
[notify.pushover]
token    = "<your app token>"
user_key = "<your user key>"
# events  = ["auth_expired", "export_complete", "error"]   # default; "export_started" is opt-in
```

When configured, gxodus posts a notification to the Pushover API for each event in `events`. `auth_expired` fires when a Takeout download is blocked on a re-auth challenge — open noVNC at `<container>:6080/vnc.html` and complete the password prompt to unblock it.

The hostname in the notification text comes from `os.Hostname()`; override with `GXODUS_PUBLIC_HOSTNAME` if your container hostname differs from the LAN address you'd type into a browser.
````

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "Document built-in Pushover notification config"
```

---

### Task 11: Verification + push

- [ ] **Step 1: Full unit test suite**

```
go test ./...
```

Expected: all PASS.

- [ ] **Step 2: Full build**

```
go build ./...
```

Expected: success.

- [ ] **Step 3: Push**

```bash
git push origin main
```

- [ ] **Step 4: Wait for ghcr build, then manually test in container**

After the GitHub Action finishes:

```sh
# On the Unraid host:
docker pull ghcr.io/thinkjk/gxodus:main
docker compose up -d

# Smoke-test the new downloader against the still-valid 5430dfbb URLs:
docker exec gxodus gxodus debug-download \
  --uuid 5430dfbb-4e4a-44e7-9d69-278cb5708616 \
  --config /config/config.toml
```

Verify:
- Real archives appear in `/exports` (multi-GB total, not 5 MB of HTML)
- `file <archive>.zip` reports `Zip archive data`
- `unzip -l <archive>.zip | head` shows real entries
- If a re-auth challenge fires, you receive the Pushover notification and can complete it via noVNC at `<unraid>:6080/vnc.html`

---

## Out of scope (intentionally not in this plan)

- Concurrent / parallel downloads — sequential is sufficient
- Resume across browser restarts — re-fetch is acceptable inside the 7-day URL window
- User-configurable Pushover message templates — hard-coded messages are sufficient for v1
