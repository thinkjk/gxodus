package browser

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

const (
	googleLoginURL   = "https://accounts.google.com"
	googleAccountURL = "https://myaccount.google.com"
)

// InteractiveLogin opens a visible browser for the user to complete Google login.
// It returns the session cookies once login is detected.
func InteractiveLogin(ctx context.Context, remoteURL string) ([]*http.Cookie, error) {
	browserCtx, cancel, err := NewContext(ctx, Options{
		Headless:  false,
		RemoteURL: remoteURL,
	})
	if err != nil {
		return nil, fmt.Errorf("creating browser context: %w", err)
	}
	defer cancel()

	fmt.Println("Opening browser for Google login...")
	fmt.Println("Please log in to your Google account. The browser will close automatically once login is detected.")

	if err := chromedp.Run(browserCtx, chromedp.Navigate(googleLoginURL)); err != nil {
		return nil, fmt.Errorf("navigating to login: %w", err)
	}

	// Poll URL until we detect successful login
	if err := waitForLogin(browserCtx); err != nil {
		return nil, fmt.Errorf("waiting for login: %w", err)
	}

	fmt.Println("Login detected! Extracting session...")

	cookies, err := ExtractCookies(browserCtx)
	if err != nil {
		return nil, fmt.Errorf("extracting cookies: %w", err)
	}

	return cookies, nil
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

func waitForLogin(ctx context.Context) error {
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
			var currentURL string
			if err := chromedp.Run(ctx, chromedp.Location(&currentURL)); err != nil {
				continue // Browser might be in transition
			}

			if isLoggedIn(currentURL) {
				return nil
			}
		}
	}
}

func isLoggedIn(url string) bool {
	return strings.Contains(url, "myaccount.google.com") ||
		strings.Contains(url, "google.com/search") ||
		strings.Contains(url, "mail.google.com") ||
		strings.Contains(url, "drive.google.com")
}
