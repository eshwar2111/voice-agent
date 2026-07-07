package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// GeneratePKCE creates a verifier and challenge for PKCE
func GeneratePKCE() (verifier string, challenge string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = base64URLEncode(b)
	
	hash := sha256.Sum256([]byte(verifier))
	challenge = base64URLEncode(hash[:])
	return verifier, challenge, nil
}

func base64URLEncode(b []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(b), "=")
}
