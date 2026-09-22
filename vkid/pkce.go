package vkid

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// PKCE holds a code_verifier and the S256 challenge derived from it.
type PKCE struct {
	Verifier  string
	Challenge string
}

// NewPKCE generates a random 64-character verifier (VK accepts 43..128) and
// its S256 challenge.
func NewPKCE() (PKCE, error) {
	buf := make([]byte, 48)
	if _, err := rand.Read(buf); err != nil {
		return PKCE{}, err
	}
	v := base64.RawURLEncoding.EncodeToString(buf) // 64 chars
	return PKCEFromVerifier(v), nil
}

// PKCEFromVerifier derives the challenge for an existing verifier.
func PKCEFromVerifier(verifier string) PKCE {
	sum := sha256.Sum256([]byte(verifier))
	return PKCE{Verifier: verifier, Challenge: base64.RawURLEncoding.EncodeToString(sum[:])}
}

// NewState returns a random state value that satisfies VK ID's requirement
// of at least 32 characters from [A-Za-z0-9_-].
func NewState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil // 43 chars
}
