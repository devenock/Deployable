package analyzer

import "context"

// FileEntry describes a single file discovered during the walk.
type FileEntry struct {
	Path string // relative to the project root, forward-slash separated
	Size int64
	Ext  string
}

// FileManifest is the complete inventory of files considered for analysis
// (noise directories like node_modules/vendor/.git are excluded up front).
type FileManifest struct {
	Root       string
	Files      []FileEntry
	TotalFiles int
	TotalBytes int64
}

// StackInfo describes the detected language, framework, and surrounding
// project conventions.
type StackInfo struct {
	Language         string   // "Go", "Node.js", "Python", "Ruby", "Rust", "Java", "PHP", ".NET"
	LanguageVersion  string   // "1.22", "20", "3.11", etc. — best-effort, may be empty
	Framework        string   // "Chi", "Express", "FastAPI", "Django", "Rails", etc.
	Databases        []string // ["PostgreSQL", "Redis", "MongoDB"]
	HasDocker        bool
	HasDockerCompose bool
	HasGitignore     bool
	HasEnvExample    bool
	HasCIConfig      bool
	HasHealthCheck   bool
	HasLockFile      bool
	HasTests         bool
	AppPort          string
	EntryPoint       string
}

// InfraChecks captures pass/fail findings about deployment infrastructure.
type InfraChecks struct {
	DockerfileExists      bool
	DockerfileMultiStage  bool
	DockerfileHasExpose   bool
	DockerfileHasCMD      bool
	ComposeExists         bool
	ComposeHasServices    bool
	EnvExampleExists      bool
	EnvExampleMissingVars []string // vars found in code but absent from .env.example
	GitignoreExists       bool
	GitignoreCoversEnv    bool
	CIConfigExists        bool
	HealthCheckFound      bool
	LockFileFound         bool
	LockFileMissing       bool
}

// SecretFinding is a single regex hit for a potential hardcoded secret.
type SecretFinding struct {
	Name     string
	Severity string // "critical" | "high" | "medium" | "low"
	File     string
	Line     int
	Excerpt  string // redacted/truncated context, never the full raw secret
}

// Result is the complete output of the analysis pipeline.
type Result struct {
	// Layer 1 — deterministic
	Manifest       FileManifest
	StackInfo      StackInfo
	InfraChecks    InfraChecks
	EnvVars        []string
	SecretFindings []SecretFinding
	ContentHash    string

	// Layer 2 — semantic (Claude)
	Semantic *SemanticReport

	// Layer 3 — generated files (Phase 5)
	GeneratedFiles map[string]string
}

// AnalysisContext is the structured input sent to Claude for semantic
// analysis.
type AnalysisContext struct {
	StackSummary    string
	StackInfo       StackInfo
	InfraChecks     InfraChecks
	FileManifest    []FileEntry
	KeyFileContents map[string]string
	EnvVarsFound    []string
	SecretsFound    []SecretFinding
	TotalFiles      int
}

// ClaudeClient is the interface the analyzer pipeline depends on. Defined on
// the consumer side so internal/claude can implement it without analyzer
// importing claude (which would be a circular dependency the other way).
type ClaudeClient interface {
	Analyze(ctx context.Context, input *AnalysisContext) (*SemanticReport, error)
}

// Risk is a single security or architectural concern raised by semantic analysis.
type Risk struct {
	Description string `json:"description"`
	Severity    string `json:"severity"` // critical | high | medium | low
	File        string `json:"file,omitempty"`
	Line        int    `json:"line,omitempty"`
	Fix         string `json:"fix,omitempty"`
}

// PlatformRec is a ranked hosting platform recommendation.
type PlatformRec struct {
	Name         string   `json:"name"`
	Rank         int      `json:"rank"`
	MonthlyUSD   string   `json:"monthly_usd"`
	InstanceType string   `json:"instance_type"`
	Reasoning    string   `json:"reasoning"`
	DeploySteps  []string `json:"deploy_steps"`
	ConfigFile   string   `json:"config_file,omitempty"`
}

// SemanticReport is Claude's structured assessment of the project.
type SemanticReport struct {
	AuthAssessment string `json:"auth_assessment"`
	SecurityRisks  []Risk `json:"security_risks"`

	ArchitectureSummary string `json:"architecture_summary"`
	ComplexityScore     int    `json:"complexity_score"` // 1-10

	CriticalGaps []string `json:"critical_gaps"`
	Warnings     []string `json:"warnings"`
	Suggestions  []string `json:"suggestions"`

	MinRAMMB         int     `json:"min_ram_mb"`
	RecommendedRAMMB int     `json:"recommended_ram_mb"`
	MinCPU           float64 `json:"min_cpu"`
	StorageGB        int     `json:"storage_gb"`
	EstimatedRPS     int     `json:"estimated_rps"`
	Reasoning        string  `json:"reasoning"`

	Platforms []PlatformRec `json:"platforms"`

	ReadinessSummary string `json:"readiness_summary"`
	ReadinessScore   int    `json:"readiness_score"`
}
