// Package accounts manages the per-email account directories under
// $CONFIG_DIR/accounts/. Each account is one Google sign-in with its
// own session.enc, chrome-profile, and pending_export.uuid.
package accounts

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/thinkjk/gxodus/internal/config"
)

// Account describes one configured account on disk.
type Account struct {
	Email      string // the directory basename
	Dir        string // absolute path to $CONFIG_DIR/accounts/<email>
	HasSession bool   // session.enc present
}

// AccountDir returns the on-disk dir for the given email.
func AccountDir(email string) string {
	return filepath.Join(config.ConfigDir(), "accounts", email)
}

// AccountsRoot returns $CONFIG_DIR/accounts.
func AccountsRoot() string {
	return filepath.Join(config.ConfigDir(), "accounts")
}

// EnsureAccountDir creates the dir for the given email if missing.
func EnsureAccountDir(email string) error {
	return os.MkdirAll(AccountDir(email), 0700)
}

// ScanAccounts returns all account dirs found under $CONFIG_DIR/accounts/,
// sorted by email. Missing accounts dir is treated as empty (not an error).
func ScanAccounts() ([]Account, error) {
	root := AccountsRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", root, err)
	}

	out := make([]Account, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		_, err := os.Stat(filepath.Join(dir, "session.enc"))
		out = append(out, Account{
			Email:      e.Name(),
			Dir:        dir,
			HasSession: err == nil,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out, nil
}
