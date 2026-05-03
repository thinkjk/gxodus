package takeoutapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
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

// NewClient creates a Client authenticated via the given session cookies.
// userIdx is the Google account index in multi-account browser sessions
// (typically 0 for the first signed-in account, 2 for a third, etc.).
// gxodus's saved sessions usually need userIdx=0 unless the user signed in
// to a non-primary account.
func NewClient(cookies []*http.Cookie, userIdx int) (*Client, error) {
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

// CallRPC performs one batchexecute round-trip and returns the inner result
// as raw JSON bytes (the doubly-escaped payload, parsed back to a JSON value).
func (c *Client) CallRPC(ctx context.Context, rpcid, args, version string) ([]byte, error) {
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
