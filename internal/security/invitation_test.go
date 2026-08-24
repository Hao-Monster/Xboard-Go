package security

import (
	"bytes"
	"testing"
)

func TestInvitationProtectorGeneratesPurposeIsolatedAuthenticatedCodes(t *testing.T) {
	protector, err := NewInvitationProtector(bytes.Repeat([]byte{0x4d}, 32))
	if err != nil {
		t.Fatal(err)
	}
	for range 64 {
		code, err := protector.NewCode()
		if err != nil {
			t.Fatal(err)
		}
		if !ValidInvitationCode(code) {
			t.Fatalf("NewCode() = %q, want 8 ASCII alphanumeric characters", code)
		}
	}

	const ownerID int64 = 42
	const code = "aB09Zx7Q"
	digest, err := protector.CodeDigest(code)
	if err != nil || len(digest) != 32 {
		t.Fatalf("CodeDigest() length=%d error=%v", len(digest), err)
	}
	payload, err := protector.EncryptCode(ownerID, code)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(code)) {
		t.Fatal("encrypted invitation payload contains the plaintext code")
	}
	plaintext, err := protector.DecryptCode(ownerID, payload)
	if err != nil || string(plaintext) != code {
		t.Fatalf("DecryptCode() = %q, %v", plaintext, err)
	}
	for index := range plaintext {
		plaintext[index] = 0
	}
	if _, err := protector.DecryptCode(ownerID+1, payload); err == nil {
		t.Fatal("DecryptCode() accepted a different owner")
	}
	tampered := append([]byte(nil), payload...)
	tampered[len(tampered)-1] ^= 1
	if _, err := protector.DecryptCode(ownerID, tampered); err == nil {
		t.Fatal("DecryptCode() accepted a tampered payload")
	}
}

func TestInvitationProtectorRejectsInvalidInputs(t *testing.T) {
	if _, err := NewInvitationProtector(make([]byte, 31)); err == nil {
		t.Fatal("NewInvitationProtector() accepted a short key")
	}
	protector, _ := NewInvitationProtector(make([]byte, 32))
	for _, code := range []string{"short", "abcdefgh9", "abcd-123", "ＡＢＣＤ1234"} {
		if ValidInvitationCode(code) {
			t.Fatalf("ValidInvitationCode(%q) = true", code)
		}
		if _, err := protector.CodeDigest(code); err == nil {
			t.Fatalf("CodeDigest(%q) unexpectedly succeeded", code)
		}
	}
	if _, err := protector.EncryptCode(0, "Abcd1234"); err == nil {
		t.Fatal("EncryptCode() accepted an invalid owner")
	}
	if _, err := protector.DecryptCode(1, []byte("short")); err == nil {
		t.Fatal("DecryptCode() accepted malformed ciphertext")
	}
}

func BenchmarkInvitationProtector(b *testing.B) {
	protector, err := NewInvitationProtector(make([]byte, 32))
	if err != nil {
		b.Fatal(err)
	}
	const ownerID int64 = 42
	const code = "Abcd1234"
	b.Run("code-digest", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := protector.CodeDigest(code); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("encrypt-decrypt", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			ciphertext, err := protector.EncryptCode(ownerID, code)
			if err != nil {
				b.Fatal(err)
			}
			plaintext, err := protector.DecryptCode(ownerID, ciphertext)
			if err != nil {
				b.Fatal(err)
			}
			for index := range plaintext {
				plaintext[index] = 0
			}
		}
	})
}
