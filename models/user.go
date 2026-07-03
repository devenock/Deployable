package models

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a lookup finds no matching row.
var ErrNotFound = errors.New("not found")

// User mirrors the users table.
type User struct {
	ID             uuid.UUID
	Email          string
	Name           string
	PasswordHash   string
	GitHubID       *string
	GitHubLogin    *string
	GitHubToken    *string
	APIKeyHash     *string
	Plan           string
	AnalysesCount  int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Session mirrors the sessions table.
type Session struct {
	ID        string
	UserID    uuid.UUID
	ExpiresAt time.Time
	CreatedAt time.Time
}

// CreateUser inserts a new user with the given email, name, and bcrypt hash.
func CreateUser(ctx context.Context, pool *pgxpool.Pool, email, name, passwordHash string) (*User, error) {
	u := &User{}
	err := pool.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash)
		VALUES ($1, $2, $3)
		RETURNING id, email, name, password_hash, plan, analyses_count, created_at, updated_at
	`, email, name, passwordHash).Scan(
		&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Plan, &u.AnalysesCount, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

// FindUserByEmail looks up a user by email.
func FindUserByEmail(ctx context.Context, pool *pgxpool.Pool, email string) (*User, error) {
	u := &User{}
	err := pool.QueryRow(ctx, `
		SELECT id, email, name, password_hash, plan, analyses_count, created_at, updated_at
		FROM users WHERE email = $1
	`, email).Scan(
		&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Plan, &u.AnalysesCount, &u.CreatedAt, &u.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find user by email: %w", err)
	}
	return u, nil
}

// FindUserByID looks up a user by ID.
func FindUserByID(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (*User, error) {
	u := &User{}
	err := pool.QueryRow(ctx, `
		SELECT id, email, name, password_hash, plan, analyses_count, created_at, updated_at
		FROM users WHERE id = $1
	`, id).Scan(
		&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Plan, &u.AnalysesCount, &u.CreatedAt, &u.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find user by id: %w", err)
	}
	return u, nil
}

// CreateSession creates a new session row with a random session ID.
func CreateSession(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, expiresAt time.Time) (*Session, error) {
	id, err := generateSessionID()
	if err != nil {
		return nil, fmt.Errorf("generate session id: %w", err)
	}

	s := &Session{}
	err = pool.QueryRow(ctx, `
		INSERT INTO sessions (id, user_id, expires_at)
		VALUES ($1, $2, $3)
		RETURNING id, user_id, expires_at, created_at
	`, id, userID, expiresAt).Scan(&s.ID, &s.UserID, &s.ExpiresAt, &s.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return s, nil
}

// FindSession looks up a session by ID.
func FindSession(ctx context.Context, pool *pgxpool.Pool, id string) (*Session, error) {
	s := &Session{}
	err := pool.QueryRow(ctx, `
		SELECT id, user_id, expires_at, created_at
		FROM sessions WHERE id = $1
	`, id).Scan(&s.ID, &s.UserID, &s.ExpiresAt, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find session: %w", err)
	}
	return s, nil
}

// DeleteSession removes a session by ID.
func DeleteSession(ctx context.Context, pool *pgxpool.Pool, id string) error {
	_, err := pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteExpiredSessions removes all sessions past their expiry.
func DeleteExpiredSessions(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < NOW()`)
	if err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	return nil
}

func generateSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
