// Package secrets encrypts values stored in the database (TOTP secrets,
// credentials for external systems) with AES-256-GCM. The key lives in a
// separate file, so a copy of the database alone does not reveal them.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

type Box struct{ aead cipher.AEAD }

// LoadOrCreate reads a 32-byte key from path or creates one (mode 0600).
func LoadOrCreate(path string) (*Box, error) {
	key, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, fmt.Errorf("create secret key: %w", err)
		}
		if _, err := f.Write(key); err != nil {
			f.Close()
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("secret key file must contain 32 bytes")
	}
	return New(key)
}

func New(key []byte) (*Box, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext; aad binds the value to its purpose (for example
// "totp:<user id>") so ciphertexts cannot be swapped between rows.
func (b *Box) Seal(plaintext, aad string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := b.aead.Seal(nonce, nonce, []byte(plaintext), []byte(aad))
	return "v1:" + base64.RawStdEncoding.EncodeToString(ct), nil
}

func (b *Box) Open(sealed, aad string) (string, error) {
	enc, ok := strings.CutPrefix(sealed, "v1:")
	if !ok {
		return "", errors.New("unknown secret format")
	}
	raw, err := base64.RawStdEncoding.DecodeString(enc)
	if err != nil || len(raw) < b.aead.NonceSize() {
		return "", errors.New("corrupt secret")
	}
	pt, err := b.aead.Open(nil, raw[:b.aead.NonceSize()], raw[b.aead.NonceSize():], []byte(aad))
	if err != nil {
		return "", errors.New("secret cannot be decrypted (wrong key or tampered)")
	}
	return string(pt), nil
}
