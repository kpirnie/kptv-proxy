package users

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters — tuned for security without being punishing on modest hardware
const (
	argonMemory      = 64 * 1024 // 64MB
	argonIterations  = 3
	argonParallelism = 2
	argonSaltLength  = 16
	argonKeyLength   = 32
)

var (
	ErrInvalidHash         = errors.New("invalid hash format")
	ErrIncompatibleVersion = errors.New("incompatible argon2 version")
)

// HashPassword generates an Argon2id hash of the provided password.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generating salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt,
		argonIterations, argonMemory, argonParallelism, argonKeyLength)

	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory,
		argonIterations,
		argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)

	return encoded, nil
}

// VerifyPassword checks a plaintext password against a stored Argon2id hash.
func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		return false, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, ErrInvalidHash
	}
	if version != argon2.Version {
		return false, ErrIncompatibleVersion
	}

	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrInvalidHash
	}

	decodedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, ErrInvalidHash
	}

	comparison := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(decodedHash)))

	return subtle.ConstantTimeCompare(decodedHash, comparison) == 1, nil
}
