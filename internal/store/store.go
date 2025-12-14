package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"
)

var db *sql.DB

var ErrDuplicateUsername = errors.New("username already exists")

type User struct {
	ID          string
	Username    string
	DisplayName string
	CreatedAt   time.Time
}

func Init(ctx context.Context) error {
	// пробуем взять строку подключения к БД из переменной окружения DATABASE_URL
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/voice_chat_base?sslmode=disable"
	}

	// - инициализируем err и ниже используем = вместо (:=) тк db инициализирована выше
	var err error
	// Создаётся пул соединений к Postgres через драйвер pgx
	// lazy connection (соединение реально ещё не устанавливается)
	db, err = sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(10)
	db.SetConnMaxLifetime(time.Hour)

	// пигнуем сервер и проверяем что все работает
	if err := db.PingContext(ctx); err != nil {
		return err
	}

	// инициализация пустой таблицы users
	_, err = db.ExecContext(ctx, `
    CREATE TABLE IF NOT EXISTS users (
        id TEXT PRIMARY KEY,
        username TEXT UNIQUE NOT NULL,
        password_hash TEXT NOT NULL,
        display_name TEXT NOT NULL,
        created_at TIMESTAMP WITH TIME ZONE DEFAULT now()
    );
    `)
	return err
}

func CreateUser(ctx context.Context, id, username, password, displayName string) error {
	// хешируем пароль с использованием bcrypt для безопасного хранения
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	// сохраняем нового пользователя в таблицу users
	_, err = db.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, display_name) VALUES ($1,$2,$3,$4)`, id, username, string(hash), displayName)
	if err != nil {
		// проверяем, не является ли это ошибкой нарушения уникальности username в бд (UNIQUE)
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "23505") {
			return ErrDuplicateUsername
		}
		return err
	}
	return nil
}

func Authenticate(ctx context.Context, username, password string) (*User, error) {
	var u User
	var hash string
	// выполняем запрос к БД для получения данных пользователя по username
	row := db.QueryRowContext(ctx, `SELECT id, password_hash, display_name, created_at FROM users WHERE username=$1`, username)

	// сканируем результат запроса в структуру User
	if err := row.Scan(&u.ID, &hash, &u.DisplayName, &u.CreatedAt); err != nil {
		// если пользователь не найден, возвращаем nil без ошибки
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	// проверяем соответствие пароля с хешем из БД
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		// пароль не совпадает, возвращаем nil без ошибки
		return nil, nil
	}
	// если пароль совпадает, заполняем поле структуры User
	u.Username = username
	// возвращаем данные пользователя при успешной аутентификации
	return &u, nil
}

func GetUserByID(ctx context.Context, id string) (*User, error) {
	var u User
	// выполняем запрос к БД для получения данных пользователя по ID
	row := db.QueryRowContext(ctx, `SELECT id, username, display_name, created_at FROM users WHERE id=$1`, id)
	// сканируем результат запроса в структуру User
	if err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.CreatedAt); err != nil {
		// если пользователь не найден, возвращаем nil без ошибки
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	// возвращаем данные пользователя
	return &u, nil
}
