package users

import (
	"crypto/rand"
	"fmt"
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

// HasPermission checks if a permission bitmask includes the given permission.
func HasPermission(permissions int, perm int) bool {
	return permissions&perm != 0
}
