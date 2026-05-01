package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var Version = "dev"

var (
	cfgFile      string
	verbose      bool
	remoteChrome string
)

var rootCmd = &cobra.Command{
	Use:   "gxodus",
	Short: "Automate Google Takeout exports",
	Long:  "gxodus automates the entire Google Takeout flow: authenticate, export, poll, download, and extract.",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Browserless's chromium runs with --enable-automation, which Google's
		// bot detection blocks (login redirects, "browser may not be secure").
		// Force all Google-facing operations through our local chromium, which
		// has the stealth flags applied. Keep the flag so existing setups don't
		// break with "unknown flag", but warn that it's no longer honored.
		if remoteChrome != "" {
			fmt.Fprintln(os.Stderr, "Note: --remote-chrome is ignored — local chromium is used to avoid Google's bot detection on browserless.")
			remoteChrome = ""
		}
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default ~/.config/gxodus/config.toml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable verbose output")
	rootCmd.PersistentFlags().StringVar(&remoteChrome, "remote-chrome", "", "[ignored] previously a remote Chrome WebSocket URL; now always uses local chromium")
}

func Execute() error {
	return rootCmd.Execute()
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("gxodus %s\n", Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
