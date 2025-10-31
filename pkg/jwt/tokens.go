package jwt

import (
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

// Claims defines JWT payload.
type Claims struct {
	UserID string `json:"user_id"`
	TeamID string `json:"team_id,omitempty"`
	jwtlib.RegisteredClaims
}

// GenerateToken issues a signed JWT with provided secret and ttl.
func GenerateToken(userID, teamID, secret string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: userID,
		TeamID: teamID,
		RegisteredClaims: jwtlib.RegisteredClaims{
			Issuer:    "peep",
			IssuedAt:  jwtlib.NewNumericDate(now),
			ExpiresAt: jwtlib.NewNumericDate(now.Add(ttl)),
		},
	}
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// Parse validates and extracts claims from token.
func Parse(token string, secret string) (*Claims, error) {
	parsed, err := jwtlib.ParseWithClaims(token, &Claims{}, func(t *jwtlib.Token) (interface{}, error) {
		return []byte(secret), nil
	}, jwtlib.WithValidMethods([]string{jwtlib.SigningMethodHS256.Name}))
	if err != nil {
		return nil, err
	}
	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, jwtlib.ErrTokenInvalidClaims
	}
	return claims, nil
}
