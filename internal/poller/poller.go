package poller

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/thinkjk/gxodus/internal/browser"
)

type Config struct {
	Interval    time.Duration
	RemoteURL   string
	Cookies     []*http.Cookie
}

type Result struct {
	DownloadURLs []string
	Duration     time.Duration
}

// Poll checks the Takeout export status at regular intervals until the export
// is complete or the context is cancelled.
func Poll(ctx context.Context, cfg Config) (*Result, error) {
	start := time.Now()
	attempt := 0
	backoff := cfg.Interval

	fmt.Printf("Polling for export completion every %s...\n", cfg.Interval)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		status, err := checkOnce(ctx, cfg)
		if err != nil {
			attempt++
			backoff = nextBackoff(backoff, cfg.Interval)
			fmt.Printf("Poll error (attempt %d, retrying in %s): %v\n", attempt, backoff, err)

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
				continue
			}
		}

		// Reset backoff on success
		attempt = 0
		backoff = cfg.Interval

		switch status.State {
		case "complete":
			duration := time.Since(start)
			fmt.Printf("Export complete! (took %s)\n", duration.Round(time.Second))
			return &Result{
				DownloadURLs: status.DownloadURLs,
				Duration:     duration,
			}, nil

		case "in_progress":
			fmt.Printf("[%s] Export still in progress...\n", time.Now().Format("15:04"))

		case "failed":
			return nil, fmt.Errorf("export failed")

		case "none":
			return nil, fmt.Errorf("no export found — was the export initiated?")

		default:
			fmt.Printf("[%s] Export status: %s\n", time.Now().Format("15:04"), status.State)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(cfg.Interval):
		}
	}
}

func checkOnce(ctx context.Context, cfg Config) (*browser.ExportStatus, error) {
	browserCtx, cancel, err := browser.NewContext(ctx, browser.Options{
		Headless:    false,
		RemoteURL:   cfg.RemoteURL,
		UserDataDir: browser.ProfileDir(),
	})
	if err != nil {
		return nil, fmt.Errorf("creating browser: %w", err)
	}
	defer cancel()

	if err := browser.InjectCookies(browserCtx, cfg.Cookies); err != nil {
		return nil, fmt.Errorf("injecting cookies: %w", err)
	}

	return browser.CheckExportStatus(browserCtx)
}

func nextBackoff(current, base time.Duration) time.Duration {
	next := current * 2
	maxBackoff := 30 * time.Minute
	if next > maxBackoff {
		return maxBackoff
	}
	return next
}
