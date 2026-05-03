package poller

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/thinkjk/gxodus/internal/browser"
	"github.com/thinkjk/gxodus/internal/takeoutapi"
)

type Config struct {
	Interval time.Duration
	Cookies  []*http.Cookie
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
	client, err := takeoutapi.NewClient(cfg.Cookies, 0)
	if err != nil {
		return nil, fmt.Errorf("creating takeout client: %w", err)
	}

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

func nextBackoff(current, base time.Duration) time.Duration {
	next := current * 2
	maxBackoff := 30 * time.Minute
	if next > maxBackoff {
		return maxBackoff
	}
	return next
}
