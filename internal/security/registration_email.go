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
)

var registrationEmailCipherMagic = []byte("XRE1")

const (
	registrationEmailMACLabel        = "xboard-go:registration-email:mac-key:v1"
	registrationEmailEncryptionLabel = "xboard-go:registration-email:encryption-key:v1"
	registrationEmailCipherAAD       = "xboard-go:registration-email:code:v1\x00"
)

// RegistrationEmailProtector purpose-isolates registration challenges from
// password-reset codes even though both derive from the deployment key.
type RegistrationEmailProtector struct {
	macKey [sha256.Size]byte
	aead   cipher.AEAD
}

func NewRegistrationEmailProtector(masterKey []byte) (*RegistrationEmailProtector, error) {
	if len(masterKey) != 32 {
		return nil, errors.New("registration email master key must be exactly 32 bytes")
	}
	macKey := deriveRegistrationEmailKey(masterKey, registrationEmailMACLabel)
	encryptionKey := deriveRegistrationEmailKey(masterKey, registrationEmailEncryptionLabel)
	block, err := aes.NewCipher(encryptionKey[:])
	for index := range encryptionKey {
		encryptionKey[index] = 0
	}
	if err != nil {
		return nil, fmt.Errorf("create registration email cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create registration email authenticated cipher: %w", err)
	}
	return &RegistrationEmailProtector{macKey: macKey, aead: aead}, nil
}

func (protector *RegistrationEmailProtector) NewCode() (string, error) {
	if protector == nil || protector.aead == nil {
		return "", errors.New("registration email protector is unavailable")
	}
	limit := ^uint32(0) - (^uint32(0) % passwordResetCodeSpace)
	var bytes [4]byte
	for {
		if _, err := io.ReadFull(rand.Reader, bytes[:]); err != nil {
			return "", fmt.Errorf("generate registration email code: %w", err)
		}
		value := binary.BigEndian.Uint32(bytes[:])
		if value < limit {
			return strconv.FormatUint(uint64(value%passwordResetCodeSpace+100_000), 10), nil
		}
	}
}

func (protector *RegistrationEmailProtector) EmailDigest(email string) ([]byte, error) {
	if protector == nil || protector.aead == nil || !normalizedPasswordResetEmail(email) {
		return nil, errors.New("registration email must be normalized")
	}
	return protector.digest("email\x00", email), nil
}

func (protector *RegistrationEmailProtector) CodeDigest(email, code string) ([]byte, error) {
	if protector == nil || protector.aead == nil || !normalizedPasswordResetEmail(email) || !validPasswordResetCode(code) {
		return nil, errors.New("registration email or code is invalid")
	}
	return protector.digest("code\x00", email, "\x00", code), nil
}

func (protector *RegistrationEmailProtector) EncryptCode(email, code string) ([]byte, error) {
	if protector == nil || protector.aead == nil || !normalizedPasswordResetEmail(email) || !validPasswordResetCode(code) {
		return nil, errors.New("registration email or code is invalid")
	}
	nonce := make([]byte, protector.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate registration email nonce: %w", err)
	}
	result := make([]byte, 0, len(registrationEmailCipherMagic)+len(nonce)+len(code)+protector.aead.Overhead())
	result = append(result, registrationEmailCipherMagic...)
	result = append(result, nonce...)
	result = protector.aead.Seal(result, nonce, []byte(code), []byte(registrationEmailCipherAAD+email))
	return result, nil
}

func (protector *RegistrationEmailProtector) DecryptCode(email string, payload []byte) ([]byte, error) {
	if protector == nil || protector.aead == nil || !normalizedPasswordResetEmail(email) {
		return nil, errors.New("registration email protector is unavailable")
	}
	prefixBytes := len(registrationEmailCipherMagic) + protector.aead.NonceSize()
	if len(payload) < prefixBytes+protector.aead.Overhead() || !hmac.Equal(payload[:len(registrationEmailCipherMagic)], registrationEmailCipherMagic) {
		return nil, errors.New("registration email code payload is malformed")
	}
	nonce := payload[len(registrationEmailCipherMagic):prefixBytes]
	plaintext, err := protector.aead.Open(nil, nonce, payload[prefixBytes:], []byte(registrationEmailCipherAAD+email))
	if err != nil || !validPasswordResetCode(string(plaintext)) {
		for index := range plaintext {
			plaintext[index] = 0
		}
		return nil, errors.New("registration email code authentication failed")
	}
	return plaintext, nil
}

func (protector *RegistrationEmailProtector) digest(parts ...string) []byte {
	mac := hmac.New(sha256.New, protector.macKey[:])
	for _, part := range parts {
		_, _ = mac.Write([]byte(part))
	}
	return mac.Sum(nil)
}

func deriveRegistrationEmailKey(masterKey []byte, label string) [sha256.Size]byte {
	mac := hmac.New(sha256.New, masterKey)
	_, _ = mac.Write([]byte(label))
	var key [sha256.Size]byte
	copy(key[:], mac.Sum(nil))
	return key
}
