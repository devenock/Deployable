package handlers

import (
	"bytes"
	"html/template"
	"log"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"

	"deployable/cache"
	"deployable/internal/mailer"
)

// Deps holds dependencies shared across all handlers.
type Deps struct {
	Pool        *pgxpool.Pool
	Redis       *cache.Client
	Tmpl        *template.Template
	Version     string
	Mailer      mailer.Mailer
	AppURL      string
	GitHubOAuth *oauth2.Config
	GoogleOAuth *oauth2.Config
}

// Render executes the named template with data, writing HTML to w.
func (d Deps) Render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := d.Tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template render error (%s): %v", name, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// RenderString executes the named template with data and returns the result
// as a string, for building email bodies rather than writing to an
// http.ResponseWriter.
func (d Deps) RenderString(name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := d.Tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
