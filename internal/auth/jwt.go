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
	jwtSecret = []byte(s) // сохраняем секрет как массив байт для использования при подписи
}

func GenerateToken(userID, username string, ttl time.Duration) (string, error) {
	// claims - payload JWT токена, содержащий данные о пользователе и время жизни токена
	claims := jwt.MapClaims{
		"sub":  userID,
		"name": username,
		"exp":  time.Now().Add(ttl).Unix(),
	}
	// создаем новый токен с использованием алгоритка подписи HMAC с HS256 и claims
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	// tok — это объект токена, который ещё не подписан, но содержит все данные и выбранный метод подписи.
	return tok.SignedString(jwtSecret) // подписываем токен с использованием секрета и возвращаем строку токена
}

func ParseToken(tokenStr string) (userID string, username string, err error) {
	// парсим jwt токен, представленный в виде строки из tokenStr
	// Разбираем header и payload (claims) и проверяем подпись с использованием callback-функции, которая возвр. jwtSecret
	// проверка токена с помощью jwtSecret происходит внутри библеатеки jwt, в jwt.Parse
	tok, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		// проверяем, что алгоритм подписи HMAC (SHA-256)
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrTokenUnverifiable
		}
		// Возвращаем секретный ключ для проверки подписи токена (на уровне библиотеки jwt)
		return jwtSecret, nil
	})
	if err != nil {
		// ошибка парсинга или валидации подписи
		return "", "", err
	}
	if !tok.Valid {
		// токен разобран, но подпись не прошла валидацию или истек срок
		return "", "", jwt.ErrTokenInvalidClaims
	}
	// m - payload токена, представляем его в виде MapClaims (ключ-значение)
	// по ключам извлекаем значения и приводим их к string
	if m, ok := tok.Claims.(jwt.MapClaims); ok {
		sub, _ := m["sub"].(string)
		name, _ := m["name"].(string)
		return sub, name, nil // возвращаем ID пользователя и username из payload токена
	}

	// возвращаем ошибку, если claims не удалось привести к jwt.MapClaims
	// или если payload некорректный / отсутствуют нужные поля
	return "", "", jwt.ErrTokenInvalidClaims
}
