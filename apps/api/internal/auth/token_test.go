package auth_test

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"paperless-api/internal/auth"
)

const testSecret = "test-secret-for-unit-tests"

func TestIssueAndParseAccessToken(t *testing.T) {
	roles := []string{"signer", "auditor"}
	tok, err := auth.IssueAccessToken(testSecret, 42, "alice", roles)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	claims, err := auth.ParseAccessToken(testSecret, tok)
	if err != nil {
		t.Fatalf("ParseAccessToken: %v", err)
	}
	if claims.UserID != 42 {
		t.Errorf("UserID got %d, want 42", claims.UserID)
	}
	if claims.Username != "alice" {
		t.Errorf("Username got %q, want alice", claims.Username)
	}
	if len(claims.Roles) != 2 {
		t.Errorf("Roles len got %d, want 2", len(claims.Roles))
	}
}

func TestParseAccessToken_WrongSecret(t *testing.T) {
	tok, _ := auth.IssueAccessToken(testSecret, 1, "bob", nil)
	_, err := auth.ParseAccessToken("wrong-secret", tok)
	if err == nil {
		t.Fatal("expected error for wrong secret, got nil")
	}
}

func TestParseAccessToken_Expired(t *testing.T) {
	// Forge an already-expired token directly.
	now := time.Now().Add(-time.Hour)
	claims := auth.Claims{
		UserID:   1,
		Username: "expired-user",
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := tok.SignedString([]byte(testSecret))

	_, err := auth.ParseAccessToken(testSecret, signed)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
}

func TestCheckPassword(t *testing.T) {
	hash, err := auth.HashPassword("mypassword")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !auth.CheckPassword("mypassword", hash) {
		t.Error("CheckPassword: expected true for correct password")
	}
	if auth.CheckPassword("wrongpassword", hash) {
		t.Error("CheckPassword: expected false for wrong password")
	}
}

func TestGenerateRefreshToken(t *testing.T) {
	a, err := auth.GenerateRefreshToken()
	if err != nil {
		t.Fatalf("GenerateRefreshToken: %v", err)
	}
	b, _ := auth.GenerateRefreshToken()
	if a == b {
		t.Error("two refresh tokens should not be equal")
	}
	if len(a) != 64 {
		t.Errorf("expected 64 hex chars, got %d", len(a))
	}
}
