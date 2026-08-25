package security

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestPasswordHasherRoundTrip(t *testing.T) {
	hasher := NewPasswordHasher(PasswordParams{
		MemoryKiB:   8 * 1024,
		Iterations:  1,
		Parallelism: 1,
		SaltLength:  16,
		KeyLength:   32,
	})

	encoded, err := hasher.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if strings.Contains(encoded, "correct horse") {
		t.Fatal("encoded hash contains password material")
	}
	if ok := hasher.Verify("correct horse battery staple", encoded); !ok {
		t.Fatal("Verify() rejected the correct password")
	}
	if ok := hasher.Verify("wrong password", encoded); ok {
		t.Fatal("Verify() accepted a wrong password")
	}
	if ok := hasher.Verify("correct horse battery staple", "not-a-hash"); ok {
		t.Fatal("Verify() accepted a malformed hash")
	}
}

func TestPasswordHasherAcceptsOnlyBoundedLegacyBcrypt(t *testing.T) {
	hasher := NewPasswordHasher(PasswordParams{
		MemoryKiB:   8 * 1024,
		Iterations:  1,
		Parallelism: 1,
		SaltLength:  16,
		KeyLength:   32,
	})

	encoded, err := bcrypt.GenerateFromPassword([]byte("legacy-password-123"), 10)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	phpEncoded := "$2y$" + string(encoded[4:])
	if !IsLegacyBcryptHash(phpEncoded) {
		t.Fatal("IsLegacyBcryptHash() rejected a supported PHP bcrypt hash")
	}
	if !hasher.Verify("legacy-password-123", phpEncoded) {
		t.Fatal("Verify() rejected the correct legacy bcrypt password")
	}
	if hasher.Verify("wrong-password", phpEncoded) {
		t.Fatal("Verify() accepted a wrong legacy bcrypt password")
	}

	weak, err := bcrypt.GenerateFromPassword([]byte("legacy-password-123"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword(weak) error = %v", err)
	}
	if IsLegacyBcryptHash(string(weak)) || hasher.Verify("legacy-password-123", string(weak)) {
		t.Fatal("weak legacy bcrypt cost was accepted")
	}

	for _, invalid := range []string{
		"$2a$99$invalid",
		"$2x$10$invalid",
		"$2b$10$short",
		"not-a-hash",
	} {
		if IsLegacyBcryptHash(invalid) || hasher.Verify("legacy-password-123", invalid) {
			t.Fatalf("invalid legacy hash %q was accepted", invalid)
		}
	}
}

func TestOpaqueTokenStoresOnlyDigest(t *testing.T) {
	first, err := NewOpaqueToken(32)
	if err != nil {
		t.Fatalf("NewOpaqueToken() error = %v", err)
	}
	second, err := NewOpaqueToken(32)
	if err != nil {
		t.Fatalf("NewOpaqueToken() error = %v", err)
	}
	if first.Plaintext == second.Plaintext {
		t.Fatal("independent tokens must be unique")
	}
	if first.Digest == first.Plaintext || strings.Contains(first.Digest, first.Plaintext) {
		t.Fatal("digest exposes plaintext token")
	}
	if got := DigestToken(first.Plaintext); got != first.Digest {
		t.Fatalf("DigestToken() = %q, want %q", got, first.Digest)
	}
}

func TestRandomHexUsesTheRequestedEntropy(t *testing.T) {
	first, err := NewRandomHex(10)
	if err != nil {
		t.Fatalf("NewRandomHex() error = %v", err)
	}
	second, err := NewRandomHex(10)
	if err != nil {
		t.Fatalf("NewRandomHex() second error = %v", err)
	}
	if len(first) != 20 || len(second) != 20 || first == second {
		t.Fatalf("random labels first=%q second=%q", first, second)
	}
	if _, err := NewRandomHex(7); err == nil {
		t.Fatal("NewRandomHex() accepted insufficient entropy")
	}
}
