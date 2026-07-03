package handlers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"deployable/middleware"
	"deployable/models"
)

// ReportView godoc
// @Summary      View an analysis report
// @Description  Renders the full deployment readiness report for a public slug: readiness score, detected stack, secret findings, infrastructure gaps, Claude's semantic analysis, resource estimates, and platform recommendations. Anonymous-submitted reports expire after 7 days; logged-in users' reports are permanent.
// @Tags         web
// @Produce      html
// @Param        slug  path  string  true  "Report slug"
// @Success      200  {string}  string  "HTML report page"
// @Failure      404  {string}  string  "Unknown or expired report"
// @Router       /report/{slug} [get]
func ReportView(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")

		report, err := models.FindReportBySlug(r.Context(), deps.Pool, slug)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			deps.Render(w, "404", map[string]any{"Title": "Report Not Found"})
			return
		}

		if report.ExpiresAt != nil && report.ExpiresAt.Before(time.Now()) {
			w.WriteHeader(http.StatusNotFound)
			deps.Render(w, "404", map[string]any{"Title": "Report Expired"})
			return
		}

		user, _ := middleware.UserFromContext(r.Context())

		deps.Render(w, "report-index", map[string]any{
			"Title":  "Report",
			"User":   user,
			"Report": report,
		})
	}
}
