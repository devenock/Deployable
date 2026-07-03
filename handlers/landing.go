package handlers

import "net/http"

// LandingHandler renders the public marketing landing page.
func LandingHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deps.Render(w, "landing", map[string]any{
			"Title": "",
		})
	}
}
