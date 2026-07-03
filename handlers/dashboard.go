package handlers

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"deployable/middleware"
	"deployable/models"
)

const dashboardPageSize = 20

// Dashboard godoc
// @Summary      Dashboard — your reports
// @Description  Requires a session cookie. Lists the caller's reports, most recent first, with search (matches source, language, or framework) and pagination. Requests carrying the HX-Request header (search/pagination) get just the results partial; everything else gets the full page.
// @Tags         web
// @Produce      html
// @Param        search  query  string  false  "Filter by source, language, or framework"
// @Param        page    query  int     false  "Page number, 1-indexed"
// @Success      200  {string}  string  "HTML page or partial"
// @Failure      303  {string}  string  "No valid session — redirects to /login"
// @Router       /dashboard [get]
func Dashboard(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := middleware.UserFromContext(r.Context())

		search := strings.TrimSpace(r.URL.Query().Get("search"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		offset := (page - 1) * dashboardPageSize

		reports, total, err := models.ListReportsByUser(r.Context(), deps.Pool, user.ID, search, dashboardPageSize, offset)
		if err != nil {
			log.Printf("list reports for dashboard: %v", err)
			http.Error(w, "could not load dashboard", http.StatusInternalServerError)
			return
		}

		totalPages := (total + dashboardPageSize - 1) / dashboardPageSize
		if totalPages < 1 {
			totalPages = 1
		}

		data := map[string]any{
			"Title":      "Dashboard",
			"User":       user,
			"Reports":    reports,
			"Search":     search,
			"Page":       page,
			"TotalPages": totalPages,
			"Total":      total,
			"HasPrev":    page > 1,
			"HasNext":    page < totalPages,
		}

		if r.Header.Get("HX-Request") == "true" {
			deps.Render(w, "dashboard-results", data)
			return
		}

		_, hasGitHub := githubClientForUser(deps, r, user.ID)
		connectedRepos, err := models.ListConnectedRepos(r.Context(), deps.Pool, user.ID)
		if err != nil {
			log.Printf("list connected repos for dashboard: %v", err)
		}
		data["HasGitHub"] = hasGitHub
		data["ConnectedRepos"] = connectedRepos
		data["ActiveNav"] = "dashboard"

		deps.Render(w, "dashboard-index", data)
	}
}
