package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thinkjk/gxodus/internal/accounts"
	"github.com/thinkjk/gxodus/internal/auth"
	"github.com/thinkjk/gxodus/internal/config"
	"github.com/thinkjk/gxodus/internal/downloader"
	"github.com/thinkjk/gxodus/internal/extractor"
	"github.com/thinkjk/gxodus/internal/notify"
	"github.com/thinkjk/gxodus/internal/poller"
	"github.com/thinkjk/gxodus/internal/takeoutapi"
)

var (
	outputDir       string
	extract         bool
	noKeepZip       bool
	pollInterval    string
	fileSize        string
	fileType        string
	frequency       string
	noActivityLogs  bool
	resumeUUID      string
	exportAccount   string
)

// Sentinel errors so classifyFailures can group results without
// re-importing takeoutapi types.
var (
	errSessionExpiredSentinel = errors.New("session expired (sentinel)")
	errSomeOtherFailure       = errors.New("other failure (sentinel)")
)

type accountResult struct {
	Email string
	Err   error // nil = success
}

// classifyFailures partitions per-account results into auth-failures
// (use ErrSessionExpired sentinel) and other failures. Successful
// results contribute to neither.
func classifyFailures(results []accountResult) (auth, other []string) {
	for _, r := range results {
		if r.Err == nil {
			continue
		}
		if errors.Is(r.Err, takeoutapi.ErrSessionExpired) || errors.Is(r.Err, errSessionExpiredSentinel) {
			auth = append(auth, r.Email)
			continue
		}
		other = append(other, r.Email)
	}
	return auth, other
}

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export data from Google Takeout (all configured accounts by default)",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		applyFlagOverrides(cfg)

		all, err := accounts.ScanAccounts()
		if err != nil {
			return fmt.Errorf("scanning accounts: %w", err)
		}
		if len(all) == 0 {
			fmt.Fprintln(os.Stderr, "No accounts configured. Run 'gxodus auth' to add one.")
			notify.Fire(cfg.Notify, "auth_expired", notify.EventData{Error: "no accounts configured"})
			os.Exit(1)
		}

		// Filter by --account if provided.
		if exportAccount != "" {
			filtered := []accounts.Account{}
			for _, a := range all {
				if a.Email == exportAccount {
					filtered = append(filtered, a)
					break
				}
			}
			if len(filtered) == 0 {
				return fmt.Errorf("--account %q not found", exportAccount)
			}
			all = filtered
		}

		var results []accountResult
		for _, a := range all {
			if !a.HasSession {
				fmt.Printf("[%s] no session.enc; skipping. Run 'gxodus auth --account %s' to set up.\n", a.Email, a.Email)
				results = append(results, accountResult{Email: a.Email, Err: errSomeOtherFailure})
				continue
			}
			err := runExportForAccount(ctx, a.Email, a.Dir, cfg)
			results = append(results, accountResult{Email: a.Email, Err: err})
		}

		authFails, otherFails := classifyFailures(results)
		if len(authFails) > 0 {
			if err := writeFailedAccounts(authFails); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not write .failed-accounts: %v\n", err)
			}
			os.Exit(1)
		}
		if len(otherFails) > 0 {
			os.Exit(3)
		}
		fmt.Println("All accounts complete.")
		return nil
	},
}

func runExportForAccount(ctx context.Context, email, accountDir string, cfg *config.Config) error {
	fmt.Printf("\n=== Account: %s ===\n", email)

	cookies, err := auth.LoadSession(accountDir)
	if err != nil {
		notify.Fire(cfg.Notify, "auth_expired", notify.EventData{Account: email, Error: err.Error()})
		return err
	}
	fmt.Printf("[%s] Loaded %d cookies from saved session.\n", email, len(cookies))

	client, err := takeoutapi.NewClient(cookies, 0)
	if err != nil {
		notify.Fire(cfg.Notify, "error", notify.EventData{Account: email, Error: err.Error()})
		return err
	}

	products := defaultProductSlugs()
	if cfg.ActivityLogs {
		products = append(products, "bond")
	}

	sizeBytes, err := parseFileSize(cfg.FileSize)
	if err != nil {
		notify.Fire(cfg.Notify, "error", notify.EventData{Account: email, Error: err.Error()})
		return err
	}

	var trackUUID string
	switch {
	case resumeUUID != "":
		fmt.Printf("[%s] Resuming export (uuid=%s, --export-uuid flag) — skipping CreateExport.\n", email, resumeUUID)
		trackUUID = resumeUUID
	default:
		if persisted, err := readPendingExport(accountDir); err != nil {
			fmt.Fprintf(os.Stderr, "[%s] warning: could not read pending-export marker: %v\n", email, err)
		} else if persisted != "" {
			fmt.Printf("[%s] Resuming export (uuid=%s) — skipping CreateExport.\n", email, persisted)
			trackUUID = persisted
		}
	}

	if trackUUID == "" {
		newExport, err := client.CreateExport(ctx, takeoutapi.CreateExportOptions{
			Products:  products,
			Format:    strings.ToUpper(cfg.FileType),
			SizeBytes: sizeBytes,
			Frequency: cfg.Frequency,
		})
		if err != nil {
			if errors.Is(err, takeoutapi.ErrSessionExpired) {
				fmt.Fprintf(os.Stderr, "[%s] Session expired — cookies are stale and need re-auth via noVNC.\n", email)
				notify.Fire(cfg.Notify, "auth_expired", notify.EventData{Account: email, Error: err.Error()})
				return err
			}
			fmt.Fprintf(os.Stderr, "[%s] CreateExport failed: %v\n", email, err)
			notify.Fire(cfg.Notify, "error", notify.EventData{Account: email, Error: err.Error()})
			return err
		}
		if newExport.UUID == "" {
			err := fmt.Errorf("CreateExport returned no UUID")
			notify.Fire(cfg.Notify, "error", notify.EventData{Account: email, Error: err.Error()})
			return err
		}
		trackUUID = newExport.UUID
		fmt.Printf("[%s] Export submitted (uuid=%s)\n", email, trackUUID)
		if err := writePendingExport(accountDir, trackUUID); err != nil {
			fmt.Fprintf(os.Stderr, "[%s] warning: could not persist pending-export marker: %v\n", email, err)
		}
	}
	notify.Fire(cfg.Notify, "export_started", notify.EventData{Account: email})

	pollDuration, err := cfg.PollDuration()
	if err != nil {
		return fmt.Errorf("invalid poll interval: %w", err)
	}

	pollResult, err := poller.Poll(ctx, poller.Config{
		Interval:   pollDuration,
		Cookies:    cookies,
		ExportUUID: trackUUID,
	})
	if err != nil {
		if errors.Is(err, takeoutapi.ErrSessionExpired) {
			fmt.Fprintf(os.Stderr, "[%s] Session expired during poll.\n", email)
			notify.Fire(cfg.Notify, "auth_expired", notify.EventData{Account: email, Error: err.Error()})
			return err
		}
		fmt.Fprintf(os.Stderr, "[%s] Poll failed: %v\n", email, err)
		notify.Fire(cfg.Notify, "error", notify.EventData{Account: email, Error: err.Error()})
		return err
	}

	resolvedOutput := filepath.Join(cfg.ResolveOutputDir(), email)
	dlResult, err := downloader.Download(ctx, pollResult.DownloadURLs, resolvedOutput, cookies, cfg.Notify, accountDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[%s] Download failed: %v\n", email, err)
		notify.Fire(cfg.Notify, "error", notify.EventData{Account: email, Error: err.Error()})
		return err
	}

	fmt.Printf("[%s] Downloaded %d file(s), total size: %s\n", email, len(dlResult.Files), formatSize(dlResult.TotalSize))

	if err := clearPendingExport(accountDir); err != nil {
		fmt.Fprintf(os.Stderr, "[%s] warning: could not clear pending-export marker: %v\n", email, err)
	}

	if cfg.Extract {
		extResult, err := extractor.Extract(dlResult.Files, resolvedOutput, extractor.Options{KeepZip: cfg.KeepZip})
		if err != nil {
			notify.Fire(cfg.Notify, "error", notify.EventData{Account: email, Error: err.Error()})
			return fmt.Errorf("extracting archives: %w", err)
		}
		fmt.Printf("[%s] Extracted %d files to %s\n", email, extResult.FileCount, extResult.OutputDir)
	}

	notify.Fire(cfg.Notify, "export_complete", notify.EventData{
		Account:    email,
		OutputPath: resolvedOutput,
		ExportSize: dlResult.TotalSize,
		Duration:   pollResult.Duration,
	})
	fmt.Printf("[%s] Done.\n", email)
	return nil
}

func applyFlagOverrides(cfg *config.Config) {
	if outputDir != "" {
		cfg.OutputDir = outputDir
	}
	if extract {
		cfg.Extract = true
	}
	if noKeepZip {
		cfg.KeepZip = false
	}
	if pollInterval != "" {
		cfg.PollInterval = pollInterval
	}
	if fileSize != "" {
		cfg.FileSize = fileSize
	}
	if fileType != "" {
		cfg.FileType = fileType
	}
	if frequency != "" {
		cfg.Frequency = frequency
	}
	if noActivityLogs {
		cfg.ActivityLogs = false
	}
}

func init() {
	exportCmd.Flags().StringVarP(&outputDir, "output", "o", "", "output directory for downloaded archives")
	exportCmd.Flags().BoolVar(&extract, "extract", false, "extract archives into organized directories")
	exportCmd.Flags().BoolVar(&noKeepZip, "no-keep-zip", false, "remove ZIP files after extraction (requires --extract)")
	exportCmd.Flags().StringVar(&pollInterval, "poll-interval", "", "poll interval for checking export status (e.g., 5m, 10m)")
	exportCmd.Flags().StringVar(&fileSize, "file-size", "", "archive split size (1GB, 2GB, 4GB, 10GB, 50GB)")
	exportCmd.Flags().StringVar(&fileType, "file-type", "", "archive type (zip, tgz)")
	exportCmd.Flags().StringVar(&frequency, "frequency", "", "export frequency (once, every_2_months)")
	exportCmd.Flags().BoolVar(&noActivityLogs, "no-activity-logs", false, "skip the Access Log Activity item")
	exportCmd.Flags().StringVar(&resumeUUID, "export-uuid", "", "skip CreateExport and resume polling an existing export by UUID")
	exportCmd.Flags().StringVar(&exportAccount, "account", "", "limit export to a single account (default: all configured accounts)")
	rootCmd.AddCommand(exportCmd)
}

func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// defaultProductSlugs returns the canonical product list for a "select all"
// Takeout export, minus "bond" (Access Log Activity) which is opt-in.
//
// Catalog is account-scoped — different accounts surface different products
// (e.g. fi only appears for Google Fi subscribers; play_console only for Play
// developers). This list is the union captured 2026-05-04 across two
// personal accounts. Sending a slug an account doesn't have appears to be
// silently ignored by Takeout. Re-scrape via the WIZ_global_data block if a
// Workspace or Fi/Play account surfaces something new.
func defaultProductSlugs() []string {
	return []string{
		"ai_sandbox", "alerts", "analytics", "android", "apps_marketplace",
		"arts_and_culture", "assisted_calling", "backlight", "blogger", "books",
		"brand_accounts", "calendar", "checkin", "chrome", "chrome_os",
		"chrome_web_store", "classroom", "contacts", "course_kit", "custom_search",
		"developer_platform", "discover", "drive", "earth", "family",
		"feedback", "fi", "fiber", "fit", "fitbit",
		"gemini", "gmail", "google_account", "google_ads", "google_cloud_search",
		"google_finance", "google_one", "google_pay", "google_store", "google_wallet",
		"groups", "hangouts_chat", "hats_surveys", "home_graph", "keep",
		"local_actions", "location_history", "manufacturer_center", "maps", "meet",
		"merchant_center", "messages", "my_activity", "my_business", "my_orders",
		"nest", "news", "package_tracking", "personal_safety", "photos",
		"pixel_telemetry", "play", "play_console", "play_games_services", "play_movies",
		"podcasts", "profile", "reminders", "save", "search",
		"search_console", "search_notifications", "search_ugc", "shopping", "streetview",
		"support_content", "tasks", "voice", "voice_and_audio_activity", "workflows",
		"youtube",
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
