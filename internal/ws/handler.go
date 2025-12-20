package ws

import (
	"encoding/json"
	"log"
	"net/http"

	"voicechat/internal/auth"
	"voicechat/internal/store"

	"github.com/gorilla/websocket"
)

// Upgrader используется для повышения HTTP-соединения до WebSocket.
// !!! ВНИМАНИЕ: CheckOrigin сейчас всегда возвращает true — это небезопасно в продакшене.
// рекомендуется проверять Origin или полагаться на авторизацию, чтобы предотвратить CSRF.
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// SignalMessage — структура сигнального сообщения, используемого для обмена данными
// WebRTC между клиентами через сервер (join, offer, answer, candidate, leave).
// поля отражают минимальный набор данных, передаваемых в JSON-пакете.
type SignalMessage struct {
	Type        string          `json:"type"`           // "join","offer","answer","candidate","leave"
	Room        string          `json:"room,omitempty"` // room id (for join)
	From        string          `json:"from,omitempty"` // user id (optional)
	To          string          `json:"to,omitempty"`   // target user id (optional)
	SDP         string          `json:"sdp,omitempty"`
	SDPType     string          `json:"sdpType,omitempty"`     // "offer"/"answer"
	Candidate   json.RawMessage `json:"candidate,omitempty"`   // ICE candidate object (passed through)
	DisplayName string          `json:"displayName,omitempty"` // optional nicename
	Token       string          `json:"token,omitempty"`
}

// HandleWebSocket апгрейдит HTTP-соединение до WebSocket, выполняет аутентификацию
// по токену, добавляет пользователя в указанную комнату и запускает обработку
// сигнальных сообщений. Первый пакет от клиента должен быть типа "join" и
// содержать `room` и `token` — они используются для проверки и идентификации.
// если в первом сообщении присутствует SDP-офер, сервер попытается сразу ответить.
func HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// апгрейдим соединение до WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("ws upgrade:", err)
		return
	}

	// читаем первое сообщение — оно должно быть join-сообщением
	_, raw, err := conn.ReadMessage()
	if err != nil {
		log.Println("read initial ws:", err)
		_ = conn.Close()
		return
	}

	// парсим JSON первого сообщения в структуру сигналинга
	var msg SignalMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		log.Println("invalid initial msg:", err)
		_ = conn.Close()
		return
	}

	// проверяем, что первое сообщение join и комната указана
	if msg.Type != "join" || msg.Room == "" {
		log.Println("first message must be join with non-empty room")
		_ = conn.Close()
		return
	}

	// проверяем, что клиент передал JWT-токен
	if msg.Token == "" {
		log.Println("join without token: unauthorized")
		_ = conn.Close()
		return
	}

	// валидируем JWT и извлекаем userID
	uid, _, err := auth.ParseToken(msg.Token)
	if err != nil {
		log.Println("invalid token:", err)
		_ = conn.Close()
		return
	}

	// загружаем профиль/запись пользователя из БД, полученная по userID, который мы извлекли из JWT-токена.
	prof, err := store.GetUserByID(r.Context(), uid)
	if err != nil || prof == nil {
		log.Println("user not found for token")
		_ = conn.Close()
		return
	}

	// получаем существующую комнату или создаём новую
	room := GetOrCreateRoom(msg.Room)

	// проверяем, что пользователь ещё не подключён к этой комнате
	if room.HasUser(uid) {
		log.Printf("❌ BLOCKED: user \"%s\" (id=%s) already in room %s\n", prof.DisplayName, uid, msg.Room)
		_ = conn.Close()
		return
	}

	// создаём объект пользователя, привязанный к WebSocket и комнате
	user := NewUser(conn, room)

	// устанавливаем отображаемое имя из профиля в БД
	user.DisplayName = prof.DisplayName
	// используем ID пользователя из JWT как идентификатор подключения
	user.ID = uid

	// перед добавлением пользователя в комнату проверяем, есть ли он там, предотвращая гонку
	if !room.AddUser(user) {
		log.Printf("❌ BLOCKED (race): user \"%s\" (id=%s) already in room %s\n", prof.DisplayName, uid, msg.Room)
		_ = conn.Close()
		return
	}
	log.Printf("✅ ALLOWED: user \"%s\" (id=%s) joining room %s\n", prof.DisplayName, uid, msg.Room)

	// если клиент сразу прислал SDP offer — принимаем его и отправляем answer
	if msg.SDP != "" && msg.SDPType == "offer" {
		if err := user.ReceiveOfferAndAnswerBack(msg.SDP); err != nil {
			log.Println("handle initial offer:", err)
			user.Close()
			return
		}
	}

	// запускаем горутину для чтения сообщений от клиента
	go user.ReadPump()
}
