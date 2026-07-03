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
	"golang.org/x/oauth2"
	oauthgithub "golang.org/x/oauth2/github"
	oauthgoogle "golang.org/x/oauth2/google"

	assets "deployable"
	"deployable/cache"
	"deployable/db"
	_ "deployable/docs"
	"deployable/handlers"
	"deployable/internal/mailer"
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

	// Mailer (SMTP via Mailtrap in dev; falls back to console logging if
	// SMTP_HOST/SMTP_USERNAME are unset)
	mail := mailer.New(mailer.ConfigFromEnv())

	appURL := os.Getenv("APP_URL")
	if appURL == "" {
		appURL = "http://localhost:" + envOr("PORT", "8080")
	}

	// Init handlers
	deps := handlers.Deps{
		Pool:        pool,
		Redis:       rdb,
		Tmpl:        tmpl,
		Version:     version,
		Mailer:      mail,
		AppURL:      appURL,
		GitHubOAuth: newOAuthConfig(oauthgithub.Endpoint, "GITHUB_CLIENT_ID", "GITHUB_CLIENT_SECRET", "GITHUB_REDIRECT_URL", []string{"read:user", "user:email"}),
		GoogleOAuth: newOAuthConfig(oauthgoogle.Endpoint, "GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "GOOGLE_REDIRECT_URL", []string{"openid", "email", "profile"}),
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
	r.With(middleware.OptionalAuth(pool, rdb)).Get("/", handlers.LandingHandler(deps))
	r.Get("/login", handlers.LoginPage(deps))
	r.Post("/login", handlers.Login(deps))
	r.Get("/register", handlers.RegisterPage(deps))
	r.Post("/register", handlers.Register(deps))
	r.Post("/logout", handlers.Logout(deps))
	r.Get("/verify-email", handlers.VerifyEmailPage(deps))
	r.Post("/verify-email", handlers.VerifyEmail(deps))
	r.Post("/resend-otp", handlers.ResendOTP(deps))
	r.Get("/forgot-password", handlers.ForgotPasswordPage(deps))
	r.Post("/forgot-password", handlers.ForgotPassword(deps))
	r.Get("/reset-password", handlers.ResetPasswordPage(deps))
	r.Post("/reset-password", handlers.ResetPassword(deps))

	// OAuth login
	r.Get("/auth/github", handlers.GitHubOAuthStart(deps))
	// Wrapped in OptionalAuth (not just plain) so GitHubOAuthCallback can see
	// the logged-in user for the "connect GitHub for repo access" flow —
	// GitHubConnectStart below requires a session, so a user is always
	// present in context by the time intent=="connect" reaches the callback.
	r.With(middleware.OptionalAuth(pool, rdb)).Get("/auth/github/callback", handlers.GitHubOAuthCallback(deps))
	r.Get("/auth/google", handlers.GoogleOAuthStart(deps))
	r.Get("/auth/google/callback", handlers.GoogleOAuthCallback(deps))
	r.With(middleware.RequireAuth(pool, rdb)).Get("/auth/github/connect", handlers.GitHubConnectStart(deps))

	// Public report view. OptionalAuth so the nav shows signed-in state and
	// ReportView can compute ownership (for the rescan/delete actions).
	r.Group(func(r chi.Router) {
		r.Use(middleware.OptionalAuth(pool, rdb))
		r.Get("/report/{slug}", handlers.ReportView(deps))
		r.Get("/report/{slug}/download", handlers.ReportDownload(deps))
		r.With(middleware.RequireAuth(pool, rdb), middleware.RateLimit(rdb)).Post("/report/{slug}/rescan", handlers.ReportRescan(deps))
		r.With(middleware.RequireAuth(pool, rdb)).Delete("/report/{slug}", handlers.ReportDelete(deps))
	})

	// Analyze — public (anonymous analysis is allowed per ARCHITECTURE.md).
	// OptionalAuth attaches a user to the job/report when a session cookie
	// happens to be present, without requiring one. RateLimit is applied
	// only to the action that starts a new analysis (POST /analyze/zip) —
	// not to viewing the page or polling an already-started job's status,
	// which HTMX does every 2s and would otherwise exhaust the hourly quota
	// within seconds of a single analysis starting.
	r.Group(func(r chi.Router) {
		r.Use(middleware.OptionalAuth(pool, rdb))
		r.Get("/analyze", handlers.AnalyzePage(deps))
		r.With(middleware.RateLimit(rdb)).Post("/analyze/zip", handlers.AnalyzeZip(deps))
		r.With(middleware.RateLimit(rdb)).Post("/analyze/github", handlers.AnalyzeGitHub(deps))
		r.Get("/analyze/{jobID}/status", handlers.AnalyzeStatus(deps))
		r.Get("/analyze/{jobID}/processing", handlers.ProcessingPage(deps))
	})

	// Protected routes
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAuth(pool, rdb))
		r.Get("/dashboard", handlers.Dashboard(deps))
		r.Post("/account/api-key", handlers.GenerateAPIKey(deps))
	})

	// REST API for CLI — same rationale as the web /analyze group: only the
	// action that starts a new analysis is rate-limited, not status polling.
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(middleware.RequireAPIKey(pool, rdb))
		r.With(middleware.RateLimit(rdb)).Post("/analyze", handlers.APIAnalyze(deps))
		r.Get("/analyze/{jobID}", handlers.APIAnalyzeStatus(deps))
		r.Get("/report/{slug}", handlers.APIReport(deps))
	})

	// CLI install script.
	r.Get("/install.sh", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/x-shellscript")
		http.ServeFileFS(w, r, assets.InstallScript, "scripts/install.sh")
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
		"lower":       strings.ToLower,
		"upper":       strings.ToUpper,
		"join":        strings.Join,
		"add":         func(a, b int) int { return a + b },
		"sub":         func(a, b int) int { return a - b },
		"mul":         func(a, b int) int { return a * b },
		"currentYear": func() int { return time.Now().Year() },
		"list":        func(items ...string) []string { return items },
		"fileID": func(name string) string {
			return strings.NewReplacer("/", "-", ".", "-").Replace(name)
		},
		"percent": func(cur, total int) int {
			if total <= 0 {
				return 0
			}
			p := cur * 100 / total
			if p > 100 {
				return 100
			}
			return p
		},
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

// newOAuthConfig builds an oauth2.Config from env vars. ClientID/Secret are
// left empty (and the config effectively disabled — see handlers.oauthStart)
// if not yet provisioned.
func newOAuthConfig(endpoint oauth2.Endpoint, clientIDKey, clientSecretKey, redirectURLKey string, scopes []string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     os.Getenv(clientIDKey),
		ClientSecret: os.Getenv(clientSecretKey),
		RedirectURL:  os.Getenv(redirectURLKey),
		Endpoint:     endpoint,
		Scopes:       scopes,
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
