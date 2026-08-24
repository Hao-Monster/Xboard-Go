package settings

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

var (
	cipherMagic = []byte("XBS1")
	cipherAAD   = []byte("xboard-go:app-settings:smtp-password:v1")
)

const maxPlaintextBytes = 4 << 10

// Cipher protects application-setting secrets at rest with authenticated
// encryption. The key must be supplied independently of the database.
type Cipher struct {
	aead cipher.AEAD
}

func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, errors.New("settings encryption key must be exactly 32 bytes")
	}
	keyCopy := append([]byte(nil), key...)
	block, err := aes.NewCipher(keyCopy)
	for index := range keyCopy {
		keyCopy[index] = 0
	}
	if err != nil {
		return nil, fmt.Errorf("create settings cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create settings authenticated cipher: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

func (box *Cipher) Encrypt(plaintext []byte) ([]byte, error) {
	if box == nil || box.aead == nil {
		return nil, errors.New("settings cipher is unavailable")
	}
	if len(plaintext) == 0 || len(plaintext) > maxPlaintextBytes {
		return nil, errors.New("settings secret must contain between 1 and 4096 bytes")
	}
	nonce := make([]byte, box.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate settings cipher nonce: %w", err)
	}
	result := make([]byte, 0, len(cipherMagic)+len(nonce)+len(plaintext)+box.aead.Overhead())
	result = append(result, cipherMagic...)
	result = append(result, nonce...)
	result = box.aead.Seal(result, nonce, plaintext, cipherAAD)
	return result, nil
}

func (box *Cipher) Decrypt(payload []byte) ([]byte, error) {
	if box == nil || box.aead == nil {
		return nil, errors.New("settings cipher is unavailable")
	}
	prefixBytes := len(cipherMagic) + box.aead.NonceSize()
	if len(payload) < prefixBytes+box.aead.Overhead() || !bytes.Equal(payload[:len(cipherMagic)], cipherMagic) {
		return nil, errors.New("settings secret payload is malformed")
	}
	nonce := payload[len(cipherMagic):prefixBytes]
	plaintext, err := box.aead.Open(nil, nonce, payload[prefixBytes:], cipherAAD)
	if err != nil {
		return nil, errors.New("settings secret authentication failed")
	}
	if len(plaintext) == 0 || len(plaintext) > maxPlaintextBytes {
		return nil, errors.New("settings secret payload contains an invalid plaintext length")
	}
	return plaintext, nil
}
