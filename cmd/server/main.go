package main

import (
	"context"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	httpSwagger "github.com/swaggo/http-swagger"

	assets "deployable"
	"deployable/cache"
	"deployable/db"
	_ "deployable/docs"
	"deployable/handlers"
	"deployable/middleware"
)

var embeddedFiles = assets.Files

var version = "dev"

// @title                       Deployable API
// @version                     0.1.0-phase1
// @description                 Deployment Readiness Platform — Phase 1 (Foundation) API surface. Endpoints marked 501 are intentional stubs; full implementations land in later phases (see the description on each operation for which phase).
// @contact.name                Deployable
// @host                        localhost:8080
// @BasePath                    /
// @schemes                     http https
// @securityDefinitions.apikey  ApiKeyAuth
// @in                          header
// @name                        X-API-Key
// @description                 Per-user API key (Phase 4 issues these). SHA-256 hash is checked against users.api_key_hash. Session-cookie-protected web routes (/analyze, /dashboard, /logout) are documented per-operation since Swagger 2.0 has no cookie security scheme.
func main() {
	_ = godotenv.Load()

	// Validate required env vars — fail loudly
	required := []string{
		"DATABASE_URL", "REDIS_URL", "SECRET_KEY",
		"ENCRYPTION_KEY", "ANTHROPIC_API_KEY",
	}
	for _, key := range required {
		if strings.TrimSpace(os.Getenv(key)) == "" {
			log.Fatalf("FATAL: required env var %s is not set", key)
		}
	}

	ctx := context.Background()

	// Connect Postgres + run migrations
	pool, err := db.Connect(ctx)
	if err != nil {
		log.Fatalf("Database setup failed: %v", err)
	}
	defer pool.Close()

	// Connect Redis
	rdb, err := cache.Connect(os.Getenv("REDIS_URL"))
	if err != nil {
		log.Fatalf("Redis setup failed: %v", err)
	}
	defer rdb.Close()

	// Ensure data directories
	for _, dir := range []string{
		os.Getenv("UPLOADS_DIR"),
		os.Getenv("TMP_DIR"),
	} {
		if dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				log.Fatalf("Failed to create dir %s: %v", dir, err)
			}
		}
	}

	// Parse templates
	tmpl, err := template.New("").Funcs(templateFuncs()).ParseFS(
		embeddedFiles, "templates/**/*.html", "templates/*.html",
	)
	if err != nil {
		log.Fatalf("Template parse failed: %v", err)
	}

	// Init handlers
	deps := handlers.Deps{
		Pool:    pool,
		Redis:   rdb,
		Tmpl:    tmpl,
		Version: version,
	}

	// Router
	r := chi.NewRouter()
	r.Use(chimiddleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.Timeout(180 * time.Second))
	r.Use(chimiddleware.StripSlashes)

	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		deps.Render(w, "404", map[string]any{"Title": "Not Found"})
	})

	// Static files (embedded)
	r.Handle("/static/*", http.FileServer(http.FS(embeddedFiles)))

	// Health check
	r.Get("/health", handlers.HealthHandler(pool, rdb, version))

	// API docs (Swagger UI, generated from handler annotations via swaggo/swag)
	r.Get("/docs/*", httpSwagger.Handler(httpSwagger.URL("/docs/doc.json")))

	// Public routes
	r.Get("/", handlers.LandingHandler(deps))
	r.Get("/login", handlers.LoginPage(deps))
	r.Post("/login", handlers.Login(deps))
	r.Get("/register", handlers.RegisterPage(deps))
	r.Post("/register", handlers.Register(deps))
	r.Post("/logout", handlers.Logout(deps))

	// Protected routes
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAuth(pool, rdb))
		r.Get("/analyze", handlers.AnalyzePage(deps))
		r.Get("/dashboard", handlers.Dashboard(deps))
	})

	// REST API for CLI
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(middleware.RequireAPIKey(pool, rdb))
		r.Use(middleware.RateLimit(rdb))
		r.Post("/analyze", handlers.APIAnalyze(deps))
		r.Get("/analyze/{jobID}", handlers.APIAnalyzeStatus(deps))
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 200 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("Deployable v%s running on :%s", version, port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down gracefully...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
	log.Println("Shutdown complete")
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"lower": strings.ToLower,
		"upper": strings.ToUpper,
		"join":  strings.Join,
		"add":   func(a, b int) int { return a + b },
		"sub":   func(a, b int) int { return a - b },
		"mul":   func(a, b int) int { return a * b },
		"scoreColor": func(score int) string {
			switch {
			case score >= 80:
				return "text-green-400"
			case score >= 60:
				return "text-yellow-400"
			case score >= 40:
				return "text-orange-400"
			default:
				return "text-red-400"
			}
		},
		"severityColor": func(sev string) string {
			switch sev {
			case "critical":
				return "bg-red-900 text-red-200 border-red-700"
			case "high":
				return "bg-orange-900 text-orange-200 border-orange-700"
			case "medium":
				return "bg-yellow-900 text-yellow-200 border-yellow-700"
			default:
				return "bg-gray-800 text-gray-300 border-gray-600"
			}
		},
		"timeAgo": timeAgo,
		"safeURL": func(s string) template.URL { return template.URL(s) },
	}
}

func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h ago"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + "d ago"
	}
}
