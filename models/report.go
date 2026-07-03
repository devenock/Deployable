package models

import (
	"time"

	"github.com/google/uuid"
)

// Report mirrors the reports table.
// DB methods (Create, FindBySlug, etc.) are implemented in Phase 2.
type Report struct {
	ID               uuid.UUID
	JobID            uuid.UUID
	UserID           *uuid.UUID
	Slug             string
	IsPublic         bool

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

	Platforms       []any
	GeneratedFiles  map[string]string

	ContentHash string
	ExpiresAt   *time.Time
	CreatedAt   time.Time
}
