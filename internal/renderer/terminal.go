// Package renderer formats analysis results for the CLI's terminal output.
// It knows nothing about how the data was obtained (offline pipeline vs.
// the API) — both paths convert into the shared Summary shape defined here.
package renderer

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Summary is the renderer's input shape — populated either from a local
// analyzer.Result (offline mode) or from the API's report JSON (online mode).
type Summary struct {
	Language        string   `json:"language"`
	LanguageVersion string   `json:"language_version,omitempty"`
	Framework       string   `json:"framework,omitempty"`
	Databases       []string `json:"databases,omitempty"`

	ReadinessScore   int    `json:"readiness_score"`
	SecurityScore    int    `json:"security_score,omitempty"`
	ReadinessSummary string `json:"readiness_summary,omitempty"`

	CriticalGaps []string `json:"critical_gaps,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
	Suggestions  []string `json:"suggestions,omitempty"`

	SecretFindings []SecretFinding `json:"secret_findings,omitempty"`

	MinRAMMB          int     `json:"min_ram_mb,omitempty"`
	RecRAMMB          int     `json:"rec_ram_mb,omitempty"`
	MinCPU            float64 `json:"min_cpu,omitempty"`
	StorageGB         int     `json:"storage_gb,omitempty"`
	EstRPS            int     `json:"est_rps,omitempty"`
	ResourceReasoning string  `json:"resource_reasoning,omitempty"`

	Platforms []Platform `json:"platforms,omitempty"`

	// ReportURL is set when the CLI submitted via the API with an API key
	// (a shareable report was created); empty in --offline mode.
	ReportURL string `json:"report_url,omitempty"`
}

// SecretFinding is one flagged hardcoded secret.
type SecretFinding struct {
	Name     string `json:"name"`
	Severity string `json:"severity"`
	File     string `json:"file,omitempty"`
}

// Platform is one ranked deployment target recommendation.
type Platform struct {
	Name       string `json:"name"`
	MonthlyUSD string `json:"monthly_usd,omitempty"`
	Reasoning  string `json:"reasoning,omitempty"`
}

// Renderer writes colored (or plain, if color is disabled) terminal output.
type Renderer struct {
	out   io.Writer
	color bool
}

// New builds a Renderer. color controls whether ANSI styling is applied —
// callers should disable it for --no-color or when stdout isn't a TTY.
func New(color bool) *Renderer {
	return &Renderer{out: os.Stdout, color: color}
}

func (r *Renderer) style(fg string, bold bool) lipgloss.Style {
	s := lipgloss.NewStyle()
	if !r.color {
		return s
	}
	if fg != "" {
		s = s.Foreground(lipgloss.Color(fg))
	}
	return s.Bold(bold)
}

const (
	colorBrand   = "#8b5cf6"
	colorGreen   = "#22c55e"
	colorYellow  = "#eab308"
	colorOrange  = "#f97316"
	colorRed     = "#ef4444"
	colorGray    = "#6b7280"
	colorDefault = ""
)

// PrintHeader prints the CLI banner.
func (r *Renderer) PrintHeader() {
	fmt.Fprintln(r.out, r.style(colorBrand, true).Render("Deployable")+r.style(colorGray, false).Render(" — deployment readiness checker"))
	fmt.Fprintln(r.out)
}

// PrintStep prints a single in-progress status line.
func (r *Renderer) PrintStep(msg string) {
	fmt.Fprintln(r.out, r.style(colorGray, false).Render("→")+" "+msg)
}

// PrintWarning prints a non-fatal warning (e.g. falling back to offline mode).
func (r *Renderer) PrintWarning(msg string) {
	fmt.Fprintln(r.out, r.style(colorYellow, true).Render("⚠")+"  "+msg)
}

// PrintError prints a fatal error.
func (r *Renderer) PrintError(msg string) {
	fmt.Fprintln(r.out, r.style(colorRed, true).Render("✗")+"  "+msg)
}

func scoreColor(score int) string {
	switch {
	case score >= 80:
		return colorGreen
	case score >= 60:
		return colorYellow
	case score >= 40:
		return colorOrange
	default:
		return colorRed
	}
}

func severityColor(sev string) string {
	switch sev {
	case "critical":
		return colorRed
	case "high":
		return colorOrange
	case "medium":
		return colorYellow
	default:
		return colorGray
	}
}

// PrintResult renders the full terminal report.
func (r *Renderer) PrintResult(s Summary) {
	fmt.Fprintln(r.out)

	scoreLine := fmt.Sprintf("Readiness Score: %s", r.style(scoreColor(s.ReadinessScore), true).Render(fmt.Sprintf("%d/100", s.ReadinessScore)))
	fmt.Fprintln(r.out, scoreLine)

	stack := s.Language
	if s.LanguageVersion != "" {
		stack += " " + s.LanguageVersion
	}
	if s.Framework != "" {
		stack += " · " + s.Framework
	}
	if len(s.Databases) > 0 {
		stack += " · " + strings.Join(s.Databases, ", ")
	}
	if stack != "" {
		fmt.Fprintln(r.out, r.style(colorGray, false).Render(stack))
	}

	if s.ReadinessSummary != "" {
		fmt.Fprintln(r.out)
		fmt.Fprintln(r.out, s.ReadinessSummary)
	}

	r.printList("Critical Issues", colorRed, "✗", s.CriticalGaps)
	r.printList("Warnings", colorYellow, "⚠", s.Warnings)
	r.printList("Suggestions", colorBrand, "•", s.Suggestions)

	if len(s.SecretFindings) > 0 {
		fmt.Fprintln(r.out)
		fmt.Fprintln(r.out, r.style(colorDefault, true).Render("Secrets found:"))
		for _, f := range s.SecretFindings {
			line := fmt.Sprintf("  %s %s", r.style(severityColor(f.Severity), true).Render("["+f.Severity+"]"), f.Name)
			if f.File != "" {
				line += r.style(colorGray, false).Render(" — " + f.File)
			}
			fmt.Fprintln(r.out, line)
		}
	}

	if s.RecRAMMB > 0 || s.MinRAMMB > 0 {
		fmt.Fprintln(r.out)
		fmt.Fprintln(r.out, r.style(colorDefault, true).Render("Resource Estimates"))
		fmt.Fprintf(r.out, "  Minimum:     %dMB RAM · %.1f vCPU · %dGB storage\n", s.MinRAMMB, s.MinCPU, s.StorageGB)
		fmt.Fprintf(r.out, "  Recommended: %dMB RAM\n", s.RecRAMMB)
		if s.EstRPS > 0 {
			fmt.Fprintf(r.out, "  Estimated:   ~%d req/s\n", s.EstRPS)
		}
		if s.ResourceReasoning != "" {
			fmt.Fprintln(r.out, r.style(colorGray, false).Render("  "+s.ResourceReasoning))
		}
	}

	if len(s.Platforms) > 0 {
		fmt.Fprintln(r.out)
		fmt.Fprintln(r.out, r.style(colorDefault, true).Render("Deploy To"))
		for i, p := range s.Platforms {
			cost := p.MonthlyUSD
			if cost == "" {
				cost = "—"
			}
			fmt.Fprintf(r.out, "  %d. %-12s %-10s %s\n", i+1, p.Name, cost, r.style(colorGray, false).Render(p.Reasoning))
		}
	}

	fmt.Fprintln(r.out)
	if s.ReportURL != "" {
		fmt.Fprintln(r.out, r.style(colorBrand, true).Render("Full report: ")+s.ReportURL)
	} else {
		fmt.Fprintln(r.out, r.style(colorGray, false).Render("Run with --api-key to get a shareable report URL."))
	}
}

func (r *Renderer) printList(title, color, bullet string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, r.style(color, true).Render(fmt.Sprintf("%s (%d)", title, len(items))))
	for _, item := range items {
		fmt.Fprintf(r.out, "  %s %s\n", r.style(color, false).Render(bullet), item)
	}
}

// PrintJSON marshals v as indented JSON to stdout — used for --output json.
func (r *Renderer) PrintJSON(v any) error {
	enc := json.NewEncoder(r.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
