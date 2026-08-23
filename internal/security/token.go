package security

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
)

type OpaqueToken struct {
	Plaintext string
	Digest    string
	Prefix    string
}

func NewOpaqueToken(byteLength int) (OpaqueToken, error) {
	if byteLength < 16 || byteLength > 128 {
		return OpaqueToken{}, errors.New("token length must be between 16 and 128 bytes")
	}

	random := make([]byte, byteLength)
	if _, err := rand.Read(random); err != nil {
		return OpaqueToken{}, err
	}
	plaintext := base64.RawURLEncoding.EncodeToString(random)
	prefixLength := min(8, len(plaintext))
	return OpaqueToken{
		Plaintext: plaintext,
		Digest:    DigestToken(plaintext),
		Prefix:    plaintext[:prefixLength],
	}, nil
}

func DigestToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
