package main

import (
	"log"
	"context"
	"net/http"
	"time"
	"encoding/json"
	"github.com/google/uuid"
	"fmt"

	"voicechat/internal/ws"
	"voicechat/internal/store"
	"voicechat/internal/auth"

	"github.com/gorilla/mux"
)

func main() {
	ctx := context.Background()
	// init DB
	if err := store.Init(ctx); err != nil {
		log.Fatal("store init:", err)
	}
	auth.Init()

	r := mux.NewRouter()
	// auth endpoints
	r.HandleFunc("/api/register", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Username, Password string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		if req.Username == "" || req.Password == "" {
			http.Error(w, "empty", http.StatusBadRequest)
			return
		}
		id := uuid.New().String()
		if err := store.CreateUser(r.Context(), id, req.Username, req.Password, req.Username); err != nil {
			// Check if it's a duplicate username error
			if err.Error() == "pq: duplicate key value violates unique constraint \"users_username_key\"" {
				http.Error(w, "username already exists", http.StatusConflict)
				return
			}
			http.Error(w, "create user error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}).Methods("POST")

	r.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Username, Password string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		u, err := store.Authenticate(r.Context(), req.Username, req.Password)
		if err != nil || u == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		tok, err := auth.GenerateToken(u.ID, u.Username, 24*time.Hour)
		if err != nil {
			http.Error(w, "token error", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": tok})
	}).Methods("POST")

	r.HandleFunc("/api/me", func(w http.ResponseWriter, r *http.Request) {
		// simple bearer token
		authz := r.Header.Get("Authorization")
		if authz == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var token string
		if n, _ := fmt.Sscanf(authz, "Bearer %s", &token); n != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		uid, _, err := auth.ParseToken(token)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		u, err := store.GetUserByID(r.Context(), uid)
		if err != nil || u == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(u)
	}).Methods("GET")
	r.HandleFunc("/ws", ws.HandleWebSocket)
	r.PathPrefix("/").Handler(http.FileServer(http.Dir("static")))

	addr := ":8080"
	log.Printf("Starting server on %s\n", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatal(err)
	}
}
