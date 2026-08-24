package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func newSubscriptionToken() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate subscription token: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}
