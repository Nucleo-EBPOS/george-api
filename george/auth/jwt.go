package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims do access token do George.
type Claims struct {
	UserID uint   `json:"uid"`
	OrgID  uint   `json:"org"`
	Role   string `json:"role"`
	IsOps  bool   `json:"ops,omitempty"`
	Type   string `json:"typ"` // access
	jwt.RegisteredClaims
}

// Signer assina/valida access tokens HS256.
type Signer struct {
	key []byte
	ttl time.Duration
}

// NewSigner cria o assinador.
func NewSigner(secret string, ttl time.Duration) *Signer {
	return &Signer{key: []byte(secret), ttl: ttl}
}

// TTL do access token.
func (s *Signer) TTL() time.Duration { return s.ttl }

// Access emite um access token.
func (s *Signer) Access(userID, orgID uint, role string, isOps bool) (string, error) {
	if len(s.key) == 0 {
		return "", errors.New("GEORGE_JWT_SECRET não configurado")
	}
	now := time.Now()
	jti, err := RandomToken(8)
	if err != nil {
		return "", err
	}
	c := Claims{
		UserID: userID, OrgID: orgID, Role: role, IsOps: isOps, Type: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.ttl)),
			Issuer:    "george-api",
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.key)
}

// Parse valida um access token.
func (s *Signer) Parse(token string) (*Claims, error) {
	c := &Claims{}
	t, err := jwt.ParseWithClaims(token, c, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("método de assinatura inválido")
		}
		return s.key, nil
	}, jwt.WithIssuer("george-api"))
	if err != nil {
		return nil, err
	}
	if !t.Valid || c.Type != "access" || c.UserID == 0 {
		return nil, errors.New("token inválido")
	}
	return c, nil
}
