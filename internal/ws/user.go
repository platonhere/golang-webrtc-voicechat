package ws

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

type User struct {
	ID          string
	DisplayName string
	Conn        *websocket.Conn
	PC          *webrtc.PeerConnection
	room        *Room

	outgoing map[string]*webrtc.TrackLocalStaticRTP
	outMtx   sync.RWMutex

	closeOnce sync.Once
}

func NewUser(conn *websocket.Conn, room *Room) (*User, error) {
	u := &User{
		ID:       uuid.New().String(),
		Conn:     conn,
		outgoing: make(map[string]*webrtc.TrackLocalStaticRTP),
	}
	return u, nil
}

func (u *User) ReadPump() {
	defer u.Close()

	for {
		_, raw, err := u.Conn.ReadMessage()
		if err != nil {
			log.Println("ws read:", err)
			return
		}
		var msg SignalMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Println("invalid signal json:", err)
			continue
		}
		switch msg.Type {
		case "join":
			if msg.SDP != "" && msg.SDPType == "offer" {
				if err := u.ReceiveOfferAndAnswerBack(msg.SDP); err != nil {
					log.Println("error answering join offer:", err)
					return
				}
			}
		case "candidate":
			var cand webrtc.ICECandidateInit
			if len(msg.Candidate) > 0 {
				if err := json.Unmarshal(msg.Candidate, &cand); err == nil {
					if u.PC != nil {
						if err := u.PC.AddICECandidate(cand); err != nil {
							log.Println("AddICECandidate error:", err)
						}
					}
				}
			}
		case "answer":
			if msg.SDP != "" && msg.SDPType == "answer" {
				if u.PC == nil {
					log.Println("received answer but PC is nil")
					continue
				}
				sdp := webrtc.SessionDescription{
					Type: webrtc.SDPTypeAnswer,
					SDP:  msg.SDP,
				}
				if err := u.PC.SetRemoteDescription(sdp); err != nil {
					log.Println("SetRemoteDescription answer:", err)
				}
			}
		case "leave":
			return
		default:
			log.Println("unknown msg type:", msg.Type)
		}
	}
}

func (u *User) ReceiveOfferAndAnswerBack(offerSDP string) error {
	cfg := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}
	pc, err := webrtc.NewPeerConnection(cfg)
	if err != nil {
		return err
	}
	u.PC = pc

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		cj := c.ToJSON()
		m := SignalMessage{Type: "candidateFromServer"}
		raw, _ := json.Marshal(cj)
		m.Candidate = raw
		_ = u.Conn.WriteJSON(m)
	})

	pc.OnTrack(func(remoteTrack *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		srcID := u.ID
		log.Printf("OnTrack: got track from %s codec=%s\n", srcID, remoteTrack.Codec().MimeType)

		if u.room != nil {
			u.room.IterateUsers(func(other *User) {
				if other.ID == srcID {
					return
				}
				cap := remoteTrack.Codec().RTPCodecCapability
				localTrack, err := webrtc.NewTrackLocalStaticRTP(cap, "audio", srcID)
				if err != nil {
					log.Println("create track local:", err)
					return
				}
				other.outMtx.Lock()
				other.outgoing[srcID] = localTrack
				other.outMtx.Unlock()

				if _, err := other.PC.AddTrack(localTrack); err != nil {
					log.Println("other.PC.AddTrack error:", err)
				}
				go other.Negotiate()
			})
		}

		for {
			pkt, _, err := remoteTrack.ReadRTP()
			if err != nil {
				log.Println("remoteTrack.ReadRTP:", err)
				return
			}
			if u.room != nil {
				u.room.IterateUsers(func(dest *User) {
					if dest.ID == srcID {
						return
					}
					dest.outMtx.RLock()
					tr := dest.outgoing[srcID]
					dest.outMtx.RUnlock()
					if tr != nil {
						if writeErr := tr.WriteRTP(pkt); writeErr != nil {
							log.Println("WriteRTP error:", writeErr)
						}
					}
				})
			}
		}
	})

	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offerSDP}
	if err := pc.SetRemoteDescription(offer); err != nil {
		return err
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return err
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		return err
	}

	gatherComplete := webrtc.GatheringCompletePromise(pc)
	<-gatherComplete

	local := pc.LocalDescription()
	resp := SignalMessage{
		Type:    "answer",
		SDP:     local.SDP,
		SDPType: local.Type.String(),
	}
	if err := u.Conn.WriteJSON(resp); err != nil {
		return err
	}
	return nil
}

func (u *User) Negotiate() {
	if u.PC == nil {
		return
	}
	offer, err := u.PC.CreateOffer(nil)
	if err != nil {
		log.Println("CreateOffer:", err)
		return
	}
	if err := u.PC.SetLocalDescription(offer); err != nil {
		log.Println("SetLocalDescription:", err)
		return
	}
	gatherComplete := webrtc.GatheringCompletePromise(u.PC)
	<-gatherComplete
	local := u.PC.LocalDescription()
	msg := SignalMessage{
		Type:    "offer",
		SDP:     local.SDP,
		SDPType: local.Type.String(),
	}
	if err := u.Conn.WriteJSON(msg); err != nil {
		log.Println("send offer:", err)
	}
}

func (u *User) Close() {
	u.closeOnce.Do(func() {
		log.Println("closing user", u.ID)
		if u.room != nil {
			u.room.RemoveUser(u)
		}
		if u.PC != nil {
			_ = u.PC.Close()
		}
		if u.Conn != nil {
			_ = u.Conn.Close()
		}
	})
}
