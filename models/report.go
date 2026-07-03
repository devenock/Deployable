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

// Report mirrors the reports table.
type Report struct {
	ID       uuid.UUID
	JobID    uuid.UUID
	UserID   *uuid.UUID
	Slug     string
	IsPublic bool

	Language        string
	LanguageVersion string
	Framework       string
	Databases       []string
	Services        []string

	ReadinessScore  int
	ComplexityScore int
	SecurityScore   int

	DeterministicFindings map[string]any
	SemanticAnalysis      map[string]any

	MinRAMMB          *int
	RecRAMMB          *int
	MinCPU            *float64
	StorageGB         *int
	EstRPS            *int
	ResourceReasoning string

	Platforms      []any
	GeneratedFiles map[string]string

	ContentHash string
	ExpiresAt   *time.Time
	CreatedAt   time.Time
}

const reportColumns = `
	id, job_id, user_id, slug, is_public,
	language, language_version, framework, databases, services,
	readiness_score, complexity_score, security_score,
	deterministic_findings, semantic_analysis,
	min_ram_mb, rec_ram_mb, min_cpu, storage_gb, est_rps, resource_reasoning,
	platforms, generated_files,
	content_hash, expires_at, created_at
`

func scanReport(row pgx.Row) (*Report, error) {
	r := &Report{}
	err := row.Scan(
		&r.ID, &r.JobID, &r.UserID, &r.Slug, &r.IsPublic,
		&r.Language, &r.LanguageVersion, &r.Framework, &r.Databases, &r.Services,
		&r.ReadinessScore, &r.ComplexityScore, &r.SecurityScore,
		&r.DeterministicFindings, &r.SemanticAnalysis,
		&r.MinRAMMB, &r.RecRAMMB, &r.MinCPU, &r.StorageGB, &r.EstRPS, &r.ResourceReasoning,
		&r.Platforms, &r.GeneratedFiles,
		&r.ContentHash, &r.ExpiresAt, &r.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

// CreateReport inserts a new report. draft.Slug is generated if empty.
func CreateReport(ctx context.Context, pool *pgxpool.Pool, draft *Report) (*Report, error) {
	slug := draft.Slug
	if slug == "" {
		var err error
		slug, err = generateSlug()
		if err != nil {
			return nil, fmt.Errorf("generate slug: %w", err)
		}
	}

	r, err := scanReport(pool.QueryRow(ctx, `
		INSERT INTO reports (
			job_id, user_id, slug, is_public,
			language, language_version, framework, databases, services,
			readiness_score, complexity_score, security_score,
			deterministic_findings, semantic_analysis,
			min_ram_mb, rec_ram_mb, min_cpu, storage_gb, est_rps, resource_reasoning,
			platforms, generated_files,
			content_hash, expires_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8, $9,
			$10, $11, $12,
			$13, $14,
			$15, $16, $17, $18, $19, $20,
			$21, $22,
			$23, $24
		)
		RETURNING `+reportColumns,
		draft.JobID, draft.UserID, slug, draft.IsPublic,
		draft.Language, draft.LanguageVersion, draft.Framework, draft.Databases, draft.Services,
		draft.ReadinessScore, draft.ComplexityScore, draft.SecurityScore,
		draft.DeterministicFindings, draft.SemanticAnalysis,
		draft.MinRAMMB, draft.RecRAMMB, draft.MinCPU, draft.StorageGB, draft.EstRPS, draft.ResourceReasoning,
		draft.Platforms, draft.GeneratedFiles,
		draft.ContentHash, draft.ExpiresAt,
	))
	if err != nil {
		return nil, fmt.Errorf("create report: %w", err)
	}
	return r, nil
}

// FindReportBySlug looks up a report by its public slug.
func FindReportBySlug(ctx context.Context, pool *pgxpool.Pool, slug string) (*Report, error) {
	r, err := scanReport(pool.QueryRow(ctx, `SELECT `+reportColumns+` FROM reports WHERE slug = $1`, slug))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find report by slug: %w", err)
	}
	return r, nil
}

// FindReportByID looks up a report by its primary key.
func FindReportByID(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (*Report, error) {
	r, err := scanReport(pool.QueryRow(ctx, `SELECT `+reportColumns+` FROM reports WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find report by id: %w", err)
	}
	return r, nil
}

// FindReportByJobID looks up the report produced by a given analysis job.
func FindReportByJobID(ctx context.Context, pool *pgxpool.Pool, jobID uuid.UUID) (*Report, error) {
	r, err := scanReport(pool.QueryRow(ctx, `SELECT `+reportColumns+` FROM reports WHERE job_id = $1`, jobID))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find report by job id: %w", err)
	}
	return r, nil
}

// FindReportByContentHash looks up the most recent report for a content hash,
// used for deduplication (same project analyzed twice returns the cached report).
func FindReportByContentHash(ctx context.Context, pool *pgxpool.Pool, hash string) (*Report, error) {
	r, err := scanReport(pool.QueryRow(ctx, `
		SELECT `+reportColumns+` FROM reports
		WHERE content_hash = $1 AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY created_at DESC
		LIMIT 1
	`, hash))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find report by content hash: %w", err)
	}
	return r, nil
}

func generateSlug() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
