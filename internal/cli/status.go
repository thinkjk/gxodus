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

// pickSingleAccount resolves a single account from the configured set.
// If --account flag is given, finds that one. If exactly 1 account
// exists, picks it. If 0 or 2+ and no flag, errors with guidance.
func pickSingleAccount(all []accounts.Account, flag string) (*accounts.Account, error) {
	if flag != "" {
		for i := range all {
			if all[i].Email == flag {
				return &all[i], nil
			}
		}
		return nil, fmt.Errorf("--account %q not found", flag)
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("no accounts configured; run 'gxodus auth'")
	}
	if len(all) == 1 {
		return &all[0], nil
	}
	emails := make([]string, len(all))
	for i, a := range all {
		emails[i] = a.Email
	}
	return nil, fmt.Errorf("multiple accounts configured (%v); use --account <email>", emails)
}

func init() {
	statusCmd.Flags().StringVar(&statusAccount, "account", "", "target a specific account by email")
	rootCmd.AddCommand(statusCmd)
}
