package secrets

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// RandomPassword returns a URL-safe random password of n bytes (encoded length > n).
func RandomPassword(n int) (string, error) {
	if n < 16 {
		n = 16
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// RandomAPIKey returns a goincus_ prefixed API key.
func RandomAPIKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate api key: %w", err)
	}
	return "gic_" + hex.EncodeToString(buf), nil
}
