package security

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
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

// LoginIdentityDigest produces a domain-separated, normalized identifier for
// persistent login throttling without storing account identifiers in plaintext.
func LoginIdentityDigest(email string) [sha256.Size]byte {
	normalized := strings.ToLower(strings.TrimSpace(email))
	return sha256.Sum256([]byte("xboard-go/login-failure/v1\x00" + normalized))
}

func NewRandomHex(byteLength int) (string, error) {
	if byteLength < 8 || byteLength > 64 {
		return "", errors.New("random label length must be between 8 and 64 bytes")
	}
	random := make([]byte, byteLength)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return hex.EncodeToString(random), nil
}
