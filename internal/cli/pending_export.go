package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thinkjk/gxodus/internal/config"
)

// pending_export.uuid lives next to session.enc and config.toml. It marks an
// export that's been created at Google but not yet downloaded. On startup,
// `gxodus export` reads it and resumes polling that UUID instead of creating
// a fresh export — so a container restart mid-poll doesn't fire another full
// backup. Cleared after a successful download.

func pendingExportPath() string {
	return filepath.Join(config.ConfigDir(), "pending_export.uuid")
}

// readPendingExport returns the persisted UUID, or "" if no marker exists.
// Returns an error only on filesystem trouble — a missing file is normal.
func readPendingExport() (string, error) {
	data, err := os.ReadFile(pendingExportPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading %s: %w", pendingExportPath(), err)
	}
	uuid := strings.TrimSpace(string(data))
	return uuid, nil
}

func writePendingExport(uuid string) error {
	if err := config.EnsureConfigDir(); err != nil {
		return fmt.Errorf("ensuring config dir: %w", err)
	}
	if err := os.WriteFile(pendingExportPath(), []byte(uuid+"\n"), 0600); err != nil {
		return fmt.Errorf("writing %s: %w", pendingExportPath(), err)
	}
	return nil
}

func clearPendingExport() error {
	if err := os.Remove(pendingExportPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing %s: %w", pendingExportPath(), err)
	}
	return nil
}
