package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/chromedp/chromedp"
)

const (
	googleLoginURL      = "https://accounts.google.com"
	googleAccountURL    = "https://myaccount.google.com"
	defaultDevToolsPort = 9222
)

// InteractiveLogin spawns a plain Chromium (no chromedp launcher, no automation
// flags) so the user can complete Google login without triggering Google's
// "browser may not be secure" block. Login completion is detected by polling
// the DevTools HTTP API. Once detected, chromedp attaches via NewRemoteAllocator
// to extract cookies, then chromium is shut down.
//
// remoteURL is intentionally ignored: interactive login REQUIRES a local
// chromium so we can spawn it without CDP-driven automation flags. The remote
// browserless instance is reserved for headless export/status flows.
func InteractiveLogin(ctx context.Context, _ string) ([]*http.Cookie, error) {
	chromePath := os.Getenv("CHROME_PATH")
	if chromePath == "" {
		chromePath = "chromium"
	}

	profileDir := ProfileDir()
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		return nil, fmt.Errorf("creating chrome profile dir: %w", err)
	}

	// Remove stale singleton locks left behind if a prior chromium exited
	// without cleaning up (e.g. container hard-killed mid-session). Without
	// this, the new chromium refuses to start and waitForDevTools times out.
	for _, name := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket"} {
		_ = os.Remove(filepath.Join(profileDir, name))
	}

	port := defaultDevToolsPort
	debugBaseURL := fmt.Sprintf("http://localhost:%d", port)

	args := []string{
		fmt.Sprintf("--user-data-dir=%s", profileDir),
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--no-first-run",
		"--no-default-browser-check",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--start-maximized",
		googleLoginURL,
	}

	cmd := exec.CommandContext(ctx, chromePath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 3 * time.Second

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("launching chromium: %w", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			_ = cmd.Wait()
		}
	}()

	if err := waitForDevTools(ctx, debugBaseURL, 30*time.Second); err != nil {
		return nil, fmt.Errorf("chromium devtools not ready: %w", err)
	}

	fmt.Println("Opening browser for Google login...")
	fmt.Println("Please log in to your Google account. The browser will close automatically once login is detected.")

	if err := pollForLogin(ctx, debugBaseURL); err != nil {
		return nil, fmt.Errorf("waiting for login: %w", err)
	}

	fmt.Println("Login detected! Extracting session...")

	wsURL, err := getBrowserWebSocketURL(debugBaseURL)
	if err != nil {
		return nil, fmt.Errorf("getting browser websocket url: %w", err)
	}

	browserCtx, cancel, err := NewContext(ctx, Options{RemoteURL: wsURL})
	if err != nil {
		return nil, fmt.Errorf("attaching chromedp: %w", err)
	}
	defer cancel()

	cookies, err := ExtractCookies(browserCtx)
	if err != nil {
		return nil, fmt.Errorf("extracting cookies: %w", err)
	}
	return cookies, nil
}

func pollForLogin(ctx context.Context, baseURL string) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	timeout := time.After(10 * time.Minute)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("login timed out after 10 minutes")
		case <-ticker.C:
			tabs, err := listTabs(baseURL)
			if err != nil {
				continue
			}
			if findLoggedInTab(tabs) {
				return nil
			}
		}
	}
}

// CheckSession validates that saved cookies represent an active Google session.
func CheckSession(ctx context.Context, cookies []*http.Cookie, remoteURL string) (bool, error) {
	browserCtx, cancel, err := NewContext(ctx, Options{
		Headless:  true,
		RemoteURL: remoteURL,
	})
	if err != nil {
		return false, fmt.Errorf("creating browser context: %w", err)
	}
	defer cancel()

	if err := InjectCookies(browserCtx, cookies); err != nil {
		return false, fmt.Errorf("injecting cookies: %w", err)
	}

	if err := chromedp.Run(browserCtx, chromedp.Navigate(googleAccountURL)); err != nil {
		return false, fmt.Errorf("navigating to account page: %w", err)
	}

	// Wait for page to settle
	time.Sleep(3 * time.Second)

	var currentURL string
	if err := chromedp.Run(browserCtx, chromedp.Location(&currentURL)); err != nil {
		return false, fmt.Errorf("getting current URL: %w", err)
	}

	// If we're redirected to login, session is expired
	if strings.Contains(currentURL, "accounts.google.com/") {
		return false, nil
	}

	return true, nil
}

func isLoggedIn(url string) bool {
	return strings.Contains(url, "myaccount.google.com") ||
		strings.Contains(url, "google.com/search") ||
		strings.Contains(url, "mail.google.com") ||
		strings.Contains(url, "drive.google.com")
}

type devtoolsTab struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type devtoolsVersion struct {
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func findLoggedInTab(tabs []devtoolsTab) bool {
	for _, t := range tabs {
		if t.Type == "page" && isLoggedIn(t.URL) {
			return true
		}
	}
	return false
}

func getBrowserWebSocketURL(baseURL string) (string, error) {
	resp, err := http.Get(baseURL + "/json/version")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var v devtoolsVersion
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", err
	}
	if v.WebSocketDebuggerURL == "" {
		return "", fmt.Errorf("no webSocketDebuggerUrl in /json/version response")
	}
	return v.WebSocketDebuggerURL, nil
}

func listTabs(baseURL string) ([]devtoolsTab, error) {
	resp, err := http.Get(baseURL + "/json/list")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var tabs []devtoolsTab
	if err := json.NewDecoder(resp.Body).Decode(&tabs); err != nil {
		return nil, err
	}
	return tabs, nil
}

func waitForDevTools(ctx context.Context, baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		resp, err := http.Get(baseURL + "/json/version")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("devtools at %s not ready within %s", baseURL, timeout)
}
