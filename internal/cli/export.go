package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/thinkjk/gxodus/internal/auth"
	"github.com/thinkjk/gxodus/internal/config"
	"github.com/thinkjk/gxodus/internal/downloader"
	"github.com/thinkjk/gxodus/internal/extractor"
	"github.com/thinkjk/gxodus/internal/notify"
	"github.com/thinkjk/gxodus/internal/poller"
	"github.com/thinkjk/gxodus/internal/takeoutapi"
	"github.com/spf13/cobra"
)

var (
	outputDir      string
	extract        bool
	noKeepZip      bool
	pollInterval   string
	fileSize       string
	fileType       string
	frequency      string
	noActivityLogs bool
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export data from Google Takeout",
	Long:  "Initiates a Google Takeout export, polls for completion, and downloads the archive.",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		// Apply CLI flag overrides
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

		// Check for saved session
		if !auth.SessionExists() {
			notify.Fire(cfg.Notify, "auth_expired", notify.EventData{
				Error: "no saved session found",
			})
			fmt.Fprintln(os.Stderr, "No saved session. Run 'gxodus auth' to log in first.")
			os.Exit(1)
		}

		cookies, err := auth.LoadSession()
		if err != nil {
			notify.Fire(cfg.Notify, "auth_expired", notify.EventData{Error: err.Error()})
			fmt.Fprintf(os.Stderr, "Failed to load session: %v\nRun 'gxodus auth' to log in again.\n", err)
			os.Exit(1)
		}

		fmt.Printf("Loaded %d cookies from saved session.\n", len(cookies))

		// (No pre-flight CheckSession: it duplicated the redirect-to-login
		//  detection that InitiateExport already does on takeout.google.com,
		//  and routing it through a remote browserless tripped Google's bot
		//  detection on myaccount.google.com — causing false "session
		//  expired" failures with valid cookies, and a re-auth loop.
		//  If the session is genuinely expired, InitiateExport below will
		//  detect the redirect and fire the same auth_expired notification.)

		client, err := takeoutapi.NewClient(cookies, 0)
		if err != nil {
			return fmt.Errorf("creating takeout client: %w", err)
		}

		products := defaultProductSlugs()
		if cfg.ActivityLogs {
			products = append(products, "bond") // "bond" is the slug for Access Log Activity
		}

		sizeBytes, err := parseFileSize(cfg.FileSize)
		if err != nil {
			notify.Fire(cfg.Notify, "error", notify.EventData{Error: err.Error()})
			return fmt.Errorf("file size: %w", err)
		}

		newExport, err := client.CreateExport(ctx, takeoutapi.CreateExportOptions{
			Products:  products,
			Format:    strings.ToUpper(cfg.FileType),
			SizeBytes: sizeBytes,
			Frequency: cfg.Frequency,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "CreateExport failed: %v\n", err)
			notify.Fire(cfg.Notify, "error", notify.EventData{Error: err.Error()})
			os.Exit(2)
		}

		fmt.Printf("Export submitted (uuid=%s)\n", newExport.UUID)
		notify.Fire(cfg.Notify, "export_started", notify.EventData{})

		// Poll for completion
		pollDuration, err := cfg.PollDuration()
		if err != nil {
			return fmt.Errorf("invalid poll interval: %w", err)
		}

		pollResult, err := poller.Poll(ctx, poller.Config{
			Interval: pollDuration,
			Cookies:  cookies,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Poll failed: %v\n", err)
			notify.Fire(cfg.Notify, "error", notify.EventData{Error: err.Error()})
			os.Exit(3)
		}

		// Download archives
		resolvedOutput := cfg.ResolveOutputDir()
		dlResult, err := downloader.Download(pollResult.DownloadURLs, resolvedOutput)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Download failed: %v\n", err)
			notify.Fire(cfg.Notify, "error", notify.EventData{Error: err.Error()})
			os.Exit(3)
		}

		fmt.Printf("Downloaded %d file(s), total size: %s\n", len(dlResult.Files), formatSize(dlResult.TotalSize))

		// Extract if requested
		if cfg.Extract {
			extResult, err := extractor.Extract(dlResult.Files, resolvedOutput, extractor.Options{
				KeepZip: cfg.KeepZip,
			})
			if err != nil {
				notify.Fire(cfg.Notify, "error", notify.EventData{Error: err.Error()})
				return fmt.Errorf("extracting archives: %w", err)
			}
			fmt.Printf("Extracted %d files to %s\n", extResult.FileCount, extResult.OutputDir)
		}

		notify.Fire(cfg.Notify, "export_complete", notify.EventData{
			OutputPath: resolvedOutput,
			ExportSize: dlResult.TotalSize,
			Duration:   pollResult.Duration,
		})

		fmt.Println("Done!")
		return nil
	},
}

func init() {
	exportCmd.Flags().StringVarP(&outputDir, "output", "o", "", "output directory for downloaded archives")
	exportCmd.Flags().BoolVar(&extract, "extract", false, "extract archives into organized directories")
	exportCmd.Flags().BoolVar(&noKeepZip, "no-keep-zip", false, "remove ZIP files after extraction (requires --extract)")
	exportCmd.Flags().StringVar(&pollInterval, "poll-interval", "", "poll interval for checking export status (e.g., 5m, 10m)")
	exportCmd.Flags().StringVar(&fileSize, "file-size", "", "archive split size (1GB, 2GB, 4GB, 10GB, 50GB)")
	exportCmd.Flags().StringVar(&fileType, "file-type", "", "archive type (zip, tgz)")
	exportCmd.Flags().StringVar(&frequency, "frequency", "", "export frequency (once, every_2_months)")
	exportCmd.Flags().BoolVar(&noActivityLogs, "no-activity-logs", false, "skip the Access Log Activity item (off by default in Google UI; gxodus selects it by default)")
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
