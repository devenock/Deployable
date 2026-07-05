package analyzer

import (
	"context"
	"fmt"
)

const keyFileMaxBytes = 2000

// ProgressFunc reports pipeline step progress: a 1-indexed step number and a
// short human-readable message, matching the 6-step model shown on the
// processing page.
type ProgressFunc func(step int, message string)

// Run executes the full deterministic + semantic analysis pipeline on a
// directory with no progress reporting. See RunWithProgress.
func Run(ctx context.Context, dir string, claudeClient ClaudeClient) (*Result, error) {
	return RunWithProgress(ctx, dir, claudeClient, func(int, string) {})
}

// RunWithProgress is Run, additionally invoking onProgress before each of
// the 6 pipeline steps so a caller can surface live status (e.g. via
// Redis-backed HTMX polling). claudeClient == nil skips semantic analysis
// (offline mode); a non-nil client whose call fails degrades gracefully to a
// deterministic fallback score rather than failing the whole analysis.
func RunWithProgress(ctx context.Context, dir string, claudeClient ClaudeClient, onProgress ProgressFunc) (*Result, error) {
	result := &Result{}

	onProgress(1, "Reading project files")
	manifest, err := WalkFiles(dir)
	if err != nil {
		return nil, fmt.Errorf("file walk failed: %w", err)
	}
	result.Manifest = manifest
	result.ContentHash = manifest.ContentHash(func(done, total int) {
		onProgress(1, fmt.Sprintf("Reading project files (%d/%d)", done, total))
	})

	onProgress(2, "Detecting stack and framework")
	result.StackInfo = DetectStack(manifest)
	result.InfraChecks = CheckInfra(manifest, dir)
	result.EnvVars = ExtractEnvVars(manifest, dir)

	onProgress(3, "Scanning for security issues")
	result.SecretFindings = ScanSecrets(manifest, dir, func(done, total int) {
		onProgress(3, fmt.Sprintf("Scanning for security issues (%d/%d)", done, total))
	})
	applyEnvExampleCrossReference(&result.InfraChecks, manifest, dir, result.EnvVars)

	onProgress(4, "Analyzing with AI")
	if claudeClient != nil {
		semCtx := BuildClaudeContext(result, dir)
		semantic, err := claudeClient.Analyze(ctx, semCtx)
		if err != nil {
			result.Semantic = &SemanticReport{
				ReadinessSummary: "Semantic analysis unavailable: " + err.Error(),
				ReadinessScore:   estimateScoreFromDeterministic(result),
			}
		} else {
			result.Semantic = semantic
		}
	} else {
		result.Semantic = &SemanticReport{
			ReadinessSummary: "Semantic analysis skipped (offline mode).",
			ReadinessScore:   estimateScoreFromDeterministic(result),
		}
	}

	onProgress(5, "Estimating resource requirements")
	// Resource estimates are populated as part of the semantic report above;
	// this step exists to keep processing-page progress aligned with the
	// documented 6-step model.

	onProgress(6, "Generating deployment files")
	// Actual file generation happens in the caller (internal/generator),
	// which imports analyzer's types — analyzer can't import generator
	// without creating a cycle. GeneratedFiles is populated by the caller
	// right after RunWithProgress returns.

	return result, nil
}

func applyEnvExampleCrossReference(checks *InfraChecks, manifest FileManifest, dir string, envVars []string) {
	if !checks.EnvExampleExists {
		return
	}
	set := newFileSet(manifest)
	name := firstExisting(set, ".env.example", ".env.sample", ".env.dist")
	content, ok := readRoot(dir, name)
	if !ok {
		return
	}
	known := map[string]bool{}
	for _, k := range ParseEnvExampleKeys(content) {
		known[k] = true
	}
	for _, v := range envVars {
		if !known[v] {
			checks.EnvExampleMissingVars = append(checks.EnvExampleMissingVars, v)
		}
	}
}

// BuildClaudeContext assembles the structured input sent to Claude:
// deterministic findings plus a handful of key file contents, each
// truncated to keyFileMaxBytes.
func BuildClaudeContext(result *Result, dir string) *AnalysisContext {
	stackSummary := result.StackInfo.Language
	if result.StackInfo.Framework != "" {
		stackSummary += " (" + result.StackInfo.Framework + ")"
	}
	if len(result.StackInfo.Databases) > 0 {
		stackSummary += " with "
		for i, db := range result.StackInfo.Databases {
			if i > 0 {
				stackSummary += ", "
			}
			stackSummary += db
		}
	}

	keyFiles := map[string]string{}
	for _, candidate := range keyFileCandidates(result.StackInfo) {
		if content, err := ReadFileCapped(dir, candidate, keyFileMaxBytes); err == nil && content != "" {
			keyFiles[candidate] = content
		}
	}

	return &AnalysisContext{
		StackSummary:    stackSummary,
		StackInfo:       result.StackInfo,
		InfraChecks:     result.InfraChecks,
		FileManifest:    result.Manifest.Files,
		KeyFileContents: keyFiles,
		EnvVarsFound:    result.EnvVars,
		SecretsFound:    result.SecretFindings,
		TotalFiles:      result.Manifest.TotalFiles,
	}
}

func keyFileCandidates(stack StackInfo) []string {
	candidates := []string{"Dockerfile", "docker-compose.yml", "README.md"}
	if stack.EntryPoint != "" {
		candidates = append([]string{stack.EntryPoint}, candidates...)
	}
	return candidates
}

// estimateScoreFromDeterministic produces a fallback readiness score (0-100)
// from Layer 1 findings alone, used when semantic analysis is unavailable.
func estimateScoreFromDeterministic(result *Result) int {
	score := 100

	for _, f := range result.SecretFindings {
		switch f.Severity {
		case "critical":
			score -= 25
		case "high":
			score -= 15
		case "medium":
			score -= 8
		default:
			score -= 3
		}
	}

	checks := result.InfraChecks
	if !checks.DockerfileExists {
		score -= 10
	}
	if !checks.GitignoreExists || !checks.GitignoreCoversEnv {
		score -= 10
	}
	if !checks.CIConfigExists {
		score -= 5
	}
	if !checks.HealthCheckFound {
		score -= 10
	}
	if checks.LockFileMissing {
		score -= 5
	}
	if len(checks.EnvExampleMissingVars) > 0 {
		score -= 5
	}

	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return score
}
