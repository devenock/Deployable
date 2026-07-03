package handlers

import (
	"html/template"
	"log"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"deployable/cache"
)

// Deps holds dependencies shared across all handlers.
type Deps struct {
	Pool    *pgxpool.Pool
	Redis   *cache.Client
	Tmpl    *template.Template
	Version string
}

// Render executes the named template with data, writing HTML to w.
func (d Deps) Render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := d.Tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template render error (%s): %v", name, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}
