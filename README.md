# WebRTC internal architecture

## ICE candidates

ICE кандидаты — это все возможные сетевые пути для установления соединения `PeerConnection`:

- локальные IP (**host candidates**)
- публичные адреса через NAT (**srflx candidates**)
- маршруты через TURN сервер (**relay candidates**)

При создании `PeerConnection` его **ICE Agent**:

- собирает локальные кандидаты  
- при необходимости обращается к STUN / TURN серверам для получения внешних маршрутов

Собранные ICE кандидаты:

- передаются другой стороне через **сигналинг**
- добавляются на принимающей стороне с помощью `AddICECandidate`

ICE Agent перебирает пары кандидатов (**connectivity checks**)  
и выбирает оптимальный сетевой маршрут между сторонами `PeerConnection`.

---

## SDP negotiation

SDP-переговоры описывают, **как стороны будут участвовать в соединении**.

1)Клиент (**Sender**) создаёт `offer` — описывает, что он будет отправлять аудио.

Сервер принимает `offer` и выполняет `SetRemoteDescription(offer)` —  
сервер знает намерения клиента, но аудио ещё не идёт.

2)Сервер создаёт `answer` (`CreateAnswer`) — это **Local Description сервера**,  
где указано, как сервер будет участвовать в соединении.

Сервер выполняет `SetLocalDescription(answer)`.

3)Сервер отправляет `answer` клиенту — клиент устанавливает её как `Remote Description`.

SDP-переговоры завершены, `PeerConnection` установлен.

---

## Tracks on server

**TrackRemote** принадлежит отправителю (**src**).

На сервере для каждого отправителя существует один `TrackRemote`,  
который принимает его входящий аудиопоток.

**TrackLocalStaticRTP** создаётся сервером для каждого получателя (**dest**).

Для одного отправителя сервер:

- создаёт отдельный `TrackLocalStaticRTP` для каждого получателя
- хранит их в `User.outgoing[srcID]` и через них пересылает аудио другим пользователям

---

## RTP forwarding

Когда сервер начинает получать RTP-поток от отправителя в `TrackRemote`:

- создаёт локальные треки (`TrackLocalStaticRTP`) для других пользователей (**Receivers**), чтобы пересылать им аудио Sender’а.

После этого аудиопоток идёт:

- клиент получает треки от сервера
- сервер пересылает трек Sender’а всем другим Receivers

---

## Схема SDP-переговоров и запуска RTP-потока 
Клиент: createOffer().
    
Сервер:
  SetRemoteDescription(offer), CreateAnswer(), SetLocalDescription(answer).
    
Клиент: SetRemoteDescription(answer).
    
  (соединение установлено).    
Клиент начинает слать RTP.
        
Сервер: OnTrack() :
- читает RTP
- создаёт TrackLocal для других
- AddTrack()
- renegotiation
