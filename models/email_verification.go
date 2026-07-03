package models

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	otpLength         = 6
	otpTTL            = 15 * time.Minute
	otpMaxAttempts    = 5
	otpResendCooldown = 60 * time.Second
)

var (
	// ErrOTPExpired is returned when the OTP existed but is past its expiry.
	ErrOTPExpired = errors.New("otp expired")
	// ErrOTPInvalid is returned when the submitted code does not match.
	ErrOTPInvalid = errors.New("otp invalid")
	// ErrOTPLocked is returned once the attempt limit has been exhausted.
	ErrOTPLocked = errors.New("otp locked, too many attempts")
)

// CreateEmailVerification generates a new 6-digit OTP for the user, stores
// its hash with a 15-minute expiry, and returns the plaintext code for the
// caller to email. Any previous unconsumed codes for the user are left in
// place but become irrelevant since verification always checks the latest.
func CreateEmailVerification(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) (code string, err error) {
	code, err = generateOTP(otpLength)
	if err != nil {
		return "", fmt.Errorf("generate otp: %w", err)
	}

	hash := hashOTP(code)
	_, err = pool.Exec(ctx, `
		INSERT INTO email_verifications (user_id, code_hash, expires_at)
		VALUES ($1, $2, $3)
	`, userID, hash, time.Now().Add(otpTTL))
	if err != nil {
		return "", fmt.Errorf("create email verification: %w", err)
	}
	return code, nil
}

// VerifyEmailOTP checks the submitted code against the user's most recent
// verification row. On a wrong code it increments the attempt counter; after
// otpMaxAttempts wrong attempts the code is locked out. On success the row
// is marked consumed. Callers should follow success with MarkEmailVerified.
func VerifyEmailOTP(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, code string) error {
	var (
		id        uuid.UUID
		codeHash  string
		attempts  int
		expiresAt time.Time
	)
	err := pool.QueryRow(ctx, `
		SELECT id, code_hash, attempts, expires_at
		FROM email_verifications
		WHERE user_id = $1 AND consumed_at IS NULL
		ORDER BY created_at DESC
		LIMIT 1
	`, userID).Scan(&id, &codeHash, &attempts, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("find email verification: %w", err)
	}

	if attempts >= otpMaxAttempts {
		return ErrOTPLocked
	}
	if time.Now().After(expiresAt) {
		return ErrOTPExpired
	}

	if subtle.ConstantTimeCompare([]byte(hashOTP(code)), []byte(codeHash)) != 1 {
		if _, err := pool.Exec(ctx, `UPDATE email_verifications SET attempts = attempts + 1 WHERE id = $1`, id); err != nil {
			return fmt.Errorf("record failed otp attempt: %w", err)
		}
		return ErrOTPInvalid
	}

	if _, err := pool.Exec(ctx, `UPDATE email_verifications SET consumed_at = NOW() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("consume email verification: %w", err)
	}
	return nil
}

// CanResendEmailVerification reports whether enough time has passed since
// the last OTP was issued to allow sending another one.
func CanResendEmailVerification(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) (bool, error) {
	var createdAt time.Time
	err := pool.QueryRow(ctx, `
		SELECT created_at FROM email_verifications
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, userID).Scan(&createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("check email verification cooldown: %w", err)
	}
	return time.Since(createdAt) >= otpResendCooldown, nil
}

func hashOTP(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

func generateOTP(length int) (string, error) {
	digits := make([]byte, length)
	max := big.NewInt(10)
	for i := range digits {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		digits[i] = byte('0' + n.Int64())
	}
	return string(digits), nil
}
