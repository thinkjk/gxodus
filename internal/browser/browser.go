package browser

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/storage"
	"github.com/chromedp/chromedp"
	"github.com/thinkjk/gxodus/internal/config"
)

type Options struct {
	Headless     bool
	RemoteURL    string // WebSocket URL for remote Chrome
	UserDataDir  string
}

// NewContext creates a chromedp context with the given options.
// Returns the context, a cancel function, and any error.
func NewContext(ctx context.Context, opts Options) (context.Context, context.CancelFunc, error) {
	if opts.RemoteURL != "" {
		return newRemoteContext(ctx, opts.RemoteURL)
	}
	return newLocalContext(ctx, opts)
}

func newLocalContext(ctx context.Context, opts Options) (context.Context, context.CancelFunc, error) {
	chromedpOpts := []chromedp.ExecAllocatorOption{
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.DisableGPU,
		chromedp.NoSandbox,
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
	}

	// Use CHROME_PATH env var if set (for Docker with Chromium)
	if chromePath := os.Getenv("CHROME_PATH"); chromePath != "" {
		chromedpOpts = append(chromedpOpts, chromedp.ExecPath(chromePath))
	}

	if opts.Headless {
		chromedpOpts = append(chromedpOpts, chromedp.Headless)
	}

	if opts.UserDataDir != "" {
		chromedpOpts = append(chromedpOpts, chromedp.UserDataDir(opts.UserDataDir))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, chromedpOpts...)
	taskCtx, taskCancel := chromedp.NewContext(allocCtx)

	cancel := func() {
		taskCancel()
		allocCancel()
	}

	return taskCtx, cancel, nil
}

func newRemoteContext(ctx context.Context, wsURL string) (context.Context, context.CancelFunc, error) {
	// NoModifyURL: chromedp's default behavior is to fetch /json/version from
	// the URL and use the webSocketDebuggerUrl it returns. Browserless v2
	// returns chromium's internal bind address (ws://0.0.0.0:3000/...) there,
	// which is unreachable from outside the browserless container. Use the URL
	// the user gave us verbatim.
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(ctx, wsURL, chromedp.NoModifyURL)
	taskCtx, taskCancel := chromedp.NewContext(allocCtx)

	cancel := func() {
		taskCancel()
		allocCancel()
	}

	return taskCtx, cancel, nil
}

// InjectCookies sets cookies in the browser from saved session data.
func InjectCookies(ctx context.Context, cookies []*http.Cookie) error {
	for _, c := range cookies {
		domain := c.Domain
		if domain == "" {
			domain = ".google.com"
		}

		expr := cdp.TimeSinceEpoch(time.Now().Add(24 * time.Hour))
		err := chromedp.Run(ctx,
			network.SetCookie(c.Name, c.Value).
				WithDomain(domain).
				WithPath(c.Path).
				WithSecure(c.Secure).
				WithHTTPOnly(c.HttpOnly).
				WithExpires(&expr),
		)
		if err != nil {
			return fmt.Errorf("setting cookie %s: %w", c.Name, err)
		}
	}
	fmt.Printf("[diag] Injected %d cookies into browser.\n", len(cookies))
	return nil
}

// ExtractCookies gets all browser cookies. Uses Storage.getCookies (browser-
// wide) rather than Network.getCookies (current-page only) because when chromedp
// attaches via NewRemoteAllocator it lands on a fresh blank tab whose "current
// URL" is about:blank, which would return no cookies.
func ExtractCookies(ctx context.Context) ([]*http.Cookie, error) {
	var cdpCookies []*network.Cookie
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		cdpCookies, err = storage.GetCookies().Do(ctx)
		return err
	}))
	if err != nil {
		return nil, fmt.Errorf("getting cookies: %w", err)
	}

	var googleCookies, total int
	var cookies []*http.Cookie
	for _, c := range cdpCookies {
		total++
		if strings.Contains(c.Domain, "google.com") {
			googleCookies++
		}
		cookies = append(cookies, &http.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HttpOnly: c.HTTPOnly,
		})
	}
	fmt.Printf("Extracted %d cookies (%d google.com).\n", total, googleCookies)

	return cookies, nil
}

// Screenshot captures the current page state for debugging.
func Screenshot(ctx context.Context, name string) error {
	debugDir := filepath.Join(config.ConfigDir(), "debug")
	if err := os.MkdirAll(debugDir, 0700); err != nil {
		return err
	}

	var buf []byte
	if err := chromedp.Run(ctx, chromedp.FullScreenshot(&buf, 90)); err != nil {
		return fmt.Errorf("taking screenshot: %w", err)
	}

	path := filepath.Join(debugDir, fmt.Sprintf("%s-%d.png", name, time.Now().Unix()))
	return os.WriteFile(path, buf, 0600)
}
