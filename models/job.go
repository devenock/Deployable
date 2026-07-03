package models

import (
	"time"

	"github.com/google/uuid"
)

// AnalysisJob mirrors the analysis_jobs table.
// DB methods (Create, UpdateStatus, etc.) are implemented in Phase 2.
type AnalysisJob struct {
	ID           uuid.UUID
	UserID       *uuid.UUID
	InputType    string // "zip" | "github" | "cli"
	InputRef     string
	Status       string // "pending" | "running" | "complete" | "failed"
	CurrentStep  int
	TotalSteps   int
	StepMessage  string
	ErrorMsg     string
	IPAddress    string
	StartedAt    *time.Time
	CompletedAt  *time.Time
	CreatedAt    time.Time
}
