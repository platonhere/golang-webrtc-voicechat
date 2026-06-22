# VoiceChat Conference

WebRTC-приложение для голосовых и видеоконференций с авторизацией, комнатами, WebSocket signaling и серверной пересылкой media tracks через Pion.

Проект реализует минимальный backend для real-time конференций: регистрация и вход по JWT, хранение пользователей в PostgreSQL, подключение к комнате через WebSocket, SDP/ICE signaling, прием audio/video RTP-потоков на сервере и пересылка треков другим участникам комнаты.

![Go](https://img.shields.io/badge/Go-1.24-00ADD8)
![WebRTC](https://img.shields.io/badge/WebRTC-Pion-5A67D8)
![WebSocket](https://img.shields.io/badge/WebSocket-Gorilla-111827)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-pgx-336791)

## Оглавление

- [Архитектура](#архитектура)
- [Как работает конференция](#как-работает-конференция)
- [Структура проекта](#структура-проекта)
- [Технологический стек](#технологический-стек)
- [Предварительные требования](#предварительные-требования)
- [Установка и запуск](#установка-и-запуск)
- [Конфигурация](#конфигурация)
- [HTTP API](#http-api)
- [WebSocket signaling](#websocket-signaling)
- [Разработка и проверки](#разработка-и-проверки)
- [Ограничения](#ограничения)
- [Roadmap](#roadmap)
- [English Version](#english-version)

## Архитектура

```mermaid
flowchart LR
    BrowserA["Browser A"] --> HTTP["Go HTTP API"]
    BrowserB["Browser B"] --> HTTP
    HTTP --> Postgres["PostgreSQL users"]
    BrowserA <-->|WebSocket signaling| WS["/ws handler"]
    BrowserB <-->|WebSocket signaling| WS
    WS --> Room["In-memory room registry"]
    BrowserA <-->|WebRTC PeerConnection| PionA["Pion PeerConnection A"]
    BrowserB <-->|WebRTC PeerConnection| PionB["Pion PeerConnection B"]
    PionA --> Relay["RTP media relay"]
    Relay --> PionB
    PionB --> Relay
    Relay --> PionA
```

### Основной поток

1. Пользователь регистрируется через `POST /api/register`.
2. Пользователь входит через `POST /api/login` и получает JWT.
3. Клиент запрашивает доступ к камере и микрофону через `getUserMedia`.
4. Клиент создает `RTCPeerConnection`, добавляет локальные audio/video tracks и формирует SDP offer.
5. Первый WebSocket-пакет на `/ws` отправляется как `join` вместе с room id, JWT и SDP offer.
6. Сервер валидирует JWT, создает Pion `PeerConnection`, принимает offer и отвечает SDP answer.
7. После успешного initial handshake пользователь добавляется в комнату.
8. Когда сервер получает remote audio/video tracks, он создает локальные tracks для других участников.
9. При появлении новых tracks сервер запускает renegotiation и рассылает SDP offer получателям.
10. RTP-пакеты читаются из `TrackRemote` отправителя и пишутся в `TrackLocalStaticRTP` получателей.

## Как работает конференция

### Signaling

Сигналинг проходит через WebSocket endpoint `/ws`. Сервер не передает media по WebSocket: WebSocket используется только для служебных сообщений WebRTC.

```text
client
  -> join(room, token, offer)
server
  -> answer
client/server
  -> candidate
server
  -> offer on renegotiation
client
  -> answer
```

### Media relay

Сервер работает как простой SFU-like relay:

- каждый участник имеет один Pion `PeerConnection`;
- входящие audio/video tracks приходят на сервер как `TrackRemote`;
- для каждого другого участника сервер создает отдельный `TrackLocalStaticRTP`;
- ключ исходящего трека учитывает отправителя и тип media: `source_user_id + kind`;
- audio и video не перетирают друг друга и пересылаются как отдельные tracks.

### Комнаты

Комнаты хранятся в памяти процесса. Если комната становится пустой, она удаляется из глобальной таблицы rooms.

Текущая модель допускает один активный WebSocket-сеанс одного пользователя в одной комнате. Для теста с двумя участниками нужны два разных аккаунта.

## Структура проекта

```text
cmd/server/
  main.go              HTTP server, routes, static files, WebSocket endpoint
internal/
  auth/                JWT generation and validation
  store/               PostgreSQL access, user registration and authentication
  ws/                  rooms, users, WebSocket signaling, Pion media relay
static/
  index.html           browser UI, auth forms, room controls, WebRTC client
check_token.html       small helper page for token diagnostics
go.mod                 Go module and dependencies
go.sum                 dependency lock file
```

## Технологический стек

| Компонент | Технологии |
| --- | --- |
| Language | Go 1.24 |
| HTTP routing | Gorilla Mux |
| WebSocket | Gorilla WebSocket |
| WebRTC server | Pion WebRTC v4 |
| Persistence | PostgreSQL, pgx stdlib driver |
| Auth | JWT HS256, bcrypt password hashing |
| Frontend | Vanilla HTML, CSS, JavaScript |
| Browser media | MediaDevices API, RTCPeerConnection |
| Quality | gofmt, go test |

## Предварительные требования

- Go 1.24 или новее.
- PostgreSQL, доступный локально или через `DATABASE_URL`.
- Современный браузер с поддержкой WebRTC.
- Доступ к камере и микрофону.

Важно: `getUserMedia` работает в браузере только в secure context. Для локальной разработки `http://localhost` и `http://127.0.0.1` разрешены. Открывать `static/index.html` через `file://` нельзя: API-запросы и WebSocket будут работать некорректно.

## Установка и запуск

Клонировать репозиторий:

```bash
git clone <your-repo-url>
cd voicechat
```

Создать PostgreSQL-базу:

```bash
createdb voice_chat_base
```

Если используется стандартная локальная база, дополнительных переменных не нужно. По умолчанию приложение подключается к:

```text
postgres://postgres:postgres@localhost:5432/voice_chat_base?sslmode=disable
```

Загрузить зависимости:

```bash
go mod download
```

Запустить сервер:

```bash
go run ./cmd/server
```

Открыть приложение:

```text
http://localhost:8080
```

Для проверки конференции:

1. Откройте `http://localhost:8080` в двух браузерах или в обычном окне и инкогнито.
2. Зарегистрируйте два разных аккаунта.
3. Войдите под каждым аккаунтом.
4. Подключите оба аккаунта к одной комнате, например `room1`.
5. Разрешите доступ к камере и микрофону.

## Конфигурация

| Переменная | Описание | Default / пример |
| --- | --- | --- |
| `DATABASE_URL` | PostgreSQL DSN | `postgres://postgres:postgres@localhost:5432/voice_chat_base?sslmode=disable` |
| `VOICECHAT_JWT_SECRET` | Secret для подписи JWT | `dev-secret-do-not-use-in-prod` |

Пример запуска с явной конфигурацией:

```bash
DATABASE_URL="postgres://postgres:postgres@localhost:5432/voice_chat_base?sslmode=disable" \
VOICECHAT_JWT_SECRET="change-me" \
go run ./cmd/server
```

Runtime defaults:

| Параметр | Значение |
| --- | --- |
| HTTP address | `:8080` |
| Static files directory | `static` |
| Default room in UI | `room1` |
| JWT TTL | `24h` |
| STUN server | `stun:stun.l.google.com:19302` |
| Users table | auto-created on startup |

## HTTP API

| Method | Path | Описание |
| --- | --- | --- |
| `POST` | `/api/register` | Регистрация пользователя |
| `POST` | `/api/login` | Вход пользователя и выдача JWT |
| `GET` | `/api/me` | Получение текущего пользователя по Bearer token |
| `GET` | `/` | Статический UI |
| `GET` | `/ws` | WebSocket endpoint для signaling |

### Регистрация

```bash
curl -i -X POST http://localhost:8080/api/register \
  -H "Content-Type: application/json" \
  -d '{"username":"alice","password":"secret"}'
```

Успешный ответ:

```text
HTTP/1.1 201 Created
```

### Логин

```bash
curl -s -X POST http://localhost:8080/api/login \
  -H "Content-Type: application/json" \
  -d '{"username":"alice","password":"secret"}'
```

Пример ответа:

```json
{
  "token": "jwt-token"
}
```

### Текущий пользователь

```bash
curl -s http://localhost:8080/api/me \
  -H "Authorization: Bearer <jwt-token>"
```

Пример ответа:

```json
{
  "ID": "user-id",
  "Username": "alice",
  "DisplayName": "alice",
  "CreatedAt": "2026-06-22T12:00:00Z"
}
```

## WebSocket signaling

Первое сообщение после открытия WebSocket должно быть `join`.

```json
{
  "type": "join",
  "room": "room1",
  "token": "jwt-token",
  "sdp": "v=0...",
  "sdpType": "offer"
}
```

Поддерживаемые signaling-сообщения:

| Type | Direction | Назначение |
| --- | --- | --- |
| `join` | client -> server | Аутентификация, вход в комнату, initial SDP offer |
| `answer` | client -> server | Ответ клиента на renegotiation offer сервера |
| `candidate` | client -> server | ICE candidate от браузера |
| `candidateFromServer` | server -> client | ICE candidate от Pion PeerConnection |
| `offer` | server -> client | Renegotiation offer при добавлении новых tracks |
| `leave` | client -> server | Выход из комнаты |

## Разработка и проверки

Форматирование:

```bash
gofmt -w cmd internal
```

Тесты и компиляционная проверка:

```bash
go test ./...
```

Проверка зависимостей:

```bash
go mod tidy
```

Полезные команды для диагностики:

```bash
lsof -nP -iTCP:8080 -sTCP:LISTEN
```

```bash
curl -I http://localhost:8080/
```


## Roadmap

- TURN configuration для production NAT traversal.
- Docker Compose с PostgreSQL и готовым local dev окружением.
- Room roster: список участников, имена на видео-плитках, presence events.
- Mute/camera state signaling между участниками.
- Screen sharing.
- Chat messages внутри комнаты.
- Metrics и structured logs для WebRTC state transitions.
- Graceful cleanup outgoing tracks при отключении участника.
- Browser integration tests для сценария двух участников.
- Origin policy и production-ready auth/session layer.

## English Version

# VoiceChat Conference

WebRTC voice and video conferencing app with JWT authentication, PostgreSQL-backed users, WebSocket signaling, and server-side media track forwarding through Pion.

## Pipeline

```text
register/login
  -> JWT token
  -> getUserMedia(audio + video)
  -> RTCPeerConnection offer
  -> WebSocket join(room + token + SDP)
  -> Pion PeerConnection answer
  -> room membership
  -> remote audio/video tracks
  -> RTP forwarding to other participants
  -> renegotiation when new tracks are added
```

## Stack

- Go 1.24
- Gorilla Mux
- Gorilla WebSocket
- Pion WebRTC v4
- PostgreSQL via pgx
- JWT HS256
- bcrypt
- Vanilla HTML, CSS and JavaScript

## Run

```bash
createdb voice_chat_base
go mod download
go run ./cmd/server
```

Open:

```text
http://localhost:8080
```

Do not open `static/index.html` directly through `file://`. The browser UI must be served by the Go server so `/api/*` and `/ws` resolve correctly.

## API

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/api/register` | Register user |
| `POST` | `/api/login` | Login and return JWT |
| `GET` | `/api/me` | Return current user |
| `GET` | `/ws` | WebSocket signaling endpoint |

## Production Notes

- Configure a strong `VOICECHAT_JWT_SECRET`.
- Add TURN servers for reliable NAT traversal.
- Restrict WebSocket origins.
- Move room state out of process before horizontal scaling.
- Add browser-level WebRTC integration tests.
