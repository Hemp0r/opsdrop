package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// Claims represents the JWT payload.
type Claims struct {
	UserID   int64  `json:"uid"`
	Username string `json:"uname"`
	jwt.RegisteredClaims
}

// preHash applies SHA-256 before bcrypt to avoid the 72-byte password truncation.
func preHash(password string) string {
	h := sha256.Sum256([]byte(password))
	return base64.StdEncoding.EncodeToString(h[:])
}

// HashPassword hashes the supplied plain text password.
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(preHash(password)), bcrypt.DefaultCost)
	return string(bytes), err
}

// CheckPassword validates a password against the stored hash.
// It tries the pre-hashed format first and falls back to legacy direct comparison
// for backward compatibility with existing password hashes.
func CheckPassword(hash, password string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(preHash(password))); err == nil {
		return nil
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

// GenerateToken produces a JWT string signed with the provided secret.
func GenerateToken(userID int64, username string, secret []byte, ttl time.Duration) (string, error) {
	claims := Claims{
		UserID:   userID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "opsdrop",
			Subject:   username,
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
			ExpiresAt: jwt.NewNumericDate(time.Now().UTC().Add(ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// ParseToken validates the JWT and returns its claims.
func ParseToken(token string, secret []byte) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(token, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := parsed.Claims.(*Claims); ok && parsed.Valid {
		return claims, nil
	}
	return nil, errors.New("invalid token")
}
