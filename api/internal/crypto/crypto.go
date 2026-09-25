// Package crypto provides authenticated encryption for secrets at rest.
//
// Ciphertext format: base64(StdEncoding) of nonce(12 bytes) || AES-256-GCM sealed data.
// The key is sha256(APP_SECRET).
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

var ErrDecrypt = errors.New("crypto: decryption failed")

type Box struct {
	aead cipher.AEAD
}

// New derives an AES-256-GCM key from secret.
func New(secret string) (*Box, error) {
	if secret == "" {
		return nil, errors.New("crypto: empty secret")
	}
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Encrypt seals plaintext with a random nonce and returns base64 output.
func (b *Box) Encrypt(plaintext []byte) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("crypto: nonce: %w", err)
	}
	out := b.aead.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Decrypt reverses Encrypt. Any tampering yields ErrDecrypt.
func (b *Box) Decrypt(ciphertext string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, ErrDecrypt
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns+b.aead.Overhead() {
		return nil, ErrDecrypt
	}
	pt, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

// EncryptString encrypts s; the empty string maps to the empty string
// (meaning "no secret stored").
func (b *Box) EncryptString(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	return b.Encrypt([]byte(s))
}

// DecryptString decrypts c; the empty string maps to the empty string.
func (b *Box) DecryptString(c string) (string, error) {
	if c == "" {
		return "", nil
	}
	pt, err := b.Decrypt(c)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}
