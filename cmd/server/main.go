package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"

	"voicechat/internal/auth"
	"voicechat/internal/store"
	"voicechat/internal/ws"

	"github.com/gorilla/mux"
)

func main() {
	ctx := context.Background()
	// инициализируем бд postgres, создаем подключение и саму таблицу если -> not exists
	if err := store.Init(ctx); err != nil {
		log.Fatal("store init:", err)
	}

	// инициализация jwt-секрета
	auth.Init()

	// инициализация маршрутизатора
	r := mux.NewRouter()

	// регистрируем POST-эндпоинт для регистрации пользователя
	r.HandleFunc("/api/register", func(w http.ResponseWriter, r *http.Request) {
		// декодируем JSON из тела запроса в локальную структуру
		var req struct{ Username, Password string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		if req.Username == "" || req.Password == "" {
			http.Error(w, "empty", http.StatusBadRequest)
			return
		}

		// генерируем уникальный UUID для идентификатора пользователя
		id := uuid.New().String()
		// создаем пользователя в БД
		if err := store.CreateUser(r.Context(), id, req.Username, req.Password, req.Username); err != nil {

			// проверяем, не является ли это ошибкой дублирования имени юзера
			if errors.Is(err, store.ErrDuplicateUsername) {
				http.Error(w, "username already exists", http.StatusConflict)
				return
			}
			// любая другая ошибка - возвращаем 500 Internal Server Error
			http.Error(w, "create user error", http.StatusInternalServerError)
			return
		}

		// если все прошло успешно, возвращаем 201 Created
		w.WriteHeader(http.StatusCreated)
	}).Methods("POST")

	// регистрируем POST-эндпоинт для входа пользователя
	r.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		// декодируем JSON с username и password из тела запроса в локальную структуру
		var req struct{ Username, Password string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		// проверяем username и password через store.Authenticate в БД
		u, err := store.Authenticate(r.Context(), req.Username, req.Password)
		if err != nil || u == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// генерируем JWT токен с временем жизни 24 часа
		tok, err := auth.GenerateToken(u.ID, u.Username, 24*time.Hour)
		if err != nil {
			http.Error(w, "token error", http.StatusInternalServerError)
			return
		}
		// возвращаем токен клиенту в JSON формате
		_ = json.NewEncoder(w).Encode(map[string]string{"token": tok})
	}).Methods("POST")

	// регистрируем GET-эндпоинт для получения информации о текущем пользователе
	r.HandleFunc("/api/me", func(w http.ResponseWriter, r *http.Request) {
		// получаем заголовок Authorization из запроса
		authz := r.Header.Get("Authorization")
		if authz == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// извлекаем токен из формата "Bearer <token>"
		var token string
		if n, _ := fmt.Sscanf(authz, "Bearer %s", &token); n != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// валидируем и парсим JWT токен, получаем ID пользователя
		uid, _, err := auth.ParseToken(token)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// получаем данные пользователя из БД по ID
		u, err := store.GetUserByID(r.Context(), uid)
		if err != nil || u == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		// возвращаем данные пользователя в JSON формате
		_ = json.NewEncoder(w).Encode(u)
	}).Methods("GET")

	// регистрируем WebSocket эндпоинт
	r.HandleFunc("/ws", ws.HandleWebSocket)
	// регистрируем статические файлы (HTML, CSS, JS) из папки static для всех остальных маршрутов
	r.PathPrefix("/").Handler(http.FileServer(http.Dir("static")))

	// Старт HTTP-сервера на порту 8080
	addr := ":8080"
	log.Printf("Starting server on %s\n", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatal(err)
	}
}
