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

// GenerateToken creates a cryptographically secure 64-character alphanumeric token.
func GenerateToken() (string, error) {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	bytes := make([]byte, 64)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generating token: %w", err)
	}
	for i, b := range bytes {
		bytes[i] = chars[b%byte(len(chars))]
	}
	return string(bytes), nil
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
