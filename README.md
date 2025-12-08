# VoiceChat MVP

Простой WebRTC voicechat с авторизацией и Postgres.

## Установка

### 1. Postgres

```bash
# Если нет Postgres, установи:
brew install postgresql@15

# Запусти Postgres:
brew services start postgresql@15

# Создай БД (если её нет):
createdb voicechat
```

### 2. Go зависимости

```bash
cd voicechat
go mod download
```

## Запуск

```bash
# С переменной окружения для JWT
VOICECHAT_JWT_SECRET="your-secret-key-at-least-32-chars" go run ./cmd/server

# Или если DATABASE_URL нестандартный:
DATABASE_URL="postgres://user:pass@localhost:5432/voicechat?sslmode=disable" \
VOICECHAT_JWT_SECRET="your-secret-key-at-least-32-chars" \
go run ./cmd/server
```

Откройте http://localhost:8080

## Как использовать

1. **Register** — создай аккаунт
2. **Login** — залогинься (токен сохранится автоматически)
3. **Connect** — подключись к комнате
4. Открой второй браузер/вкладку с другим пользователем
5. Оба должны видеть друг друга и слышать!

## Архитектура

- **Backend:** Go, gorilla/websocket, gorilla/mux, pion/webrtc
- **Database:** Postgres с bcrypt для паролей
- **Auth:** JWT tokens (24h expiry)
- **Frontend:** Vanilla HTML/JS
- **Protocol:** WebRTC offer/answer signaling через WebSocket

## Особенности

- ✅ Регистрация и авторизация
- ✅ Многоимер WebRTC (каждый юзер слышит всех)
- ✅ SFU архитектура (сервер пересылает RTP пакеты)
- ✅ Trickle ICE (кандидаты отправляются по мере сбора)
- ✅ Защита от дубликатов (один юзер не может дважды быть в одной комнате)
- ✅ Логирование с именами пользователей

## Безопасность (важно на продакшене!)

- Установи сильный `VOICECHAT_JWT_SECRET` (минимум 32 символа)
- Измени `CheckOrigin` в `internal/ws/handler.go` для production
- Используй HTTPS в production
- Установи правильные CORS headers
