package security

import (
	"bytes"
	"testing"
)

func TestPasswordResetProtectorGeneratesBindsAndEncryptsCodes(t *testing.T) {
	protector, err := NewPasswordResetProtector(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	code, err := protector.NewCode()
	if err != nil || !validPasswordResetCode(code) {
		t.Fatalf("NewCode() = %q, %v", code, err)
	}
	email := "user@example.test"
	emailDigest, err := protector.EmailDigest(email)
	if err != nil || len(emailDigest) != 32 {
		t.Fatalf("EmailDigest() length = %d, error = %v", len(emailDigest), err)
	}
	codeDigest, err := protector.CodeDigest(email, code)
	if err != nil || len(codeDigest) != 32 || bytes.Contains(codeDigest, []byte(code)) {
		t.Fatalf("CodeDigest() did not protect code: length=%d error=%v", len(codeDigest), err)
	}
	ciphertext, err := protector.EncryptCode(email, code)
	if err != nil || bytes.Contains(ciphertext, []byte(code)) {
		t.Fatalf("EncryptCode() exposed code or failed: %q, %v", ciphertext, err)
	}
	plaintext, err := protector.DecryptCode(email, ciphertext)
	if err != nil || string(plaintext) != code {
		t.Fatalf("DecryptCode() = %q, %v", plaintext, err)
	}
	for index := range plaintext {
		plaintext[index] = 0
	}
	if _, err := protector.DecryptCode("other@example.test", ciphertext); err == nil {
		t.Fatal("DecryptCode() accepted a ciphertext for another email")
	}
	tampered := append([]byte(nil), ciphertext...)
	tampered[len(tampered)-1] ^= 1
	if _, err := protector.DecryptCode(email, tampered); err == nil {
		t.Fatal("DecryptCode() accepted tampered ciphertext")
	}
}

func TestPasswordResetProtectorRejectsInvalidKeysAndInputs(t *testing.T) {
	if _, err := NewPasswordResetProtector(make([]byte, 31)); err == nil {
		t.Fatal("NewPasswordResetProtector() accepted a short key")
	}
	protector, err := NewPasswordResetProtector(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := protector.EmailDigest(" USER@example.test "); err == nil {
		t.Fatal("EmailDigest() accepted an unnormalized email")
	}
	if _, err := protector.CodeDigest("user@example.test", "12345a"); err == nil {
		t.Fatal("CodeDigest() accepted a non-numeric code")
	}
}

func BenchmarkPasswordResetProtector(b *testing.B) {
	protector, err := NewPasswordResetProtector(make([]byte, 32))
	if err != nil {
		b.Fatal(err)
	}
	const email = "benchmark@example.test"
	const code = "483729"
	b.Run("code-digest", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := protector.CodeDigest(email, code); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("encrypt-decrypt", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			ciphertext, err := protector.EncryptCode(email, code)
			if err != nil {
				b.Fatal(err)
			}
			plaintext, err := protector.DecryptCode(email, ciphertext)
			if err != nil {
				b.Fatal(err)
			}
			for index := range plaintext {
				plaintext[index] = 0
			}
		}
	})
}
