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
)

var invitationCipherMagic = []byte("XIC1")

const (
	invitationMACLabel        = "xboard-go:invitation:mac-key:v1"
	invitationEncryptionLabel = "xboard-go:invitation:encryption-key:v1"
	invitationCipherAAD       = "xboard-go:invitation:code:v1\x00"
	invitationCodeAlphabet    = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	invitationCodeLength      = 8
)

// InvitationProtector makes invitation codes searchable without retaining
// plaintext and keeps listable ciphertext bound to the code owner.
type InvitationProtector struct {
	macKey [sha256.Size]byte
	aead   cipher.AEAD
}

func NewInvitationProtector(masterKey []byte) (*InvitationProtector, error) {
	if len(masterKey) != 32 {
		return nil, errors.New("invitation master key must be exactly 32 bytes")
	}
	macKey := deriveInvitationKey(masterKey, invitationMACLabel)
	encryptionKey := deriveInvitationKey(masterKey, invitationEncryptionLabel)
	block, err := aes.NewCipher(encryptionKey[:])
	for index := range encryptionKey {
		encryptionKey[index] = 0
	}
	if err != nil {
		return nil, fmt.Errorf("create invitation cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create invitation authenticated cipher: %w", err)
	}
	return &InvitationProtector{macKey: macKey, aead: aead}, nil
}

func (protector *InvitationProtector) NewCode() (string, error) {
	if protector == nil || protector.aead == nil {
		return "", errors.New("invitation protector is unavailable")
	}
	result := make([]byte, invitationCodeLength)
	var randomByte [1]byte
	// 248 is the largest multiple of 62 below 256. Rejecting the tail avoids
	// modulo bias without introducing an unbounded allocation.
	for index := range result {
		for {
			if _, err := io.ReadFull(rand.Reader, randomByte[:]); err != nil {
				return "", fmt.Errorf("generate invitation code: %w", err)
			}
			if randomByte[0] < 248 {
				result[index] = invitationCodeAlphabet[int(randomByte[0])%len(invitationCodeAlphabet)]
				break
			}
		}
	}
	return string(result), nil
}

func ValidInvitationCode(code string) bool {
	if len(code) != invitationCodeLength {
		return false
	}
	for index := range code {
		character := code[index]
		if (character < '0' || character > '9') && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') {
			return false
		}
	}
	return true
}

func (protector *InvitationProtector) CodeDigest(code string) ([]byte, error) {
	if protector == nil || protector.aead == nil || !ValidInvitationCode(code) {
		return nil, errors.New("invitation code is invalid")
	}
	mac := hmac.New(sha256.New, protector.macKey[:])
	_, _ = mac.Write([]byte("code\x00"))
	_, _ = mac.Write([]byte(code))
	return mac.Sum(nil), nil
}

func (protector *InvitationProtector) EncryptCode(ownerID int64, code string) ([]byte, error) {
	if protector == nil || protector.aead == nil || ownerID < 1 || !ValidInvitationCode(code) {
		return nil, errors.New("invitation owner or code is invalid")
	}
	nonce := make([]byte, protector.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate invitation nonce: %w", err)
	}
	result := make([]byte, 0, len(invitationCipherMagic)+len(nonce)+len(code)+protector.aead.Overhead())
	result = append(result, invitationCipherMagic...)
	result = append(result, nonce...)
	return protector.aead.Seal(result, nonce, []byte(code), invitationAAD(ownerID)), nil
}

func (protector *InvitationProtector) DecryptCode(ownerID int64, payload []byte) ([]byte, error) {
	if protector == nil || protector.aead == nil || ownerID < 1 {
		return nil, errors.New("invitation protector is unavailable")
	}
	prefixBytes := len(invitationCipherMagic) + protector.aead.NonceSize()
	if len(payload) < prefixBytes+protector.aead.Overhead() || !hmac.Equal(payload[:len(invitationCipherMagic)], invitationCipherMagic) {
		return nil, errors.New("invitation code payload is malformed")
	}
	nonce := payload[len(invitationCipherMagic):prefixBytes]
	plaintext, err := protector.aead.Open(nil, nonce, payload[prefixBytes:], invitationAAD(ownerID))
	if err != nil || !ValidInvitationCode(string(plaintext)) {
		for index := range plaintext {
			plaintext[index] = 0
		}
		return nil, errors.New("invitation code authentication failed")
	}
	return plaintext, nil
}

func invitationAAD(ownerID int64) []byte {
	result := make([]byte, len(invitationCipherAAD)+8)
	copy(result, invitationCipherAAD)
	binary.BigEndian.PutUint64(result[len(invitationCipherAAD):], uint64(ownerID))
	return result
}

func deriveInvitationKey(masterKey []byte, label string) [sha256.Size]byte {
	mac := hmac.New(sha256.New, masterKey)
	_, _ = mac.Write([]byte(label))
	var key [sha256.Size]byte
	copy(key[:], mac.Sum(nil))
	return key
}
