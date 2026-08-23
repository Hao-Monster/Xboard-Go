package security

import (
	"strings"
	"testing"
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
