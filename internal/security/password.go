package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

type PasswordParams struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

type PasswordHasher struct {
	params PasswordParams
}

func DefaultPasswordHasher() PasswordHasher {
	return NewPasswordHasher(PasswordParams{
		MemoryKiB:   64 * 1024,
		Iterations:  2,
		Parallelism: 2,
		SaltLength:  16,
		KeyLength:   32,
	})
}

func NewPasswordHasher(params PasswordParams) PasswordHasher {
	return PasswordHasher{params: params}
}

func (h PasswordHasher) Hash(password string) (string, error) {
	if password == "" {
		return "", errors.New("password is required")
	}
	if err := validatePasswordParams(h.params); err != nil {
		return "", err
	}

	salt := make([]byte, h.params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, h.params.Iterations, h.params.MemoryKiB, h.params.Parallelism, h.params.KeyLength)

	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		h.params.MemoryKiB,
		h.params.Iterations,
		h.params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func (h PasswordHasher) Verify(password, encoded string) bool {
	params, salt, expected, err := parsePasswordHash(encoded)
	if err != nil {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, params.Iterations, params.MemoryKiB, params.Parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func parsePasswordHash(encoded string) (PasswordParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return PasswordParams{}, nil, nil, errors.New("invalid password hash format")
	}

	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return PasswordParams{}, nil, nil, errors.New("invalid password hash parameters")
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil {
		return PasswordParams{}, nil, nil, errors.New("invalid password hash salt")
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil {
		return PasswordParams{}, nil, nil, errors.New("invalid password hash key")
	}

	params := PasswordParams{
		MemoryKiB:   memory,
		Iterations:  iterations,
		Parallelism: parallelism,
		SaltLength:  uint32(len(salt)),
		KeyLength:   uint32(len(key)),
	}
	if err := validatePasswordParams(params); err != nil {
		return PasswordParams{}, nil, nil, err
	}
	return params, salt, key, nil
}

func validatePasswordParams(params PasswordParams) error {
	if params.MemoryKiB < 8*1024 || params.MemoryKiB > 256*1024 {
		return errors.New("argon2 memory must be between 8192 and 262144 KiB")
	}
	if params.Iterations < 1 || params.Iterations > 10 {
		return errors.New("argon2 iterations must be between 1 and 10")
	}
	if params.Parallelism < 1 || params.Parallelism > 16 {
		return errors.New("argon2 parallelism must be between 1 and 16")
	}
	if params.SaltLength < 16 || params.SaltLength > 64 {
		return errors.New("argon2 salt length must be between 16 and 64")
	}
	if params.KeyLength < 16 || params.KeyLength > 64 {
		return errors.New("argon2 key length must be between 16 and 64")
	}
	return nil
}
