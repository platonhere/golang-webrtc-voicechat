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
	Conn        *websocket.Conn        // WebSocket соединение с клиентом; используется для обмена сигнальными сообщениями
	PC          *webrtc.PeerConnection // PeerConnection этого пользователя; через него проходит весь RTP-трафик (аудио) и происходит SDP-переговоры
	room        *Room

	// outgoing хранит локальные TrackLocalStaticRTP для каждого источника
	// у одного источника - несколько треков, в которые он отправяет пакеты
	// ключ srcID - (id отправителя/источника)
	// значение - локальный трек получателя, в который приходит звук от отправителя (через сервер)
	outgoing map[string]*webrtc.TrackLocalStaticRTP
	outMtx   sync.RWMutex

	// защищает SDP-переговоры от race condition
	negotiationMtx sync.Mutex

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
		_ = u.Conn.WriteJSON(m)
	})

	// когда приходит трек от этого пользователя — реплицируем его другим
	// -
	// OnTrack вызывается, когда сервер получает аудиотрек (TrackRemote) от конкретного пользователя (отправителя).
	// для каждого другого пользователя комнаты создаётся локальный трек (TrackLocalStaticRTP), который будет принимать RTP-пакеты от сервера.
	// этот локальный трек добавляется в PeerConnection получателя, чтобы клиент мог его слушать.
	// RTP пакеты из исходного TrackRemote читаются в цикле и пишутся во все локальные треки других пользователей — это фактическая пересылка аудио.
	// Negotiate вызывается после добавления трека, чтобы инициировать SDP-переговоры и сообщить клиентам про новый трек.
	pc.OnTrack(func(remoteTrack *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		srcID := u.ID
		// логируем получение трека от конкретного пользователя
		log.Printf("OnTrack: got track from %s codec=%s\n", srcID, remoteTrack.Codec().MimeType)

		if u.room != nil {
			// проходим по всем пользователям в комнате
			u.room.IterateUsers(func(other *User) {
				// не реплицируем трек обратно отправителю
				if other.ID == srcID {
					return
				}
				// если PeerConnection получателя ещё не готов - скип
				if other.PC == nil {
					log.Printf("skip adding track for user %s: PC not ready\n", other.ID)
					return
				}
				// получаем параметры кодека удаленного трека, который прислал отправитель
				cap := remoteTrack.Codec().RTPCodecCapability
				// создаём локальный трек для получателя, чтобы сервер мог писать в него RTP пакеты
				localTrack, err := webrtc.NewTrackLocalStaticRTP(cap, "audio", srcID)
				if err != nil {
					log.Println("create track local:", err)
					return
				}
				// записываем в мапу лок.трек получателя для конкретного отправителя (srcID)
				other.outMtx.Lock()
				other.outgoing[srcID] = localTrack
				other.outMtx.Unlock()

				// добавляем трек в PeerConnection получателя
				if _, err := other.PC.AddTrack(localTrack); err != nil {
					log.Println("other.PC.AddTrack error:", err)
				}
				// в горутине инициируем повторную SDP re-negotiation с other, сервер создаёт offer, отправляет по WS, ждёт answer
				go other.Negotiate()
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

					// берем локальный трек получателя
					dest.outMtx.RLock()
					tr := dest.outgoing[srcID]
					dest.outMtx.RUnlock()

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
	// после этого клиент сможет установить remote description и начать передачу аудио
	if err := u.Conn.WriteJSON(resp); err != nil {
		return err
	}
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
	if err := u.Conn.WriteJSON(msg); err != nil {
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
