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

// ErrNotFound is returned when a lookup finds no matching row.
var ErrNotFound = errors.New("not found")

// User mirrors the users table.
type User struct {
	ID              uuid.UUID
	Email           string
	Name            string
	PasswordHash    string
	GitHubID        *string
	GitHubLogin     *string
	GitHubToken     *string
	GoogleID        *string
	APIKeyHash      *string
	Plan            string
	AnalysesCount   int
	EmailVerifiedAt *time.Time
	WelcomedAt      *time.Time
	LastLoginAt     *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// IsEmailVerified reports whether the user has completed OTP or OAuth email verification.
func (u *User) IsEmailVerified() bool {
	return u.EmailVerifiedAt != nil
}

// Session mirrors the sessions table.
type Session struct {
	ID        string
	UserID    uuid.UUID
	ExpiresAt time.Time
	CreatedAt time.Time
}

const userColumns = `
	id, email, name, password_hash, github_id, github_login, google_id,
	plan, analyses_count, email_verified_at, welcomed_at, last_login_at,
	created_at, updated_at
`

func scanUser(row pgx.Row) (*User, error) {
	u := &User{}
	err := row.Scan(
		&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.GitHubID, &u.GitHubLogin, &u.GoogleID,
		&u.Plan, &u.AnalysesCount, &u.EmailVerifiedAt, &u.WelcomedAt, &u.LastLoginAt,
		&u.CreatedAt, &u.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// CreateUser inserts a new, unverified user with the given email, name, and
// bcrypt hash. email_verified_at is left NULL until OTP verification.
func CreateUser(ctx context.Context, pool *pgxpool.Pool, email, name, passwordHash string) (*User, error) {
	u, err := scanUser(pool.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash)
		VALUES ($1, $2, $3)
		RETURNING `+userColumns, email, name, passwordHash))
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

// CreateOAuthUser inserts a new user from a trusted OAuth profile. The email
// is considered pre-verified by the provider, so email_verified_at is set
// immediately and no password is set.
func CreateOAuthUser(ctx context.Context, pool *pgxpool.Pool, email, name string) (*User, error) {
	u, err := scanUser(pool.QueryRow(ctx, `
		INSERT INTO users (email, name, email_verified_at)
		VALUES ($1, $2, NOW())
		RETURNING `+userColumns, email, name))
	if err != nil {
		return nil, fmt.Errorf("create oauth user: %w", err)
	}
	return u, nil
}

// FindUserByEmail looks up a user by email.
func FindUserByEmail(ctx context.Context, pool *pgxpool.Pool, email string) (*User, error) {
	u, err := scanUser(pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE email = $1`, email))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find user by email: %w", err)
	}
	return u, nil
}

// FindUserByID looks up a user by ID.
func FindUserByID(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (*User, error) {
	u, err := scanUser(pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find user by id: %w", err)
	}
	return u, nil
}

// FindUserByGitHubID looks up a user by their linked GitHub account ID.
func FindUserByGitHubID(ctx context.Context, pool *pgxpool.Pool, githubID string) (*User, error) {
	u, err := scanUser(pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE github_id = $1`, githubID))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find user by github id: %w", err)
	}
	return u, nil
}

// FindUserByGoogleID looks up a user by their linked Google account ID.
func FindUserByGoogleID(ctx context.Context, pool *pgxpool.Pool, googleID string) (*User, error) {
	u, err := scanUser(pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE google_id = $1`, googleID))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find user by google id: %w", err)
	}
	return u, nil
}

// LinkGitHubAccount attaches a GitHub account ID/login to an existing user
// (used when an OAuth email matches an existing email/password account).
func LinkGitHubAccount(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, githubID, githubLogin string) error {
	_, err := pool.Exec(ctx, `UPDATE users SET github_id = $1, github_login = $2, updated_at = NOW() WHERE id = $3`,
		githubID, githubLogin, userID)
	if err != nil {
		return fmt.Errorf("link github account: %w", err)
	}
	return nil
}

// LinkGoogleAccount attaches a Google account ID to an existing user.
func LinkGoogleAccount(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, googleID string) error {
	_, err := pool.Exec(ctx, `UPDATE users SET google_id = $1, updated_at = NOW() WHERE id = $2`, googleID, userID)
	if err != nil {
		return fmt.Errorf("link google account: %w", err)
	}
	return nil
}

// CreateGitHubUser inserts a new user from a trusted GitHub OAuth profile.
func CreateGitHubUser(ctx context.Context, pool *pgxpool.Pool, email, name, githubID, githubLogin string) (*User, error) {
	u, err := scanUser(pool.QueryRow(ctx, `
		INSERT INTO users (email, name, github_id, github_login, email_verified_at)
		VALUES ($1, $2, $3, $4, NOW())
		RETURNING `+userColumns, email, name, githubID, githubLogin))
	if err != nil {
		return nil, fmt.Errorf("create github user: %w", err)
	}
	return u, nil
}

// CreateGoogleUser inserts a new user from a trusted Google OAuth profile.
func CreateGoogleUser(ctx context.Context, pool *pgxpool.Pool, email, name, googleID string) (*User, error) {
	u, err := scanUser(pool.QueryRow(ctx, `
		INSERT INTO users (email, name, google_id, email_verified_at)
		VALUES ($1, $2, $3, NOW())
		RETURNING `+userColumns, email, name, googleID))
	if err != nil {
		return nil, fmt.Errorf("create google user: %w", err)
	}
	return u, nil
}

// GenerateAPIKey creates a new random API key (returned once, in plaintext)
// and stores only its SHA-256 hash — the same one-way check
// middleware.RequireAPIKey performs. Overwrites any existing key, so
// regenerating immediately invalidates the previous one.
func GenerateAPIKey(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) (rawKey string, err error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate api key: %w", err)
	}
	rawKey = "dpl_" + hex.EncodeToString(b)

	sum := sha256.Sum256([]byte(rawKey))
	hash := hex.EncodeToString(sum[:])

	if _, err := pool.Exec(ctx, `UPDATE users SET api_key_hash = $1, updated_at = NOW() WHERE id = $2`, hash, userID); err != nil {
		return "", fmt.Errorf("store api key hash: %w", err)
	}
	return rawKey, nil
}

// HasAPIKey reports whether the user has generated an API key.
func HasAPIKey(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) (bool, error) {
	var hash *string
	err := pool.QueryRow(ctx, `SELECT api_key_hash FROM users WHERE id = $1`, userID).Scan(&hash)
	if err != nil {
		return false, fmt.Errorf("check api key: %w", err)
	}
	return hash != nil && *hash != "", nil
}

// SetGitHubToken stores an encrypted GitHub token granting repo access,
// obtained via the "connect GitHub" flow (broader scope than the read:user/
// user:email one used for sign-in). github_id/github_login are backfilled
// only if not already set, so this doesn't clobber an existing GitHub-login
// linkage for a different account.
func SetGitHubToken(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, encryptedToken, githubID, githubLogin string) error {
	_, err := pool.Exec(ctx, `
		UPDATE users
		SET github_token = $1,
		    github_id = COALESCE(github_id, $2),
		    github_login = COALESCE(github_login, $3),
		    updated_at = NOW()
		WHERE id = $4
	`, encryptedToken, githubID, githubLogin, userID)
	if err != nil {
		return fmt.Errorf("set github token: %w", err)
	}
	return nil
}

// GetGitHubToken returns the user's encrypted GitHub token, or ErrNotFound
// if they haven't connected GitHub for repo access.
func GetGitHubToken(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) (string, error) {
	var token *string
	err := pool.QueryRow(ctx, `SELECT github_token FROM users WHERE id = $1`, userID).Scan(&token)
	if err != nil {
		return "", fmt.Errorf("get github token: %w", err)
	}
	if token == nil || *token == "" {
		return "", ErrNotFound
	}
	return *token, nil
}

// MarkEmailVerified sets email_verified_at to now for the given user.
func MarkEmailVerified(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) error {
	_, err := pool.Exec(ctx, `UPDATE users SET email_verified_at = NOW(), updated_at = NOW() WHERE id = $1`, userID)
	if err != nil {
		return fmt.Errorf("mark email verified: %w", err)
	}
	return nil
}

// UpdatePassword replaces a user's bcrypt hash (used by the reset-password flow).
func UpdatePassword(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, passwordHash string) error {
	_, err := pool.Exec(ctx, `UPDATE users SET password_hash = $1, updated_at = NOW() WHERE id = $2`, passwordHash, userID)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	return nil
}

// MarkWelcomed sets welcomed_at to now for the given user.
func MarkWelcomed(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) error {
	_, err := pool.Exec(ctx, `UPDATE users SET welcomed_at = NOW(), updated_at = NOW() WHERE id = $1`, userID)
	if err != nil {
		return fmt.Errorf("mark welcomed: %w", err)
	}
	return nil
}

// RecordLogin sets last_login_at to now and reports whether this was the
// user's first-ever successful login (last_login_at was previously NULL),
// atomically, in a single round trip.
func RecordLogin(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) (isFirstLogin bool, err error) {
	err = pool.QueryRow(ctx, `
		WITH prev AS (SELECT last_login_at FROM users WHERE id = $1)
		UPDATE users SET last_login_at = NOW(), updated_at = NOW()
		WHERE id = $1
		RETURNING (SELECT last_login_at FROM prev) IS NULL
	`, userID).Scan(&isFirstLogin)
	if err != nil {
		return false, fmt.Errorf("record login: %w", err)
	}
	return isFirstLogin, nil
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

// DeleteAllUserSessions removes every session belonging to a user (used when
// a password is reset, to force re-authentication everywhere).
func DeleteAllUserSessions(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) error {
	_, err := pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("delete all user sessions: %w", err)
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
