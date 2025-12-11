package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Claims are the token payload.
type Claims struct {
	Sub    string `json:"sub"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	Scope  string `json:"scope,omitempty"`
	FileID string `json:"file_id,omitempty"`
	Exp    int64  `json:"exp"`
}

// Sign issues a compact JWT-like token using HS256.
func Sign(claims Claims, secret string) (string, error) {
	header := map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	}
	hb, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	pb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	hEnc := base64URLEncode(hb)
	pEnc := base64URLEncode(pb)
	sig := sign(hEnc+"."+pEnc, secret)
	return hEnc + "." + pEnc + "." + sig, nil
}

// Validate parses and validates a token.
func Validate(token, secret string) (Claims, error) {
	var c Claims
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return c, errors.New("invalid token format")
	}
	headerPayload := parts[0] + "." + parts[1]
	expectedSig := sign(headerPayload, secret)
	if !hmac.Equal([]byte(expectedSig), []byte(parts[2])) {
		return c, errors.New("signature mismatch")
	}
	payload, err := base64URLDecode(parts[1])
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		return c, err
	}
	if c.Exp == 0 || time.Now().UTC().Unix() > c.Exp {
		return c, errors.New("token expired")
	}
	return c, nil
}

func base64URLEncode(b []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(b), "=")
}

func base64URLDecode(s string) ([]byte, error) {
	// restore padding
	if m := len(s) % 4; m != 0 {
		s += strings.Repeat("=", 4-m)
	}
	return base64.URLEncoding.DecodeString(s)
}

func sign(data, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(data))
	return base64URLEncode(mac.Sum(nil))
}
