package auth

import (
	"strings"
	"testing"
	"time"
)

func TestParseTokenAcceptsCanonicalToken(t *testing.T) {
	secret := []byte("test-only-secret-with-at-least-32-bytes")
	token, err := GenerateToken(42, "test-user", secret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ParseToken(token, secret)
	if err != nil {
		t.Fatalf("canonical token rejected: %v", err)
	}
	if claims.UserID != 42 || claims.Username != "test-user" {
		t.Fatalf("unexpected identity: %+v", claims)
	}
}

func TestParseTokenRejectsNonCanonicalSignature(t *testing.T) {
	secret := []byte("test-only-secret-with-at-least-32-bytes")
	token, err := GenerateToken(42, "test-user", secret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	// An HS256 signature is 32 bytes: its final base64url character has two
	// unused bits. Nonzero unused bits must not provide token-string aliases,
	// because revocation identifies tokens by a hash of their exact string.
	last := strings.IndexByte(alphabet, token[len(token)-1])
	if last < 0 || last%4 != 0 {
		t.Fatal("unexpected canonical signature encoding")
	}
	for offset := 1; offset <= 3; offset++ {
		alias := token[:len(token)-1] + string(alphabet[last+offset])
		if _, err := ParseToken(alias, secret); err == nil {
			t.Errorf("accepted noncanonical signature with unused-bit offset %d", offset)
		}
	}
}
