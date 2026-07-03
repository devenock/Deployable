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
// @Description  Requires a session cookie. Three sections in one shell, switched via ?tab= (or in-page via the sidebar, no navigation): "dashboard" (default) is the stats/connected-repos overview, "analyze" embeds the same analyze-form the standalone /analyze page uses, "reports" is the searchable, paginated report list. Requests carrying the HX-Request header (search/pagination within Reports) get just the results partial; everything else gets the full page.
// @Tags         web
// @Produce      html
// @Param        search  query  string  false  "Filter by source, language, or framework (Reports section)"
// @Param        page    query  int     false  "Page number, 1-indexed (Reports section)"
// @Param        tab     query  string  false  "'analyze' or 'reports' shows that section; anything else (default) shows the overview"
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

		connectedRepos, err := models.ListConnectedRepos(r.Context(), deps.Pool, user.ID)
		if err != nil {
			log.Printf("list connected repos for dashboard: %v", err)
		}
		githubAccounts, err := models.ListGitHubAccounts(r.Context(), deps.Pool, user.ID)
		if err != nil {
			log.Printf("list github accounts for dashboard: %v", err)
		}
		stats, err := models.GetReportStats(r.Context(), deps.Pool, user.ID)
		if err != nil {
			log.Printf("get report stats for dashboard: %v", err)
			stats = &models.ReportStats{}
		}

		activeNav := "dashboard"
		switch {
		case r.URL.Query().Get("tab") == "analyze":
			activeNav = "analyze"
		case r.URL.Query().Get("tab") == "reports" || search != "":
			activeNav = "reports"
		}

		// The dashboard's Analyze section embeds the same "analyze-form"
		// partial the standalone /analyze page uses, so it needs the same
		// render data (dropzone limits, GitHub/API-key state, etc). Merge it
		// in without clobbering the dashboard's own keys (Reports, User, ...).
		for k, v := range analyzeFormData(deps, r, user) {
			if _, exists := data[k]; !exists {
				data[k] = v
			}
		}
		data["ConnectedRepos"] = connectedRepos
		data["GitHubAccounts"] = githubAccounts
		data["Stats"] = stats
		data["ActiveNav"] = activeNav

		deps.Render(w, "dashboard-index", data)
	}
}
