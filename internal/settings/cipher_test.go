package settings

import (
	"bytes"
	"testing"
)

func TestCipherEncryptsAuthenticatesAndRandomizesSettingsSecrets(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	box, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	first, err := box.Encrypt([]byte("smtp-secret-password"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := box.Encrypt([]byte("smtp-secret-password"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(first, []byte("smtp-secret-password")) || bytes.Equal(first, second) {
		t.Fatalf("ciphertexts do not protect or randomize the secret")
	}
	plain, err := box.Decrypt(first)
	if err != nil || string(plain) != "smtp-secret-password" {
		t.Fatalf("Decrypt() = (%q, %v)", plain, err)
	}

	tampered := append([]byte(nil), first...)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := box.Decrypt(tampered); err == nil {
		t.Fatal("Decrypt() accepted tampered ciphertext")
	}
	other, err := NewCipher(bytes.Repeat([]byte{0x24}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Decrypt(first); err == nil {
		t.Fatal("Decrypt() accepted a different key")
	}
}

func TestCipherRejectsInvalidKeysAndPayloads(t *testing.T) {
	if _, err := NewCipher(make([]byte, 31)); err == nil {
		t.Fatal("NewCipher() accepted a non-256-bit key")
	}
	box, err := NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := box.Encrypt(nil); err == nil {
		t.Fatal("Encrypt() accepted an empty secret")
	}
	if _, err := box.Decrypt([]byte("short")); err == nil {
		t.Fatal("Decrypt() accepted a malformed payload")
	}
}

func TestCipherSeparatesSecretPurposesWithoutBreakingSMTPCompatibility(t *testing.T) {
	box, err := NewCipher(bytes.Repeat([]byte{0x51}, 32))
	if err != nil {
		t.Fatal(err)
	}
	smtpCiphertext, err := box.Encrypt([]byte("existing-smtp-secret"))
	if err != nil {
		t.Fatal(err)
	}
	smtpPlaintext, err := box.DecryptFor(SMTPPasswordPurpose, smtpCiphertext)
	if err != nil || string(smtpPlaintext) != "existing-smtp-secret" {
		t.Fatalf("DecryptFor(SMTP) = (%q, %v)", smtpPlaintext, err)
	}

	recaptchaCiphertext, err := box.EncryptFor(RecaptchaSecretPurpose, []byte("recaptcha-secret"))
	if err != nil {
		t.Fatal(err)
	}
	recaptchaPlaintext, err := box.DecryptFor(RecaptchaSecretPurpose, recaptchaCiphertext)
	if err != nil || string(recaptchaPlaintext) != "recaptcha-secret" {
		t.Fatalf("DecryptFor(recaptcha) = (%q, %v)", recaptchaPlaintext, err)
	}
	for _, purpose := range []SecretPurpose{SMTPPasswordPurpose, RecaptchaV3SecretPurpose, TurnstileSecretPurpose, PaymentConfigPurpose} {
		if _, err := box.DecryptFor(purpose, recaptchaCiphertext); err == nil {
			t.Fatalf("DecryptFor(%q) accepted a reCAPTCHA v2 secret", purpose)
		}
	}
	if _, err := box.EncryptFor(SecretPurpose("attacker-controlled"), []byte("secret")); err == nil {
		t.Fatal("EncryptFor() accepted an unknown purpose")
	}
}
