package takeoutapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Client makes batchexecute calls against takeout.google.com using session
// cookies extracted by gxodus's auth flow. Safe for concurrent use.
type Client struct {
	baseURL  string
	userIdx  int
	hc       *http.Client
	cookies  []*http.Cookie // all session cookies, attached manually to every request
	tokens   *PageTokens
	tokensMu sync.Mutex
	reqID    atomic.Int64
}

// NewClient creates a Client authenticated via the given session cookies.
// All provided cookies are attached to every request regardless of their
// original Domain attribute — gxodus only ever calls takeout.google.com
// from this client, so cross-domain leakage isn't a concern, and bypassing
// cookiejar's domain matching ensures account/myaccount-scoped cookies
// (which takeout sometimes needs) actually get sent.
func NewClient(cookies []*http.Cookie, userIdx int) (*Client, error) {
	filtered := filterCookies(cookies)
	fmt.Fprintf(os.Stderr, "[takeoutapi] NewClient: %d cookies in, %d after dedup+filter\n",
		len(cookies), len(filtered))
	return &Client{
		baseURL: "https://takeout.google.com",
		userIdx: userIdx,
		hc:      &http.Client{},
		cookies: filtered,
	}, nil
}

// newClientForTest is a constructor that points the base URL at httptest.
func newClientForTest(baseURL string, cookies []*http.Cookie) *Client {
	return &Client{
		baseURL: baseURL,
		userIdx: 0,
		hc:      &http.Client{},
		cookies: filterCookies(cookies),
	}
}

// filterCookies dedups cookies by name and skips ones that don't apply to
// takeout.google.com. The chromedp storage.GetCookies() call returns every
// cookie in the browser regardless of host — so if both ".google.com" and
// "accounts.google.com" set a cookie with the same name (different values),
// we'd send both and Google would reject the request as CookieMismatch.
//
// Strategy:
//   1. Skip __Host-* cookies (host-only, non-transferable to takeout).
//   2. Skip login-flow cookies (LSID, ACCOUNT_CHOOSER, SMSV) — these are
//      set by accounts.google.com during sign-in and confuse takeout's
//      session validation when present.
//   3. For each remaining cookie name, keep the one with the most-generic
//      domain (".google.com" > "google.com" > specific subdomain).
func filterCookies(in []*http.Cookie) []*http.Cookie {
	skipNames := map[string]bool{
		"LSID":            true,
		"ACCOUNT_CHOOSER": true,
		"SMSV":            true,
	}

	best := map[string]*http.Cookie{}
	for _, ck := range in {
		if strings.HasPrefix(ck.Name, "__Host-") {
			continue
		}
		if skipNames[ck.Name] {
			continue
		}

		existing, found := best[ck.Name]
		if !found || cookieDomainScore(ck) > cookieDomainScore(existing) {
			best[ck.Name] = ck
		}
	}

	out := make([]*http.Cookie, 0, len(best))
	for _, ck := range best {
		out = append(out, ck)
	}
	return out
}

// cookieDomainScore prefers cookies whose Domain applies to the broadest
// set of *.google.com hosts. Higher score wins.
//
//	Domain ".google.com" or "google.com" = 100  (applies to all subdomains)
//	Domain "takeout.google.com"          = 50   (applies specifically)
//	Other *.google.com subdomains         = 10   (probably wrong context but
//	                                              keep as last-resort fallback)
func cookieDomainScore(ck *http.Cookie) int {
	domain := strings.TrimPrefix(ck.Domain, ".")
	switch domain {
	case "google.com", "":
		return 100
	case "takeout.google.com":
		return 50
	default:
		return 10
	}
}

// applyCookies attaches every session cookie to the request as a header.
// Bypasses cookiejar's domain matching — see NewClient for rationale.
func (c *Client) applyCookies(req *http.Request) {
	for _, ck := range c.cookies {
		req.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
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
	fmt.Fprintf(os.Stderr, "[takeoutapi] ensureTokens: GET %s\n", pageURL)

	// Log cookies being sent (names only, no values for safety).
	names := make([]string, 0, len(c.cookies))
	for _, ck := range c.cookies {
		names = append(names, ck.Name)
	}
	fmt.Fprintf(os.Stderr, "[takeoutapi]   sending %d cookies: %v\n", len(c.cookies), names)

	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return fmt.Errorf("building page request: %w", err)
	}
	// Mimic a real Chromium User-Agent — Google sometimes serves stub pages
	// or redirects when it sees the default Go User-Agent.
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	c.applyCookies(req)

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("fetching page: %w", err)
	}
	defer resp.Body.Close()

	fmt.Fprintf(os.Stderr, "[takeoutapi]   HTTP %d %s\n", resp.StatusCode, resp.Status)
	fmt.Fprintf(os.Stderr, "[takeoutapi]   final URL (after redirects): %s\n", resp.Request.URL.String())
	fmt.Fprintf(os.Stderr, "[takeoutapi]   content-type: %s\n", resp.Header.Get("Content-Type"))
	fmt.Fprintf(os.Stderr, "[takeoutapi]   content-length header: %s\n", resp.Header.Get("Content-Length"))
	fmt.Fprintf(os.Stderr, "[takeoutapi]   set-cookie count: %d\n", len(resp.Header.Values("Set-Cookie")))

	html, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading page: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[takeoutapi]   actual body size: %d bytes\n", len(html))

	// Dump the full HTML to a file so we can inspect it offline if needed.
	dumpPath := dumpResponseBody("takeout-page", html)
	fmt.Fprintf(os.Stderr, "[takeoutapi]   full body dumped to: %s\n", dumpPath)

	// Quick markers — does the body contain the strings we expect?
	body := string(html)
	checkMarker(body, "WIZ_global_data")
	checkMarker(body, "SNlM0e")
	checkMarker(body, "cfb2h")
	checkMarker(body, "<title>")
	checkMarker(body, "accounts.google.com")
	checkMarker(body, "ServiceLogin")
	checkMarker(body, "takeout.google.com")

	// Extract title for quick identification of what page we got.
	if m := regexp.MustCompile(`<title>([^<]+)</title>`).FindStringSubmatch(body); len(m) > 1 {
		fmt.Fprintf(os.Stderr, "[takeoutapi]   page title: %q\n", m[1])
	}

	// First 800 chars of body to logs.
	excerpt := body
	if len(excerpt) > 800 {
		excerpt = excerpt[:800]
	}
	fmt.Fprintf(os.Stderr, "[takeoutapi]   body excerpt (first 800 chars):\n--- BEGIN EXCERPT ---\n%s\n--- END EXCERPT ---\n", excerpt)

	// Diagnostic: list every WIZ_global_data block + its cfb2h, so we can see
	// which one extractTokens picked (the page can embed >1 block; we want the
	// boq_takeoutuiserver one, not boq_identityfrontenduiserver).
	blocks := splitWizBlocks(body)
	fmt.Fprintf(os.Stderr, "[takeoutapi]   found %d WIZ_global_data block(s):\n", len(blocks))
	for i, b := range blocks {
		bl := "(no cfb2h)"
		if m := buildRE.FindStringSubmatch(b); len(m) >= 2 {
			bl = m[1]
		}
		fmt.Fprintf(os.Stderr, "[takeoutapi]     block %d: cfb2h=%s\n", i, bl)
	}

	tokens, err := extractTokens(body)
	if err != nil {
		return fmt.Errorf("extracting tokens from page (HTTP %d, %d bytes body, dumped to %s): %w",
			resp.StatusCode, len(html), dumpPath, err)
	}

	fmt.Fprintf(os.Stderr, "[takeoutapi]   ✓ extracted XSRF token (%d chars), buildLabel=%q, sessionID=%q\n",
		len(tokens.XSRF), tokens.BuildLabel, tokens.SessionID)

	c.tokens = tokens
	return nil
}

// dumpResponseBody writes raw bytes to a debug file for offline inspection.
// Returns the path written to (or an error string if the write failed).
func dumpResponseBody(prefix string, body []byte) string {
	dir := "/config/debug"
	if err := os.MkdirAll(dir, 0700); err != nil {
		dir = "/tmp"
		_ = os.MkdirAll(dir, 0700)
	}
	path := fmt.Sprintf("%s/%s-%d.html", dir, prefix, time.Now().Unix())
	if err := os.WriteFile(path, body, 0600); err != nil {
		return "(write failed: " + err.Error() + ")"
	}
	return path
}

// checkMarker logs whether the body contains a substring — useful for quick
// page identification.
func checkMarker(body, marker string) {
	fmt.Fprintf(os.Stderr, "[takeoutapi]   body contains %-22q: %v\n", marker, strings.Contains(body, marker))
}

// CallRPC performs one batchexecute round-trip and returns the inner result
// as raw JSON bytes (the doubly-escaped payload, parsed back to a JSON value).
func (c *Client) CallRPC(ctx context.Context, rpcid, args, version string) ([]byte, error) {
	argsExcerpt := args
	if len(argsExcerpt) > 200 {
		argsExcerpt = argsExcerpt[:200] + "..."
	}
	fmt.Fprintf(os.Stderr, "[takeoutapi] CallRPC: rpcid=%s version=%s args=%s\n", rpcid, version, argsExcerpt)

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
	if c.tokens.SessionID != "" {
		q.Set("f.sid", c.tokens.SessionID)
	}
	q.Set("bl", c.tokens.BuildLabel)
	q.Set("hl", "en")
	q.Set("pageId", "none")
	q.Set("soc-app", "1")
	q.Set("soc-platform", "1")
	q.Set("soc-device", "1")
	q.Set("_reqid", strconv.FormatInt(c.reqID.Add(100000), 10))
	q.Set("rt", "c")

	endpoint := fmt.Sprintf("%s/u/%d/_/TakeoutUi/data/batchexecute?%s", c.baseURL, c.userIdx, q.Encode())
	fmt.Fprintf(os.Stderr, "[takeoutapi]   POST %s\n", endpoint)
	fmt.Fprintf(os.Stderr, "[takeoutapi]   request body size: %d bytes\n", len(body))

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building rpc request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	req.Header.Set("X-Same-Domain", "1")
	req.Header.Set("Origin", c.baseURL)
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	// Takeout-specific extension header observed on every batchexecute call in
	// real browser captures. Reads tolerate its absence; writes (U5lrKc) may not.
	req.Header.Set("x-goog-ext-525002608-jspb", "[215]")
	// Referer is checked by some Google endpoints.
	req.Header.Set("Referer", fmt.Sprintf("%s/u/%d/", c.baseURL, c.userIdx))

	c.applyCookies(req)

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling rpc %s: %w", rpcid, err)
	}
	defer resp.Body.Close()

	fmt.Fprintf(os.Stderr, "[takeoutapi]   HTTP %d, content-type: %s, final URL: %s\n",
		resp.StatusCode, resp.Header.Get("Content-Type"), resp.Request.URL.String())

	respBytes, readErr := io.ReadAll(resp.Body)
	fmt.Fprintf(os.Stderr, "[takeoutapi]   response body size: %d bytes\n", len(respBytes))

	if resp.StatusCode != http.StatusOK {
		dumpPath := dumpResponseBody(fmt.Sprintf("rpc-%s-error", rpcid), respBytes)
		excerpt := string(respBytes)
		if len(excerpt) > 500 {
			excerpt = excerpt[:500]
		}
		return nil, fmt.Errorf("rpc %s: HTTP %d (body dumped to %s): %s",
			rpcid, resp.StatusCode, dumpPath, excerpt)
	}
	if readErr != nil {
		return nil, fmt.Errorf("reading rpc response: %w", readErr)
	}

	results, err := decodeResponse(respBytes)
	if err != nil {
		dumpPath := dumpResponseBody(fmt.Sprintf("rpc-%s-decode-fail", rpcid), respBytes)
		return nil, fmt.Errorf("decoding rpc %s response (dumped to %s): %w", rpcid, dumpPath, err)
	}

	fmt.Fprintf(os.Stderr, "[takeoutapi]   decoded %d rpc results\n", len(results))
	for _, r := range results {
		ex := string(r.RawJSON)
		if len(ex) > 100 {
			ex = ex[:100] + "..."
		}
		fmt.Fprintf(os.Stderr, "[takeoutapi]     - rpcid=%s rawJSON=%s\n", r.RpcID, ex)
	}

	for _, r := range results {
		if r.RpcID != rpcid {
			continue
		}
		if r.ErrorCode != 0 {
			dumpPath := dumpResponseBody(fmt.Sprintf("rpc-%s-error-%d", rpcid, r.ErrorCode), respBytes)
			return nil, fmt.Errorf("rpc %s returned error code %d (full body dumped to %s)",
				rpcid, r.ErrorCode, dumpPath)
		}
		if len(r.RawJSON) == 0 {
			dumpPath := dumpResponseBody(fmt.Sprintf("rpc-%s-empty", rpcid), respBytes)
			return nil, fmt.Errorf("rpc %s returned empty data (full body dumped to %s)",
				rpcid, dumpPath)
		}
		return r.RawJSON, nil
	}

	// No matching wrb.fr chunk at all — extreme edge case. Dump body.
	dumpPath := dumpResponseBody(fmt.Sprintf("rpc-%s-no-match", rpcid), respBytes)
	excerpt := string(respBytes)
	if len(excerpt) > 600 {
		excerpt = excerpt[:600]
	}
	return nil, fmt.Errorf("rpc %s not found in response (full body dumped to %s); body excerpt: %s",
		rpcid, dumpPath, excerpt)
}
