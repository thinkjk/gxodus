package poller

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/thinkjk/gxodus/internal/takeoutapi"
)

type Config struct {
	Interval   time.Duration
	Cookies    []*http.Cookie
	ExportUUID string // the UUID returned by CreateExport — required
}

type Result struct {
	DownloadURLs []string
	Duration     time.Duration
}

// ExportStatus is the parsed Takeout export state, returned by checkOnce.
// State is one of: "complete", "in_progress", "failed", "expired", "unknown".
type ExportStatus struct {
	State        string
	DownloadURLs []string
}

// Poll checks the Takeout export status at regular intervals until the export
// is complete or the context is cancelled.
func Poll(ctx context.Context, cfg Config) (*Result, error) {
	start := time.Now()
	attempt := 0
	backoff := cfg.Interval

	if cfg.ExportUUID == "" {
		return nil, fmt.Errorf("poller: ExportUUID is required (set it from CreateExport's returned UUID)")
	}

	fmt.Printf("Polling for export %s every %s...\n", cfg.ExportUUID, cfg.Interval)

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

func checkOnce(ctx context.Context, cfg Config) (*ExportStatus, error) {
	client, err := takeoutapi.NewClient(cfg.Cookies, 0)
	if err != nil {
		return nil, fmt.Errorf("creating takeout client: %w", err)
	}

	// Look up by UUID rather than picking exports[0]: with stale completed
	// exports in the account, index 0 may not be the one we just created and
	// the poller would short-circuit by downloading the wrong archives.
	e, err := client.GetExport(ctx, cfg.ExportUUID)
	if err != nil {
		return nil, fmt.Errorf("looking up export %s: %w", cfg.ExportUUID, err)
	}
	if e == nil {
		// Google hasn't indexed it in fhjYTc yet — treat as in-progress so we
		// keep polling. (Newly-created exports take a few seconds to appear.)
		return &ExportStatus{State: "in_progress"}, nil
	}

	switch e.Status {
	case takeoutapi.StatusComplete:
		return &ExportStatus{State: "complete", DownloadURLs: e.DownloadURLs}, nil
	case takeoutapi.StatusInProgress:
		return &ExportStatus{State: "in_progress"}, nil
	case takeoutapi.StatusFailed:
		return &ExportStatus{State: "failed"}, nil
	case takeoutapi.StatusExpired:
		return &ExportStatus{State: "expired"}, nil
	default:
		return &ExportStatus{State: "unknown"}, nil
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
