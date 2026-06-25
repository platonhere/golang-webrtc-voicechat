package ws

import (
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"
	"time"

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
	writeMtx    sync.Mutex
	ready       atomic.Bool

	outgoing          map[string]*webrtc.TrackLocalStaticRTP
	outMtx            sync.RWMutex
	negotiationMtx    sync.Mutex
	needsNegotiation  bool
	negotiationQueued atomic.Bool
	closeOnce         sync.Once
}

func NewUser(conn *websocket.Conn, room *Room) *User {
	u := &User{
		ID:       uuid.New().String(),
		Conn:     conn,
		outgoing: make(map[string]*webrtc.TrackLocalStaticRTP),
	}
	return u
}

func outgoingTrackKey(srcID string, kind webrtc.RTPCodecType) string {
	return srcID + ":" + kind.String()
}

func (u *User) writeSignal(msg interface{}) error {
	u.writeMtx.Lock()
	defer u.writeMtx.Unlock()
	return u.Conn.WriteJSON(msg)
}

func (u *User) IsReady() bool {
	return u.ready.Load()
}

func (u *User) MarkReady() {
	u.ready.Store(true)
}

func (u *User) QueueNegotiation() {
	if !u.negotiationQueued.CompareAndSwap(false, true) {
		return
	}

	go func() {
		time.Sleep(75 * time.Millisecond)
		u.negotiationQueued.Store(false)
		u.Negotiate()
	}()
}

func (u *User) ensureOutgoingTrack(srcID string, remoteTrack *webrtc.TrackRemote) (*webrtc.TrackLocalStaticRTP, bool) {
	if u.PC == nil {
		return nil, false
	}

	key := outgoingTrackKey(srcID, remoteTrack.Kind())
	u.outMtx.RLock()
	existing := u.outgoing[key]
	u.outMtx.RUnlock()
	if existing != nil {
		return existing, false
	}

	u.outMtx.Lock()
	defer u.outMtx.Unlock()
	if existing = u.outgoing[key]; existing != nil {
		return existing, false
	}

	kind := remoteTrack.Kind().String()
	cap := remoteTrack.Codec().RTPCodecCapability
	localTrack, err := webrtc.NewTrackLocalStaticRTP(cap, kind+"-"+srcID, srcID)
	if err != nil {
		log.Println("create track local:", err)
		return nil, false
	}

	sender, err := u.PC.AddTrack(localTrack)
	if err != nil {
		log.Println("PC.AddTrack error:", err)
		return nil, false
	}

	u.outgoing[key] = localTrack

	go func() {
		rtcpBuf := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(rtcpBuf); err != nil {
				return
			}
		}
	}()

	return localTrack, true
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

				u.negotiationMtx.Lock()
				err := u.PC.SetRemoteDescription(sdp)
				shouldRenegotiate := err == nil && u.needsNegotiation
				if shouldRenegotiate {
					u.needsNegotiation = false
				}
				u.negotiationMtx.Unlock()
				if err != nil {
					log.Println("SetRemoteDescription answer:", err)
				} else if shouldRenegotiate {
					go u.Negotiate()
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
		ICEServers: []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}},
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
		log.Printf("server ICE candidate: %+v\n", cj)
		m := SignalMessage{Type: "candidateFromServer"}
		raw, _ := json.Marshal(cj)
		m.Candidate = raw
		_ = u.writeSignal(m)
	})

	pc.OnTrack(func(remoteTrack *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		srcID := u.ID
		kind := remoteTrack.Kind().String()
		log.Printf("OnTrack: got %s track from %s codec=%s\n", kind, srcID, remoteTrack.Codec().MimeType)

		if u.room != nil {
			u.room.IterateUsers(func(other *User) {
				if other.ID == srcID {
					return
				}
				if !other.IsReady() {
					log.Printf("skip adding track for user %s: handshake not ready\n", other.ID)
					return
				}
				if other.PC == nil {
					log.Printf("skip adding track for user %s: PC not ready\n", other.ID)
					return
				}
				if _, created := other.ensureOutgoingTrack(srcID, remoteTrack); created {
					other.QueueNegotiation()
				}
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
					if !dest.IsReady() {
						return
					}

					tr, created := dest.ensureOutgoingTrack(srcID, remoteTrack)
					if created {
						dest.QueueNegotiation()
					}

					if tr != nil {
						if writeErr := tr.WriteRTP(pkt); writeErr != nil {
							log.Println("WriteRTP error:", writeErr)
						}
					}
				})
			}
		}
	})

	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offerSDP,
	}

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
	if err := u.writeSignal(resp); err != nil {
		return err
	}
	u.MarkReady()
	return nil
}

func (u *User) Negotiate() {
	if u.PC == nil {
		return
	}
	u.negotiationMtx.Lock()
	defer u.negotiationMtx.Unlock()

	if u.PC.SignalingState() != webrtc.SignalingStateStable {
		u.needsNegotiation = true
		log.Println("negotiation postponed, signaling state:", u.PC.SignalingState())
		return
	}
	u.needsNegotiation = false

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
	if err := u.writeSignal(msg); err != nil {
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
