package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thinkjk/gxodus/internal/config"
)

// failed_accounts is a newline-separated list of account emails that
// hit ErrSessionExpired during the most recent export cycle. The docker
// entrypoint reads this file on exit-1 and wipes only the listed
// sessions (rather than all of them) before re-running auth.

func failedAccountsPath() string {
	return filepath.Join(config.ConfigDir(), ".failed-accounts")
}

func writeFailedAccounts(emails []string) error {
	if err := config.EnsureConfigDir(); err != nil {
		return err
	}
	body := strings.Join(emails, "\n") + "\n"
	return os.WriteFile(failedAccountsPath(), []byte(body), 0600)
}

func readFailedAccounts() ([]string, error) {
	data, err := os.ReadFile(failedAccountsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", failedAccountsPath(), err)
	}
	out := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out, nil
}
