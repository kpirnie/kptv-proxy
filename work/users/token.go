package users

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Permission bitmask constants
const (
	PermRead        = 1 << iota // GET endpoints only
	PermConfigWrite             // POST /api/config
	PermRestart                 // POST /api/restart
	PermStreams                 // manage channels/streams
	PermLogs                    // read/clear logs
	PermXCAccounts              // manage XC output accounts
	PermEPGs                    // manage EPG sources
	PermSD                      // manage Schedules Direct accounts
)

// PermAll grants all permissions
const PermAll = PermRead | PermConfigWrite | PermRestart | PermStreams | PermLogs | PermXCAccounts | PermEPGs | PermSD

// randomAlnum returns n characters drawn uniformly from a 62-character
// alphanumeric alphabet. Bytes at or above the largest multiple of 62 are
// rejected and redrawn so the modulo cannot bias toward the first eight
// characters of the alphabet.
func randomAlnum(n int) (string, error) {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const limit = 256 - (256 % len(chars))

	out := make([]byte, 0, n)
	buf := make([]byte, n)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			out = append(out, chars[int(b)%len(chars)])
			if len(out) == n {
				break
			}
		}
	}
	return string(out), nil
}

// GenerateToken creates a cryptographically secure 64-character alphanumeric token.
func GenerateToken() (string, error) {
	s, err := randomAlnum(64)
	if err != nil {
		return "", fmt.Errorf("generating token: %w", err)
	}
	return s, nil
}

// HashToken returns the hex-encoded SHA-256 of a raw API token. Tokens are 64
// characters of crypto/rand output, so a KDF is unnecessary and a fast hash
// allows an indexed single-row lookup.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// IsLegacyTokenHash reports whether a stored hash is a pre-SHA-256 Argon2id
// value, which can no longer be verified and must be regenerated.
func IsLegacyTokenHash(hash string) bool {
	return strings.HasPrefix(hash, "$argon2id$")
}

// HasPermission checks if a permission bitmask includes the given permission.
func HasPermission(permissions int, perm int) bool {
	return permissions&perm != 0
}
