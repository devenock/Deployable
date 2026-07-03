package handlers

import (
	"log"
	"net/http"

	"deployable/middleware"
	"deployable/models"
)

// GenerateAPIKey godoc
// @Summary      Generate (or regenerate) an API key
// @Description  Requires a session_id cookie. Creates a new random API key for the CLI, invalidating any previous one, and returns an HTML partial showing it once — it is never displayed again after this response. Lands on the /analyze page's CLI section.
// @Tags         web
// @Produce      html
// @Success      200  {string}  string  "HTML partial with the raw key"
// @Failure      303  {string}  string  "No valid session — redirects to /login"
// @Router       /account/api-key [post]
func GenerateAPIKey(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := middleware.UserFromContext(r.Context())
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		rawKey, err := models.GenerateAPIKey(r.Context(), deps.Pool, user.ID)
		if err != nil {
			log.Printf("generate api key: %v", err)
			http.Error(w, "could not generate API key", http.StatusInternalServerError)
			return
		}

		deps.Render(w, "api-key-generated", map[string]any{"APIKey": rawKey})
	}
}
