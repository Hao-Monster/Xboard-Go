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
