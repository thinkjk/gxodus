package cli

import (
	"fmt"
	"os"

	"github.com/thinkjk/gxodus/internal/auth"
	"github.com/thinkjk/gxodus/internal/takeoutapi"
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

		client, err := takeoutapi.NewClient(cookies, 0)
		if err != nil {
			return fmt.Errorf("creating takeout client: %w", err)
		}

		exports, err := client.ListExports(ctx)
		if err != nil {
			return fmt.Errorf("listing exports: %w", err)
		}

		if len(exports) == 0 {
			fmt.Println("No exports found.")
			return nil
		}

		for _, e := range exports {
			fmt.Printf("- %s (%s) created %s\n",
				e.UUID,
				e.Status,
				e.CreatedAt.Format("2006-01-02 15:04"))
			for _, url := range e.DownloadURLs {
				fmt.Printf("    download: %s\n", url)
			}
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
