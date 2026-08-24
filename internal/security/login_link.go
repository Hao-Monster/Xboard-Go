package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

const (
	LoginLinkPurposeQuick = "quick"
	LoginLinkPurposeEmail = "email"

	loginLinkMACLabel        = "xboard-go:login-link:mac-key:v1"
	loginLinkEncryptionLabel = "xboard-go:login-link:encryption-key:v1"
	loginLinkCipherAAD       = "xboard-go:login-link:token:v1\x00"
)

var loginLinkCipherMagic = []byte("XLL1")

// LoginLinkProtector purpose-isolates verifier digests and only keeps the
// mailer's short-lived copy of a login token as authenticated ciphertext.
type LoginLinkProtector struct {
	macKey [sha256.Size]byte
	aead   cipher.AEAD
}

func NewLoginLinkProtector(masterKey []byte) (*LoginLinkProtector, error) {
	if len(masterKey) != 32 {
		return nil, errors.New("login link master key must be exactly 32 bytes")
	}
	macKey := deriveLoginLinkKey(masterKey, loginLinkMACLabel)
	encryptionKey := deriveLoginLinkKey(masterKey, loginLinkEncryptionLabel)
	block, err := aes.NewCipher(encryptionKey[:])
	for index := range encryptionKey {
		encryptionKey[index] = 0
	}
	if err != nil {
		return nil, fmt.Errorf("create login link cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create login link authenticated cipher: %w", err)
	}
	return &LoginLinkProtector{macKey: macKey, aead: aead}, nil
}

func (protector *LoginLinkProtector) NewToken() (string, error) {
	if protector == nil || protector.aead == nil {
		return "", errors.New("login link protector is unavailable")
	}
	random := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, random); err != nil {
		return "", fmt.Errorf("generate login link token: %w", err)
	}
	token := hex.EncodeToString(random)
	for index := range random {
		random[index] = 0
	}
	return token, nil
}

func (protector *LoginLinkProtector) TokenDigest(purpose, token string) ([]byte, error) {
	if protector == nil || protector.aead == nil || !validLoginLinkPurpose(purpose) || !validLoginLinkToken(token) {
		return nil, errors.New("login link purpose or token is invalid")
	}
	return protector.digest("token\x00", purpose, "\x00", token), nil
}

func (protector *LoginLinkProtector) EmailDigest(email string) ([]byte, error) {
	if protector == nil || protector.aead == nil || !normalizedPasswordResetEmail(email) {
		return nil, errors.New("login link email must be normalized")
	}
	return protector.digest("email\x00", email), nil
}

func (protector *LoginLinkProtector) EncryptToken(userID int64, token string) ([]byte, error) {
	if protector == nil || protector.aead == nil || userID < 1 || !validLoginLinkToken(token) {
		return nil, errors.New("login link owner or token is invalid")
	}
	nonce := make([]byte, protector.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate login link nonce: %w", err)
	}
	result := make([]byte, 0, len(loginLinkCipherMagic)+len(nonce)+len(token)+protector.aead.Overhead())
	result = append(result, loginLinkCipherMagic...)
	result = append(result, nonce...)
	return protector.aead.Seal(result, nonce, []byte(token), loginLinkOwnerAAD(userID)), nil
}

func (protector *LoginLinkProtector) DecryptToken(userID int64, payload []byte) ([]byte, error) {
	if protector == nil || protector.aead == nil || userID < 1 {
		return nil, errors.New("login link protector is unavailable")
	}
	prefixBytes := len(loginLinkCipherMagic) + protector.aead.NonceSize()
	if len(payload) < prefixBytes+protector.aead.Overhead() || !hmac.Equal(payload[:len(loginLinkCipherMagic)], loginLinkCipherMagic) {
		return nil, errors.New("login link token payload is malformed")
	}
	nonce := payload[len(loginLinkCipherMagic):prefixBytes]
	plaintext, err := protector.aead.Open(nil, nonce, payload[prefixBytes:], loginLinkOwnerAAD(userID))
	if err != nil || !validLoginLinkToken(string(plaintext)) {
		for index := range plaintext {
			plaintext[index] = 0
		}
		return nil, errors.New("login link token authentication failed")
	}
	return plaintext, nil
}

func (protector *LoginLinkProtector) digest(parts ...string) []byte {
	mac := hmac.New(sha256.New, protector.macKey[:])
	for _, part := range parts {
		_, _ = mac.Write([]byte(part))
	}
	return mac.Sum(nil)
}

func loginLinkOwnerAAD(userID int64) []byte {
	buffer := make([]byte, len(loginLinkCipherAAD)+8)
	copy(buffer, loginLinkCipherAAD)
	binary.BigEndian.PutUint64(buffer[len(loginLinkCipherAAD):], uint64(userID))
	return buffer
}

func validLoginLinkPurpose(purpose string) bool {
	return purpose == LoginLinkPurposeQuick || purpose == LoginLinkPurposeEmail
}

func validLoginLinkToken(token string) bool {
	if len(token) != 32 {
		return false
	}
	for _, character := range token {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func deriveLoginLinkKey(masterKey []byte, label string) [sha256.Size]byte {
	mac := hmac.New(sha256.New, masterKey)
	_, _ = mac.Write([]byte(label))
	var key [sha256.Size]byte
	copy(key[:], mac.Sum(nil))
	return key
}
