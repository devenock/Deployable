package handlers

import (
	"net/http"

	"deployable/middleware"
)

// LandingHandler godoc
// @Summary      Landing page
// @Description  Public marketing landing page. Signed-in visitors are redirected to /dashboard instead — the marketing page has nothing for them and shouldn't be reachable once logged in.
// @Tags         web
// @Produce      html
// @Success      200  {string}  string  "HTML page"
// @Success      303  {string}  string  "Signed in — redirects to /dashboard"
// @Router       / [get]
func LandingHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := middleware.UserFromContext(r.Context())
		if user != nil {
			http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
			return
		}
		deps.Render(w, "landing", map[string]any{
			"Title":  "",
			"User":   user,
			"AppURL": deps.AppURL,
		})
	}
}
