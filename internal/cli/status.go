package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/thinkjk/gxodus/internal/accounts"
	"github.com/thinkjk/gxodus/internal/auth"
	"github.com/thinkjk/gxodus/internal/takeoutapi"
)

var statusAccount string

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show in-progress export status (default: first configured account)",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		all, err := accounts.ScanAccounts()
		if err != nil {
			return fmt.Errorf("scanning accounts: %w", err)
		}
		acct, err := pickSingleAccount(all, statusAccount)
		if err != nil {
			return err
		}
		if !acct.HasSession {
			return fmt.Errorf("account %s has no session.enc; run 'gxodus auth --account %s'", acct.Email, acct.Email)
		}
		cookies, err := auth.LoadSession(acct.Dir)
		if err != nil {
			return fmt.Errorf("loading session: %w", err)
		}
		client, err := takeoutapi.NewClient(cookies, 0)
		if err != nil {
			return fmt.Errorf("creating client: %w", err)
		}
		exports, err := client.ListExports(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%s] list exports failed: %v\n", acct.Email, err)
			return err
		}
		fmt.Printf("[%s] %d exports:\n", acct.Email, len(exports))
		for _, e := range exports {
			fmt.Printf("  %s  status=%v  size=%d\n", e.UUID, e.Status, e.TotalBytes)
		}
		return nil
	},
}

func init() {
	statusCmd.Flags().StringVar(&statusAccount, "account", "", "target a specific account by email")
	rootCmd.AddCommand(statusCmd)
}
