package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/thinkjk/gxodus/internal/accounts"
	"github.com/thinkjk/gxodus/internal/config"
)

var (
	removeKeepExports bool
	removeForce       bool
)

var removeAccountCmd = &cobra.Command{
	Use:   "remove-account <email>",
	Short: "Remove a configured account (deletes session + chrome-profile + exports)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		email := args[0]
		dir := accounts.AccountDir(email)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return fmt.Errorf("account %s not found at %s", email, dir)
		}

		// Refuse if a download is in flight unless --force.
		if !removeForce {
			if uuid, _ := readPendingExport(dir); uuid != "" {
				return fmt.Errorf("account %s has an in-flight export (%s); pass --force to remove anyway", email, uuid)
			}
		}

		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("removing account dir: %w", err)
		}
		fmt.Printf("Removed %s\n", dir)

		if !removeKeepExports {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not load config to find output dir: %v\n", err)
			} else {
				outputDir := filepath.Join(cfg.ResolveOutputDir(), email)
				if err := os.RemoveAll(outputDir); err != nil {
					fmt.Fprintf(os.Stderr, "warning: could not remove %s: %v\n", outputDir, err)
				} else {
					fmt.Printf("Removed %s (use --keep-exports to skip next time)\n", outputDir)
				}
			}
		}
		return nil
	},
}

func init() {
	removeAccountCmd.Flags().BoolVar(&removeKeepExports, "keep-exports", false, "don't delete the per-account output dir under $OUTPUT_DIR")
	removeAccountCmd.Flags().BoolVar(&removeForce, "force", false, "remove even if an export is in flight")
	rootCmd.AddCommand(removeAccountCmd)
}
