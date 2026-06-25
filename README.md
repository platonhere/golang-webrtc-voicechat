# VoiceChat Conference

WebRTC-приложение для голосовых и видеоконференций с авторизацией, комнатами, WebSocket signaling и серверной пересылкой media tracks через Pion.

Проект реализует минимальный backend для real-time конференций: регистрация и вход по JWT, хранение пользователей в PostgreSQL, подключение к комнате через WebSocket, SDP/ICE signaling, прием audio/video RTP-потоков на сервере и пересылка треков другим участникам комнаты.

![Go](https://img.shields.io/badge/Go-1.24-00ADD8)
![WebRTC](https://img.shields.io/badge/WebRTC-Pion-5A67D8)
![WebSocket](https://img.shields.io/badge/WebSocket-Gorilla-111827)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-pgx-336791)

## Оглавление
- [Технологический стек](#технологический-стек)
- [Предварительные требования](#предварительные-требования)
- [Установка и запуск](#установка-и-запуск)
- [Конфигурация](#конфигурация)
- [HTTP API](#http-api)
- [Разработка и проверки](#разработка-и-проверки)



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


