package handlers

import "net/http"

// AnalyzePage godoc
// @Summary      Analyze page (stub)
// @Description  Requires a valid session_id cookie (checked against Redis first, Postgres on cache miss). Full zip/GitHub/CLI input handling and the processing pipeline land in Phase 2 — this stub proves auth and routing work end to end.
// @Tags         web
// @Success      501  {string}  string  "not implemented"
// @Failure      303  {string}  string  "No valid session — redirects to /login"
// @Router       /analyze [get]
func AnalyzePage(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not implemented", http.StatusNotImplemented)
	}
}
