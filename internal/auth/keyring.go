package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
	"github.com/thinkjk/gxodus/internal/config"
)

const (
	keyringService = "gxodus"
	keyringUser    = "session-key"
	keyLength      = 32 // AES-256
	keyFileName    = "session-key"
)

func keyFilePath() string {
	return filepath.Join(config.ConfigDir(), keyFileName)
}

// getOrCreateKey returns the AES key used to encrypt the session.
// Storage precedence: OS keyring (preferred on dev machines) → 0600 file
// in ConfigDir (used in containers where no D-Bus / secret service exists).
func getOrCreateKey() ([]byte, error) {
	if encoded, err := keyring.Get(keyringService, keyringUser); err == nil {
		return hex.DecodeString(encoded)
	}

	if key, err := loadKeyFromFile(keyFilePath()); err == nil {
		return key, nil
	}

	key := make([]byte, keyLength)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generating key: %w", err)
	}
	encoded := hex.EncodeToString(key)

	if err := keyring.Set(keyringService, keyringUser, encoded); err == nil {
		return key, nil
	}

	if err := config.EnsureConfigDir(); err != nil {
		return nil, fmt.Errorf("creating config dir: %w", err)
	}
	if err := saveKeyToFile(keyFilePath(), key); err != nil {
		return nil, fmt.Errorf("storing key in file: %w", err)
	}
	return key, nil
}

func saveKeyToFile(path string, key []byte) error {
	return os.WriteFile(path, []byte(hex.EncodeToString(key)), 0600)
}

func loadKeyFromFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return hex.DecodeString(string(data))
}

func DeleteKey() error {
	keyringErr := keyring.Delete(keyringService, keyringUser)
	if keyringErr != nil && keyringErr != keyring.ErrNotFound {
		// Don't fail — file fallback may still need cleanup.
	}
	if err := os.Remove(keyFilePath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing key file: %w", err)
	}
	return nil
}
