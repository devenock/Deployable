package handlers

import (
	"net/http"

	assets "deployable"
)

// DocsHandler serves a self-contained Swagger UI page for exploring and
// testing the API surface, backed by the spec at /static/openapi.yaml.
func DocsHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, err := assets.Files.ReadFile("static/docs.html")
		if err != nil {
			http.Error(w, "docs unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	}
}
