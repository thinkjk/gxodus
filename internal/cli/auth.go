package cli

import (
	"fmt"

	"github.com/thinkjk/gxodus/internal/auth"
	"github.com/thinkjk/gxodus/internal/browser"
	"github.com/spf13/cobra"
)

var (
	authCheck    bool
	authRevoke   bool
	remoteChrome string
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authenticate with Google",
	Long:  "Opens a browser window for Google login. Session cookies are saved encrypted for future use.",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		if authRevoke {
			fmt.Println("Revoking saved session...")
			if err := auth.DeleteSession(); err != nil {
				return fmt.Errorf("deleting session: %w", err)
			}
			if err := auth.DeleteKey(); err != nil {
				return fmt.Errorf("deleting keyring entry: %w", err)
			}
			fmt.Println("Session revoked.")
			return nil
		}

		if authCheck {
			if !auth.SessionExists() {
				fmt.Println("No saved session found. Run 'gxodus auth' to log in.")
				return fmt.Errorf("no session found")
			}

			cookies, err := auth.LoadSession()
			if err != nil {
				return fmt.Errorf("loading session: %w", err)
			}

			valid, err := browser.CheckSession(ctx, cookies, remoteChrome)
			if err != nil {
				return fmt.Errorf("checking session: %w", err)
			}

			if valid {
				fmt.Println("Session is valid.")
			} else {
				fmt.Println("Session has expired. Run 'gxodus auth' to log in again.")
				return fmt.Errorf("session expired")
			}
			return nil
		}

		// Interactive login
		cookies, err := browser.InteractiveLogin(ctx, remoteChrome)
		if err != nil {
			return fmt.Errorf("login failed: %w", err)
		}

		if err := auth.SaveSession(cookies); err != nil {
			return fmt.Errorf("saving session: %w", err)
		}

		fmt.Println("Session saved successfully.")
		return nil
	},
}

func init() {
	authCmd.Flags().BoolVar(&authCheck, "check", false, "validate saved session is still active")
	authCmd.Flags().BoolVar(&authRevoke, "revoke", false, "delete saved session data")
	authCmd.Flags().StringVar(&remoteChrome, "remote-chrome", "", "WebSocket URL for remote Chrome instance (e.g., ws://browserless:3000)")
	rootCmd.AddCommand(authCmd)
}
