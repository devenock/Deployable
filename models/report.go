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

// DeleteReport removes a report, scoped to its owner — a mismatched
// userID (not the owner, or an anonymous report with no owner) behaves
// identically to a nonexistent report (ErrNotFound), so this never leaks
// whether a given slug exists to someone who doesn't own it.
func DeleteReport(ctx context.Context, pool *pgxpool.Pool, id, userID uuid.UUID) error {
	tag, err := pool.Exec(ctx, `DELETE FROM reports WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete report: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ReportSummary is a Report plus the originating job's input, needed by the
// dashboard list to show what was scanned and whether rescan applies
// (input_type == "github").
type ReportSummary struct {
	Report
	InputType string
	InputRef  string
}

// ListReportsByUser returns a page of a user's reports (most recent first),
// optionally filtered by a case-insensitive substring match against the
// source (input_ref), language, or framework. total is the full matching
// count (for pagination), computed in the same round trip via a window
// function.
func ListReportsByUser(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, search string, limit, offset int) ([]*ReportSummary, int, error) {
	rows, err := pool.Query(ctx, `
		SELECT r.id, r.job_id, r.user_id, r.slug, r.is_public,
		       r.language, r.language_version, r.framework, r.databases, r.services,
		       r.readiness_score, r.complexity_score, r.security_score,
		       r.deterministic_findings, r.semantic_analysis,
		       r.min_ram_mb, r.rec_ram_mb, r.min_cpu, r.storage_gb, r.est_rps, r.resource_reasoning,
		       r.platforms, r.generated_files,
		       r.content_hash, r.expires_at, r.created_at,
		       j.input_type, COALESCE(j.input_ref, ''),
		       COUNT(*) OVER() AS total
		FROM reports r
		JOIN analysis_jobs j ON j.id = r.job_id
		WHERE r.user_id = $1
		  AND (
		    $2 = '' OR
		    j.input_ref ILIKE '%' || $2 || '%' OR
		    r.language ILIKE '%' || $2 || '%' OR
		    r.framework ILIKE '%' || $2 || '%'
		  )
		ORDER BY r.created_at DESC
		LIMIT $3 OFFSET $4
	`, userID, search, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list reports by user: %w", err)
	}
	defer rows.Close()

	var summaries []*ReportSummary
	total := 0
	for rows.Next() {
		s := &ReportSummary{}
		err := rows.Scan(
			&s.ID, &s.JobID, &s.UserID, &s.Slug, &s.IsPublic,
			&s.Language, &s.LanguageVersion, &s.Framework, &s.Databases, &s.Services,
			&s.ReadinessScore, &s.ComplexityScore, &s.SecurityScore,
			&s.DeterministicFindings, &s.SemanticAnalysis,
			&s.MinRAMMB, &s.RecRAMMB, &s.MinCPU, &s.StorageGB, &s.EstRPS, &s.ResourceReasoning,
			&s.Platforms, &s.GeneratedFiles,
			&s.ContentHash, &s.ExpiresAt, &s.CreatedAt,
			&s.InputType, &s.InputRef,
			&total,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("scan report summary: %w", err)
		}
		summaries = append(summaries, s)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list reports by user: %w", err)
	}

	return summaries, total, nil
}

// ReportStats is an at-a-glance summary of a user's report history, shown
// on the dashboard overview.
type ReportStats struct {
	TotalReports   int
	AvgScore       int
	NeedsAttention int // readiness_score < 60
}

// GetReportStats computes aggregate stats for a user's reports in one
// round trip.
func GetReportStats(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) (*ReportStats, error) {
	s := &ReportStats{}
	err := pool.QueryRow(ctx, `
		SELECT
			COUNT(*),
			COALESCE(ROUND(AVG(readiness_score)), 0)::int,
			COUNT(*) FILTER (WHERE readiness_score < 60)
		FROM reports
		WHERE user_id = $1
	`, userID).Scan(&s.TotalReports, &s.AvgScore, &s.NeedsAttention)
	if err != nil {
		return nil, fmt.Errorf("get report stats: %w", err)
	}
	return s, nil
}

func generateSlug() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
