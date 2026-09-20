package httpapi

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"net/http"
	"regexp"
	"strings"
)

var echPublicName = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?$`)

// generateAdminNodeECH uses the legacy panel's ECHConfigList/keys wire format.
// Private material is returned only to the authenticated administrator and never stored or logged.
func (s *server) generateAdminNodeECH(w http.ResponseWriter, r *http.Request) {
	var request struct {
		PublicName string `json:"public_name"`
	}
	if !decodeJSONLimit(w, r, &request, 1024) {
		return
	}
	name := strings.TrimSpace(request.PublicName)
	if name == "" {
		name = "ech.example.com"
	}
	validName := len(name) <= 253
	for _, label := range strings.Split(name, ".") {
		validName = validName && len(label) <= 63 && echPublicName.MatchString(label)
	}
	if !validName {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "ECH 查询域名无效", nil)
		return
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "密钥生成失败", nil)
		return
	}
	id := make([]byte, 1)
	if _, err := rand.Read(id); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "密钥生成失败", nil)
		return
	}
	vector := func(value []byte) []byte {
		return append(binary.BigEndian.AppendUint16(nil, uint16(len(value))), value...)
	}
	contents := append(id, 0, 32) // DHKEM(X25519, HKDF-SHA256)
	contents = append(contents, vector(key.PublicKey().Bytes())...)
	contents = append(contents, 0, 8, 0, 1, 0, 1, 0, 1, 0, 3) // SHA256 + AES128GCM / ChaCha20Poly1305
	contents = append(contents, 0, byte(len(name)))
	contents = append(contents, []byte(name)...)
	contents = append(contents, 0, 0) // no extensions
	config := append([]byte{0xfe, 0x0d}, vector(contents)...)
	keys := append(vector(key.Bytes()), vector(config)...)
	w.Header().Set("Cache-Control", "no-store")
	writeSuccess(w, http.StatusOK, map[string]string{
		"key":    string(pem.EncodeToMemory(&pem.Block{Type: "ECH KEYS", Bytes: keys})),
		"config": string(pem.EncodeToMemory(&pem.Block{Type: "ECH CONFIGS", Bytes: vector(config)})),
	})
}
