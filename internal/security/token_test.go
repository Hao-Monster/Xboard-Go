package security

import (
	"crypto/sha256"
	"testing"
)

func TestLoginIdentityDigestNormalizesAndDomainSeparates(t *testing.T) {
	canonical := LoginIdentityDigest("user@example.test")
	if canonical != LoginIdentityDigest("  USER@EXAMPLE.TEST  ") {
		t.Fatal("LoginIdentityDigest() can be bypassed with case or surrounding whitespace")
	}
	if canonical == LoginIdentityDigest("other@example.test") {
		t.Fatal("LoginIdentityDigest() collided for distinct identities")
	}
	if canonical == sha256.Sum256([]byte("user@example.test")) {
		t.Fatal("LoginIdentityDigest() is not domain separated")
	}
}
