package handlers

import "net/http"

// Dashboard godoc
// @Summary      Dashboard (stub)
// @Description  Requires a valid session_id cookie. Saved reports list lands in Phase 5.
// @Tags         web
// @Success      501  {string}  string  "not implemented"
// @Failure      303  {string}  string  "No valid session — redirects to /login"
// @Router       /dashboard [get]
func Dashboard(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not implemented", http.StatusNotImplemented)
	}
}
