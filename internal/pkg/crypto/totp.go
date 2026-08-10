package crypto

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// Minimal RFC 6238 TOTP (HMAC-SHA1, 30s step, 6 digits) — used for platform
// staff 2FA (Phase 3). No external dependency.

// GenerateTOTPSecret returns a new random base32 secret for enrollment.
func GenerateTOTPSecret() (string, error) {
	raw := make([]byte, 20) // 160-bit
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return strings.TrimRight(base32.StdEncoding.EncodeToString(raw), "="), nil
}

// VerifyTOTP checks a 6-digit code against the secret, tolerating ±1 time step
// for clock skew.
func VerifyTOTP(secretBase32, code string) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}
	secret, err := decodeBase32Secret(secretBase32)
	if err != nil {
		return false
	}
	counter := time.Now().Unix() / 30
	for _, delta := range []int64{0, -1, 1} {
		if totpAt(secret, counter+delta) == code {
			return true
		}
	}
	return false
}

// TOTPCodeNow computes the current code — used by tests / server-side checks.
func TOTPCodeNow(secretBase32 string) string {
	secret, err := decodeBase32Secret(secretBase32)
	if err != nil {
		return ""
	}
	return totpAt(secret, time.Now().Unix()/30)
}

func decodeBase32Secret(secretBase32 string) ([]byte, error) {
	s := strings.ToUpper(strings.TrimSpace(secretBase32))
	if pad := len(s) % 8; pad != 0 {
		s += strings.Repeat("=", 8-pad)
	}
	return base32.StdEncoding.DecodeString(s)
}

func totpAt(secret []byte, counter int64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(counter))
	mac := hmac.New(sha1.New, secret)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	return fmt.Sprintf("%06d", value%1000000)
}
