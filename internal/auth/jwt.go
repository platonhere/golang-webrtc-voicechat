package auth

import (
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var jwtSecret []byte

func Init() {
	// пробуем взять jwt секрет из переменной окружения VOICECHAT_JWT_SECRET
	// если секрета нет - используем дефолтный для разработки
	s := os.Getenv("VOICECHAT_JWT_SECRET")
	if s == "" {
		s = "dev-secret-do-not-use-in-prod"
	}
	jwtSecret = []byte(s)
}

func GenerateToken(userID, username string, ttl time.Duration) (string, error) {
	claims := jwt.MapClaims{
		"sub":  userID,
		"name": username,
		"exp":  time.Now().Add(ttl).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(jwtSecret)
}

func ParseToken(tokenStr string) (userID string, username string, err error) {
	tok, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrTokenUnverifiable
		}
		return jwtSecret, nil
	})
	if err != nil {
		return "", "", err
	}
	if !tok.Valid {
		return "", "", jwt.ErrTokenInvalidClaims
	}
	if m, ok := tok.Claims.(jwt.MapClaims); ok {
		sub, _ := m["sub"].(string)
		name, _ := m["name"].(string)
		return sub, name, nil
	}
	return "", "", jwt.ErrTokenInvalidClaims
}
