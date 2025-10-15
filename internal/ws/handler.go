package ws

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

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
	}

	room := GetOrCreateRoom(msg.Room)

	user, err := NewUser(conn, room)
	if err != nil {
		log.Println("NewUser error:", err)
		_ = conn.Close()
		return
	}

	if msg.DisplayName != "" {
		user.DisplayName = msg.DisplayName
	}

	room.AddUser(user)

	if msg.SDP != "" && msg.SDPType == "offer" {
		if err := user.ReceiveOfferAndAnswerBack(msg.SDP); err != nil {
			log.Println("handle initial offer:", err)
			user.Close()
			return
		}
	}

	go user.ReadPump()

}
