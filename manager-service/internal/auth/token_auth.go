package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type TokenAuthenticator struct {
	issuer     string
	secretKey  []byte
	expiration time.Duration
}

type Claims struct {
	UserID string `json:"user_id"`
	jwt.RegisteredClaims
}

func NewTokenAuthenticator(issuer string, secretKey []byte, expiration time.Duration) *TokenAuthenticator {
	return &TokenAuthenticator{
		issuer:     issuer,
		secretKey:  secretKey,
		expiration: expiration,
	}
}

func (t *TokenAuthenticator) GenerateToken(userID string) (string, error) {
	sessionID := generateSessionID()

	now := time.Now()
	claims := &Claims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    t.issuer,
			Subject:   userID,
			ID:        sessionID,
			ExpiresAt: jwt.NewNumericDate(now.Add(t.expiration)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(t.secretKey)
}

func (t *TokenAuthenticator) ValidateToken(tokenString string) (*UserContext, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return t.secretKey, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	return &UserContext{
		UserID:    claims.UserID,
		SessionID: claims.ID,
		ExpiresAt: claims.ExpiresAt.Time,
		CreatedAt: claims.IssuedAt.Time,
	}, nil
}

func generateSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}
