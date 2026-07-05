package handlers

import (
	"archive/zip"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	ghclient "deployable/internal/github"
	"deployable/middleware"
	"deployable/models"
)

// ReportView godoc
// @Summary      View an analysis report
// @Description  Renders the deployment readiness report for a public slug. Anyone can view the score and detected stack; the full report (findings, resources, platforms, files) requires being logged in — as any user, not just the report's owner, so shared links still work between registered users. Anonymous-submitted reports expire after 7 days; logged-in users' reports are permanent. Owners additionally see rescan (GitHub-sourced only) and delete actions.
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
		isOwner := user != nil && report.UserID != nil && *report.UserID == user.ID

		inputType := ""
		inputRef := ""
		if job, err := models.FindJobByID(r.Context(), deps.Pool, report.JobID); err == nil {
			inputType = job.InputType
			inputRef = job.InputRef
		}

		title := "Report"
		if inputRef != "" {
			title = inputRef + " · Report"
		}

		// Highlights "Reports" in the sidebar for signed-in visitors, so
		// landing here from the reports list (or a rescan/analyze-again)
		// doesn't feel like it dropped them somewhere untethered from the
		// rest of the app.
		activeNav := ""
		if user != nil {
			activeNav = "reports"
		}

		deps.Render(w, "report-index", map[string]any{
			"Title":         title,
			"User":          user,
			"AppURL":        deps.AppURL,
			"Report":        report,
			"IsOwner":       isOwner,
			"InputType":     inputType,
			"InputRef":      inputRef,
			"ActiveNav":     activeNav,
			"Authenticated": user != nil,
		})
	}
}

// ReportDownload godoc
// @Summary      Download a report's generated deployment files
// @Description  Zips the report's generated_files (Dockerfile, docker-compose.yml, .env.example, CI workflow, DEPLOYMENT.md, and platform config where applicable) and streams it as an attachment. Requires being logged in (as any user, same rule as viewing the full report) — no additional ownership check beyond that.
// @Tags         web
// @Produce      application/zip
// @Param        slug  path  string  true  "Report slug"
// @Success      200  {file}    file    "deployable-<slug>.zip"
// @Failure      401  {string}  string  "Sign in to download"
// @Failure      404  {string}  string  "Unknown/expired report, or no generated files"
// @Router       /report/{slug}/download [get]
func ReportDownload(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if user, _ := middleware.UserFromContext(r.Context()); user == nil {
			http.Error(w, "sign in to download this report's generated files", http.StatusUnauthorized)
			return
		}

		slug := chi.URLParam(r, "slug")

		report, err := models.FindReportBySlug(r.Context(), deps.Pool, slug)
		if err != nil || (report.ExpiresAt != nil && report.ExpiresAt.Before(time.Now())) {
			http.NotFound(w, r)
			return
		}
		if len(report.GeneratedFiles) == 0 {
			http.Error(w, "no generated files for this report", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="deployable-%s.zip"`, slug))

		zw := zip.NewWriter(w)
		for name, content := range report.GeneratedFiles {
			f, err := zw.Create(name)
			if err != nil {
				log.Printf("create zip entry %s for report %s: %v", name, slug, err)
				continue
			}
			if _, err := f.Write([]byte(content)); err != nil {
				log.Printf("write zip entry %s for report %s: %v", name, slug, err)
			}
		}
		if err := zw.Close(); err != nil {
			log.Printf("close zip for report %s: %v", slug, err)
		}
	}
}

// ReportRescan godoc
// @Summary      Re-run analysis for a GitHub-sourced report
// @Description  Requires a session cookie and ownership of the report. Only GitHub-sourced reports can be rescanned (re-fetching a URL is cheap and idempotent) — ZIP-sourced reports aren't, since the original upload isn't retained after analysis. Also requires the source repo to still be on the requester's watchlist (see startGitHubAnalysis) — if it was removed since the report was created, re-import it first. Re-fetches the same repository and kicks off a fresh analysis job, identical to submitting the URL again.
// @Tags         web
// @Produce      html
// @Param        slug  path  string  true  "Report slug"
// @Success      200  {string}  string  "HX-Redirect header points to /analyze/{jobID}/processing"
// @Failure      403  {string}  string  "Not the owner, not a GitHub-sourced report, or the repo is no longer on the watchlist"
// @Failure      404  {string}  string  "Unknown report"
// @Failure      429  {string}  string  "Rate limit exceeded"
// @Router       /report/{slug}/rescan [post]
func ReportRescan(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := middleware.UserFromContext(r.Context())
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		slug := chi.URLParam(r, "slug")
		report, err := models.FindReportBySlug(r.Context(), deps.Pool, slug)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if report.UserID == nil || *report.UserID != user.ID {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		job, err := models.FindJobByID(r.Context(), deps.Pool, report.JobID)
		if err != nil || job.InputType != "github" {
			http.Error(w, "this report can't be rescanned — only GitHub-sourced reports support rescan", http.StatusForbidden)
			return
		}

		owner, repo, err := ghclient.ParseRepoURL(job.InputRef)
		if err != nil {
			http.Error(w, "could not determine the source repository for this report", http.StatusInternalServerError)
			return
		}

		newJob, status, msg := startGitHubAnalysis(r.Context(), deps, &user.ID, owner, repo, clientIP(r))
		if status != 0 {
			http.Error(w, msg, status)
			return
		}

		w.Header().Set("HX-Redirect", "/analyze/"+newJob.ID.String()+"/processing")
		w.WriteHeader(http.StatusOK)
	}
}

// ReportDelete godoc
// @Summary      Delete a report
// @Description  Requires a session cookie and ownership of the report. A report that doesn't exist and one that isn't yours are indistinguishable (both 404) to avoid leaking which slugs exist.
// @Tags         web
// @Param        slug  path  string  true  "Report slug"
// @Success      200  {string}  string  "Deleted"
// @Failure      404  {string}  string  "Unknown report, or not the owner"
// @Router       /report/{slug} [delete]
func ReportDelete(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := middleware.UserFromContext(r.Context())
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		slug := chi.URLParam(r, "slug")
		report, err := models.FindReportBySlug(r.Context(), deps.Pool, slug)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		if err := models.DeleteReport(r.Context(), deps.Pool, report.ID, user.ID); err != nil {
			http.NotFound(w, r)
			return
		}

		w.WriteHeader(http.StatusOK)
	}
}
