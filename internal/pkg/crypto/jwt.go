package crypto

import (
	"errors"
	"fmt"
	"time"

	"github.com/genixerp/genix-backend/internal/config"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// TokenType represents the type of JWT token
type TokenType string

const (
	TokenTypeAccess  TokenType = "access"
	TokenTypeRefresh TokenType = "refresh"
)

// Claims represents JWT claims
type Claims struct {
	jwt.RegisteredClaims
	UserID        uuid.UUID `json:"uid"`
	TenantID      uuid.UUID `json:"tid"`
	Email         string    `json:"email"`
	IsSystemAdmin bool      `json:"isa"`
	TokenType     TokenType `json:"type"`
	SessionID     string    `json:"sid"`
}

// JWTManager handles JWT operations
type JWTManager struct {
	config config.JWTConfig
}

// NewJWTManager creates a new JWT manager
func NewJWTManager(cfg config.JWTConfig) *JWTManager {
	return &JWTManager{config: cfg}
}

// TokenPair represents a pair of access and refresh tokens
type TokenPair struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	TokenType    string    `json:"token_type"`
}

// GenerateTokenPair generates both access and refresh tokens
func (m *JWTManager) GenerateTokenPair(userID, tenantID uuid.UUID, email string, isSystemAdmin bool) (*TokenPair, error) {
	sessionID, err := GenerateRandomString(32)
	if err != nil {
		return nil, fmt.Errorf("failed to generate session ID: %w", err)
	}

	accessToken, expiresAt, err := m.generateToken(userID, tenantID, email, isSystemAdmin, sessionID, TokenTypeAccess)
	if err != nil {
		return nil, fmt.Errorf("failed to generate access token: %w", err)
	}

	refreshToken, _, err := m.generateToken(userID, tenantID, email, isSystemAdmin, sessionID, TokenTypeRefresh)
	if err != nil {
		return nil, fmt.Errorf("failed to generate refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt,
		TokenType:    "Bearer",
	}, nil
}

// GenerateAccessToken generates only an access token
func (m *JWTManager) GenerateAccessToken(userID, tenantID uuid.UUID, email string, isSystemAdmin bool, sessionID string) (string, time.Time, error) {
	return m.generateToken(userID, tenantID, email, isSystemAdmin, sessionID, TokenTypeAccess)
}

// GenerateRefreshToken generates only a refresh token
func (m *JWTManager) GenerateRefreshToken(userID, tenantID uuid.UUID, email string, isSystemAdmin bool, sessionID string) (string, time.Time, error) {
	return m.generateToken(userID, tenantID, email, isSystemAdmin, sessionID, TokenTypeRefresh)
}

func (m *JWTManager) generateToken(userID, tenantID uuid.UUID, email string, isSystemAdmin bool, sessionID string, tokenType TokenType) (string, time.Time, error) {
	now := time.Now()
	var expiry time.Duration

	switch tokenType {
	case TokenTypeAccess:
		expiry = m.config.AccessTokenExpiry
	case TokenTypeRefresh:
		expiry = m.config.RefreshTokenExpiry
	default:
		return "", time.Time{}, errors.New("invalid token type")
	}

	expiresAt := now.Add(expiry)

	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.New().String(),
			Subject:   userID.String(),
			Issuer:    m.config.Issuer,
			Audience:  jwt.ClaimStrings{m.config.Audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
		UserID:        userID,
		TenantID:      tenantID,
		Email:         email,
		IsSystemAdmin: isSystemAdmin,
		TokenType:     tokenType,
		SessionID:     sessionID,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(m.config.SecretKey))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to sign token: %w", err)
	}

	return tokenString, expiresAt, nil
}

// ValidateToken validates a JWT token and returns its claims
func (m *JWTManager) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(m.config.SecretKey), nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	return claims, nil
}

// ValidateAccessToken validates an access token
func (m *JWTManager) ValidateAccessToken(tokenString string) (*Claims, error) {
	claims, err := m.ValidateToken(tokenString)
	if err != nil {
		return nil, err
	}

	if claims.TokenType != TokenTypeAccess {
		return nil, ErrInvalidTokenType
	}

	return claims, nil
}

// ValidateRefreshToken validates a refresh token
func (m *JWTManager) ValidateRefreshToken(tokenString string) (*Claims, error) {
	claims, err := m.ValidateToken(tokenString)
	if err != nil {
		return nil, err
	}

	if claims.TokenType != TokenTypeRefresh {
		return nil, ErrInvalidTokenType
	}

	return claims, nil
}

// RefreshTokens generates new token pair using a refresh token
func (m *JWTManager) RefreshTokens(refreshToken string) (*TokenPair, error) {
	claims, err := m.ValidateRefreshToken(refreshToken)
	if err != nil {
		return nil, err
	}

	// Generate new token pair with the same session ID
	accessToken, expiresAt, err := m.GenerateAccessToken(
		claims.UserID,
		claims.TenantID,
		claims.Email,
		claims.IsSystemAdmin,
		claims.SessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate new access token: %w", err)
	}

	newRefreshToken, _, err := m.GenerateRefreshToken(
		claims.UserID,
		claims.TenantID,
		claims.Email,
		claims.IsSystemAdmin,
		claims.SessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate new refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: newRefreshToken,
		ExpiresAt:    expiresAt,
		TokenType:    "Bearer",
	}, nil
}

// Errors
var (
	ErrTokenExpired     = errors.New("token has expired")
	ErrInvalidToken     = errors.New("invalid token")
	ErrInvalidTokenType = errors.New("invalid token type")
)
