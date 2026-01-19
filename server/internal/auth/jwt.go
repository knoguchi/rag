package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var (
	// ErrInvalidToken is returned when the token is invalid
	ErrInvalidToken = errors.New("invalid token")
	// ErrExpiredToken is returned when the token has expired
	ErrExpiredToken = errors.New("token has expired")
	// ErrInvalidClaims is returned when the token claims are invalid
	ErrInvalidClaims = errors.New("invalid token claims")
)

// Claims represents the JWT claims for tenant authentication
type Claims struct {
	jwt.RegisteredClaims
	TenantID   string `json:"tenant_id"`
	TenantName string `json:"tenant_name,omitempty"`
}

// JWTConfig holds configuration for JWT token generation and validation
type JWTConfig struct {
	Secret     string
	Expiry     time.Duration
	Issuer     string
	SigningMethod jwt.SigningMethod
}

// DefaultJWTConfig returns a default JWT configuration
func DefaultJWTConfig(secret string) *JWTConfig {
	return &JWTConfig{
		Secret:        secret,
		Expiry:        24 * time.Hour,
		Issuer:        "rag-service",
		SigningMethod: jwt.SigningMethodHS256,
	}
}

// JWTManager handles JWT token generation and validation
type JWTManager struct {
	config *JWTConfig
}

// NewJWTManager creates a new JWT manager with the given configuration
func NewJWTManager(config *JWTConfig) *JWTManager {
	if config.SigningMethod == nil {
		config.SigningMethod = jwt.SigningMethodHS256
	}
	return &JWTManager{config: config}
}

// GenerateToken generates a JWT token for the given tenant
func (m *JWTManager) GenerateToken(tenantID uuid.UUID, tenantName string) (string, error) {
	now := time.Now()
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.New().String(),
			Issuer:    m.config.Issuer,
			Subject:   tenantID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.config.Expiry)),
			NotBefore: jwt.NewNumericDate(now),
		},
		TenantID:   tenantID.String(),
		TenantName: tenantName,
	}

	token := jwt.NewWithClaims(m.config.SigningMethod, claims)
	return token.SignedString([]byte(m.config.Secret))
}

// GenerateTokenWithExpiry generates a JWT token with a custom expiry duration
func (m *JWTManager) GenerateTokenWithExpiry(tenantID uuid.UUID, tenantName string, expiry time.Duration) (string, error) {
	now := time.Now()
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.New().String(),
			Issuer:    m.config.Issuer,
			Subject:   tenantID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(expiry)),
			NotBefore: jwt.NewNumericDate(now),
		},
		TenantID:   tenantID.String(),
		TenantName: tenantName,
	}

	token := jwt.NewWithClaims(m.config.SigningMethod, claims)
	return token.SignedString([]byte(m.config.Secret))
}

// ValidateToken validates a JWT token and returns the claims
func (m *JWTManager) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		// Verify the signing method
		if token.Method.Alg() != m.config.SigningMethod.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(m.config.Secret), nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidClaims
	}

	return claims, nil
}

// GetTenantID extracts the tenant ID from claims
func (c *Claims) GetTenantID() (uuid.UUID, error) {
	return uuid.Parse(c.TenantID)
}

// RefreshToken creates a new token based on an existing valid token
func (m *JWTManager) RefreshToken(tokenString string) (string, error) {
	claims, err := m.ValidateToken(tokenString)
	if err != nil {
		// Allow refreshing tokens that are expired but otherwise valid
		if !errors.Is(err, ErrExpiredToken) {
			return "", err
		}
		// Re-parse to get the claims even if expired
		token, _ := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
			return []byte(m.config.Secret), nil
		}, jwt.WithoutClaimsValidation())
		if token == nil {
			return "", ErrInvalidToken
		}
		var ok bool
		claims, ok = token.Claims.(*Claims)
		if !ok {
			return "", ErrInvalidClaims
		}
	}

	tenantID, err := claims.GetTenantID()
	if err != nil {
		return "", fmt.Errorf("invalid tenant ID in claims: %w", err)
	}

	return m.GenerateToken(tenantID, claims.TenantName)
}

// TokenExpiry returns the expiry time of a token
func (m *JWTManager) TokenExpiry(tokenString string) (time.Time, error) {
	claims, err := m.ValidateToken(tokenString)
	if err != nil && !errors.Is(err, ErrExpiredToken) {
		return time.Time{}, err
	}

	// If validation failed due to expiry, re-parse without validation
	if errors.Is(err, ErrExpiredToken) {
		token, _ := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
			return []byte(m.config.Secret), nil
		}, jwt.WithoutClaimsValidation())
		if token == nil {
			return time.Time{}, ErrInvalidToken
		}
		var ok bool
		claims, ok = token.Claims.(*Claims)
		if !ok {
			return time.Time{}, ErrInvalidClaims
		}
	}

	if claims.ExpiresAt == nil {
		return time.Time{}, errors.New("token has no expiry")
	}

	return claims.ExpiresAt.Time, nil
}

// IsTokenExpired checks if a token is expired
func (m *JWTManager) IsTokenExpired(tokenString string) bool {
	expiry, err := m.TokenExpiry(tokenString)
	if err != nil {
		return true
	}
	return time.Now().After(expiry)
}
