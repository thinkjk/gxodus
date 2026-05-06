package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/thinkjk/gxodus/internal/config"
	"github.com/thinkjk/gxodus/internal/statusserver"
)

var statusServerAddr string

var statusServerCmd = &cobra.Command{
	Use:   "status-server",
	Short: "Run a read-only HTTP page summarizing per-account state",
	Long: `Long-running HTTP server that renders a single HTML page
showing per-account session status, in-flight export UUIDs, and the
files currently in $OUTPUT_DIR/<email>/. Auto-refreshes every 30s.

Default port 6079 — separate from noVNC's 6080. Override with
GXODUS_STATUS_ADDR or --addr.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		return statusserver.ListenAndServe(statusServerAddr, cfg.ResolveOutputDir())
	},
}

func init() {
	statusServerCmd.Flags().StringVar(&statusServerAddr, "addr", ":6079", "address to listen on (default :6079)")
	rootCmd.AddCommand(statusServerCmd)
}
