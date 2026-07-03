package models

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GitHubAccount is one GitHub account a user has connected for repo access
// (separate from users.github_id, which identifies which GitHub identity a
// user signs in with — a user can sign in with one GitHub account and still
// connect several different ones here for repo access).
type GitHubAccount struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	GitHubID    string
	GitHubLogin string
	AvatarURL   string
	Token       string // encrypted
	ConnectedAt time.Time
}

// AddGitHubAccount adds a connected GitHub account, or refreshes its
// token/avatar if the same GitHub account was already connected (upsert on
// (user_id, github_id) — reconnecting isn't an error).
func AddGitHubAccount(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, githubID, githubLogin, avatarURL, encryptedToken string) (*GitHubAccount, error) {
	a := &GitHubAccount{}
	err := pool.QueryRow(ctx, `
		INSERT INTO github_accounts (user_id, github_id, github_login, avatar_url, token)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, github_id) DO UPDATE
		SET github_login = $3, avatar_url = $4, token = $5
		RETURNING id, user_id, github_id, github_login, COALESCE(avatar_url, ''), token, connected_at
	`, userID, githubID, githubLogin, avatarURL, encryptedToken).Scan(
		&a.ID, &a.UserID, &a.GitHubID, &a.GitHubLogin, &a.AvatarURL, &a.Token, &a.ConnectedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("add github account: %w", err)
	}
	return a, nil
}

// ListGitHubAccounts returns a user's connected GitHub accounts, oldest
// first (the order token-resolution fallback uses when a repo isn't
// explicitly tied to one).
func ListGitHubAccounts(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) ([]*GitHubAccount, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, user_id, github_id, github_login, COALESCE(avatar_url, ''), token, connected_at
		FROM github_accounts
		WHERE user_id = $1
		ORDER BY connected_at ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list github accounts: %w", err)
	}
	defer rows.Close()

	var accounts []*GitHubAccount
	for rows.Next() {
		a := &GitHubAccount{}
		if err := rows.Scan(&a.ID, &a.UserID, &a.GitHubID, &a.GitHubLogin, &a.AvatarURL, &a.Token, &a.ConnectedAt); err != nil {
			return nil, fmt.Errorf("scan github account: %w", err)
		}
		accounts = append(accounts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list github accounts: %w", err)
	}
	return accounts, nil
}

// RemoveGitHubAccount disconnects a GitHub account, scoped to its owner.
// connected_repos rows added through it are removed too (ON DELETE CASCADE
// on github_account_id) — reports generated from them are a different table
// and are untouched.
func RemoveGitHubAccount(ctx context.Context, pool *pgxpool.Pool, id, userID uuid.UUID) error {
	tag, err := pool.Exec(ctx, `DELETE FROM github_accounts WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("remove github account: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetGitHubAccountToken returns one account's encrypted token, scoped to
// its owner — used when browsing that specific account's repos.
func GetGitHubAccountToken(ctx context.Context, pool *pgxpool.Pool, accountID, userID uuid.UUID) (string, error) {
	var token string
	err := pool.QueryRow(ctx, `
		SELECT token FROM github_accounts WHERE id = $1 AND user_id = $2
	`, accountID, userID).Scan(&token)
	if err != nil {
		return "", ErrNotFound
	}
	return token, nil
}

// HasAnyGitHubAccount reports whether a user has connected at least one
// GitHub account.
func HasAnyGitHubAccount(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM github_accounts WHERE user_id = $1)`, userID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check github accounts: %w", err)
	}
	return exists, nil
}
