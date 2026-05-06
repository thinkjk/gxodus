package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/thinkjk/gxodus/internal/accounts"
)

var listAccountsCmd = &cobra.Command{
	Use:   "list-accounts",
	Short: "List configured Google accounts and their session/marker status",
	RunE: func(cmd *cobra.Command, args []string) error {
		all, err := accounts.ScanAccounts()
		if err != nil {
			return fmt.Errorf("scanning accounts: %w", err)
		}
		if len(all) == 0 {
			fmt.Println("No accounts configured. Run 'gxodus auth' to add one.")
			return nil
		}
		pending := map[string]string{}
		for _, a := range all {
			uuid, _ := readPendingExport(a.Dir)
			pending[a.Email] = uuid
		}
		rows := buildAccountRows(all, pending)
		fmt.Printf("%-40s %-12s %s\n", "EMAIL", "SESSION", "PENDING")
		for _, r := range rows {
			fmt.Println(r)
		}
		return nil
	},
}

// buildAccountRows produces one display row per account. Pure function
// for testability.
func buildAccountRows(all []accounts.Account, pending map[string]string) []string {
	out := make([]string, 0, len(all))
	for _, a := range all {
		sess := "✗ no session"
		if a.HasSession {
			sess = "✓ valid"
		}
		p := pending[a.Email]
		if p == "" {
			p = "-"
		}
		out = append(out, fmt.Sprintf("%-40s %-12s %s", a.Email, sess, p))
	}
	return out
}

func init() {
	rootCmd.AddCommand(listAccountsCmd)
}
