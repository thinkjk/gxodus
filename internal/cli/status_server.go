package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/thinkjk/gxodus/internal/config"
	"github.com/thinkjk/gxodus/internal/statusserver"
)

var (
	statusServerAddr   string
	statusServerOutput string
)

var statusServerCmd = &cobra.Command{
	Use:   "status-server",
	Short: "Run a read-only HTTP page summarizing per-account state",
	Long: `Long-running HTTP server that renders a single HTML page
showing per-account session status, in-flight export UUIDs, and the
files currently in $OUTPUT_DIR/<email>/. Auto-refreshes every 30s.

Default port 6079 — separate from noVNC's 6080. Override the listen
address with --addr or GXODUS_STATUS_ADDR. Override the noVNC port
shown in the on-page link with GXODUS_NOVNC_PORT (default 6080).

Output dir lookup precedence (matches 'gxodus export'):
  1. --output flag
  2. GXODUS_OUTPUT_DIR env var
  3. config.toml output_dir
  4. ~/gxodus-exports default`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		if statusServerOutput != "" {
			cfg.OutputDir = statusServerOutput
		} else if env := os.Getenv("GXODUS_OUTPUT_DIR"); env != "" {
			cfg.OutputDir = env
		}
		return statusserver.ListenAndServe(statusServerAddr, cfg.ResolveOutputDir())
	},
}

func init() {
	statusServerCmd.Flags().StringVar(&statusServerAddr, "addr", ":6079", "address to listen on (default :6079)")
	statusServerCmd.Flags().StringVarP(&statusServerOutput, "output", "o", "", "output directory for downloaded archives (overrides config + env)")
	rootCmd.AddCommand(statusServerCmd)
}
