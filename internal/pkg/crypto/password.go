package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2 parameters for password hashing
const (
	argon2Time    = 2
	argon2Memory  = 64 * 1024
	argon2Threads = 4
	argon2KeyLen  = 32
	saltLength    = 16
)

// HashPassword hashes a password using Argon2id
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("failed to generate salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)

	// Encode to string format: $argon2id$v=19$m=65536,t=1,p=4$<salt>$<hash>
	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argon2Memory, argon2Time, argon2Threads, b64Salt, b64Hash)

	return encoded, nil
}

// VerifyPassword verifies a password against a hash
func VerifyPassword(password, encodedHash string) (bool, error) {
	// Parse the encoded hash
	vals := strings.Split(encodedHash, "$")
	if len(vals) != 6 {
		return false, fmt.Errorf("invalid hash format")
	}

	var version int
	var memory, time uint32
	var threads uint8

	_, err := fmt.Sscanf(vals[2], "v=%d", &version)
	if err != nil {
		return false, fmt.Errorf("invalid hash version: %w", err)
	}

	_, err = fmt.Sscanf(vals[3], "m=%d,t=%d,p=%d", &memory, &time, &threads)
	if err != nil {
		return false, fmt.Errorf("invalid hash parameters: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(vals[4])
	if err != nil {
		return false, fmt.Errorf("invalid salt: %w", err)
	}

	hash, err := base64.RawStdEncoding.DecodeString(vals[5])
	if err != nil {
		return false, fmt.Errorf("invalid hash: %w", err)
	}

	// Compute hash with the same parameters
	computedHash := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(hash)))

	// Use constant-time comparison
	return subtle.ConstantTimeCompare(hash, computedHash) == 1, nil
}

// GenerateRandomString generates a cryptographically secure random string
func GenerateRandomString(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random string: %w", err)
	}
	return base64.URLEncoding.EncodeToString(bytes)[:length], nil
}

// GenerateRandomBytes generates cryptographically secure random bytes
func GenerateRandomBytes(length int) ([]byte, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return nil, fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return bytes, nil
}

// TokenSalt is the salt used for token hashing. Set from config on startup.
var TokenSalt = "genix-token-salt"

// HashToken hashes a token for secure storage
func HashToken(token string) string {
	hash := argon2.IDKey([]byte(token), []byte(TokenSalt), 1, 16*1024, 1, 32)
	return base64.RawStdEncoding.EncodeToString(hash)
}

// GenerateAPIKey generates an API key with a prefix
func GenerateAPIKey(prefix string) (key string, hash string, err error) {
	// Generate 32 random bytes
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", fmt.Errorf("failed to generate API key: %w", err)
	}

	// Create the key with prefix
	key = fmt.Sprintf("%s_%s", prefix, base64.URLEncoding.EncodeToString(bytes))

	// Hash for storage
	hash = HashToken(key)

	return key, hash, nil
}
