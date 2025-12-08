package store

import (
    "context"
    "database/sql"
    "errors"
    "os"
    "time"

    _ "github.com/jackc/pgx/v5/stdlib"
    "golang.org/x/crypto/bcrypt"
)

var db *sql.DB

type User struct {
    ID          string
    Username    string
    DisplayName string
    CreatedAt   time.Time
}

func Init(ctx context.Context) error {
    dsn := os.Getenv("DATABASE_URL")
    if dsn == "" {
        dsn = "postgres://postgres:postgres@localhost:5432/voicechat?sslmode=disable"
    }
    var err error
    db, err = sql.Open("pgx", dsn)
    if err != nil {
        return err
    }
    db.SetMaxOpenConns(10)
    db.SetConnMaxLifetime(time.Hour)

    // simple ping
    if err := db.PingContext(ctx); err != nil {
        return err
    }

    // create users table if not exists
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
    hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
    if err != nil {
        return err
    }
    _, err = db.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, display_name) VALUES ($1,$2,$3,$4)`, id, username, string(hash), displayName)
    return err
}

func Authenticate(ctx context.Context, username, password string) (*User, error) {
    var u User
    var hash string
    row := db.QueryRowContext(ctx, `SELECT id, password_hash, display_name, created_at FROM users WHERE username=$1`, username)
    if err := row.Scan(&u.ID, &hash, &u.DisplayName, &u.CreatedAt); err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return nil, nil
        }
        return nil, err
    }
    if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
        return nil, nil
    }
    u.Username = username
    return &u, nil
}

func GetUserByID(ctx context.Context, id string) (*User, error) {
    var u User
    row := db.QueryRowContext(ctx, `SELECT id, username, display_name, created_at FROM users WHERE id=$1`, id)
    if err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.CreatedAt); err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return nil, nil
        }
        return nil, err
    }
    return &u, nil
}
