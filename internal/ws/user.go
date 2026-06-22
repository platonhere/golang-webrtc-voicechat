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
	Conn        *websocket.Conn        // WebSocket соединение с клиентом; используется для обмена сигнальными сообщениями
	PC          *webrtc.PeerConnection // PeerConnection этого пользователя; через него проходит RTP-трафик (аудио/видео) и SDP-переговоры
	room        *Room
	writeMtx    sync.Mutex
	ready       atomic.Bool

	// outgoing хранит локальные TrackLocalStaticRTP для каждого источника
	// у одного источника может быть несколько треков: audio, video и т.д.
	// ключ: source user id + kind трека
	// значение: локальный трек получателя, в который приходят RTP-пакеты от отправителя
	outgoing map[string]*webrtc.TrackLocalStaticRTP
	outMtx   sync.RWMutex

	// защищает SDP-переговоры от race condition
	negotiationMtx sync.Mutex
	// повторные переговоры откладываются, если предыдущий offer ещё ждёт answer
	needsNegotiation  bool
	negotiationQueued atomic.Bool

	// закрытие выполняется только один раз
	closeOnce sync.Once
}

// NewUser создаёт объект User с временным UUID
// на этом этапе пользователь ещё не аутентифицирован
// после join handler перезаписывает u.ID значением из токена
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

// ReadPump слушает сообщения по WebSocket и обрабатывает сигнальные команды:
// - join (offer) — клиент отправил offer при первом join
// - candidate — ICE кандидат от клиента
// - answer — ответ клиента на offer сервера
// - leave — закрыть соединение
func (u *User) ReadPump() {
	defer u.Close()

	for {
		// чтение сообщения WebSocket
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
			// объединяем join + offer, потому что при первом подключении клиент сразу присылает offer
			// и сервер должен ответить answer. Если SDP есть и это offer, обрабатываем его.
			if msg.SDP != "" && msg.SDPType == "offer" {
				if err := u.ReceiveOfferAndAnswerBack(msg.SDP); err != nil {
					log.Println("error answering join offer:", err)
					return
				}
			}
		case "candidate":
			// ICE кандидаты от клиента приходят отдельными сообщениями
			var cand webrtc.ICECandidateInit
			if len(msg.Candidate) > 0 {
				if err := json.Unmarshal(msg.Candidate, &cand); err == nil {
					// проверяем, что PeerConnection уже создан
					if u.PC != nil {
						// добавляем кандидата в PeerConnection
						// после добавления ICE-агент будет пробовать установить соединение с этим кандидатом
						if err := u.PC.AddICECandidate(cand); err != nil {
							log.Println("AddICECandidate error:", err)
						}
					}
				}
			}
		case "answer":
			if msg.SDP != "" && msg.SDPType == "answer" {
				// если PeerConnection ещё не создан — ничего не делаем, логируем
				if u.PC == nil {
					log.Println("received answer but PC is nil")
					continue
				}

				// создаём объект SessionDescription с типом Answer
				// это SDP, которое клиент сформировал в ответ на наш offer
				sdp := webrtc.SessionDescription{
					Type: webrtc.SDPTypeAnswer,
					SDP:  msg.SDP,
				}

				// устанавливаем это описание как remote description в PeerConnection
				// после этого WebRTC знает, какие кодеки, форматы, ICE кандидаты использует клиент
				// теперь наш PeerConnection может начать отправлять и получать RTP/RTCP потоки
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

// ReceiveOfferAndAnswerBack создаёт PeerConnection, привязывает обработчики
// ICE кандидатов и OnTrack. Затем устанавливает remote offer, создаёт answer
// и отсылает его клиенту. Также OnTrack реплицирует потоки другим участникам.
func (u *User) ReceiveOfferAndAnswerBack(offerSDP string) error {
	// конфигурация PeerConnection: указываем ICE-серверы (STUN/TURN) для определения публичных адресов
	// и прохождения NAT, чтобы WebRTC мог установить соединение между клиентом и сервером.
	cfg := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}},
	}

	pc, err := webrtc.NewPeerConnection(cfg)
	if err != nil {
		return err
	}
	u.PC = pc

	// OnICECandidate — вызывается каждый раз, когда серверный PeerConnection находит новый ICE-кандидат.
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		// преобразуем ICE-кандидата в JSON для передачи по сигналингу
		cj := c.ToJSON()
		log.Printf("server ICE candidate: %+v\n", cj)
		// формируем сигналинговое сообщение для клиента
		m := SignalMessage{Type: "candidateFromServer"}
		// сериализуем ICE-кандидата в слайс байт для отправки клиенту
		raw, _ := json.Marshal(cj)
		m.Candidate = raw
		// отправляем ICE-кандидата клиенту по WebSocket
		// клиент добавит его в свой PeerConnection через AddICECandidate
		_ = u.writeSignal(m)
	})

	// когда приходит трек от этого пользователя — реплицируем его другим
	// -
	// OnTrack вызывается, когда сервер получает media track (TrackRemote) от конкретного пользователя (отправителя).
	// для каждого другого пользователя комнаты создаётся локальный трек (TrackLocalStaticRTP), который будет принимать RTP-пакеты от сервера.
	// этот локальный трек добавляется в PeerConnection получателя, чтобы клиент мог его слушать.
	// RTP пакеты из исходного TrackRemote читаются в цикле и пишутся во все локальные треки других пользователей — это фактическая пересылка аудио/видео.
	// Negotiate вызывается после добавления трека, чтобы инициировать SDP-переговоры и сообщить клиентам про новый трек.
	pc.OnTrack(func(remoteTrack *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		srcID := u.ID
		kind := remoteTrack.Kind().String()
		// логируем получение трека от конкретного пользователя
		log.Printf("OnTrack: got %s track from %s codec=%s\n", kind, srcID, remoteTrack.Codec().MimeType)

		if u.room != nil {
			// проходим по всем пользователям в комнате
			u.room.IterateUsers(func(other *User) {
				// не реплицируем трек обратно отправителю
				if other.ID == srcID {
					return
				}
				if !other.IsReady() {
					log.Printf("skip adding track for user %s: handshake not ready\n", other.ID)
					return
				}
				// если PeerConnection получателя ещё не готов - скип
				if other.PC == nil {
					log.Printf("skip adding track for user %s: PC not ready\n", other.ID)
					return
				}
				// создаём локальный трек для получателя, чтобы сервер мог писать в него RTP пакеты
				if _, created := other.ensureOutgoingTrack(srcID, remoteTrack); created {
					other.QueueNegotiation()
				}
			})
		}

		for {
			// читаем RTP пакет с удалённого трека отправителя
			pkt, _, err := remoteTrack.ReadRTP()
			if err != nil {
				log.Println("remoteTrack.ReadRTP:", err)
				return
			}
			// пересылаем пакет всем остальным участникам комнаты
			if u.room != nil {
				u.room.IterateUsers(func(dest *User) {
					// кроме отправителя
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

					// если смогли взять трек, пишем в него rtp-пакеты
					if tr != nil {
						if writeErr := tr.WriteRTP(pkt); writeErr != nil {
							log.Println("WriteRTP error:", writeErr)
						}
					}
				})
			}
		}
	})

	// преобразуем offer клиента в SessionDescription и ставим как remote description
	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offerSDP, // SDP клиента с его кодеками, треками и ICE

	}

	// устанавливаем remote description на серверной PeerConnection чтобы, PC знал треки, кодеки и ICE-кандидаты
	if err := pc.SetRemoteDescription(offer); err != nil {
		return err
	}

	// создаём ответ сервера (answer) и ставим как локальное описание
	// теперь сервер знает, какие треки/кодеки/ICE он предлагает клиенту
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return err
	}

	// устанавливаем локальное описание на сервере — answer
	// теперь сервер знает, какие треки/кодеки/ICE он предлагает клиенту
	if err := pc.SetLocalDescription(answer); err != nil {
		return err
	}

	// ждём, пока ICE-агент соберёт все локальные кандидаты для PeerConnection
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	<-gatherComplete

	/// берем локальное описание (answer + локальные ICE кандидаты) для отправки клиенту через WebSocket
	local := pc.LocalDescription()
	resp := SignalMessage{
		Type:    "answer",
		SDP:     local.SDP,
		SDPType: local.Type.String(),
	}
	// отправляем клиенту answer через WebSocket
	// после этого клиент сможет установить remote description и начать передачу аудио/видео
	if err := u.writeSignal(resp); err != nil {
		return err
	}
	u.MarkReady()
	return nil
}

// Negotiate запускает SDP-переговоры с клиентом.
// вызывается, когда на серверной PeerConnection меняется набор треков (добавили, удалили итд)
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

	// создаём SDP offer — описание текущего состояния PeerConnection:
	// какие треки, кодеки и направления передачи сервер предлагает клиенту
	offer, err := u.PC.CreateOffer(nil)
	if err != nil {
		log.Println("CreateOffer:", err)
		return
	}

	// устанавливаем offer как LocalDescription.
	// этим мы фиксируем состояние PeerConnection и запускаем ICE-gathering
	if err := u.PC.SetLocalDescription(offer); err != nil {
		log.Println("SetLocalDescription:", err)
		return
	}

	// ожидаем завершения ICE gathering,
	// чтобы LocalDescription содержал собранные ICE-кандидаты
	gatherComplete := webrtc.GatheringCompletePromise(u.PC)
	<-gatherComplete
	local := u.PC.LocalDescription()

	// отправляем offer клиенту через signaling (WebSocket)
	msg := SignalMessage{
		Type:    "offer",
		SDP:     local.SDP,
		SDPType: local.Type.String(),
	}
	if err := u.writeSignal(msg); err != nil {
		log.Println("send offer:", err)
	}
}

// Close аккуратно закрывает ресурсы: удаляет пользователя из комнаты,
// закрывает PeerConnection и WebSocket. Выполняется один раз (closeOnce).
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
