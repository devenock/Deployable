package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"deployable/internal/analyzer"
	"deployable/internal/client"
	"deployable/internal/renderer"
)

var (
	apiKey     string
	outputFmt  string
	offline    bool
	apiBaseURL string
	noColor    bool
)

var version = "dev"

func main() {
	root := &cobra.Command{
		Use:   "deployable [path]",
		Short: "Deployment readiness checker for your codebase",
		Long: `Deployable analyzes your codebase and tells you what's missing
before you deploy to production. It checks for security issues,
missing config files, and recommends the best hosting platform.

Examples:
  deployable .                    # analyze current directory
  deployable ./my-app             # analyze specific directory
  deployable . --offline          # deterministic checks only, no AI, no shareable URL
  deployable . --output json      # JSON output for CI/CD pipelines
  deployable . --api-key KEY      # use with your Deployable account for a shareable report URL`,
		Args: cobra.MaximumNArgs(1),
		RunE: runAnalysis,
	}

	root.Flags().StringVar(&apiKey, "api-key", os.Getenv("DEPLOYABLE_API_KEY"), "Deployable API key (or set DEPLOYABLE_API_KEY, or run 'deployable auth set')")
	root.Flags().StringVar(&outputFmt, "output", "text", "Output format: text | json")
	root.Flags().BoolVar(&offline, "offline", false, "Run offline: deterministic checks only, no AI analysis, no shareable URL")
	root.Flags().StringVar(&apiBaseURL, "api-url", envOr("DEPLOYABLE_API_URL", client.DefaultAPIURL), "Deployable API base URL")
	root.Flags().BoolVar(&noColor, "no-color", false, "Disable colored output")

	root.AddCommand(authCmd())
	root.AddCommand(versionCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func runAnalysis(cmd *cobra.Command, args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("directory not found: %s", dir)
	}

	quiet := outputFmt == "json"
	r := renderer.New(!noColor && !quiet)
	if !quiet {
		r.PrintHeader()
	}

	if offline {
		return runOffline(cmd, r, dir, quiet, "")
	}

	key := resolveAPIKey()
	if key == "" {
		return runOffline(cmd, r, dir, quiet, "No API key found — running offline. Run 'deployable auth set <key>' or pass --api-key for a shareable report URL.")
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
	defer cancel()

	c := client.New(apiBaseURL, key)

	if !quiet {
		r.PrintStep("Uploading project to Deployable...")
	}
	jobID, err := c.SubmitDirectory(ctx, dir)
	if err != nil {
		return runOffline(cmd, r, dir, quiet, fmt.Sprintf("Could not reach Deployable API (%v) — falling back to offline analysis.", err))
	}

	if !quiet {
		r.PrintStep("Analyzing with Deployable...")
	}
	summary, _, err := c.PollForResult(ctx, jobID)
	if err != nil {
		return err
	}

	return emit(r, *summary, quiet)
}

// runOffline runs the local deterministic-only pipeline (no network calls),
// used for --offline, for a missing API key, and as a fallback when the API
// is unreachable. warning, if non-empty, is shown first (skipped in JSON mode).
func runOffline(cmd *cobra.Command, r *renderer.Renderer, dir string, quiet bool, warning string) error {
	if !quiet {
		if warning != "" {
			r.PrintWarning(warning)
		}
		r.PrintStep("Running offline analysis...")
	}

	result, err := analyzer.Run(cmd.Context(), dir, nil)
	if err != nil {
		return err
	}

	return emit(r, summaryFromResult(result), quiet)
}

func emit(r *renderer.Renderer, s renderer.Summary, quiet bool) error {
	if quiet {
		return r.PrintJSON(s)
	}
	r.PrintResult(s)
	return nil
}

// summaryFromResult converts a local analyzer.Result (offline mode) into the
// renderer's shared Summary shape. Deterministic infra gaps are folded into
// Warnings since --offline never gets Claude's critical/warning/suggestion
// triage.
func summaryFromResult(result *analyzer.Result) renderer.Summary {
	s := renderer.Summary{
		Language:        result.StackInfo.Language,
		LanguageVersion: result.StackInfo.LanguageVersion,
		Framework:       result.StackInfo.Framework,
		Databases:       result.StackInfo.Databases,
		Warnings:        deterministicWarnings(result.InfraChecks),
	}

	for _, f := range result.SecretFindings {
		s.SecretFindings = append(s.SecretFindings, renderer.SecretFinding{Name: f.Name, Severity: f.Severity, File: f.File})
	}

	if sem := result.Semantic; sem != nil {
		s.ReadinessScore = sem.ReadinessScore
		s.ReadinessSummary = sem.ReadinessSummary
		s.CriticalGaps = sem.CriticalGaps
		s.Warnings = append(s.Warnings, sem.Warnings...)
		s.Suggestions = sem.Suggestions
		s.MinRAMMB = sem.MinRAMMB
		s.RecRAMMB = sem.RecommendedRAMMB
		s.MinCPU = sem.MinCPU
		s.StorageGB = sem.StorageGB
		s.EstRPS = sem.EstimatedRPS
		s.ResourceReasoning = sem.Reasoning
		for _, p := range sem.Platforms {
			s.Platforms = append(s.Platforms, renderer.Platform{Name: p.Name, MonthlyUSD: p.MonthlyUSD, Reasoning: p.Reasoning})
		}
	}

	return s
}

func deterministicWarnings(ic analyzer.InfraChecks) []string {
	var w []string
	if !ic.DockerfileExists {
		w = append(w, "No Dockerfile found")
	}
	if !ic.ComposeExists {
		w = append(w, "No docker-compose.yml found")
	}
	if !ic.EnvExampleExists {
		w = append(w, "No .env.example found")
	} else if len(ic.EnvExampleMissingVars) > 0 {
		w = append(w, fmt.Sprintf("Env vars used in code but missing from .env.example: %s", strings.Join(ic.EnvExampleMissingVars, ", ")))
	}
	if ic.GitignoreExists && !ic.GitignoreCoversEnv {
		w = append(w, ".gitignore does not exclude .env")
	}
	if !ic.CIConfigExists {
		w = append(w, "No CI config found")
	}
	if !ic.HealthCheckFound {
		w = append(w, "No health check endpoint found")
	}
	if ic.LockFileMissing {
		w = append(w, "No dependency lock file found")
	}
	return w
}

// --- auth (saved API key) ----------------------------------------------------

type cliConfig struct {
	APIKey string `json:"api_key"`
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "deployable", "config.json"), nil
}

func resolveAPIKey() string {
	if apiKey != "" {
		return apiKey
	}
	return loadSavedAPIKey()
}

func loadSavedAPIKey() string {
	path, err := configPath()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var cfg cliConfig
	if json.Unmarshal(b, &cfg) != nil {
		return ""
	}
	return cfg.APIKey
}

func saveAPIKey(key string) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cliConfig{APIKey: key}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}

func clearAPIKey() error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func authCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage your saved Deployable API key",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "set <api-key>",
		Short: "Save an API key so you don't need --api-key every run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := saveAPIKey(args[0]); err != nil {
				return err
			}
			fmt.Println("API key saved.")
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show whether an API key is saved",
		RunE: func(cmd *cobra.Command, args []string) error {
			key := loadSavedAPIKey()
			if key == "" {
				fmt.Println("No API key saved. Get one from your Deployable account's Analyze page, then run: deployable auth set <key>")
				return nil
			}
			fmt.Println("API key saved:", maskKey(key))
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "logout",
		Short: "Remove the saved API key",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := clearAPIKey(); err != nil {
				return err
			}
			fmt.Println("API key removed.")
			return nil
		},
	})

	return cmd
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:7] + "…" + key[len(key)-4:]
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("deployable " + version)
			return nil
		},
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
