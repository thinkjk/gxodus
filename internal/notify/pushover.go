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

// pushoverEndpointOverride redirects Fire's Pushover POST to a different
// URL. Set by tests; "" in production means use pushoverAPIURL.
var pushoverEndpointOverride string

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
