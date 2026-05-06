package cli

import (
	"fmt"

	"github.com/thinkjk/gxodus/internal/accounts"
)

// pickSingleAccount resolves a single account from the configured set.
// Used by `status`, the hidden `debug-*` commands, and any other
// single-account command flow. If --account flag is given, finds that
// one. If exactly 1 account exists, picks it. If 0 or 2+ and no flag,
// errors with guidance.
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
