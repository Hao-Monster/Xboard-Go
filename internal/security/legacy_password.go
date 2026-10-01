package security

import (
	"crypto/md5" // Only for verifying imported PHP credentials; never for new passwords.
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

const legacyPasswordPrefix = "$xboard-legacy$"

// EncodeLegacyPassword translates PHP's explicit legacy algorithms into a
// bounded, self-contained verifier. Successful login upgrades it to Argon2id.
func EncodeLegacyPassword(algorithm, salt, hash string) (string, error) {
	switch algorithm {
	case "md5", "md5salt", "sha256", "sha256salt":
		n := 32
		if strings.HasPrefix(algorithm, "sha256") {
			n = 64
		}
		if len(hash) != n || strings.ToLower(hash) != hash || len(salt) > 1024 {
			return "", errors.New("invalid legacy password encoding")
		}
		if _, err := hex.DecodeString(hash); err != nil {
			return "", errors.New("invalid legacy password digest")
		}
		if algorithm == "md5" || algorithm == "sha256" {
			salt = ""
		}
		return legacyPasswordPrefix + algorithm + "$" + base64.RawStdEncoding.EncodeToString([]byte(salt)) + "$" + hash, nil
	default:
		// PHP defaults to password_verify for an unrecognized/empty algorithm.
		if IsLegacyBcryptHash(hash) {
			return hash, nil
		}
		if _, _, _, err := parsePasswordHash(hash); err == nil {
			return hash, nil
		}
		return "", errors.New("unsupported legacy password hash")
	}
}
func parseLegacyPassword(encoded string) (string, string, string, bool) {
	if len(encoded) > 1600 || !strings.HasPrefix(encoded, legacyPasswordPrefix) {
		return "", "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(encoded, legacyPasswordPrefix), "$")
	if len(parts) != 3 {
		return "", "", "", false
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return "", "", "", false
	}
	canonical, err := EncodeLegacyPassword(parts[0], string(salt), parts[2])
	if err != nil || canonical != encoded {
		return "", "", "", false
	}
	return parts[0], string(salt), parts[2], true
}
func IsImportedPasswordHash(encoded string) bool {
	if IsLegacyBcryptHash(encoded) {
		return true
	}
	if _, _, _, err := parsePasswordHash(encoded); err == nil {
		return true
	}
	_, _, _, ok := parseLegacyPassword(encoded)
	return ok
}
func NeedsPasswordUpgrade(encoded string) bool {
	return IsLegacyBcryptHash(encoded) || strings.HasPrefix(encoded, legacyPasswordPrefix)
}
func verifyImportedPassword(password, encoded string) bool {
	algorithm, salt, expected, ok := parseLegacyPassword(encoded)
	if !ok {
		return false
	}
	material := password
	if strings.HasSuffix(algorithm, "salt") {
		material += salt
	}
	var actual string
	if strings.HasPrefix(algorithm, "md5") {
		sum := md5.Sum([]byte(material))
		actual = hex.EncodeToString(sum[:])
	} else {
		sum := sha256.Sum256([]byte(material))
		actual = hex.EncodeToString(sum[:])
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}
