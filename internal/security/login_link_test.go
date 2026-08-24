package security

import (
	"bytes"
	"regexp"
	"testing"
)

func TestLoginLinkProtectorGeneratesPurposeIsolatedTokens(t *testing.T) {
	protector, err := NewLoginLinkProtector(bytes.Repeat([]byte{0x5a}, 32))
	if err != nil {
		t.Fatalf("NewLoginLinkProtector() error = %v", err)
	}
	first, err := protector.NewToken()
	if err != nil {
		t.Fatalf("NewToken() error = %v", err)
	}
	second, err := protector.NewToken()
	if err != nil {
		t.Fatalf("NewToken() second error = %v", err)
	}
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(first) || first == second {
		t.Fatalf("generated tokens are invalid or repeated: %q %q", first, second)
	}
	quickDigest, err := protector.TokenDigest(LoginLinkPurposeQuick, first)
	if err != nil {
		t.Fatalf("TokenDigest(quick) error = %v", err)
	}
	emailDigest, err := protector.TokenDigest(LoginLinkPurposeEmail, first)
	if err != nil {
		t.Fatalf("TokenDigest(email) error = %v", err)
	}
	if len(quickDigest) != 32 || len(emailDigest) != 32 || bytes.Equal(quickDigest, emailDigest) {
		t.Fatalf("purpose digests are not isolated: quick=%x email=%x", quickDigest, emailDigest)
	}
	addressDigest, err := protector.EmailDigest("user@example.test")
	if err != nil || len(addressDigest) != 32 || bytes.Equal(addressDigest, emailDigest) {
		t.Fatalf("EmailDigest() = %x, err=%v", addressDigest, err)
	}
}

func TestLoginLinkProtectorAuthenticatesCiphertextAndOwner(t *testing.T) {
	protector, err := NewLoginLinkProtector(bytes.Repeat([]byte{0x31}, 32))
	if err != nil {
		t.Fatalf("NewLoginLinkProtector() error = %v", err)
	}
	token, _ := protector.NewToken()
	ciphertext, err := protector.EncryptToken(42, token)
	if err != nil {
		t.Fatalf("EncryptToken() error = %v", err)
	}
	plaintext, err := protector.DecryptToken(42, ciphertext)
	if err != nil || string(plaintext) != token {
		t.Fatalf("DecryptToken() = %q, err=%v", plaintext, err)
	}
	for index := range plaintext {
		plaintext[index] = 0
	}
	if _, err := protector.DecryptToken(43, ciphertext); err == nil {
		t.Fatal("DecryptToken() accepted the wrong owner")
	}
	tampered := append([]byte(nil), ciphertext...)
	tampered[len(tampered)-1] ^= 1
	if _, err := protector.DecryptToken(42, tampered); err == nil {
		t.Fatal("DecryptToken() accepted tampered ciphertext")
	}
}

func TestLoginLinkProtectorRejectsInvalidInput(t *testing.T) {
	if _, err := NewLoginLinkProtector(make([]byte, 31)); err == nil {
		t.Fatal("NewLoginLinkProtector() accepted a short key")
	}
	protector, _ := NewLoginLinkProtector(make([]byte, 32))
	for _, token := range []string{"", "ABCDEF0123456789ABCDEF0123456789", "abcdef", "abcdef0123456789abcdef012345678x"} {
		if _, err := protector.TokenDigest(LoginLinkPurposeQuick, token); err == nil {
			t.Fatalf("TokenDigest() accepted %q", token)
		}
	}
	if _, err := protector.TokenDigest("other", "abcdef0123456789abcdef0123456789"); err == nil {
		t.Fatal("TokenDigest() accepted an unknown purpose")
	}
	if _, err := protector.EmailDigest(" User@Example.Test "); err == nil {
		t.Fatal("EmailDigest() accepted a non-normalized email")
	}
}
