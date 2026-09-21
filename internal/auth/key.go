// Package auth generates, stores, and verifies customer API keys.
//
// A key is "sk_" followed by 43 base62 characters encoding 256 random bits.
// Only its SHA-256 hash is stored. A fast hash is appropriate because the
// key is random and high-entropy; slow password hashes exist to protect
// low-entropy secrets.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"math/big"
	"regexp"
	"strings"
)

const (
	keyPrefix     = "sk_"
	keyBodyLength = 43 // ceil(256 / log2(62))
	// DisplayPrefixLength is how many leading characters of a key are stored
	// in clear to identify it in the dashboard.
	DisplayPrefixLength = 12
)

var keyPattern = regexp.MustCompile(`^sk_[0-9A-Za-z]{43}$`)

// GenerateKey returns a new random API key.
func GenerateKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand never fails on supported platforms.
		panic("auth: read random bytes: " + err.Error())
	}
	body := new(big.Int).SetBytes(b).Text(62)
	return keyPrefix + strings.Repeat("0", keyBodyLength-len(body)) + body
}

// ValidKeyFormat reports whether key has the shape of an API key.
func ValidKeyFormat(key string) bool {
	return keyPattern.MatchString(key)
}

// HashKey returns the SHA-256 hash under which a key is stored.
func HashKey(key string) []byte {
	sum := sha256.Sum256([]byte(key))
	return sum[:]
}

// DisplayPrefix returns the part of a key that is safe to show.
func DisplayPrefix(key string) string {
	if len(key) < DisplayPrefixLength {
		return key
	}
	return key[:DisplayPrefixLength]
}
