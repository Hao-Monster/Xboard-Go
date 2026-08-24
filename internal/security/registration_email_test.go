package security

import (
	"bytes"
	"testing"
)

func TestRegistrationEmailProtectorPurposeIsolationAndAuthentication(t *testing.T) {
	master := bytes.Repeat([]byte{0x6a}, 32)
	registration, err := NewRegistrationEmailProtector(master)
	if err != nil {
		t.Fatal(err)
	}
	reset, err := NewPasswordResetProtector(master)
	if err != nil {
		t.Fatal(err)
	}
	const email = "registration@example.test"
	code, err := registration.NewCode()
	if err != nil || len(code) != 6 {
		t.Fatalf("NewCode() = %q, %v", code, err)
	}
	payload, err := registration.EncryptCode(email, code)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := registration.DecryptCode(email, payload)
	if err != nil || string(plaintext) != code {
		t.Fatalf("DecryptCode() = %q, %v", plaintext, err)
	}
	for index := range plaintext {
		plaintext[index] = 0
	}
	if _, err := registration.DecryptCode("other@example.test", payload); err == nil {
		t.Fatal("DecryptCode() accepted a different email")
	}
	if _, err := reset.DecryptCode(email, payload); err == nil {
		t.Fatal("password reset protector accepted a registration payload")
	}
	registrationDigest, _ := registration.CodeDigest(email, code)
	resetDigest, _ := reset.CodeDigest(email, code)
	if bytes.Equal(registrationDigest, resetDigest) {
		t.Fatal("registration and reset code digests are not purpose-isolated")
	}
}

func TestRegistrationEmailProtectorRejectsInvalidInputs(t *testing.T) {
	if _, err := NewRegistrationEmailProtector(make([]byte, 31)); err == nil {
		t.Fatal("NewRegistrationEmailProtector() accepted a short key")
	}
	protector, _ := NewRegistrationEmailProtector(make([]byte, 32))
	if _, err := protector.CodeDigest("UPPER@example.test", "123456"); err == nil {
		t.Fatal("CodeDigest() accepted an unnormalized email")
	}
	if _, err := protector.EncryptCode("user@example.test", "12345x"); err == nil {
		t.Fatal("EncryptCode() accepted a non-numeric code")
	}
	if _, err := protector.DecryptCode("user@example.test", []byte("short")); err == nil {
		t.Fatal("DecryptCode() accepted malformed ciphertext")
	}
}
