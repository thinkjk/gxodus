package cli

import (
	"fmt"
	"os"

	"github.com/thinkjk/gxodus/internal/auth"
	"github.com/thinkjk/gxodus/internal/browser"
	"github.com/thinkjk/gxodus/internal/config"
	"github.com/thinkjk/gxodus/internal/downloader"
	"github.com/thinkjk/gxodus/internal/extractor"
	"github.com/thinkjk/gxodus/internal/notify"
	"github.com/thinkjk/gxodus/internal/poller"
	"github.com/spf13/cobra"
)

var (
	outputDir    string
	extract      bool
	noKeepZip    bool
	pollInterval string
	fileSize     string
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

		// Validate session
		valid, err := browser.CheckSession(ctx, cookies, remoteChrome)
		if err != nil || !valid {
			notify.Fire(cfg.Notify, "auth_expired", notify.EventData{
				Error: "session expired",
			})
			fmt.Fprintln(os.Stderr, "Session has expired. Run 'gxodus auth' to log in again.")
			os.Exit(1)
		}

		// Create browser context for export
		browserCtx, cancel, err := browser.NewContext(ctx, browser.Options{
			Headless:  true,
			RemoteURL: remoteChrome,
		})
		if err != nil {
			return fmt.Errorf("creating browser: %w", err)
		}
		defer cancel()

		if err := browser.InjectCookies(browserCtx, cookies); err != nil {
			return fmt.Errorf("injecting cookies: %w", err)
		}

		// Initiate export
		_, err = browser.InitiateExport(browserCtx, browser.ExportOptions{
			FileSize: cfg.FileSize,
		})
		if err != nil {
			notify.Fire(cfg.Notify, "error", notify.EventData{Error: err.Error()})
			if err.Error() == "session expired: redirected to login page" {
				notify.Fire(cfg.Notify, "auth_expired", notify.EventData{Error: err.Error()})
				os.Exit(1)
			}
			os.Exit(2)
		}

		notify.Fire(cfg.Notify, "export_started", notify.EventData{})
		cancel() // Close browser while we wait

		// Poll for completion
		pollDuration, err := cfg.PollDuration()
		if err != nil {
			return fmt.Errorf("invalid poll interval: %w", err)
		}

		pollResult, err := poller.Poll(ctx, poller.Config{
			Interval:  pollDuration,
			RemoteURL: remoteChrome,
			Cookies:   cookies,
		})
		if err != nil {
			notify.Fire(cfg.Notify, "error", notify.EventData{Error: err.Error()})
			os.Exit(3)
		}

		// Download archives
		resolvedOutput := cfg.ResolveOutputDir()
		dlResult, err := downloader.Download(pollResult.DownloadURLs, resolvedOutput)
		if err != nil {
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
