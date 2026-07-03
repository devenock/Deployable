package handlers

import "net/http"

// LandingHandler godoc
// @Summary      Landing page
// @Description  Public marketing landing page.
// @Tags         web
// @Produce      html
// @Success      200  {string}  string  "HTML page"
// @Router       / [get]
func LandingHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deps.Render(w, "landing", map[string]any{
			"Title": "",
		})
	}
}
