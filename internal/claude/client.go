// Package claude wraps the official Anthropic Go SDK for the deployment
// readiness semantic analysis call (Layer 2 of the analysis pipeline).
package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"deployable/internal/analyzer"
)

const (
	defaultModel    = "claude-opus-4-8"
	maxTokens       = 8192
	requestTimeout  = 120 * time.Second
	keyFileMaxChars = 2000
)

// Client implements analyzer.ClaudeClient using the official Anthropic SDK.
type Client struct {
	sdk   anthropic.Client
	model string
}

// NewClient builds a Client from ANTHROPIC_API_KEY (required) and
// ANTHROPIC_MODEL (optional, defaults to claude-opus-4-8).
func NewClient() *Client {
	model := strings.TrimSpace(os.Getenv("ANTHROPIC_MODEL"))
	if model == "" {
		model = defaultModel
	}

	return &Client{
		sdk: anthropic.NewClient(
			option.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")),
			option.WithRequestTimeout(requestTimeout),
		),
		model: model,
	}
}

// Analyze sends deterministic findings and a handful of key file snippets to
// Claude and parses the structured SemanticReport it returns. Implements
// analyzer.ClaudeClient.
func (c *Client) Analyze(ctx context.Context, input *analyzer.AnalysisContext) (*analyzer.SemanticReport, error) {
	resp, err := c.sdk.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: maxTokens,
		Thinking:  anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{}},
		System: []anthropic.TextBlockParam{
			{Text: systemPrompt()},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(buildPrompt(input))),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("claude api call failed: %w", err)
	}

	raw := extractText(resp)
	if raw == "" {
		return nil, fmt.Errorf("empty text response from claude")
	}
	raw = stripCodeFences(raw)

	var report analyzer.SemanticReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		return nil, fmt.Errorf("failed to parse claude JSON: %w", err)
	}

	return &report, nil
}

// extractText returns the first (and expected only) text block in the
// response, skipping any thinking blocks that precede it.
func extractText(msg *anthropic.Message) string {
	for _, block := range msg.Content {
		if text, ok := block.AsAny().(anthropic.TextBlock); ok {
			return text.Text
		}
	}
	return ""
}

// stripCodeFences defensively removes a ```json ... ``` wrapper in case the
// model wraps its answer despite instructions not to.
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func systemPrompt() string {
	return "You are a senior DevOps engineer and cloud architect. You analyze codebases and " +
		"provide structured deployment readiness assessments. Respond with ONLY a single valid " +
		"JSON object matching the exact schema in the user's message — no explanations, no " +
		"markdown, no code fences, no text before or after the JSON object."
}

// buildPrompt assembles the structured analysis request: deterministic
// findings from Layer 1, a handful of key file contents (each truncated to
// keyFileMaxChars), and the exact JSON schema Claude must fill in.
func buildPrompt(ctx *analyzer.AnalysisContext) string {
	var b strings.Builder

	b.WriteString("Analyze this codebase for production deployment readiness.\n\n")

	b.WriteString("## Detected Stack\n")
	b.WriteString(ctx.StackSummary)
	b.WriteString("\n\n")

	b.WriteString("## Infrastructure Checks (Layer 1, deterministic)\n")
	infra := ctx.InfraChecks
	fmt.Fprintf(&b, "- Dockerfile: exists=%t multi-stage=%t has-EXPOSE=%t has-CMD=%t\n",
		infra.DockerfileExists, infra.DockerfileMultiStage, infra.DockerfileHasExpose, infra.DockerfileHasCMD)
	fmt.Fprintf(&b, "- docker-compose: exists=%t has-services=%t\n", infra.ComposeExists, infra.ComposeHasServices)
	fmt.Fprintf(&b, "- .env.example: exists=%t missing-vars=%s\n", infra.EnvExampleExists, strings.Join(infra.EnvExampleMissingVars, ", "))
	fmt.Fprintf(&b, "- .gitignore: exists=%t covers-.env=%t\n", infra.GitignoreExists, infra.GitignoreCoversEnv)
	fmt.Fprintf(&b, "- CI config: exists=%t\n", infra.CIConfigExists)
	fmt.Fprintf(&b, "- Health check endpoint found: %t\n", infra.HealthCheckFound)
	fmt.Fprintf(&b, "- Lock file present: %t\n", infra.LockFileFound)
	b.WriteString("\n")

	b.WriteString("## Environment Variables Referenced In Code\n")
	if len(ctx.EnvVarsFound) == 0 {
		b.WriteString("(none found)\n")
	} else {
		b.WriteString(strings.Join(ctx.EnvVarsFound, ", "))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	b.WriteString("## Secret Scan Findings (Layer 1, regex — values already redacted)\n")
	if len(ctx.SecretsFound) == 0 {
		b.WriteString("(none found)\n")
	} else {
		for _, f := range ctx.SecretsFound {
			fmt.Fprintf(&b, "- [%s] %s at %s:%d — %s\n", f.Severity, f.Name, f.File, f.Line, f.Excerpt)
		}
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "## Project size: %d files total\n\n", ctx.TotalFiles)

	if len(ctx.KeyFileContents) > 0 {
		b.WriteString("## Key File Contents (truncated)\n")
		for path, content := range ctx.KeyFileContents {
			truncated := content
			if len(truncated) > keyFileMaxChars {
				truncated = truncated[:keyFileMaxChars] + "\n...(truncated)"
			}
			fmt.Fprintf(&b, "### %s\n```\n%s\n```\n\n", path, truncated)
		}
	}

	b.WriteString("## Required JSON Schema\n")
	b.WriteString("Return exactly this shape, with every field populated (use empty arrays/strings, never omit a key):\n\n")
	b.WriteString(schemaDescription())

	return b.String()
}

func schemaDescription() string {
	return `{
  "auth_assessment": string — which routes/handlers have or lack authentication,
  "security_risks": [{"description": string, "severity": "critical"|"high"|"medium"|"low", "file": string, "line": int, "fix": string}],
  "architecture_summary": string — 2-3 sentences on what this app is and how it's structured,
  "complexity_score": int (1-10),
  "critical_gaps": [string] — must fix before deploying,
  "warnings": [string] — should fix,
  "suggestions": [string] — nice to have,
  "min_ram_mb": int,
  "recommended_ram_mb": int,
  "min_cpu": float,
  "storage_gb": int,
  "estimated_rps": int,
  "reasoning": string — plain English explanation of the resource estimates,
  "platforms": [{"name": string, "rank": int, "monthly_usd": string, "instance_type": string, "reasoning": string, "deploy_steps": [string], "config_file": string}],
  "readiness_summary": string — 2-3 sentence human summary,
  "readiness_score": int (0-100)
}`
}
