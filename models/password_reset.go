package models

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const passwordResetTTL = 1 * time.Hour

// PasswordReset mirrors the password_resets table.
type PasswordReset struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	ExpiresAt time.Time
}

// CreatePasswordReset generates a new single-use reset token, stores its
// hash with a 1-hour expiry, and returns the plaintext token for the caller
// to embed in the reset link. The plaintext is never persisted.
func CreatePasswordReset(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) (token string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate reset token: %w", err)
	}
	token = hex.EncodeToString(raw)

	_, err = pool.Exec(ctx, `
		INSERT INTO password_resets (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
	`, userID, hashResetToken(token), time.Now().Add(passwordResetTTL))
	if err != nil {
		return "", fmt.Errorf("create password reset: %w", err)
	}
	return token, nil
}

// FindValidPasswordReset looks up an unconsumed, unexpired reset by its
// plaintext token.
func FindValidPasswordReset(ctx context.Context, pool *pgxpool.Pool, token string) (*PasswordReset, error) {
	pr := &PasswordReset{}
	var expiresAt time.Time
	var consumedAt *time.Time
	err := pool.QueryRow(ctx, `
		SELECT id, user_id, expires_at, consumed_at
		FROM password_resets
		WHERE token_hash = $1
	`, hashResetToken(token)).Scan(&pr.ID, &pr.UserID, &expiresAt, &consumedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find password reset: %w", err)
	}
	if consumedAt != nil {
		return nil, ErrNotFound
	}
	if time.Now().After(expiresAt) {
		return nil, ErrOTPExpired
	}
	pr.ExpiresAt = expiresAt
	return pr, nil
}

// ConsumePasswordReset marks a reset token as used so it cannot be replayed.
func ConsumePasswordReset(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) error {
	_, err := pool.Exec(ctx, `UPDATE password_resets SET consumed_at = NOW() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("consume password reset: %w", err)
	}
	return nil
}

func hashResetToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
