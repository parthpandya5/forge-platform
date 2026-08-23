package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

type Cipher struct {
	aead cipher.AEAD
}

func New(encodedKey, environment string) (*Cipher, error) {
	var key []byte
	if encodedKey != "" {
		decoded, err := base64.StdEncoding.DecodeString(encodedKey)
		if err != nil {
			return nil, errors.New("FORGE_ENCRYPTION_KEY must be base64 encoded")
		}
		key = decoded
	} else if environment == "development" || environment == "test" {
		// This deterministic local key is deliberately unsuitable for shared or production deployments.
		digest := sha256.Sum256([]byte("forge-development-only-secret-key"))
		key = digest[:]
	} else {
		return nil, errors.New("FORGE_ENCRYPTION_KEY is required outside development")
	}
	if len(key) != 32 {
		return nil, errors.New("FORGE_ENCRYPTION_KEY must decode to exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize secret cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize secret AEAD: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Encrypt(plaintext []byte, associatedData []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("generate secret nonce: %w", err)
	}
	ciphertext = c.aead.Seal(nil, nonce, plaintext, associatedData)
	return ciphertext, nonce, nil
}

func (c *Cipher) Decrypt(ciphertext, nonce, associatedData []byte) ([]byte, error) {
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, associatedData)
	if err != nil {
		return nil, errors.New("decrypt secret: authentication failed")
	}
	return plaintext, nil
}
