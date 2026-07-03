// Package crypto encrypts secrets (currently: GitHub OAuth tokens) at rest
// using AES-256-GCM, keyed by the ENCRYPTION_KEY env var.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

// EncryptToken encrypts plaintext and returns hex(nonce || ciphertext || tag).
func EncryptToken(plaintext string) (string, error) {
	gcm, err := newGCM()
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(sealed), nil
}

// DecryptToken reverses EncryptToken.
func DecryptToken(encoded string) (string, error) {
	gcm, err := newGCM()
	if err != nil {
		return "", err
	}

	raw, err := hex.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode encrypted token: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("encrypted token is truncated")
	}

	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt token: %w", err)
	}
	return string(plaintext), nil
}

func newGCM() (cipher.AEAD, error) {
	key, err := hex.DecodeString(os.Getenv("ENCRYPTION_KEY"))
	if err != nil || len(key) != 32 {
		return nil, errors.New("ENCRYPTION_KEY must be a 64-character hex string (32 bytes); generate with: openssl rand -hex 32")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
