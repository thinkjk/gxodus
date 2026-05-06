package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/thinkjk/gxodus/internal/accounts"
	"github.com/thinkjk/gxodus/internal/auth"
	"github.com/thinkjk/gxodus/internal/browser"
	"github.com/thinkjk/gxodus/internal/config"
)

var (
	authCheck   bool
	authRevoke  bool
	authNewFlag bool
	authAccount string
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authenticate with Google",
	Long: `Authenticate via interactive browser login.

  gxodus auth                       — refresh sole account, OR add first account
                                      if none exists yet. Errors if 2+ accounts.
  gxodus auth --new                 — add a new account (fresh chromium profile;
                                      email derived from the post-login DOM scrape)
  gxodus auth --account <email>     — refresh that specific account; treats as
                                      adding if the dir doesn't yet exist
                                      (uses --account email instead of scrape)
  gxodus auth --check               — validate current session(s)
  gxodus auth --revoke              — delete saved session(s)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		if authRevoke {
			return runAuthRevoke()
		}
		if authCheck {
			return runAuthCheck(ctx)
		}

		existing, err := accounts.ScanAccounts()
		if err != nil {
			return fmt.Errorf("scanning accounts: %w", err)
		}

		// Decide whether this is "add new" or "refresh existing".
		var (
			profileDir      string
			fixedEmail      string // when set, skip the scrape and use this email
			usingNewProfile bool
		)
		switch {
		case authNewFlag:
			// Always add new with a fresh temp profile.
			profileDir, err = newTempProfileDir()
			if err != nil {
				return err
			}
			usingNewProfile = true
		case authAccount != "":
			// Targeting a specific email.
			ad := accounts.AccountDir(authAccount)
			if err := os.MkdirAll(ad, 0700); err != nil {
				return fmt.Errorf("ensuring account dir: %w", err)
			}
			profileDir = browser.ProfileDir(ad)
			fixedEmail = authAccount // skip scrape; trust the flag
		case len(existing) == 0:
			// First-time setup convenience: behave as --new.
			profileDir, err = newTempProfileDir()
			if err != nil {
				return err
			}
			usingNewProfile = true
		case len(existing) == 1:
			// Refresh the sole account.
			profileDir = browser.ProfileDir(existing[0].Dir)
			fixedEmail = existing[0].Email
		default:
			return fmt.Errorf("multiple accounts configured (%d); use --account <email> to refresh a specific one or --new to add another", len(existing))
		}

		cookies, scrapedEmail, err := browser.InteractiveLogin(ctx, "", profileDir)
		if err != nil {
			return fmt.Errorf("login failed: %w", err)
		}

		email := fixedEmail
		if email == "" {
			email = scrapedEmail
		}
		if email == "" {
			// Email scrape failed AND no flag/existing account told us which one.
			// Save cookies to a recovery dir so the user can manually rescue.
			pending := filepath.Join(config.ConfigDir(), fmt.Sprintf(".pending-auth-%d", time.Now().Unix()))
			if err := os.MkdirAll(pending, 0700); err != nil {
				return fmt.Errorf("creating pending-auth dir: %w", err)
			}
			if err := auth.SaveSession(pending, cookies); err != nil {
				return fmt.Errorf("saving cookies to pending-auth dir: %w", err)
			}
			return fmt.Errorf("email scrape failed; cookies saved to %s — rename to accounts/<email>/ manually after looking up the email", pending)
		}

		dest := accounts.AccountDir(email)
		if err := accounts.EnsureAccountDir(email); err != nil {
			return fmt.Errorf("ensuring account dir for %s: %w", email, err)
		}
		if err := auth.SaveSession(dest, cookies); err != nil {
			return fmt.Errorf("saving session for %s: %w", email, err)
		}

		// If we used a temp profile (--new or first-account), move it into place.
		if usingNewProfile {
			if err := os.Rename(profileDir, browser.ProfileDir(dest)); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not move temp chrome-profile %s into %s: %v (next chromium spawn will create a fresh one)\n",
					profileDir, browser.ProfileDir(dest), err)
			}
		}

		fmt.Printf("Session saved for %s.\n", email)
		return nil
	},
}

func newTempProfileDir() (string, error) {
	dir := filepath.Join(config.ConfigDir(), fmt.Sprintf(".tmp-profile-%d", time.Now().Unix()))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("creating temp profile dir: %w", err)
	}
	return dir, nil
}

func runAuthRevoke() error {
	existing, err := accounts.ScanAccounts()
	if err != nil {
		return fmt.Errorf("scanning accounts: %w", err)
	}
	if authAccount != "" {
		// Revoke specific account
		dir := accounts.AccountDir(authAccount)
		if err := auth.DeleteSession(dir); err != nil {
			return fmt.Errorf("deleting session for %s: %w", authAccount, err)
		}
		fmt.Printf("Session for %s revoked.\n", authAccount)
		return nil
	}
	// Revoke all
	for _, a := range existing {
		if err := auth.DeleteSession(a.Dir); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not delete session for %s: %v\n", a.Email, err)
		}
	}
	if err := auth.DeleteKey(); err != nil {
		return fmt.Errorf("deleting keyring entry: %w", err)
	}
	fmt.Printf("Revoked %d session(s).\n", len(existing))
	return nil
}

func runAuthCheck(ctx context.Context) error {
	existing, err := accounts.ScanAccounts()
	if err != nil {
		return fmt.Errorf("scanning accounts: %w", err)
	}
	if len(existing) == 0 {
		fmt.Println("No accounts configured.")
		return fmt.Errorf("no accounts")
	}
	allOK := true
	for _, a := range existing {
		if !a.HasSession {
			fmt.Printf("%s: no session.enc — run 'gxodus auth --account %s'\n", a.Email, a.Email)
			allOK = false
			continue
		}
		cookies, err := auth.LoadSession(a.Dir)
		if err != nil {
			fmt.Printf("%s: load failed (%v)\n", a.Email, err)
			allOK = false
			continue
		}
		valid, err := browser.CheckSession(ctx, cookies, "")
		if err != nil {
			fmt.Printf("%s: check failed (%v)\n", a.Email, err)
			allOK = false
			continue
		}
		if valid {
			fmt.Printf("%s: ✓ valid\n", a.Email)
		} else {
			fmt.Printf("%s: ✗ expired — run 'gxodus auth --account %s'\n", a.Email, a.Email)
			allOK = false
		}
	}
	if !allOK {
		return fmt.Errorf("some accounts need re-auth")
	}
	return nil
}

func init() {
	authCmd.Flags().BoolVar(&authCheck, "check", false, "validate saved session(s) are still active")
	authCmd.Flags().BoolVar(&authRevoke, "revoke", false, "delete saved session(s)")
	authCmd.Flags().BoolVar(&authNewFlag, "new", false, "add a new account (fresh chromium profile)")
	authCmd.Flags().StringVar(&authAccount, "account", "", "target a specific account by email")
	rootCmd.AddCommand(authCmd)
}
