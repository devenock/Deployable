package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AnalysisJob mirrors the analysis_jobs table.
type AnalysisJob struct {
	ID          uuid.UUID
	UserID      *uuid.UUID
	InputType   string // "zip" | "github" | "cli"
	InputRef    string
	Status      string // "pending" | "running" | "complete" | "failed"
	CurrentStep int
	TotalSteps  int
	StepMessage string
	ErrorMsg    string
	IPAddress   string
	StartedAt   *time.Time
	CompletedAt *time.Time
	CreatedAt   time.Time
}

// input_ref, step_message, error_msg, and ip_address are nullable columns;
// COALESCE keeps them scannable into plain (non-pointer) string fields.
const jobColumns = `
	id, user_id, input_type, COALESCE(input_ref, ''), status, current_step, total_steps,
	COALESCE(step_message, ''), COALESCE(error_msg, ''), COALESCE(ip_address, ''), started_at, completed_at, created_at
`

func scanJob(row pgx.Row) (*AnalysisJob, error) {
	j := &AnalysisJob{}
	err := row.Scan(
		&j.ID, &j.UserID, &j.InputType, &j.InputRef, &j.Status, &j.CurrentStep, &j.TotalSteps,
		&j.StepMessage, &j.ErrorMsg, &j.IPAddress, &j.StartedAt, &j.CompletedAt, &j.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return j, nil
}

// CreateJob inserts a new pending analysis job. userID is nil for anonymous submissions.
func CreateJob(ctx context.Context, pool *pgxpool.Pool, userID *uuid.UUID, inputType, inputRef, ipAddress string) (*AnalysisJob, error) {
	j, err := scanJob(pool.QueryRow(ctx, `
		INSERT INTO analysis_jobs (user_id, input_type, input_ref, ip_address)
		VALUES ($1, $2, $3, $4)
		RETURNING `+jobColumns, userID, inputType, inputRef, ipAddress))
	if err != nil {
		return nil, fmt.Errorf("create job: %w", err)
	}
	return j, nil
}

// FindJobByID looks up a job by ID.
func FindJobByID(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (*AnalysisJob, error) {
	j, err := scanJob(pool.QueryRow(ctx, `SELECT `+jobColumns+` FROM analysis_jobs WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find job by id: %w", err)
	}
	return j, nil
}

// MarkJobRunning transitions a job to running and records the current step/message.
func MarkJobRunning(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, step int, message string) error {
	_, err := pool.Exec(ctx, `
		UPDATE analysis_jobs
		SET status = 'running', current_step = $2, step_message = $3,
		    started_at = COALESCE(started_at, NOW())
		WHERE id = $1
	`, id, step, message)
	if err != nil {
		return fmt.Errorf("mark job running: %w", err)
	}
	return nil
}

// MarkJobComplete transitions a job to complete.
func MarkJobComplete(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		UPDATE analysis_jobs
		SET status = 'complete', current_step = total_steps, step_message = 'Done', completed_at = NOW()
		WHERE id = $1
	`, id)
	if err != nil {
		return fmt.Errorf("mark job complete: %w", err)
	}
	return nil
}

// MarkJobFailed transitions a job to failed with an error message.
func MarkJobFailed(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, errMsg string) error {
	_, err := pool.Exec(ctx, `
		UPDATE analysis_jobs
		SET status = 'failed', error_msg = $2, completed_at = NOW()
		WHERE id = $1
	`, id, errMsg)
	if err != nil {
		return fmt.Errorf("mark job failed: %w", err)
	}
	return nil
}
