package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

var passwordResetCipherMagic = []byte("XPR1")

const (
	passwordResetMACLabel        = "xboard-go:password-reset:mac-key:v1"
	passwordResetEncryptionLabel = "xboard-go:password-reset:encryption-key:v1"
	passwordResetCipherAAD       = "xboard-go:password-reset:code:v1\x00"
	passwordResetCodeSpace       = uint32(900_000)
)

// PasswordResetProtector keeps low-entropy six-digit compatibility codes
// confidential at rest and binds every verifier to its normalized email.
type PasswordResetProtector struct {
	macKey [sha256.Size]byte
	aead   cipher.AEAD
}

func NewPasswordResetProtector(masterKey []byte) (*PasswordResetProtector, error) {
	if len(masterKey) != 32 {
		return nil, errors.New("password reset master key must be exactly 32 bytes")
	}
	macKey := derivePasswordResetKey(masterKey, passwordResetMACLabel)
	encryptionKey := derivePasswordResetKey(masterKey, passwordResetEncryptionLabel)
	block, err := aes.NewCipher(encryptionKey[:])
	for index := range encryptionKey {
		encryptionKey[index] = 0
	}
	if err != nil {
		return nil, fmt.Errorf("create password reset cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create password reset authenticated cipher: %w", err)
	}
	return &PasswordResetProtector{macKey: macKey, aead: aead}, nil
}

func (protector *PasswordResetProtector) NewCode() (string, error) {
	if protector == nil || protector.aead == nil {
		return "", errors.New("password reset protector is unavailable")
	}
	// Rejection sampling avoids modulo bias while using a single uint32 in the
	// overwhelmingly common case.
	limit := ^uint32(0) - (^uint32(0) % passwordResetCodeSpace)
	var bytes [4]byte
	for {
		if _, err := io.ReadFull(rand.Reader, bytes[:]); err != nil {
			return "", fmt.Errorf("generate password reset code: %w", err)
		}
		value := binary.BigEndian.Uint32(bytes[:])
		if value < limit {
			return strconv.FormatUint(uint64(value%passwordResetCodeSpace+100_000), 10), nil
		}
	}
}

func (protector *PasswordResetProtector) EmailDigest(email string) ([]byte, error) {
	if protector == nil || protector.aead == nil || !normalizedPasswordResetEmail(email) {
		return nil, errors.New("password reset email must be normalized")
	}
	return protector.digest("email\x00", email), nil
}

func (protector *PasswordResetProtector) CodeDigest(email, code string) ([]byte, error) {
	if protector == nil || protector.aead == nil || !normalizedPasswordResetEmail(email) || !validPasswordResetCode(code) {
		return nil, errors.New("password reset email or code is invalid")
	}
	return protector.digest("code\x00", email, "\x00", code), nil
}

func (protector *PasswordResetProtector) EncryptCode(email, code string) ([]byte, error) {
	if protector == nil || protector.aead == nil || !normalizedPasswordResetEmail(email) || !validPasswordResetCode(code) {
		return nil, errors.New("password reset email or code is invalid")
	}
	nonce := make([]byte, protector.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate password reset nonce: %w", err)
	}
	result := make([]byte, 0, len(passwordResetCipherMagic)+len(nonce)+len(code)+protector.aead.Overhead())
	result = append(result, passwordResetCipherMagic...)
	result = append(result, nonce...)
	result = protector.aead.Seal(result, nonce, []byte(code), []byte(passwordResetCipherAAD+email))
	return result, nil
}

func (protector *PasswordResetProtector) DecryptCode(email string, payload []byte) ([]byte, error) {
	if protector == nil || protector.aead == nil || !normalizedPasswordResetEmail(email) {
		return nil, errors.New("password reset protector is unavailable")
	}
	prefixBytes := len(passwordResetCipherMagic) + protector.aead.NonceSize()
	if len(payload) < prefixBytes+protector.aead.Overhead() || !hmac.Equal(payload[:len(passwordResetCipherMagic)], passwordResetCipherMagic) {
		return nil, errors.New("password reset code payload is malformed")
	}
	nonce := payload[len(passwordResetCipherMagic):prefixBytes]
	plaintext, err := protector.aead.Open(nil, nonce, payload[prefixBytes:], []byte(passwordResetCipherAAD+email))
	if err != nil || !validPasswordResetCode(string(plaintext)) {
		for index := range plaintext {
			plaintext[index] = 0
		}
		return nil, errors.New("password reset code authentication failed")
	}
	return plaintext, nil
}

func (protector *PasswordResetProtector) digest(parts ...string) []byte {
	if protector == nil {
		return nil
	}
	mac := hmac.New(sha256.New, protector.macKey[:])
	for _, part := range parts {
		_, _ = mac.Write([]byte(part))
	}
	return mac.Sum(nil)
}

func derivePasswordResetKey(masterKey []byte, label string) [sha256.Size]byte {
	mac := hmac.New(sha256.New, masterKey)
	_, _ = mac.Write([]byte(label))
	var key [sha256.Size]byte
	copy(key[:], mac.Sum(nil))
	return key
}

func normalizedPasswordResetEmail(email string) bool {
	return email != "" && len(email) <= 320 && email == strings.ToLower(strings.TrimSpace(email))
}

func validPasswordResetCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, character := range code {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
