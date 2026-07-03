package handlers

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"deployable/internal/analyzer"
	"deployable/internal/claude"
	"deployable/middleware"
	"deployable/models"
)

// analysisSteps mirrors the 6-step model described in CLAUDE.md's processing
// page spec and internal/analyzer's ProgressFunc callback order.
var analysisSteps = []string{
	"Reading project files",
	"Detecting stack and framework",
	"Scanning for security issues",
	"Analyzing with AI",
	"Estimating resource requirements",
	"Generating deployment files",
}

const (
	// defaultMaxUploadBytes bounds the *source tree* the analyzer reads —
	// node_modules/vendor/.git/dist/build are already excluded from the
	// walk (see walker.go's excludedDirs), so a real project's source
	// rarely gets close to this. It's tight only when those directories
	// are zipped in without exclusion.
	defaultMaxUploadBytes  = 100 * 1024 * 1024 // 100MB
	jobStateTTL            = 2 * time.Hour
	defaultAnalysisTimeout = 180 * time.Second
	anonReportTTLDays      = 7
)

// AnalyzePage godoc
// @Summary      Analyze page
// @Description  Public, rate-limited input page for starting an analysis (zip upload; GitHub/CLI tabs land in Phase 3/4). Works for anonymous visitors — if a session cookie is present the resulting job/report is attributed to that user.
// @Tags         web
// @Produce      html
// @Success      200  {string}  string  "HTML page"
// @Router       /analyze [get]
func AnalyzePage(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := middleware.UserFromContext(r.Context())
		maxBytes := envInt64("MAX_UPLOAD_BYTES", defaultMaxUploadBytes)
		deps.Render(w, "analyze-index", map[string]any{
			"Title":     "Analyze",
			"User":      user,
			"MaxUpload": maxBytes / (1024 * 1024),
		})
	}
}

// AnalyzeZip godoc
// @Summary      Submit a project as a ZIP upload
// @Description  Validates and extracts the uploaded zip (max 100MB by default, configurable via MAX_UPLOAD_BYTES; zip-slip protected), creates an analysis job, and kicks off the analysis pipeline in the background. Responds with an HX-Redirect header to the processing page rather than a body — the client (HTMX) follows it as a full navigation.
// @Tags         web
// @Accept       mpfd
// @Param        file  formData  file  true  "Project source as a .zip archive"
// @Success      200  {string}  string  "HX-Redirect header points to /analyze/{jobID}/processing"
// @Failure      400  {string}  string  "Missing file or wrong extension"
// @Failure      413  {string}  string  "File exceeds the configured upload limit"
// @Router       /analyze/zip [post]
func AnalyzeZip(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		maxBytes := envInt64("MAX_UPLOAD_BYTES", defaultMaxUploadBytes)
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

		if err := r.ParseMultipartForm(32 << 20); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				http.Error(w, fmt.Sprintf(
					"Your zip exceeds the %dMB upload limit. Try excluding node_modules, vendor, .git, dist, or build directories before zipping — they're not needed for analysis anyway.",
					maxBytes/(1024*1024),
				), http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Invalid or corrupted zip upload", http.StatusBadRequest)
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "Missing file", http.StatusBadRequest)
			return
		}
		defer file.Close()

		if !strings.EqualFold(filepath.Ext(header.Filename), ".zip") {
			http.Error(w, "File must be a .zip archive", http.StatusBadRequest)
			return
		}

		var userID *uuid.UUID
		if user, ok := middleware.UserFromContext(r.Context()); ok {
			userID = &user.ID
		}

		job, err := models.CreateJob(r.Context(), deps.Pool, userID, "zip", header.Filename, clientIP(r))
		if err != nil {
			log.Printf("create job: %v", err)
			http.Error(w, "could not start analysis", http.StatusInternalServerError)
			return
		}

		extractDir := filepath.Join(tmpDir(), job.ID.String())
		if err := os.MkdirAll(extractDir, 0755); err != nil {
			log.Printf("mkdir extract dir: %v", err)
			http.Error(w, "could not start analysis", http.StatusInternalServerError)
			return
		}

		if err := extractZip(file, header.Size, extractDir, maxBytes); err != nil {
			_ = os.RemoveAll(extractDir)
			log.Printf("extract zip for job %s: %v", job.ID, err)
			http.Error(w, "invalid zip file", http.StatusBadRequest)
			return
		}

		analysisRoot := normalizeExtractRoot(extractDir)

		setJobStatus(context.Background(), deps, job.ID, "pending")
		go runAnalysisPipeline(deps, job.ID, userID, extractDir, analysisRoot)

		w.Header().Set("HX-Redirect", "/analyze/"+job.ID.String()+"/processing")
		w.WriteHeader(http.StatusOK)
	}
}

// ProcessingPage godoc
// @Summary      Analysis processing page
// @Description  Renders the live-progress page for a running job; the page itself polls GET /analyze/{jobID}/status every 2 seconds via HTMX.
// @Tags         web
// @Produce      html
// @Param        jobID  path  string  true  "Analysis job ID"
// @Success      200  {string}  string  "HTML page"
// @Failure      404  {string}  string  "Unknown job"
// @Router       /analyze/{jobID}/processing [get]
func ProcessingPage(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID, err := uuid.Parse(chi.URLParam(r, "jobID"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		job, err := models.FindJobByID(r.Context(), deps.Pool, jobID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		deps.Render(w, "analyze-processing", map[string]any{
			"Title":       "Analyzing",
			"JobID":       job.ID.String(),
			"Steps":       analysisSteps,
			"CurrentStep": job.CurrentStep,
			"StepMessage": job.StepMessage,
		})
	}
}

// AnalyzeStatus godoc
// @Summary      Poll analysis job status
// @Description  HTMX polling target — returns an HTML partial reflecting current progress. Once the job completes, responds with an HX-Redirect header pointing at the report page instead of a body.
// @Tags         web
// @Produce      html
// @Param        jobID  path  string  true  "Analysis job ID"
// @Success      200  {string}  string  "Progress partial, or HX-Redirect to /report/{slug} once complete"
// @Failure      404  {string}  string  "Unknown job"
// @Router       /analyze/{jobID}/status [get]
func AnalyzeStatus(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID, err := uuid.Parse(chi.URLParam(r, "jobID"))
		if err != nil {
			http.NotFound(w, r)
			return
		}

		status := getJobStatus(r.Context(), deps, jobID)
		step, _, message := getJobProgress(r.Context(), deps, jobID)

		if status == "" {
			job, err := models.FindJobByID(r.Context(), deps.Pool, jobID)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			status = job.Status
			step = job.CurrentStep
			message = job.StepMessage
		}

		if status == "complete" {
			if slug, ok := reportSlugForJob(r.Context(), deps, jobID); ok {
				w.Header().Set("HX-Redirect", "/report/"+slug)
				w.WriteHeader(http.StatusOK)
				return
			}
		}

		deps.Render(w, "analyze-steps", map[string]any{
			"Steps":       analysisSteps,
			"CurrentStep": step,
			"StepMessage": message,
			"Failed":      status == "failed",
		})
	}
}

// --- zip extraction -------------------------------------------------------

// normalizeExtractRoot handles the common case where an archive wraps the
// entire project in a single top-level directory (e.g. `zip -r out.zip
// myproject/`, or a GitHub codeball). If dir contains exactly one entry and
// it's a directory, that subdirectory is treated as the real project root —
// otherwise dir itself is returned unchanged.
func normalizeExtractRoot(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return dir
	}
	return filepath.Join(dir, entries[0].Name())
}

// extractZip safely extracts a zip archive to destDir, rejecting path
// traversal (zip-slip) and enforcing an overall decompressed-size cap.
func extractZip(src io.ReaderAt, size int64, destDir string, maxBytes int64) error {
	zr, err := zip.NewReader(src, size)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}

	cleanDest := filepath.Clean(destDir)
	var totalBytes int64
	const decompressionCapMultiplier = 4 // guard against zip bombs

	for _, f := range zr.File {
		cleanName := filepath.Clean(f.Name)
		if strings.HasPrefix(cleanName, "..") || filepath.IsAbs(cleanName) {
			return fmt.Errorf("unsafe path in zip: %s", f.Name)
		}
		destPath := filepath.Join(cleanDest, cleanName)
		if destPath != cleanDest && !strings.HasPrefix(destPath, cleanDest+string(os.PathSeparator)) {
			return fmt.Errorf("zip path escapes destination: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return err
			}
			continue
		}

		totalBytes += int64(f.UncompressedSize64)
		if totalBytes > maxBytes*decompressionCapMultiplier {
			return fmt.Errorf("zip contents exceed size limit")
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}

		if err := extractZipFile(f, destPath, maxBytes); err != nil {
			return err
		}
	}
	return nil
}

func extractZipFile(f *zip.File, destPath string, maxBytes int64) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, io.LimitReader(rc, maxBytes))
	return err
}

// --- pipeline orchestration ------------------------------------------------

// analysisSem bounds how many analyses run concurrently, to avoid memory
// exhaustion from simultaneous large zip extractions (ARCHITECTURE.md §11).
// Sized from MAX_CONCURRENT_ANALYSES at package init.
var analysisSem = make(chan struct{}, int(envInt64("MAX_CONCURRENT_ANALYSES", 10)))

// runAnalysisPipeline analyzes analysisRoot (which may be a subdirectory of
// extractDir — see normalizeExtractRoot) and removes extractDir (the whole
// upload, not just the analysis root) once done.
func runAnalysisPipeline(deps Deps, jobID uuid.UUID, userID *uuid.UUID, extractDir, analysisRoot string) {
	defer os.RemoveAll(extractDir)

	analysisSem <- struct{}{}
	defer func() { <-analysisSem }()

	ctx, cancel := context.WithTimeout(context.Background(), analysisTimeout())
	defer cancel()

	setJobStatus(ctx, deps, jobID, "running")

	dir := analysisRoot
	manifest, err := analyzer.WalkFiles(dir)
	if err != nil {
		failJob(ctx, deps, jobID, "could not read project files")
		return
	}

	if cached, err := models.FindReportByContentHash(ctx, deps.Pool, manifest.ContentHash()); err == nil {
		setJobProgress(ctx, deps, jobID, len(analysisSteps), "Using cached analysis")
		completeJobWithReport(ctx, deps, jobID, cached)
		return
	}

	onProgress := func(step int, message string) {
		_ = models.MarkJobRunning(ctx, deps.Pool, jobID, step, message)
		setJobProgress(ctx, deps, jobID, step, message)
	}

	var claudeClient analyzer.ClaudeClient
	if strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != "" {
		claudeClient = claude.NewClient()
	}

	result, err := analyzer.RunWithProgress(ctx, dir, claudeClient, onProgress)
	if err != nil {
		log.Printf("analysis pipeline failed for job %s: %v", jobID, err)
		failJob(ctx, deps, jobID, "analysis failed, please try again")
		return
	}

	report, err := saveReport(ctx, deps, jobID, userID, result)
	if err != nil {
		log.Printf("save report failed for job %s: %v", jobID, err)
		failJob(ctx, deps, jobID, "could not save report")
		return
	}

	completeJobWithReport(ctx, deps, jobID, report)
}

func saveReport(ctx context.Context, deps Deps, jobID uuid.UUID, userID *uuid.UUID, result *analyzer.Result) (*models.Report, error) {
	sem := result.Semantic

	// map[string]any's value type is `any`, so these nested structs/slices
	// don't need pre-flattening — pgx's JSONB encoder calls json.Marshal on
	// the whole tree, which handles arbitrary nested Go values natively.
	deterministic := map[string]any{
		"stack_info":      result.StackInfo,
		"infra_checks":    result.InfraChecks,
		"env_vars":        result.EnvVars,
		"secret_findings": result.SecretFindings,
	}

	// Report.SemanticAnalysis and Report.Platforms are statically typed as
	// map[string]any / []any, so these two (unlike the map above) do need a
	// JSON round-trip to fit the field type.
	semanticJSON := map[string]any{}
	platforms := []any{}
	if sem != nil {
		semanticJSON = structToMap(sem)
		if p := sliceToAny(sem.Platforms); p != nil {
			platforms = p
		}
	}

	var expiresAt *time.Time
	isPublic := true
	if userID == nil {
		t := time.Now().Add(anonReportTTLDays * 24 * time.Hour)
		expiresAt = &t
	}

	draft := &models.Report{
		JobID:                 jobID,
		UserID:                userID,
		IsPublic:              isPublic,
		Language:              result.StackInfo.Language,
		LanguageVersion:       result.StackInfo.LanguageVersion,
		Framework:             result.StackInfo.Framework,
		Databases:             result.StackInfo.Databases,
		Services:              []string{},
		SecurityScore:         computeSecurityScore(result.SecretFindings),
		DeterministicFindings: deterministic,
		SemanticAnalysis:      semanticJSON,
		Platforms:             platforms,
		GeneratedFiles:        result.GeneratedFiles,
		ContentHash:           result.ContentHash,
		ExpiresAt:             expiresAt,
	}

	if sem != nil {
		draft.ReadinessScore = sem.ReadinessScore
		draft.ComplexityScore = sem.ComplexityScore
		draft.MinRAMMB = intPtr(sem.MinRAMMB)
		draft.RecRAMMB = intPtr(sem.RecommendedRAMMB)
		draft.MinCPU = float64Ptr(sem.MinCPU)
		draft.StorageGB = intPtr(sem.StorageGB)
		draft.EstRPS = intPtr(sem.EstimatedRPS)
		draft.ResourceReasoning = sem.Reasoning
	}

	return models.CreateReport(ctx, deps.Pool, draft)
}

func computeSecurityScore(findings []analyzer.SecretFinding) int {
	score := 100
	for _, f := range findings {
		switch f.Severity {
		case "critical":
			score -= 30
		case "high":
			score -= 15
		case "medium":
			score -= 8
		default:
			score -= 3
		}
	}
	if score < 0 {
		score = 0
	}
	return score
}

func completeJobWithReport(ctx context.Context, deps Deps, jobID uuid.UUID, report *models.Report) {
	if err := models.MarkJobComplete(ctx, deps.Pool, jobID); err != nil {
		log.Printf("mark job complete: %v", err)
	}
	setJobStatus(ctx, deps, jobID, "complete")
	// Needed for the content-hash dedup path, where report.JobID is the
	// original job that first produced the report, not this one — without
	// this, AnalyzeStatus's FindReportByJobID fallback would never find it.
	_ = deps.Redis.Set(ctx, "job:"+jobID.String()+":report_id", report.ID.String(), jobStateTTL)
}

// reportSlugForJob resolves the report slug to redirect to once a job
// completes: the Redis report_id set by completeJobWithReport (works for
// both the normal and content-hash-dedup paths) falling back to a direct
// job_id lookup in Postgres if that key expired or was never set.
func reportSlugForJob(ctx context.Context, deps Deps, jobID uuid.UUID) (string, bool) {
	if reportIDStr, err := deps.Redis.Get(ctx, "job:"+jobID.String()+":report_id"); err == nil && reportIDStr != "" {
		if reportID, err := uuid.Parse(reportIDStr); err == nil {
			if report, err := models.FindReportByID(ctx, deps.Pool, reportID); err == nil {
				return report.Slug, true
			}
		}
	}
	if report, err := models.FindReportByJobID(ctx, deps.Pool, jobID); err == nil {
		return report.Slug, true
	}
	return "", false
}

func failJob(ctx context.Context, deps Deps, jobID uuid.UUID, msg string) {
	if err := models.MarkJobFailed(ctx, deps.Pool, jobID, msg); err != nil {
		log.Printf("mark job failed: %v", err)
	}
	setJobStatus(ctx, deps, jobID, "failed")
	setJobProgress(ctx, deps, jobID, 0, msg)
}

// --- Redis job state --------------------------------------------------------

func setJobStatus(ctx context.Context, deps Deps, jobID uuid.UUID, status string) {
	_ = deps.Redis.Set(ctx, "job:"+jobID.String()+":status", status, jobStateTTL)
}

func getJobStatus(ctx context.Context, deps Deps, jobID uuid.UUID) string {
	s, _ := deps.Redis.Get(ctx, "job:"+jobID.String()+":status")
	return s
}

func setJobProgress(ctx context.Context, deps Deps, jobID uuid.UUID, step int, message string) {
	payload, err := json.Marshal(map[string]any{
		"step": step, "total": len(analysisSteps), "message": message,
	})
	if err != nil {
		return
	}
	_ = deps.Redis.Set(ctx, "job:"+jobID.String()+":progress", payload, jobStateTTL)
}

func getJobProgress(ctx context.Context, deps Deps, jobID uuid.UUID) (step int, total int, message string) {
	raw, err := deps.Redis.Get(ctx, "job:"+jobID.String()+":progress")
	if err != nil {
		return 0, 0, ""
	}
	var state struct {
		Step    int    `json:"step"`
		Total   int    `json:"total"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(raw), &state) != nil {
		return 0, 0, ""
	}
	return state.Step, state.Total, state.Message
}

// --- small helpers -----------------------------------------------------------

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	return r.RemoteAddr
}

func tmpDir() string {
	if d := os.Getenv("TMP_DIR"); d != "" {
		return d
	}
	return os.TempDir()
}

func analysisTimeout() time.Duration {
	secs := envInt64("ANALYSIS_TIMEOUT_SECONDS", int64(defaultAnalysisTimeout/time.Second))
	return time.Duration(secs) * time.Second
}

func envInt64(key string, fallback int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

func intPtr(v int) *int             { return &v }
func float64Ptr(v float64) *float64 { return &v }

// structToMap round-trips a value through JSON to get a plain
// map[string]any / []any tree suitable for a JSONB column — simplest way to
// reuse the analyzer types' existing json tags without hand-writing a mirror
// struct.
func structToMap(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return map[string]any{}
	}
	return m
}

// sliceToAny round-trips a typed slice through JSON to get a plain []any
// suitable for a JSONB column.
func sliceToAny(v any) []any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out []any
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}
