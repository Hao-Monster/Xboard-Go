package security

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestImportedPHPPasswordAlgorithms(t *testing.T) {
	h := DefaultPasswordHasher()
	for _, algorithm := range []string{"md5", "md5salt", "sha256", "sha256salt"} {
		t.Run(algorithm, func(t *testing.T) {
			material := "fixture-password"
			if algorithm == "md5salt" || algorithm == "sha256salt" {
				material += "fixture-salt"
			}
			var digest string
			if algorithm == "md5" || algorithm == "md5salt" {
				v := md5.Sum([]byte(material))
				digest = hex.EncodeToString(v[:])
			} else {
				v := sha256.Sum256([]byte(material))
				digest = hex.EncodeToString(v[:])
			}
			encoded, err := EncodeLegacyPassword(algorithm, "fixture-salt", digest)
			if err != nil {
				t.Fatal(err)
			}
			if !h.Verify("fixture-password", encoded) || h.Verify("wrong", encoded) || !NeedsPasswordUpgrade(encoded) {
				t.Fatal("imported verifier mismatch")
			}
		})
	}
	for _, v := range []string{"$xboard-legacy$md5$%%%$deadbeef", "$xboard-legacy$other$$abcd"} {
		if h.Verify("fixture-password", v) || IsImportedPasswordHash(v) {
			t.Fatal("malformed verifier accepted")
		}
	}
}
