package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/zalando/go-keyring"
)

const (
	keyringService = "gxodus"
	keyringUser    = "session-key"
	keyLength      = 32 // AES-256
)

func getOrCreateKey() ([]byte, error) {
	// Try to get existing key from keyring
	encoded, err := keyring.Get(keyringService, keyringUser)
	if err == nil {
		return hex.DecodeString(encoded)
	}

	// Key doesn't exist, generate a new one
	key := make([]byte, keyLength)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generating key: %w", err)
	}

	encoded = hex.EncodeToString(key)
	if err := keyring.Set(keyringService, keyringUser, encoded); err != nil {
		return nil, fmt.Errorf("storing key in keyring: %w", err)
	}

	return key, nil
}

func DeleteKey() error {
	err := keyring.Delete(keyringService, keyringUser)
	if err != nil && err != keyring.ErrNotFound {
		return fmt.Errorf("deleting key from keyring: %w", err)
	}
	return nil
}
