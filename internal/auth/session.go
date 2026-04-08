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

	"github.com/thinkjk/gxodus/internal/config"
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

func SessionPath() string {
	return filepath.Join(config.ConfigDir(), sessionFile)
}

func SaveSession(cookies []*http.Cookie) error {
	if err := config.EnsureConfigDir(); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
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

	if err := os.WriteFile(SessionPath(), encrypted, 0600); err != nil {
		return fmt.Errorf("writing session file: %w", err)
	}

	return nil
}

func LoadSession() ([]*http.Cookie, error) {
	encrypted, err := os.ReadFile(SessionPath())
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

func DeleteSession() error {
	path := SessionPath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing session file: %w", err)
	}
	return nil
}

func SessionExists() bool {
	_, err := os.Stat(SessionPath())
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
