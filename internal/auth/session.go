package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const sessionFile = "session.enc"

type CookieData struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Secure   bool   `json:"secure"`
	HTTPOnly bool   `json:"http_only"`
}

// SessionPath returns the session-file path for a given account dir.
func SessionPath(accountDir string) string {
	return filepath.Join(accountDir, sessionFile)
}

func SaveSession(accountDir string, cookies []*http.Cookie) error {
	if err := os.MkdirAll(accountDir, 0700); err != nil {
		return fmt.Errorf("creating account dir: %w", err)
	}

	data := make([]CookieData, len(cookies))
	for i, c := range cookies {
		data[i] = CookieData{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HTTPOnly: c.HttpOnly,
		}
	}

	plaintext, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshaling cookies: %w", err)
	}

	key, err := getOrCreateKey()
	if err != nil {
		return fmt.Errorf("getting encryption key: %w", err)
	}

	encrypted, err := encrypt(plaintext, key)
	if err != nil {
		return fmt.Errorf("encrypting session: %w", err)
	}

	if err := os.WriteFile(SessionPath(accountDir), encrypted, 0600); err != nil {
		return fmt.Errorf("writing session file: %w", err)
	}
	return nil
}

func LoadSession(accountDir string) ([]*http.Cookie, error) {
	encrypted, err := os.ReadFile(SessionPath(accountDir))
	if err != nil {
		return nil, fmt.Errorf("reading session file: %w", err)
	}

	key, err := getOrCreateKey()
	if err != nil {
		return nil, fmt.Errorf("getting encryption key: %w", err)
	}

	plaintext, err := decrypt(encrypted, key)
	if err != nil {
		return nil, fmt.Errorf("decrypting session: %w", err)
	}

	var data []CookieData
	if err := json.Unmarshal(plaintext, &data); err != nil {
		return nil, fmt.Errorf("unmarshaling cookies: %w", err)
	}

	cookies := make([]*http.Cookie, len(data))
	for i, d := range data {
		cookies[i] = &http.Cookie{
			Name:     d.Name,
			Value:    d.Value,
			Domain:   d.Domain,
			Path:     d.Path,
			Secure:   d.Secure,
			HttpOnly: d.HTTPOnly,
		}
	}

	return cookies, nil
}

func DeleteSession(accountDir string) error {
	if err := os.Remove(SessionPath(accountDir)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing session file: %w", err)
	}
	return nil
}

func SessionExists(accountDir string) bool {
	_, err := os.Stat(SessionPath(accountDir))
	return err == nil
}

func encrypt(plaintext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func decrypt(ciphertext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}
