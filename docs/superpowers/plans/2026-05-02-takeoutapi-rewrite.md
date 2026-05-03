# Takeout API Rewrite Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace chromedp-based Takeout export/poll/status with HTTP calls to Google's `batchexecute` API, so gxodus only needs chromium for the (rare) interactive login.

**Architecture:** New `internal/takeoutapi/` package that wraps Google's `https://takeout.google.com/u/N/_/TakeoutUi/data/batchexecute` endpoint. Authenticates via the session cookies gxodus already extracts during login. Uses HTTP only — no chromium spawn, no DOM scraping. `internal/cli/{export,status}.go` and `internal/poller/poller.go` switch from `browser.InitiateExport`/`CheckExportStatus` to `takeoutapi.Client` methods. `internal/browser/login.go` is unchanged — login still uses chromedp because Google's bot detection blocks pure-HTTP login.

**Tech Stack:**
- Go 1.26
- Standard library: `net/http`, `encoding/json`, `net/url`, `strings`, `bufio`, `context`, `time`
- Test deps: `testing`, `net/http/httptest` (matches existing `internal/browser/login_test.go` style)
- No new third-party deps. (We considered using `pndurette/pybatchexecute` as reference but Go reimplementation is small enough.)

## Context

Tonight's chromedp-based gxodus works but is brittle: Google rotates Material Design class names, headless chromium triggers bot detection, and every poll/export cycle spawns a chromium that needs Xvfb + noVNC infrastructure. Our 2026-05-02 spike (`docs/spikes/2026-05-02-batchexecute-api.md`) proved the web UI's underlying batchexecute API is reachable with session cookies — no Google Cloud project enablement needed. We decoded:

- The request envelope: `f.req=[[[rpcid, args-as-JSON-string, null, version]]]&at=<XSRF>`
- The response chunk format: `)]}'\n` prefix + length-prefixed JSON arrays
- The create-export rpcid `U5lrKc` and its full args structure (file size in bytes, product slugs, frequency code, format)
- The list-exports rpcid `fhjYTc` and its full response shape (export UUID, creation timestamp, product catalog, status flags)
- The "Access Log Activity" slug is `bond` (not `access_log_activity`)

Some response shapes are still unknown (the `U5lrKc` create response, the status enum value for completed exports, where download URLs appear when complete). The plan handles these with a small "discovery" tool that captures real responses before we commit to data structures.

## Strategy

Build in 4 phases:

1. **Foundations** (Tasks 1-4): batchexecute envelope, response parser, XSRF token extraction, HTTP client.
2. **Discovery** (Tasks 5-6): a `gxodus debug-api` command + manual capture of responses we don't have yet.
3. **Domain layer** (Tasks 7-9): typed `CreateExport`, `ListExports`, `GetExport` methods.
4. **Integration + cleanup** (Tasks 10-15): wire into CLI/poller, delete chromedp helpers, update Docker entrypoint, E2E verify.

## File Structure

| File | Responsibility | Action |
|---|---|---|
| `internal/takeoutapi/batchexecute.go` | Envelope encode + chunked response decode | **Create** |
| `internal/takeoutapi/batchexecute_test.go` | Unit tests for envelope + decode | **Create** |
| `internal/takeoutapi/xsrf.go` | Extract `at` token + build label from a Takeout HTML page | **Create** |
| `internal/takeoutapi/xsrf_test.go` | Unit tests for HTML extraction | **Create** |
| `internal/takeoutapi/client.go` | `Client` struct: cookies, http.Client, XSRF cache, request counter, `callRPC` method | **Create** |
| `internal/takeoutapi/client_test.go` | httptest-based client tests | **Create** |
| `internal/takeoutapi/types.go` | `Export`, `ExportStatus`, `CreateExportOptions` types | **Create** |
| `internal/takeoutapi/exports.go` | `CreateExport`, `ListExports`, `GetExport` methods | **Create** |
| `internal/takeoutapi/exports_test.go` | Method tests against captured fixtures | **Create** |
| `internal/cli/debug_api.go` | Hidden `debug-api` command for capturing responses | **Create** |
| `internal/cli/export.go` | Replace `browser.InitiateExport` call with `takeoutapi.Client.CreateExport` | Modify |
| `internal/cli/status.go` | Replace `browser.CheckExportStatus` call with `takeoutapi.Client.ListExports` | Modify |
| `internal/poller/poller.go` | Replace per-poll chromium spawn with `takeoutapi.Client.ListExports` | Modify |
| `internal/browser/takeout.go` | Delete `InitiateExport`, `CheckExportStatus`, all the helpers; keep only `dumpButtons`/`logPageState`/`Screenshot` (still useful for login debugging) | Modify (large delete) |
| `docker-entrypoint.sh` | No longer pre-start Xvfb in the export loop; only on-demand for re-auth | Modify |
| `testdata/responses/` | Fixture files captured by Task 6 | **Create dir** |

No changes to: `internal/auth/*` (cookie storage), `internal/browser/{browser,login}.go` (login still uses chromedp), `internal/config/*`, `internal/downloader/*`, `internal/extractor/*`, `internal/notify/*`.

## Known unknowns (resolved by Task 6 discovery)

1. **`U5lrKc` create-export response shape.** Does it return the new export's UUID? A status code? An empty success?
2. **Status enum value for "complete" exports.** We know position [9] = `0` for in-progress; need the value(s) for complete/failed.
3. **Download URL location in `fhjYTc` response when complete.** Likely a populated array at one of the currently-`null` positions.
4. **`OIek4b` purpose.** Takes a UUID; could be getStatus, cancel, or delete. We may not need it at all.
5. **Frequency / flag enum values in `U5lrKc` args.** Position [4]=`5` for once? Position [11]=`"2"` for once-shot? We'll triangulate by making test calls with different settings.

Tasks 7-9 use our best-guess response structures based on the partial info we have; Task 6 either confirms them or tells us what to adjust.

---

## Task 1: Batchexecute envelope encoder

Encodes a single rpcid + args + version + XSRF token into the form-encoded body Google's batchexecute endpoint expects.

**Files:**
- Create: `internal/takeoutapi/batchexecute.go`
- Create: `internal/takeoutapi/batchexecute_test.go`

- [ ] **Step 1: Create the failing test** — `internal/takeoutapi/batchexecute_test.go`:

```go
package takeoutapi

import (
	"net/url"
	"strings"
	"testing"
)

func TestEncodeRequest(t *testing.T) {
	body, err := encodeRequest("U5lrKc", `[["bond"],["drive"]]`, "generic", "AT_TOKEN:1777768009152")
	if err != nil {
		t.Fatalf("encodeRequest: %v", err)
	}

	parsed, err := url.ParseQuery(body)
	if err != nil {
		t.Fatalf("parsing body: %v", err)
	}

	gotFreq := parsed.Get("f.req")
	wantFreq := `[[["U5lrKc","[[\"bond\"],[\"drive\"]]",null,"generic"]]]`
	if gotFreq != wantFreq {
		t.Errorf("f.req = %q, want %q", gotFreq, wantFreq)
	}

	if got := parsed.Get("at"); got != "AT_TOKEN:1777768009152" {
		t.Errorf("at = %q, want %q", got, "AT_TOKEN:1777768009152")
	}

	// Also sanity-check the body has both keys, not e.g. duplicated.
	if !strings.Contains(body, "f.req=") || !strings.Contains(body, "&at=") {
		t.Errorf("body missing expected keys: %s", body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/takeoutapi/ -run TestEncodeRequest -v`
Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Implement** — create `internal/takeoutapi/batchexecute.go`:

```go
// Package takeoutapi wraps Google Takeout's internal batchexecute endpoint.
// See docs/spikes/2026-05-02-batchexecute-api.md for protocol notes.
package takeoutapi

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// encodeRequest builds the URL-encoded body for a batchexecute POST.
// args is a pre-serialized JSON string (the "doubly-escaped" inner payload —
// it gets serialized again as a JSON string when the envelope is marshaled).
// version is "generic" or "1" depending on the rpcid.
// atToken is the XSRF token from the page HTML.
func encodeRequest(rpcid, args, version, atToken string) (string, error) {
	envelope := [][][]interface{}{{{rpcid, args, nil, version}}}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("marshaling envelope: %w", err)
	}

	values := url.Values{}
	values.Set("f.req", string(encoded))
	values.Set("at", atToken)
	return values.Encode(), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/takeoutapi/ -v`
Expected: PASS — `TestEncodeRequest` passes.

- [ ] **Step 5: Commit**

```bash
git add internal/takeoutapi/batchexecute.go internal/takeoutapi/batchexecute_test.go
git commit -m "Add batchexecute envelope encoder"
```

---

## Task 2: Batchexecute response decoder

Parses Google's `)]}'`-prefixed length-chunked response into a list of (rpcid, raw-result-JSON) pairs.

**Files:**
- Modify: `internal/takeoutapi/batchexecute.go` (append decoder)
- Modify: `internal/takeoutapi/batchexecute_test.go` (append tests)

- [ ] **Step 1: Add the failing test** — append to `internal/takeoutapi/batchexecute_test.go`:

```go
func TestDecodeResponse(t *testing.T) {
	// Captured shape from a real fhjYTc response (truncated for the test).
	body := []byte(")]}'\n" +
		"58\n" +
		`[["wrb.fr","fhjYTc","[null,\"hello\"]",null,null,null,"generic"]]` + "\n" +
		"57\n" +
		`[["di",123],["af.httprm",122,"-8797266961199462245",9]]` + "\n" +
		"27\n" +
		`[["e",4,null,null,30387]]`)

	results, err := decodeResponse(body)
	if err != nil {
		t.Fatalf("decodeResponse: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	if results[0].RpcID != "fhjYTc" {
		t.Errorf("RpcID = %q, want %q", results[0].RpcID, "fhjYTc")
	}

	if string(results[0].RawJSON) != `[null,"hello"]` {
		t.Errorf("RawJSON = %s, want %s", results[0].RawJSON, `[null,"hello"]`)
	}
}

func TestDecodeResponse_StripsAntiHijackPrefix(t *testing.T) {
	body := []byte(")]}'\n" + "10\n" + `["wrb.fr"]`)
	if _, err := decodeResponse(body); err != nil {
		t.Errorf("expected no error after stripping prefix: %v", err)
	}
}

func TestDecodeResponse_NoPrefixIsError(t *testing.T) {
	body := []byte(`{"oops": "json"}`)
	if _, err := decodeResponse(body); err == nil {
		t.Error("expected error for missing )]}' prefix")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/takeoutapi/ -run TestDecodeResponse -v`
Expected: FAIL — `decodeResponse` undefined.

- [ ] **Step 3: Implement** — append to `internal/takeoutapi/batchexecute.go`:

```go
import (
	"bufio"
	"bytes"
	"errors"
	"strconv"
)

// rpcResult is one decoded rpc invocation from a batchexecute response.
type rpcResult struct {
	RpcID   string
	RawJSON []byte // the inner JSON-string parsed back out (the "doubly-escaped" payload)
}

const antiHijackPrefix = ")]}'\n"

// decodeResponse parses Google's chunked batchexecute response body.
// Returns one rpcResult per "wrb.fr"-tagged chunk in the response.
func decodeResponse(body []byte) ([]rpcResult, error) {
	if !bytes.HasPrefix(body, []byte(antiHijackPrefix)) {
		return nil, errors.New("response missing )]}' prefix — not a batchexecute body")
	}
	body = bytes.TrimPrefix(body, []byte(antiHijackPrefix))

	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024) // 16 MB max chunk

	var results []rpcResult
	for {
		// Read the length line.
		if !scanner.Scan() {
			break
		}
		lengthLine := strings.TrimSpace(scanner.Text())
		if lengthLine == "" {
			continue
		}
		chunkLen, err := strconv.Atoi(lengthLine)
		if err != nil {
			return nil, fmt.Errorf("parsing chunk length %q: %w", lengthLine, err)
		}

		// Read exactly chunkLen bytes for the chunk JSON.
		// Scanner's Bytes() includes the newline — work with the underlying reader instead.
		chunkBytes := make([]byte, chunkLen)
		if _, err := io.ReadFull(scanner.Reader(), chunkBytes); err != nil {
			return nil, fmt.Errorf("reading chunk of %d bytes: %w", chunkLen, err)
		}

		// ... (parse chunk, filter for wrb.fr, extract rpcid + inner JSON-string)
	}
	return results, nil
}
```

Wait — `bufio.Scanner` doesn't expose its underlying reader. We need a different approach: read the whole body into memory, tokenize manually.

Replace the implementation with this simpler manual parser:

```go
// decodeResponse parses Google's chunked batchexecute response body.
// Returns one rpcResult per "wrb.fr"-tagged chunk in the response.
func decodeResponse(body []byte) ([]rpcResult, error) {
	if !bytes.HasPrefix(body, []byte(antiHijackPrefix)) {
		return nil, errors.New("response missing )]}' prefix — not a batchexecute body")
	}
	body = bytes.TrimPrefix(body, []byte(antiHijackPrefix))

	var results []rpcResult
	for len(body) > 0 {
		// Each chunk: <decimal length>\n<JSON bytes>
		nl := bytes.IndexByte(body, '\n')
		if nl <= 0 {
			break
		}
		lengthStr := strings.TrimSpace(string(body[:nl]))
		if lengthStr == "" {
			body = body[nl+1:]
			continue
		}
		chunkLen, err := strconv.Atoi(lengthStr)
		if err != nil {
			return nil, fmt.Errorf("parsing chunk length %q: %w", lengthStr, err)
		}
		body = body[nl+1:]
		if len(body) < chunkLen {
			return nil, fmt.Errorf("chunk says %d bytes but only %d remain", chunkLen, len(body))
		}
		chunkJSON := body[:chunkLen]
		body = body[chunkLen:]

		// Each chunk is a JSON array of arrays; the rpc results are the elements
		// whose first item is the literal string "wrb.fr".
		var chunkArr [][]interface{}
		if err := json.Unmarshal(chunkJSON, &chunkArr); err != nil {
			// Some chunks (the "di"/"e" diagnostic ones) won't match this shape — skip.
			continue
		}
		for _, entry := range chunkArr {
			if len(entry) < 3 {
				continue
			}
			tag, ok := entry[0].(string)
			if !ok || tag != "wrb.fr" {
				continue
			}
			rpcid, ok := entry[1].(string)
			if !ok {
				continue
			}
			inner, ok := entry[2].(string)
			if !ok {
				continue
			}
			results = append(results, rpcResult{
				RpcID:   rpcid,
				RawJSON: []byte(inner),
			})
		}
	}
	return results, nil
}
```

(Drop the earlier `bufio` / `io` imports — only `encoding/json`, `net/url`, `bytes`, `errors`, `strconv`, `strings`, `fmt` are needed.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/takeoutapi/ -v`
Expected: PASS — all three decode tests + `TestEncodeRequest`.

- [ ] **Step 5: Commit**

```bash
git add internal/takeoutapi/batchexecute.go internal/takeoutapi/batchexecute_test.go
git commit -m "Add batchexecute response decoder"
```

---

## Task 3: XSRF token + build label extraction

Google's batchexecute requires an `at` (XSRF) token and a `bl` (build label) URL parameter. Both come from the takeout.google.com page HTML — embedded in a global JS object. Extract them so the client can request fresh tokens when needed.

**Files:**
- Create: `internal/takeoutapi/xsrf.go`
- Create: `internal/takeoutapi/xsrf_test.go`

- [ ] **Step 1: Create the failing test** — `internal/takeoutapi/xsrf_test.go`:

```go
package takeoutapi

import "testing"

func TestExtractTokens(t *testing.T) {
	// Realistic snippet of the page HTML — Google embeds tokens in WIZ_global_data.
	html := `<!DOCTYPE html><html><head>...</head><body>
<script>WIZ_global_data = {"SNlM0e":"ALYeEnkc1UxeQ3U_BuS-1yJoUbY8:1777768009152","cfb2h":"boq_identityfrontenduiserver_20260429.06_p0","other":"junk"};</script>
</body></html>`

	tokens, err := extractTokens(html)
	if err != nil {
		t.Fatalf("extractTokens: %v", err)
	}

	if tokens.XSRF != "ALYeEnkc1UxeQ3U_BuS-1yJoUbY8:1777768009152" {
		t.Errorf("XSRF = %q", tokens.XSRF)
	}
	if tokens.BuildLabel != "boq_identityfrontenduiserver_20260429.06_p0" {
		t.Errorf("BuildLabel = %q", tokens.BuildLabel)
	}
}

func TestExtractTokens_MissingXSRF(t *testing.T) {
	html := `<html><body>no tokens here</body></html>`
	if _, err := extractTokens(html); err == nil {
		t.Error("expected error when XSRF token absent")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/takeoutapi/ -run TestExtractTokens -v`
Expected: FAIL — `extractTokens` undefined.

- [ ] **Step 3: Implement** — create `internal/takeoutapi/xsrf.go`:

```go
package takeoutapi

import (
	"fmt"
	"regexp"
)

// PageTokens are the per-page secrets needed to call batchexecute.
type PageTokens struct {
	XSRF       string // the "at" parameter
	BuildLabel string // the "bl" parameter (rotates with Google deploys)
}

// SNlM0e = XSRF; cfb2h = build label.
// We regex these out instead of parsing JS — the surrounding object can have
// any number of trailing keys with arbitrary nesting.
var (
	xsrfRE  = regexp.MustCompile(`"SNlM0e":"([^"]+)"`)
	buildRE = regexp.MustCompile(`"cfb2h":"([^"]+)"`)
)

// extractTokens parses the takeout.google.com page HTML for the XSRF token
// and build label. Both live in the WIZ_global_data global object embedded
// in the page.
func extractTokens(html string) (*PageTokens, error) {
	xsrf := xsrfRE.FindStringSubmatch(html)
	if len(xsrf) < 2 {
		return nil, fmt.Errorf("XSRF token (SNlM0e) not found in page HTML")
	}
	build := buildRE.FindStringSubmatch(html)
	if len(build) < 2 {
		return nil, fmt.Errorf("build label (cfb2h) not found in page HTML")
	}
	return &PageTokens{
		XSRF:       xsrf[1],
		BuildLabel: build[1],
	}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/takeoutapi/ -v`
Expected: PASS — both extractTokens tests + previous tests.

- [ ] **Step 5: Commit**

```bash
git add internal/takeoutapi/xsrf.go internal/takeoutapi/xsrf_test.go
git commit -m "Add XSRF token + build label extraction from Takeout page HTML"
```

---

## Task 4: HTTP client foundation

The `Client` struct holds cookies, an `http.Client`, a cached `PageTokens`, and a request counter. Its `callRPC` method does one batchexecute round-trip and returns the parsed result.

**Files:**
- Create: `internal/takeoutapi/client.go`
- Create: `internal/takeoutapi/client_test.go`

- [ ] **Step 1: Create the failing test** — `internal/takeoutapi/client_test.go`:

```go
package takeoutapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_CallRPC(t *testing.T) {
	var capturedURL, capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Two paths: GET / for the HTML (XSRF bootstrap), POST /_/TakeoutUi/data/batchexecute for the call.
		if r.Method == "GET" {
			_, _ = w.Write([]byte(`<html><script>WIZ_global_data = {"SNlM0e":"TEST_AT","cfb2h":"TEST_BL"};</script></html>`))
			return
		}
		capturedURL = r.URL.String()
		bodyBytes, _ := io.ReadAll(r.Body)
		capturedBody = string(bodyBytes)
		_, _ = w.Write([]byte(")]}'\n26\n[[\"wrb.fr\",\"X1Y2Z3\",\"42\",null,null,null,\"generic\"]]"))
	}))
	defer srv.Close()

	c := newClientForTest(srv.URL, []*http.Cookie{{Name: "SID", Value: "fake"}})
	raw, err := c.callRPC(context.Background(), "X1Y2Z3", "[]", "generic")
	if err != nil {
		t.Fatalf("callRPC: %v", err)
	}

	if string(raw) != "42" {
		t.Errorf("raw = %q, want %q", raw, "42")
	}

	// Verify we sent the right URL params and body.
	if !strings.Contains(capturedURL, "rpcids=X1Y2Z3") {
		t.Errorf("URL missing rpcid: %s", capturedURL)
	}
	if !strings.Contains(capturedURL, "bl=TEST_BL") {
		t.Errorf("URL missing build label: %s", capturedURL)
	}
	if !strings.Contains(capturedBody, "at=TEST_AT") {
		t.Errorf("body missing XSRF token: %s", capturedBody)
	}
	if !strings.Contains(capturedBody, "X1Y2Z3") {
		t.Errorf("body missing rpcid: %s", capturedBody)
	}

	// Sanity: at least one cookie made it.
	_ = json.Marshal // silence import if unused
}
```

(Note: the test references `newClientForTest`, a test-only constructor that hard-codes the base URL. We add it in step 3.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/takeoutapi/ -run TestClient_CallRPC -v`
Expected: FAIL — `newClientForTest` and `Client.callRPC` undefined.

- [ ] **Step 3: Implement** — create `internal/takeoutapi/client.go`:

```go
package takeoutapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
)

// Client makes batchexecute calls against takeout.google.com using session
// cookies extracted by gxodus's auth flow. Safe for concurrent use.
type Client struct {
	baseURL  string
	userIdx  int
	hc       *http.Client
	tokens   *PageTokens
	tokensMu sync.Mutex
	reqID    atomic.Int64
}

// New creates a Client authenticated via the given session cookies.
// userIdx is the Google account index in multi-account browser sessions
// (typically 0 for the first signed-in account, 2 for a third, etc.).
// gxodus's saved sessions usually need userIdx=0 unless the user signed in
// to a non-primary account.
func New(cookies []*http.Cookie, userIdx int) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("creating cookie jar: %w", err)
	}
	u, _ := url.Parse("https://takeout.google.com")
	jar.SetCookies(u, cookies)

	return &Client{
		baseURL: "https://takeout.google.com",
		userIdx: userIdx,
		hc:      &http.Client{Jar: jar},
	}, nil
}

// newClientForTest is a constructor that points the base URL at httptest.
func newClientForTest(baseURL string, cookies []*http.Cookie) *Client {
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(baseURL)
	jar.SetCookies(u, cookies)
	return &Client{
		baseURL: baseURL,
		userIdx: 0,
		hc:      &http.Client{Jar: jar},
	}
}

// ensureTokens fetches the takeout page once, caches the XSRF + build label.
func (c *Client) ensureTokens(ctx context.Context) error {
	c.tokensMu.Lock()
	defer c.tokensMu.Unlock()
	if c.tokens != nil {
		return nil
	}
	pageURL := fmt.Sprintf("%s/u/%d/", c.baseURL, c.userIdx)
	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return fmt.Errorf("building page request: %w", err)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("fetching page: %w", err)
	}
	defer resp.Body.Close()
	html, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading page: %w", err)
	}
	tokens, err := extractTokens(string(html))
	if err != nil {
		return fmt.Errorf("extracting tokens from page: %w", err)
	}
	c.tokens = tokens
	return nil
}

// callRPC performs one batchexecute round-trip and returns the inner result
// as raw JSON bytes (the doubly-escaped payload, parsed back to a JSON value).
func (c *Client) callRPC(ctx context.Context, rpcid, args, version string) ([]byte, error) {
	if err := c.ensureTokens(ctx); err != nil {
		return nil, err
	}

	body, err := encodeRequest(rpcid, args, version, c.tokens.XSRF)
	if err != nil {
		return nil, err
	}

	q := url.Values{}
	q.Set("rpcids", rpcid)
	q.Set("source-path", fmt.Sprintf("/u/%d/", c.userIdx))
	q.Set("bl", c.tokens.BuildLabel)
	q.Set("hl", "en")
	q.Set("pageId", "none")
	q.Set("soc-app", "1")
	q.Set("soc-platform", "1")
	q.Set("soc-device", "1")
	q.Set("_reqid", strconv.FormatInt(c.reqID.Add(100000), 10))
	q.Set("rt", "c")

	endpoint := fmt.Sprintf("%s/u/%d/_/TakeoutUi/data/batchexecute?%s", c.baseURL, c.userIdx, q.Encode())
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building rpc request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	req.Header.Set("X-Same-Domain", "1")
	req.Header.Set("Origin", c.baseURL)

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling rpc %s: %w", rpcid, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("rpc %s: HTTP %d: %s", rpcid, resp.StatusCode, bodyBytes)
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading rpc response: %w", err)
	}

	results, err := decodeResponse(respBytes)
	if err != nil {
		return nil, err
	}

	// Find our rpc's result.
	for _, r := range results {
		if r.RpcID == rpcid {
			return r.RawJSON, nil
		}
	}
	return nil, fmt.Errorf("rpc %s not found in response", rpcid)
}
```

(Add `"strings"` to the existing imports if not already present.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/takeoutapi/ -v`
Expected: PASS — all tests including `TestClient_CallRPC`.

- [ ] **Step 5: Commit**

```bash
git add internal/takeoutapi/client.go internal/takeoutapi/client_test.go
git commit -m "Add takeoutapi.Client with XSRF bootstrap + callRPC"
```

---

## Task 5: `gxodus debug-api` discovery command

A hidden CLI command that takes a rpcid + args + version and dumps the parsed response to stdout. We use this in Task 6 to capture the response shapes we don't have yet.

**Files:**
- Create: `internal/cli/debug_api.go`

- [ ] **Step 1: Create the file** — `internal/cli/debug_api.go`:

```go
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/thinkjk/gxodus/internal/auth"
	"github.com/thinkjk/gxodus/internal/takeoutapi"
)

var (
	debugRpcid   string
	debugArgs    string
	debugVersion string
	debugUserIdx int
)

var debugAPICmd = &cobra.Command{
	Use:    "debug-api",
	Short:  "Make a raw batchexecute call (debugging only)",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		if !auth.SessionExists() {
			return fmt.Errorf("no saved session — run 'gxodus auth' first")
		}
		cookies, err := auth.LoadSession()
		if err != nil {
			return fmt.Errorf("loading session: %w", err)
		}

		client, err := takeoutapi.NewClient(cookies, debugUserIdx)
		if err != nil {
			return fmt.Errorf("creating client: %w", err)
		}

		raw, err := client.CallRPC(ctx, debugRpcid, debugArgs, debugVersion)
		if err != nil {
			fmt.Fprintf(os.Stderr, "rpc failed: %v\n", err)
			os.Exit(1)
		}

		// Pretty-print the JSON for human reading.
		var pretty interface{}
		if err := json.Unmarshal(raw, &pretty); err != nil {
			fmt.Println(string(raw))
			return nil
		}
		out, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Println(string(out))
		return nil
	},
}

func init() {
	debugAPICmd.Flags().StringVar(&debugRpcid, "rpcid", "", "batchexecute rpcid (e.g. fhjYTc)")
	debugAPICmd.Flags().StringVar(&debugArgs, "args", "[]", "rpc args as JSON string")
	debugAPICmd.Flags().StringVar(&debugVersion, "version", "generic", `rpc version, "generic" or "1"`)
	debugAPICmd.Flags().IntVar(&debugUserIdx, "user", 0, "Google account index (0 = primary)")
	_ = debugAPICmd.MarkFlagRequired("rpcid")
	rootCmd.AddCommand(debugAPICmd)
}
```

This requires exporting `Client.CallRPC` and `New` (currently `Client.callRPC` and `New` lowercase) from the takeoutapi package. Update `internal/takeoutapi/client.go`:

- [ ] **Step 2: Export the public API** — modify `internal/takeoutapi/client.go`:

Rename `callRPC` → `CallRPC` (one method, two call sites — the method itself and `client_test.go`).
Rename `New` → `NewClient` for clarity (it's `takeoutapi.NewClient` from outside).

In `internal/takeoutapi/client.go`:
```go
// New becomes:
func NewClient(cookies []*http.Cookie, userIdx int) (*Client, error) {
```

```go
// callRPC becomes:
func (c *Client) CallRPC(ctx context.Context, rpcid, args, version string) ([]byte, error) {
```

In `internal/takeoutapi/client_test.go`, update the call site:
```go
raw, err := c.CallRPC(context.Background(), "X1Y2Z3", "[]", "generic")
```

- [ ] **Step 3: Verify it compiles**

Run: `go build ./...`
Expected: clean build.

- [ ] **Step 4: Run all tests**

Run: `go test ./...`
Expected: PASS — `TestClient_CallRPC` still passes after rename.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/debug_api.go internal/takeoutapi/client.go internal/takeoutapi/client_test.go
git commit -m "Add hidden debug-api command for capturing batchexecute responses"
```

---

## Task 6: Capture missing responses (manual user step)

Use `debug-api` to capture real responses for `U5lrKc`, `RN3tcc`, and `OIek4b`. Save them as JSON fixtures so subsequent tasks can write tests against real shapes.

**Prerequisites:**
- Run `gxodus auth` so session.enc exists with valid cookies.
- The user has at least one in-progress export (we can use the one from 2026-05-02).

**Files:**
- Create: `testdata/responses/fhjYTc-with-in-progress.json`
- Create: `testdata/responses/U5lrKc-create-success.json` (if we successfully create one)
- Create: `testdata/responses/OIek4b-status.json` (if we figure out what it does)

- [ ] **Step 1: Capture `fhjYTc` (list exports) with current cookies**

Run from a machine with valid `~/.config/gxodus/session.enc`:

```bash
gxodus debug-api --rpcid fhjYTc --version generic > testdata/responses/fhjYTc-with-in-progress.json
```

Verify the file contains the list of current exports. Should match the shape we already documented in `docs/spikes/2026-05-02-batchexecute-api.md`.

- [ ] **Step 2: Capture `RN3tcc` for comparison**

```bash
gxodus debug-api --rpcid RN3tcc --version 1 --args '[]' > testdata/responses/RN3tcc-list.json
```

If this 404s or returns an error, the rpcid may be variant-specific. Note the failure and skip — `fhjYTc` is the canonical list-exports rpcid, so `RN3tcc` only matters if it returns different/better data.

- [ ] **Step 3: Capture `U5lrKc` (create export) — careful, this CREATES a real export**

⚠️ This will trigger Google to start a new export on the user's account. Only run if you actually want a new export, or after deleting the in-progress one.

Build the args carefully — based on our spike, the structure is:

```bash
ARGS='["ac.t.st",[["drive"]],"ZIP",null,5,null,2147483648,1,null,null,null,"2"]'
gxodus debug-api --rpcid U5lrKc --version generic --args "$ARGS" > testdata/responses/U5lrKc-create-success.json
```

(`2147483648` = 2 GB. We pick just `["drive"]` to minimize the size of any test export.)

Capture the response shape. This tells us:
- Does it return the new export's UUID?
- Does it return a status?
- What error format if a parameter is wrong?

- [ ] **Step 4: Capture `OIek4b` with our test export's UUID**

```bash
EXPORT_UUID=$(jq -r '.[1][0][1][1]' testdata/responses/fhjYTc-with-in-progress.json)
gxodus debug-api --rpcid OIek4b --version generic --args "[[\"$EXPORT_UUID\"]]" > testdata/responses/OIek4b-status.json
```

If it returns export status → it's `getStatus`. If it returns a confirmation → it might be `cancel` or `delete`. If it returns the export with extra detail → it's a "get" call.

- [ ] **Step 5: Document what each response contains**

Update `docs/spikes/2026-05-02-batchexecute-api.md` with the new findings — specifically:
- The `U5lrKc` create response shape (this is the highest-value missing piece).
- What `OIek4b` actually does.
- Any new structural insights.

- [ ] **Step 6: Commit fixtures + docs update**

```bash
git add testdata/responses/ docs/spikes/2026-05-02-batchexecute-api.md
git commit -m "Capture batchexecute response fixtures for fhjYTc, U5lrKc, OIek4b"
```

---

## Task 7: Domain types based on captured responses

Define `Export`, `ExportStatus`, `CreateExportOptions` types using the real shapes from Task 6's captures.

**Files:**
- Create: `internal/takeoutapi/types.go`
- Create: `internal/takeoutapi/types_test.go`

- [ ] **Step 1: Create the failing test** — `internal/takeoutapi/types_test.go`:

```go
package takeoutapi

import (
	"os"
	"testing"
)

func TestParseExport_FromFhjYTcFixture(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/responses/fhjYTc-with-in-progress.json")
	if err != nil {
		t.Skipf("fixture not present (run Task 6 to capture): %v", err)
	}

	exports, err := parseExportListResponse(raw)
	if err != nil {
		t.Fatalf("parseExportListResponse: %v", err)
	}

	if len(exports) == 0 {
		t.Fatal("expected at least one export in fixture")
	}

	e := exports[0]
	if e.UUID == "" {
		t.Errorf("export UUID empty")
	}
	if e.Status == StatusUnknown {
		t.Errorf("export status not parsed: %+v", e)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/takeoutapi/ -run TestParseExport -v`
Expected: FAIL — types and `parseExportListResponse` undefined (or skipped if fixture missing).

- [ ] **Step 3: Implement** — create `internal/takeoutapi/types.go`:

```go
package takeoutapi

import (
	"encoding/json"
	"fmt"
	"time"
)

// ExportStatus is the parsed state of a Takeout export.
type ExportStatus int

const (
	StatusUnknown ExportStatus = iota
	StatusInProgress
	StatusComplete
	StatusFailed
	StatusExpired
)

func (s ExportStatus) String() string {
	switch s {
	case StatusInProgress:
		return "in_progress"
	case StatusComplete:
		return "complete"
	case StatusFailed:
		return "failed"
	case StatusExpired:
		return "expired"
	default:
		return "unknown"
	}
}

// Export is a parsed Takeout export from the fhjYTc list response.
type Export struct {
	UUID         string
	CreatedAt    time.Time
	Status       ExportStatus
	DownloadURLs []string // populated when Status == StatusComplete
}

// CreateExportOptions configures a new export request (sent to U5lrKc).
type CreateExportOptions struct {
	Products []string // slugs like "drive", "gmail", "bond" (Access Log Activity)
	Format   string   // "ZIP" or "TGZ"
	SizeBytes int64   // archive split size, e.g. 2*1024*1024*1024 for 2 GB
	Frequency string  // "once" | "every_2_months" — translated to internal codes
}

// parseExportListResponse extracts Export structs from an fhjYTc response payload.
// The shape (positional indexes) was reverse-engineered in the 2026-05-02 spike.
// See docs/spikes/2026-05-02-batchexecute-api.md.
func parseExportListResponse(raw json.RawMessage) ([]*Export, error) {
	// Top-level shape: [null, [[null, [<export-fields>]]], null, "<userId>", false, [<duplicate>]]
	var top []json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("unmarshal top: %w", err)
	}
	if len(top) < 2 {
		return nil, fmt.Errorf("response too short: %d top-level elements", len(top))
	}

	var wrapper [][]json.RawMessage
	if err := json.Unmarshal(top[1], &wrapper); err != nil {
		return nil, fmt.Errorf("unmarshal wrapper: %w", err)
	}

	var exports []*Export
	for _, w := range wrapper {
		if len(w) < 2 {
			continue
		}
		exp, err := parseExportFields(w[1])
		if err != nil {
			continue // skip malformed
		}
		exports = append(exports, exp)
	}
	return exports, nil
}

// parseExportFields parses one export's positional-indexed fields.
// Positions per the 2026-05-02 spike:
//   [1] = UUID, [9] = status code, [22] = creation timestamp ms.
// Download URLs: TBD by Task 6 capture; for now this returns empty.
func parseExportFields(raw json.RawMessage) (*Export, error) {
	var fields []json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("unmarshal fields: %w", err)
	}
	if len(fields) < 23 {
		return nil, fmt.Errorf("export fields too short: %d", len(fields))
	}

	exp := &Export{}

	if err := json.Unmarshal(fields[1], &exp.UUID); err != nil {
		return nil, fmt.Errorf("parsing UUID: %w", err)
	}

	var statusCode int
	if err := json.Unmarshal(fields[9], &statusCode); err == nil {
		exp.Status = mapStatusCode(statusCode)
	}

	var createdMs int64
	if err := json.Unmarshal(fields[22], &createdMs); err == nil {
		exp.CreatedAt = time.UnixMilli(createdMs)
	}

	return exp, nil
}

// mapStatusCode is best-guess pending Task 6 capture of completed exports.
// Position [9] = 0 confirmed for in-progress. Other values inferred until proven.
func mapStatusCode(code int) ExportStatus {
	switch code {
	case 0:
		return StatusInProgress
	case 1:
		return StatusComplete
	case 2:
		return StatusFailed
	case 3:
		return StatusExpired
	default:
		return StatusUnknown
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/takeoutapi/ -v`
Expected: PASS if fixture exists, SKIP if not.

- [ ] **Step 5: Commit**

```bash
git add internal/takeoutapi/types.go internal/takeoutapi/types_test.go
git commit -m "Add Export, ExportStatus types + parser for fhjYTc response"
```

---

## Task 8: `Client.ListExports` method

The high-level method that calls `fhjYTc` and returns parsed Export structs.

**Files:**
- Create: `internal/takeoutapi/exports.go`
- Create: `internal/takeoutapi/exports_test.go`

- [ ] **Step 1: Create the failing test** — `internal/takeoutapi/exports_test.go`:

```go
package takeoutapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestClient_ListExports(t *testing.T) {
	fixture, err := os.ReadFile("../../testdata/responses/fhjYTc-with-in-progress.json")
	if err != nil {
		t.Skipf("fixture not present: %v", err)
	}

	// Wrap the inner JSON in a batchexecute envelope.
	envelope := []byte(")]}'\n" + intToString(len(fixture)+50) + "\n" +
		`[["wrb.fr","fhjYTc",` + jsonString(string(fixture)) + `,null,null,null,"generic"]]`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			_, _ = w.Write([]byte(`<html><script>WIZ_global_data = {"SNlM0e":"AT","cfb2h":"BL"};</script></html>`))
			return
		}
		_, _ = w.Write(envelope)
	}))
	defer srv.Close()

	c := newClientForTest(srv.URL, []*http.Cookie{{Name: "SID", Value: "fake"}})
	exports, err := c.ListExports(context.Background())
	if err != nil {
		t.Fatalf("ListExports: %v", err)
	}
	if len(exports) == 0 {
		t.Fatal("expected at least one export")
	}
	if exports[0].UUID == "" {
		t.Error("export UUID empty")
	}
}

// Test helpers — encode a Go string as a JSON string literal.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func intToString(n int) string {
	return strconv.Itoa(n)
}
```

(Adjust imports — needs `encoding/json` and `strconv`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/takeoutapi/ -run TestClient_ListExports -v`
Expected: FAIL — `ListExports` undefined.

- [ ] **Step 3: Implement** — create `internal/takeoutapi/exports.go`:

```go
package takeoutapi

import (
	"context"
	"fmt"
)

// ListExports returns all exports visible on the user's manage-exports page.
// Includes in-progress, complete, and recently-expired exports.
func (c *Client) ListExports(ctx context.Context) ([]*Export, error) {
	raw, err := c.CallRPC(ctx, "fhjYTc", "[]", "generic")
	if err != nil {
		return nil, fmt.Errorf("listing exports: %w", err)
	}
	return parseExportListResponse(raw)
}

// GetExport fetches a single export by UUID. Returns nil + nil error if not found.
func (c *Client) GetExport(ctx context.Context, uuid string) (*Export, error) {
	exports, err := c.ListExports(ctx)
	if err != nil {
		return nil, err
	}
	for _, e := range exports {
		if e.UUID == uuid {
			return e, nil
		}
	}
	return nil, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/takeoutapi/ -v`
Expected: PASS — `TestClient_ListExports` passes (assuming fixture present).

- [ ] **Step 5: Commit**

```bash
git add internal/takeoutapi/exports.go internal/takeoutapi/exports_test.go
git commit -m "Add Client.ListExports and Client.GetExport"
```

---

## Task 9: `Client.CreateExport` method

Translates `CreateExportOptions` into the positional `U5lrKc` args and makes the call. The exact frequency/flag enum values may need adjustment based on Task 6 captures.

**Files:**
- Modify: `internal/takeoutapi/exports.go` (append)
- Modify: `internal/takeoutapi/exports_test.go` (append)

- [ ] **Step 1: Add the failing test** — append to `internal/takeoutapi/exports_test.go`:

```go
func TestClient_CreateExport_PayloadShape(t *testing.T) {
	var capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			_, _ = w.Write([]byte(`<html><script>WIZ_global_data = {"SNlM0e":"AT","cfb2h":"BL"};</script></html>`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		capturedBody = string(body)
		// Minimal valid empty response.
		_, _ = w.Write([]byte(")]}'\n45\n[[\"wrb.fr\",\"U5lrKc\",\"[\\\"OK\\\"]\",null,null,null,\"generic\"]]"))
	}))
	defer srv.Close()

	c := newClientForTest(srv.URL, []*http.Cookie{{Name: "SID", Value: "fake"}})
	_, err := c.CreateExport(context.Background(), CreateExportOptions{
		Products:  []string{"drive", "bond"},
		Format:    "ZIP",
		SizeBytes: 2 * 1024 * 1024 * 1024, // 2 GB
		Frequency: "once",
	})
	if err != nil {
		t.Fatalf("CreateExport: %v", err)
	}

	// Verify the captured body contains key payload markers.
	must := []string{
		`U5lrKc`,
		`ac.t.st`,
		`drive`,
		`bond`,
		`ZIP`,
		`2147483648`, // 2 GB in bytes
	}
	for _, m := range must {
		if !strings.Contains(capturedBody, m) {
			t.Errorf("body missing %q: %s", m, capturedBody)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/takeoutapi/ -run TestClient_CreateExport -v`
Expected: FAIL — `CreateExport` undefined.

- [ ] **Step 3: Implement** — append to `internal/takeoutapi/exports.go`:

```go
import (
	"encoding/json"
)

// CreateExport submits a new Takeout export request. Returns the (best-effort)
// parsed response — may include the new export's UUID once we know the exact
// U5lrKc response shape (see Task 6 / docs/spikes/2026-05-02-batchexecute-api.md).
func (c *Client) CreateExport(ctx context.Context, opts CreateExportOptions) (*Export, error) {
	args, err := buildCreateExportArgs(opts)
	if err != nil {
		return nil, fmt.Errorf("building create-export args: %w", err)
	}

	raw, err := c.CallRPC(ctx, "U5lrKc", string(args), "generic")
	if err != nil {
		return nil, fmt.Errorf("creating export: %w", err)
	}

	// Best-effort: try to extract a UUID from the response. If the shape is
	// different than expected, return a minimal Export{} so the caller can
	// fall back to ListExports to find the new one by recency.
	exp := &Export{Status: StatusInProgress}
	var anyResp interface{}
	if err := json.Unmarshal(raw, &anyResp); err == nil {
		if uuid := scrapeUUID(anyResp); uuid != "" {
			exp.UUID = uuid
		}
	}
	return exp, nil
}

// buildCreateExportArgs constructs the positional args payload for U5lrKc.
// Positions documented in docs/spikes/2026-05-02-batchexecute-api.md.
func buildCreateExportArgs(opts CreateExportOptions) ([]byte, error) {
	products := make([][]string, len(opts.Products))
	for i, p := range opts.Products {
		products[i] = []string{p}
	}

	freqCode, ok := frequencyCodes[opts.Frequency]
	if !ok && opts.Frequency != "" {
		return nil, fmt.Errorf("unknown frequency %q (use once|every_2_months)", opts.Frequency)
	}
	if opts.Frequency == "" {
		freqCode = frequencyCodes["once"]
	}

	format := opts.Format
	if format == "" {
		format = "ZIP"
	}

	size := opts.SizeBytes
	if size == 0 {
		size = 2 * 1024 * 1024 * 1024 // default 2 GB
	}

	args := []interface{}{
		"ac.t.st",
		products,
		format,
		nil,
		freqCode,
		nil,
		size,
		1, // unknown flag — captured value
		nil, nil, nil,
		"2", // unknown trailing — captured value
	}
	return json.Marshal(args)
}

// frequencyCodes maps human-friendly names to the integer codes Google uses.
// Values are best-guess from a single capture; adjust when Task 6 captures
// every-2-months variant for comparison.
var frequencyCodes = map[string]int{
	"once":           5,
	"every_2_months": 6, // GUESS — confirm with capture
}

// scrapeUUID walks an arbitrary parsed-JSON tree looking for a UUID-shaped
// string (8-4-4-4-12 hex). Useful when we don't know the exact response shape.
func scrapeUUID(v interface{}) string {
	switch t := v.(type) {
	case string:
		if isUUID(t) {
			return t
		}
	case []interface{}:
		for _, item := range t {
			if u := scrapeUUID(item); u != "" {
				return u
			}
		}
	case map[string]interface{}:
		for _, item := range t {
			if u := scrapeUUID(item); u != "" {
				return u
			}
		}
	}
	return ""
}

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func isUUID(s string) bool {
	return uuidRE.MatchString(s)
}
```

(Add `"regexp"` to imports.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/takeoutapi/ -v`
Expected: PASS — all tests including `TestClient_CreateExport_PayloadShape`.

- [ ] **Step 5: Commit**

```bash
git add internal/takeoutapi/exports.go internal/takeoutapi/exports_test.go
git commit -m "Add Client.CreateExport with U5lrKc args builder"
```

---

## Task 10: Wire `CreateExport` into `cli/export.go`

Replace the chromedp-based `browser.InitiateExport` call with `takeoutapi.Client.CreateExport`. Keep the cookie loading (still from auth.LoadSession). Drop the browser context creation.

**Files:**
- Modify: `internal/cli/export.go`

- [ ] **Step 1: Read the current call site** — locate `internal/cli/export.go` lines 80-115 (the browser context creation and `InitiateExport` call).

- [ ] **Step 2: Replace the browser+initiate block**

Replace (current code):

```go
		browserCtx, cancel, err := browser.NewContext(ctx, browser.Options{
			Headless:    false,
			RemoteURL:   remoteChrome,
			UserDataDir: browser.ProfileDir(),
		})
		if err != nil {
			return fmt.Errorf("creating browser: %w", err)
		}
		defer cancel()

		if err := browser.InjectCookies(browserCtx, cookies); err != nil {
			return fmt.Errorf("injecting cookies: %w", err)
		}

		_, err = browser.InitiateExport(browserCtx, browser.ExportOptions{
			FileSize:     cfg.FileSize,
			FileType:     cfg.FileType,
			Frequency:    cfg.Frequency,
			ActivityLogs: cfg.ActivityLogs,
		})
```

With:

```go
		client, err := takeoutapi.NewClient(cookies, 0)
		if err != nil {
			return fmt.Errorf("creating takeout client: %w", err)
		}

		products := defaultProductSlugs()
		if cfg.ActivityLogs {
			products = append(products, "bond") // "bond" is the slug for Access Log Activity
		}

		sizeBytes, err := parseFileSize(cfg.FileSize)
		if err != nil {
			notify.Fire(cfg.Notify, "error", notify.EventData{Error: err.Error()})
			return fmt.Errorf("file size: %w", err)
		}

		newExport, err := client.CreateExport(ctx, takeoutapi.CreateExportOptions{
			Products:  products,
			Format:    strings.ToUpper(cfg.FileType),
			SizeBytes: sizeBytes,
			Frequency: cfg.Frequency,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "CreateExport failed: %v\n", err)
			notify.Fire(cfg.Notify, "error", notify.EventData{Error: err.Error()})
			os.Exit(2)
		}

		fmt.Printf("Export submitted (uuid=%s)\n", newExport.UUID)
```

- [ ] **Step 3: Add helper functions** — append to `internal/cli/export.go`:

```go
// defaultProductSlugs returns the canonical product list for a "select all"
// Takeout export, minus "bond" (Access Log Activity) which is opt-in.
// Captured 2026-05-02; if Google adds a product, this list will be missing it.
func defaultProductSlugs() []string {
	return []string{
		"alerts", "analytics", "android", "arts_and_culture", "course_kit",
		"blogger", "brand_accounts", "calendar", "chrome", "chrome_os",
		"chrome_web_store", "classroom", "contacts", "discover", "drive",
		"family", "fiber", "fit", "fitbit", "ai_sandbox", "gemini",
		"google_account", "google_ads", "my_business", "hangouts_chat",
		"google_cloud_search", "developer_platform", "earth", "feedback",
		"google_finance", "support_content", "meet", "google_one", "google_pay",
		"photos", "books", "play_games_services", "play_movies", "play",
		"podcasts", "hats_surveys", "shopping", "google_store", "google_wallet",
		"apps_marketplace", "groups", "home_graph", "keep", "gmail",
		"manufacturer_center", "maps", "local_actions", "merchant_center",
		"messages", "my_activity", "nest", "news", "package_tracking",
		"search_console", "personal_safety", "assisted_calling", "backlight",
		"pixel_telemetry", "profile", "custom_search", "my_orders", "reminders",
		"save", "search_ugc", "search_notifications", "streetview", "tasks",
		"location_history", "voice", "voice_and_audio_activity", "workflows",
	}
}

// parseFileSize converts a config string like "2GB" to bytes.
func parseFileSize(size string) (int64, error) {
	switch size {
	case "", "2GB":
		return 2 * 1024 * 1024 * 1024, nil
	case "1GB":
		return 1 * 1024 * 1024 * 1024, nil
	case "4GB":
		return 4 * 1024 * 1024 * 1024, nil
	case "10GB":
		return 10 * 1024 * 1024 * 1024, nil
	case "50GB":
		return 50 * 1024 * 1024 * 1024, nil
	default:
		return 0, fmt.Errorf("unknown file_size %q", size)
	}
}
```

- [ ] **Step 4: Update imports** — modify the import block in `internal/cli/export.go`:

Add `"github.com/thinkjk/gxodus/internal/takeoutapi"` and ensure `"strings"` is present. Remove `"github.com/thinkjk/gxodus/internal/browser"` if no other code in the file uses it (check for `browser.` references — `browser.NewContext`, `browser.InjectCookies`, `browser.InitiateExport`, `browser.ExportOptions` should all be gone after this task; but the auth flow upstream may still use it — verify).

- [ ] **Step 5: Verify it compiles**

Run: `go build ./...`
Expected: clean build.

- [ ] **Step 6: Run all tests**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/export.go
git commit -m "Wire takeoutapi.Client.CreateExport into export command"
```

---

## Task 11: Wire `ListExports` into poller

Replace the per-cycle chromium spawn in `internal/poller/poller.go` with a `takeoutapi.Client.GetExport` lookup.

**Files:**
- Modify: `internal/poller/poller.go`

- [ ] **Step 1: Replace `checkOnce`**

Find the existing `checkOnce` function (currently spawns chromium, navigates, parses status). Replace it with:

```go
func checkOnce(ctx context.Context, cfg Config) (*browser.ExportStatus, error) {
	client, err := takeoutapi.NewClient(cfg.Cookies, 0)
	if err != nil {
		return nil, fmt.Errorf("creating takeout client: %w", err)
	}

	// If the caller passed a specific UUID to track, fetch just that export.
	// Otherwise return the most recent one.
	exports, err := client.ListExports(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing exports: %w", err)
	}

	if len(exports) == 0 {
		return &browser.ExportStatus{State: "none"}, nil
	}

	// Most recent is index 0 (Google sorts newest first in the UI).
	e := exports[0]

	switch e.Status {
	case takeoutapi.StatusComplete:
		return &browser.ExportStatus{State: "complete", DownloadURLs: e.DownloadURLs}, nil
	case takeoutapi.StatusInProgress:
		return &browser.ExportStatus{State: "in_progress"}, nil
	case takeoutapi.StatusFailed:
		return &browser.ExportStatus{State: "failed"}, nil
	case takeoutapi.StatusExpired:
		return &browser.ExportStatus{State: "expired"}, nil
	default:
		return &browser.ExportStatus{State: "unknown"}, nil
	}
}
```

- [ ] **Step 2: Update `Config` struct** — find `internal/poller/poller.go`'s `Config` and ensure it carries cookies (not RemoteURL):

```go
type Config struct {
	Interval time.Duration
	Cookies  []*http.Cookie
}
```

(Remove `RemoteURL`. Update the call site in `cli/export.go` to pass cookies.)

- [ ] **Step 3: Update import block**

Add `"github.com/thinkjk/gxodus/internal/takeoutapi"`. Keep `"github.com/thinkjk/gxodus/internal/browser"` only because `ExportStatus` is still defined there (Task 13 will move it).

- [ ] **Step 4: Update the call site in `cli/export.go`**

Find:
```go
		pollResult, err := poller.Poll(ctx, poller.Config{
			Interval:  pollDuration,
			RemoteURL: remoteChrome,
			Cookies:   cookies,
		})
```

Replace `RemoteURL: remoteChrome,` with nothing (remove the line):
```go
		pollResult, err := poller.Poll(ctx, poller.Config{
			Interval: pollDuration,
			Cookies:  cookies,
		})
```

- [ ] **Step 5: Verify it compiles**

Run: `go build ./...`
Expected: clean build.

- [ ] **Step 6: Run all tests**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/poller/poller.go internal/cli/export.go
git commit -m "Wire takeoutapi.Client into poller (no more chromium per cycle)"
```

---

## Task 12: Wire into `cli/status.go`

Same pattern as the poller — replace the chromedp-based `browser.CheckExportStatus` with `takeoutapi.Client.ListExports`.

**Files:**
- Modify: `internal/cli/status.go`

- [ ] **Step 1: Replace the browser-context block**

Replace (current code):

```go
		browserCtx, cancel, err := browser.NewContext(ctx, browser.Options{
			Headless:    false,
			RemoteURL:   remoteChrome,
			UserDataDir: browser.ProfileDir(),
		})
		if err != nil {
			return fmt.Errorf("creating browser: %w", err)
		}
		defer cancel()

		if err := browser.InjectCookies(browserCtx, cookies); err != nil {
			return fmt.Errorf("injecting cookies: %w", err)
		}

		status, err := browser.CheckExportStatus(browserCtx)
		if err != nil {
			return fmt.Errorf("checking status: %w", err)
		}
```

With:

```go
		client, err := takeoutapi.NewClient(cookies, 0)
		if err != nil {
			return fmt.Errorf("creating takeout client: %w", err)
		}

		exports, err := client.ListExports(ctx)
		if err != nil {
			return fmt.Errorf("listing exports: %w", err)
		}

		if len(exports) == 0 {
			fmt.Println("No exports found.")
			return nil
		}

		for _, e := range exports {
			fmt.Printf("- %s (%s) created %s\n",
				e.UUID,
				e.Status,
				e.CreatedAt.Format("2006-01-02 15:04"))
			for _, url := range e.DownloadURLs {
				fmt.Printf("    download: %s\n", url)
			}
		}
		return nil
```

- [ ] **Step 2: Update imports**

Add `"github.com/thinkjk/gxodus/internal/takeoutapi"`. Remove the `"github.com/thinkjk/gxodus/internal/browser"` import if no other code in the file uses it.

- [ ] **Step 3: Verify it compiles**

Run: `go build ./...`
Expected: clean build.

- [ ] **Step 4: Run all tests**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/status.go
git commit -m "Wire takeoutapi.Client into status command"
```

---

## Task 13: Delete unused chromedp helpers

`browser.InitiateExport`, `browser.CheckExportStatus`, and all their helper functions (selectActivityLog, selectComboboxOption, setFileType, setFileSize, setFrequency, clickCreateExport, scrollAndClickNextStep, configureExportOptions, the takeout-specific URL constants) are no longer called. Delete them. Keep `dumpButtons`, `logPageState`, `Screenshot` (still useful when debugging the auth flow if it breaks).

**Files:**
- Modify: `internal/browser/takeout.go` (large delete)
- Modify: `internal/browser/takeout_test.go` (delete tests for deleted helpers)

- [ ] **Step 1: Identify what to delete**

Run `grep -rn "browser\.InitiateExport\|browser\.CheckExportStatus\|browser\.ExportOptions\|browser\.ExportStatus" internal/` from the repo root. Should return zero hits after Tasks 10-12.

If hits remain, fix those call sites first. Then proceed to delete.

- [ ] **Step 2: Delete the unused functions**

In `internal/browser/takeout.go`, delete:
- `takeoutURL`, `takeoutManageURL` constants
- `ExportResult`, `ExportOptions`, `ExportStatus` types
- `InitiateExport`, `CheckExportStatus` functions
- `selectActivityLog`, `selectComboboxOption`, `setFileType`, `setFileSize`, `setFrequency`, `clickCreateExport`, `scrollAndClickNextStep`, `configureExportOptions`, `fileTypeDisplayText`, `fileSizeDisplayText`, `frequencyRadioValue` functions

Keep:
- `dumpButtons`, `logPageState`, `Screenshot`, `wrapErr` — useful for debugging the login flow
- The package declaration and imports (prune unused imports)

- [ ] **Step 3: Delete the unit tests for deleted helpers**

In `internal/browser/takeout_test.go`, delete:
- `TestFileTypeDisplayText`
- `TestFileSizeDisplayText`
- `TestFrequencyRadioValue`

If the file becomes empty (just `package browser`), delete the whole file.

- [ ] **Step 4: Verify it compiles**

Run: `go build ./...`
Expected: clean build with no unused-import errors.

- [ ] **Step 5: Run all tests**

Run: `go test ./...`
Expected: PASS — no broken references after the deletions.

- [ ] **Step 6: Commit**

```bash
git add internal/browser/takeout.go internal/browser/takeout_test.go
git commit -m "Delete chromedp-based Takeout export helpers (replaced by takeoutapi)"
```

---

## Task 14: Docker entrypoint cleanup

Xvfb + noVNC are only needed for interactive login now. Don't pre-start them in the long-running export loop; the `run_auth` path already starts them on demand.

**Files:**
- Modify: `docker-entrypoint.sh`

- [ ] **Step 1: Remove the pre-start in the export loop**

Find the export-loop block in `docker-entrypoint.sh` (around line 107):

```sh
elif [ "$COMMAND" = "export" ]; then
    if [ -n "$GXODUS_INTERVAL" ]; then
        # Long-running scheduled mode...
        ensure_xvfb       # ← REMOVE THIS LINE — only needed for re-auth
```

Delete the `ensure_xvfb` line at the top of the export branch. The `run_auth` function already calls `ensure_xvfb` itself when re-auth is needed.

- [ ] **Step 2: Update the entrypoint banner messaging**

Find the top-level `ensure_xvfb` call (around line 61):

```sh
# Xvfb is needed for non-interactive export too — chromium runs non-headless
# on display :99 to share the same fingerprint as the auth chromium.
ensure_xvfb
```

Replace the comment and remove the unconditional call:

```sh
# Xvfb is now only needed for interactive re-auth (run_auth starts it on
# demand). Export, status, and poll all use HTTP via the takeoutapi package.
```

(Delete the `ensure_xvfb` line itself.)

- [ ] **Step 3: Sanity-check shell**

Run: `sh -n docker-entrypoint.sh`
Expected: clean parse.

- [ ] **Step 4: Commit**

```bash
git add docker-entrypoint.sh
git commit -m "Stop pre-starting Xvfb on container boot — only needed for re-auth"
```

---

## Task 15: End-to-end verification on Unraid

**Pre-flight:**
- All previous tasks committed and pushed.
- Docker image built and pushed (Tasks 1-14 produced a working binary).
- A valid `session.enc` exists on the Unraid container's `/config` volume.

- [ ] **Step 1: Push commits**

```bash
git push origin main
```

- [ ] **Step 2: Build and push the image**

```bash
SHORT=$(git rev-parse --short HEAD)
docker buildx build --platform linux/amd64 \
  -t ghcr.io/thinkjk/gxodus:main \
  -t ghcr.io/thinkjk/gxodus:sha-$SHORT \
  --push .
```

- [ ] **Step 3: Pull on Unraid**

```bash
docker pull ghcr.io/thinkjk/gxodus:main
docker restart gxodus
docker logs -f gxodus
```

- [ ] **Step 4: Verify export creation via API**

Expected log output:

```
gxodus: command=export
...
Loaded N cookies from saved session.
Export submitted (uuid=...)
Polling for export completion every 1h0m0s...
```

The container should start, load cookies, fire one HTTP `U5lrKc` call, and immediately enter the polling loop. **No chromium spawn for export.**

- [ ] **Step 5: Verify polling via API**

After ~1 hour, expected log:

```
[14:23] Export still in progress...
```

OR (if the user's existing export from 2026-05-02 has completed by now):

```
Export complete! (took ...)
Downloaded N file(s), total size: X GB
```

Either way, **no chromium spawn for the poll**. Verify with `docker exec gxodus ps aux | grep -i chromium` — should be empty (chromium not running).

- [ ] **Step 6: Verify auth still works**

Trigger a fresh re-auth by deleting the session and restarting:

```bash
docker exec gxodus rm /config/session.enc
docker restart gxodus
```

Expected: `Starting noVNC stack...` appears in logs (proving on-demand Xvfb startup works), chromium spawns for login, user can complete login via http://<unraid-ip>:6080/vnc.html, gxodus saves new session, export proceeds via API.

- [ ] **Step 7: Verify status command via API**

```bash
docker exec gxodus gxodus status --config /config/config.toml
```

Expected: prints the current export list with UUIDs, status, and creation times. **Sub-second response** (vs. the multi-second chromium spawn it used to need).

---

## Verification

**Automated (per task):**

```bash
go test ./...      # all unit tests pass
go build ./...     # binary compiles
sh -n docker-entrypoint.sh
```

**Manual end-to-end (Task 15):**

- Container starts and triggers an export via HTTP, no chromium spawn.
- Polling cycles complete in <1 second each (vs. ~10 seconds chromium spawn before).
- Re-auth via noVNC still works (chromium starts on-demand for that flow only).
- `gxodus status` returns instantly.

---

## What's still TBD after this plan ships

1. **Frequency code values for `every_2_months`** — Task 9 hardcodes a guess (`6`). Confirm by capturing one create call with frequency=every_2_months and adjust the map.
2. **Status enum values for "complete" / "failed" / "expired"** — Task 7 hardcodes guesses (1, 2, 3). Confirm when an export actually completes by re-running `debug-api --rpcid fhjYTc` and inspecting position [9].
3. **Download URL location in `fhjYTc` response** — currently `parseExportFields` returns `DownloadURLs: nil` always. Once we have a captured "completed" response, update `parseExportFields` to extract the URLs from the right position.
4. **Build-label rotation** — `extractTokens` reads the label fresh from the page on every `Client` instantiation (cached for the client lifetime). If a long-running container outlives a Google deploy, the label will go stale and batchexecute calls will start failing. Fix: add a "refresh tokens on 4xx" retry loop in `Client.CallRPC`.
5. **rpcid rotation** — `U5lrKc`/`fhjYTc` are hardcoded. If Google rotates them in a UI deploy, all calls fail. Fix: add an "extract rpcids from page JS" helper, similar to `extractTokens`.

These are follow-up improvements, not blockers for this plan. The MVP this plan delivers is "export + poll + status work via HTTP, no chromium for normal operation."
