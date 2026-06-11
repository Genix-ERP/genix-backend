package crypto

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// HashPassword / VerifyPassword
// ---------------------------------------------------------------------------

func TestHashPassword_AndVerify(t *testing.T) {
	tests := []struct {
		name     string
		password string
	}{
		{"simple password", "password123"},
		{"empty password", ""},
		{"unicode password", "p@$$w0rd-\u00fc\u00f1\u00ee\u00e7\u00f8d\u00e9"},
		{"long password", strings.Repeat("a", 256)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hash, err := HashPassword(tc.password)
			if err != nil {
				t.Fatalf("HashPassword(%q) returned error: %v", tc.password, err)
			}
			if hash == "" {
				t.Fatal("HashPassword returned empty hash")
			}

			// Correct password must verify
			ok, err := VerifyPassword(tc.password, hash)
			if err != nil {
				t.Fatalf("VerifyPassword returned error: %v", err)
			}
			if !ok {
				t.Error("VerifyPassword should return true for the correct password")
			}
		})
	}
}

func TestVerifyPassword_WrongPassword(t *testing.T) {
	hash, err := HashPassword("correct-password")
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}

	ok, err := VerifyPassword("wrong-password", hash)
	if err != nil {
		t.Fatalf("VerifyPassword returned error: %v", err)
	}
	if ok {
		t.Error("VerifyPassword should return false for a wrong password")
	}
}

func TestHashPassword_UniqueSalts(t *testing.T) {
	h1, err := HashPassword("same")
	if err != nil {
		t.Fatal(err)
	}
	h2, err := HashPassword("same")
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Error("two hashes of the same password should differ (random salt)")
	}
}

func TestVerifyPassword_InvalidHash(t *testing.T) {
	tests := []struct {
		name string
		hash string
	}{
		{"empty string", ""},
		{"random garbage", "not-a-valid-hash"},
		{"wrong segment count", "$argon2id$v=19$m=65536"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := VerifyPassword("password", tc.hash)
			if err == nil {
				t.Error("expected error for invalid hash format")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// HashToken
// ---------------------------------------------------------------------------

func TestHashToken_Deterministic(t *testing.T) {
	token := "test-token-abc123"
	h1 := HashToken(token)
	h2 := HashToken(token)

	if h1 == "" {
		t.Fatal("HashToken returned empty string")
	}
	if h1 != h2 {
		t.Errorf("HashToken should be deterministic: got %q and %q", h1, h2)
	}
}

func TestHashToken_DifferentInputs(t *testing.T) {
	h1 := HashToken("token-a")
	h2 := HashToken("token-b")

	if h1 == h2 {
		t.Error("different tokens should produce different hashes")
	}
}

// ---------------------------------------------------------------------------
// GenerateRandomString (stands in for GenerateSecureToken)
// ---------------------------------------------------------------------------

func TestGenerateRandomString(t *testing.T) {
	tests := []struct {
		name   string
		length int
	}{
		{"length 16", 16},
		{"length 32", 32},
		{"length 64", 64},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := GenerateRandomString(tc.length)
			if err != nil {
				t.Fatalf("GenerateRandomString(%d) returned error: %v", tc.length, err)
			}
			if len(s) != tc.length {
				t.Errorf("expected length %d, got %d", tc.length, len(s))
			}
		})
	}
}

func TestGenerateRandomString_Unique(t *testing.T) {
	s1, err := GenerateRandomString(32)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := GenerateRandomString(32)
	if err != nil {
		t.Fatal(err)
	}
	if s1 == s2 {
		t.Error("two random strings should differ")
	}
}
