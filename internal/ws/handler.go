package ws

import (
	"encoding/json"
	"log"
	"net/http"

	"voicechat/internal/auth"
	"voicechat/internal/store"

	"github.com/gorilla/websocket"
)

// На проде это небезопасно; нужно проверять origin или внедрять auth.
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

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

	// require token and validate
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

	// prevent duplicate user in room
	if room.HasUser(uid) {
		log.Printf("❌ BLOCKED: user \"%s\" (id=%s) already in room %s\n", prof.DisplayName, uid, msg.Room)
		_ = conn.Close()
		return
	}

	user := NewUser(conn, room)

	// always use display_name from user profile (stored in DB)
	user.DisplayName = prof.DisplayName
	// use authenticated user id (from token) as connection ID so room membership is tracked by account
	user.ID = uid

	// Try to add user to room (double-check protection against race condition)
	if !room.AddUser(user) {
		log.Printf("❌ BLOCKED (race): user \"%s\" (id=%s) already in room %s\n", prof.DisplayName, uid, msg.Room)
		_ = conn.Close()
		return
	}
	log.Printf("✅ ALLOWED: user \"%s\" (id=%s) joining room %s\n", prof.DisplayName, uid, msg.Room)

	if msg.SDP != "" && msg.SDPType == "offer" {
		if err := user.ReceiveOfferAndAnswerBack(msg.SDP); err != nil {
			log.Println("handle initial offer:", err)
			user.Close()
			return
		}
	}

	go user.ReadPump()
}
