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
// рекомендуется проверять Origin или полагаться на авторизацию
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// SignalMessage — структура сигнального сообщения, используемого для обмена данными
// WebRTC между клиентами через сервер (join, offer, answer, candidate, leave)
type SignalMessage struct {
	Type        string          `json:"type"`
	Room        string          `json:"room,omitempty"`
	From        string          `json:"from,omitempty"`
	To          string          `json:"to,omitempty"`
	SDP         string          `json:"sdp,omitempty"`
	SDPType     string          `json:"sdpType,omitempty"`
	Candidate   json.RawMessage `json:"candidate,omitempty"`
	DisplayName string          `json:"displayName,omitempty"`
	Token       string          `json:"token,omitempty"`
}

func HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("ws upgrade:", err)
		return
	}

	_, raw, err := conn.ReadMessage()
	if err != nil {
		log.Println("read initial ws:", err)
		_ = conn.Close()
		return
	}

	var msg SignalMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		log.Println("invalid initial msg:", err)
		_ = conn.Close()
		return
	}

	if msg.Type != "join" || msg.Room == "" {
		log.Println("first message must be join with non-empty room")
		_ = conn.Close()
		return
	}

	if msg.Token == "" {
		log.Println("join without token: unauthorized")
		_ = conn.Close()
		return
	}

	uid, _, err := auth.ParseToken(msg.Token)
	if err != nil {
		log.Println("invalid token:", err)
		_ = conn.Close()
		return
	}

	prof, err := store.GetUserByID(r.Context(), uid)
	if err != nil || prof == nil {
		log.Println("user not found for token")
		_ = conn.Close()
		return
	}

	room := GetOrCreateRoom(msg.Room)
	user := NewUser(conn, room)
	user.DisplayName = prof.DisplayName
	user.ID = uid

	if msg.SDP != "" && msg.SDPType == "offer" {
		if err := user.ReceiveOfferAndAnswerBack(msg.SDP); err != nil {
			log.Println("handle initial offer:", err)
			user.Close()
			return
		}
	}

	// Join the room after the initial answer, so peers do not negotiate with a half-open connection.
	if room.HasUser(uid) {
		log.Printf("blocked: user %q (id=%s) already in room %s\n", prof.DisplayName, uid, msg.Room)
		user.Close()
		return
	}
	if !room.AddUser(user) {
		log.Printf("blocked after race: user %q (id=%s) already in room %s\n", prof.DisplayName, uid, msg.Room)
		user.Close()
		return
	}
	log.Printf("allowed: user %q (id=%s) joining room %s\n", prof.DisplayName, uid, msg.Room)

	go user.ReadPump()
}
