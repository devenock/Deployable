package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"deployable/middleware"
	"deployable/models"
)

// APIAnalyze godoc
// @Summary      Submit a project as a ZIP upload
// @Description  Requires an X-API-Key header. Accepts the same multipart .zip upload as the web /analyze/zip endpoint (max 100MB by default, configurable via MAX_UPLOAD_BYTES) and kicks off the same analysis pipeline, attributed to the API key's owner. Subject to Redis-backed rate limiting (default 5/hour).
// @Tags         api
// @Security     ApiKeyAuth
// @Accept       mpfd
// @Produce      json
// @Param        file  formData  file  true  "Project source as a .zip archive"
// @Success      202  {object}  APIJobCreated
// @Failure      400  {object}  ErrorResponse
// @Failure      401  {object}  ErrorResponse
// @Failure      413  {object}  ErrorResponse
// @Failure      429  {object}  ErrorResponse
// @Router       /api/v1/analyze [post]
func APIAnalyze(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := middleware.UserFromContext(r.Context()) // guaranteed by RequireAPIKey
		userID := &user.ID

		job, status, msg := acceptZipUpload(w, r, deps, userID)
		if status != 0 {
			writeJSONError(w, status, msg)
			return
		}

		writeJSON(w, http.StatusAccepted, APIJobCreated{JobID: job.ID.String()})
	}
}

// APIAnalyzeStatus godoc
// @Summary      Get analysis job status
// @Description  Requires an X-API-Key header. Polls the same Redis-backed progress state the web processing page uses. Once status is "complete", report_slug is set — fetch the full result from GET /api/v1/report/{slug}.
// @Tags         api
// @Security     ApiKeyAuth
// @Produce      json
// @Param        jobID  path  string  true  "Analysis job ID"
// @Success      200  {object}  APIJobStatus
// @Failure      401  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Failure      429  {object}  ErrorResponse
// @Router       /api/v1/analyze/{jobID} [get]
func APIAnalyzeStatus(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID, err := uuid.Parse(chi.URLParam(r, "jobID"))
		if err != nil {
			writeJSONError(w, http.StatusNotFound, "unknown job")
			return
		}

		status := getJobStatus(r.Context(), deps, jobID)
		step, total, message := getJobProgress(r.Context(), deps, jobID)

		if status == "" {
			job, err := models.FindJobByID(r.Context(), deps.Pool, jobID)
			if err != nil {
				writeJSONError(w, http.StatusNotFound, "unknown job")
				return
			}
			status = job.Status
			step = job.CurrentStep
			total = job.TotalSteps
			message = job.StepMessage
		}
		if total == 0 {
			total = len(analysisSteps)
		}

		resp := APIJobStatus{JobID: jobID.String(), Status: status, Step: step, TotalSteps: total, Message: message}
		if status == "complete" {
			if slug, ok := reportSlugForJob(r.Context(), deps, jobID); ok {
				resp.ReportSlug = slug
			}
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// APIReport godoc
// @Summary      Get a report as JSON
// @Description  Requires an X-API-Key header. Returns the same data as the web report page (/report/{slug}) in a machine-readable shape — used by the CLI to render its terminal summary.
// @Tags         api
// @Security     ApiKeyAuth
// @Produce      json
// @Param        slug  path  string  true  "Report slug"
// @Success      200  {object}  APIReportPayload
// @Failure      401  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Failure      429  {object}  ErrorResponse
// @Router       /api/v1/report/{slug} [get]
func APIReport(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")

		report, err := models.FindReportBySlug(r.Context(), deps.Pool, slug)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, "report not found")
			return
		}

		writeJSON(w, http.StatusOK, buildAPIReportPayload(deps, report))
	}
}

// --- response shapes ---------------------------------------------------------

// APIJobCreated is returned by POST /api/v1/analyze.
type APIJobCreated struct {
	JobID string `json:"job_id"`
}

// APIJobStatus is returned by GET /api/v1/analyze/{jobID}.
type APIJobStatus struct {
	JobID      string `json:"job_id"`
	Status     string `json:"status"`
	Step       int    `json:"step"`
	TotalSteps int    `json:"total_steps"`
	Message    string `json:"message"`
	ReportSlug string `json:"report_slug,omitempty"`
}

// APIReportPayload is returned by GET /api/v1/report/{slug}.
type APIReportPayload struct {
	Slug            string   `json:"slug"`
	URL             string   `json:"url"`
	Language        string   `json:"language"`
	LanguageVersion string   `json:"language_version"`
	Framework       string   `json:"framework"`
	Databases       []string `json:"databases"`

	ReadinessScore  int `json:"readiness_score"`
	ComplexityScore int `json:"complexity_score"`
	SecurityScore   int `json:"security_score"`

	ReadinessSummary string   `json:"readiness_summary"`
	CriticalGaps     []string `json:"critical_gaps"`
	Warnings         []string `json:"warnings"`
	Suggestions      []string `json:"suggestions"`

	MinRAMMB          *int     `json:"min_ram_mb,omitempty"`
	RecRAMMB          *int     `json:"rec_ram_mb,omitempty"`
	MinCPU            *float64 `json:"min_cpu,omitempty"`
	StorageGB         *int     `json:"storage_gb,omitempty"`
	EstRPS            *int     `json:"est_rps,omitempty"`
	ResourceReasoning string   `json:"resource_reasoning"`

	Platforms      []any    `json:"platforms"`
	GeneratedFiles []string `json:"generated_files"`
}

func buildAPIReportPayload(deps Deps, report *models.Report) APIReportPayload {
	var sem struct {
		ReadinessSummary string   `json:"readiness_summary"`
		CriticalGaps     []string `json:"critical_gaps"`
		Warnings         []string `json:"warnings"`
		Suggestions      []string `json:"suggestions"`
	}
	if b, err := json.Marshal(report.SemanticAnalysis); err == nil {
		_ = json.Unmarshal(b, &sem)
	}

	files := make([]string, 0, len(report.GeneratedFiles))
	for name := range report.GeneratedFiles {
		files = append(files, name)
	}

	return APIReportPayload{
		Slug:              report.Slug,
		URL:               deps.AppURL + "/report/" + report.Slug,
		Language:          report.Language,
		LanguageVersion:   report.LanguageVersion,
		Framework:         report.Framework,
		Databases:         report.Databases,
		ReadinessScore:    report.ReadinessScore,
		ComplexityScore:   report.ComplexityScore,
		SecurityScore:     report.SecurityScore,
		ReadinessSummary:  sem.ReadinessSummary,
		CriticalGaps:      sem.CriticalGaps,
		Warnings:          sem.Warnings,
		Suggestions:       sem.Suggestions,
		MinRAMMB:          report.MinRAMMB,
		RecRAMMB:          report.RecRAMMB,
		MinCPU:            report.MinCPU,
		StorageGB:         report.StorageGB,
		EstRPS:            report.EstRPS,
		ResourceReasoning: report.ResourceReasoning,
		Platforms:         report.Platforms,
		GeneratedFiles:    files,
	}
}

// --- small helpers -----------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ErrorResponse{Error: msg})
}
