package cli

import (
	"fmt"
	"os"

	"github.com/thinkjk/gxodus/internal/auth"
	"github.com/thinkjk/gxodus/internal/browser"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check export status",
	Long:  "Opens the Takeout status page and displays the current export state.",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		if !auth.SessionExists() {
			fmt.Fprintln(os.Stderr, "No saved session. Run 'gxodus auth' to log in first.")
			os.Exit(1)
		}

		cookies, err := auth.LoadSession()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load session: %v\n", err)
			os.Exit(1)
		}

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

		status, err := browser.CheckExportStatus(browserCtx)
		if err != nil {
			return fmt.Errorf("checking status: %w", err)
		}

		switch status.State {
		case "complete":
			fmt.Println("Export is complete and ready for download.")
			fmt.Printf("Download URLs: %d file(s)\n", len(status.DownloadURLs))
		case "in_progress":
			fmt.Println("Export is still in progress...")
		case "none":
			fmt.Println("No exports found.")
		case "failed":
			fmt.Println("Export has failed.")
		default:
			fmt.Printf("Export status: %s\n", status.State)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
