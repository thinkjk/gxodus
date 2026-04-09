package cli

import (
	"fmt"

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
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default ~/.config/gxodus/config.toml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable verbose output")
	rootCmd.PersistentFlags().StringVar(&remoteChrome, "remote-chrome", "", "WebSocket URL for remote Chrome instance (e.g., ws://browserless:3000)")
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
