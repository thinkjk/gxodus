package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// pending_export.uuid lives inside each account dir
// ($CONFIG_DIR/accounts/<email>/pending_export.uuid). It marks an
// export that's been created at Google but not yet downloaded. On
// startup, gxodus export reads it and resumes polling that UUID
// instead of creating a fresh export — so a container restart
// mid-poll doesn't fire another full backup. Cleared after a
// successful download.

func pendingExportPath(accountDir string) string {
	return filepath.Join(accountDir, "pending_export.uuid")
}

func readPendingExport(accountDir string) (string, error) {
	data, err := os.ReadFile(pendingExportPath(accountDir))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading %s: %w", pendingExportPath(accountDir), err)
	}
	return strings.TrimSpace(string(data)), nil
}

func writePendingExport(accountDir, uuid string) error {
	if err := os.MkdirAll(accountDir, 0700); err != nil {
		return fmt.Errorf("ensuring account dir: %w", err)
	}
	if err := os.WriteFile(pendingExportPath(accountDir), []byte(uuid+"\n"), 0600); err != nil {
		return fmt.Errorf("writing %s: %w", pendingExportPath(accountDir), err)
	}
	return nil
}

func clearPendingExport(accountDir string) error {
	if err := os.Remove(pendingExportPath(accountDir)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing %s: %w", pendingExportPath(accountDir), err)
	}
	return nil
}
