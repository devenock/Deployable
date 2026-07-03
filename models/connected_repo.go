package models

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ConnectedRepo mirrors the connected_repos table — a user's watchlist of
// GitHub repos, independent of whether any of them have been scanned yet.
type ConnectedRepo struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	GitHubID      int64
	FullName      string
	Private       bool
	DefaultBranch string
	AddedAt       time.Time
}

// AddConnectedRepo adds a repo to a user's watchlist, or refreshes its
// metadata (full_name/private/default_branch) if it was already added —
// covers the repo having been renamed on GitHub since.
func AddConnectedRepo(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, githubID int64, fullName string, private bool, defaultBranch string) (*ConnectedRepo, error) {
	r := &ConnectedRepo{}
	err := pool.QueryRow(ctx, `
		INSERT INTO connected_repos (user_id, github_id, full_name, private, default_branch)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, github_id) DO UPDATE
		SET full_name = $3, private = $4, default_branch = $5
		RETURNING id, user_id, github_id, full_name, private, default_branch, added_at
	`, userID, githubID, fullName, private, defaultBranch).Scan(
		&r.ID, &r.UserID, &r.GitHubID, &r.FullName, &r.Private, &r.DefaultBranch, &r.AddedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("add connected repo: %w", err)
	}
	return r, nil
}

// RemoveConnectedRepo removes a repo from a user's watchlist, scoped to its
// owner — same "doesn't exist and isn't yours look identical" pattern as
// DeleteReport.
func RemoveConnectedRepo(ctx context.Context, pool *pgxpool.Pool, id, userID uuid.UUID) error {
	tag, err := pool.Exec(ctx, `DELETE FROM connected_repos WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("remove connected repo: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListConnectedRepos returns a user's watchlist, most recently added first.
func ListConnectedRepos(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) ([]*ConnectedRepo, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, user_id, github_id, full_name, private, default_branch, added_at
		FROM connected_repos
		WHERE user_id = $1
		ORDER BY added_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list connected repos: %w", err)
	}
	defer rows.Close()

	var repos []*ConnectedRepo
	for rows.Next() {
		r := &ConnectedRepo{}
		if err := rows.Scan(&r.ID, &r.UserID, &r.GitHubID, &r.FullName, &r.Private, &r.DefaultBranch, &r.AddedAt); err != nil {
			return nil, fmt.Errorf("scan connected repo: %w", err)
		}
		repos = append(repos, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list connected repos: %w", err)
	}
	return repos, nil
}
