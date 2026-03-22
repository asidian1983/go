package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the JWT payload. UserID is replicated in the standard Subject field.
type Claims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// Service handles JWT generation and validation.
type Service struct {
	secret []byte
	expiry time.Duration
}

// NewService creates a JWT service.
// Returns an error if secret is shorter than 32 characters (NIST SP 800-117 minimum).
func NewService(secret string, expiry time.Duration) (*Service, error) {
	if len(secret) < 32 {
		return nil, errors.New("jwt: secret must be at least 32 characters")
	}
	return &Service{secret: []byte(secret), expiry: expiry}, nil
}

// Generate issues a signed HS256 JWT for the given user.
func (s *Service) Generate(userID, username string) (string, error) {
	now := time.Now()
	claims := Claims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.expiry)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
}

// Validate parses and validates a JWT string.
// Rejects tokens with unexpected signing algorithms to prevent alg-confusion attacks.
func (s *Service) Validate(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(
		tokenStr,
		&Claims{},
		func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("jwt: unexpected signing method %v", t.Header["alg"])
			}
			return s.secret, nil
		},
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("jwt: invalid token")
	}
	return claims, nil
}
