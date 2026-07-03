package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"deployable/cache"
	"deployable/models"
)

type contextKey string

// UserContextKey is the context key the authenticated user is stored under.
const UserContextKey contextKey = "user"

const sessionCacheTTL = 5 * time.Minute

type cachedSession struct {
	UserID uuid.UUID `json:"user_id"`
	Email  string    `json:"email"`
	Plan   string    `json:"plan"`
}

// UserFromContext returns the authenticated user attached by RequireAuth.
func UserFromContext(ctx context.Context) (*models.User, bool) {
	u, ok := ctx.Value(UserContextKey).(*models.User)
	return u, ok
}

// RequireAuth reads the session_id cookie, validates it against Redis (fast
// path) then Postgres (fallback), and attaches the resolved user to the
// request context. Invalid or missing sessions redirect to /login.
func RequireAuth(pool *pgxpool.Pool, rdb *cache.Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := resolveSessionUser(r.Context(), pool, rdb, r)
			if !ok {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), UserContextKey, user)))
		})
	}
}

// OptionalAuth resolves the session_id cookie the same way RequireAuth does,
// but never blocks the request — anonymous visitors pass through with no
// user in context. Used for routes like /analyze that work for both
// anonymous and logged-in visitors (rate limits and report ownership differ
// based on whether a user is present).
func OptionalAuth(pool *pgxpool.Pool, rdb *cache.Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if user, ok := resolveSessionUser(r.Context(), pool, rdb, r); ok {
				r = r.WithContext(context.WithValue(r.Context(), UserContextKey, user))
			}
			next.ServeHTTP(w, r)
		})
	}
}

func resolveSessionUser(ctx context.Context, pool *pgxpool.Pool, rdb *cache.Client, r *http.Request) (*models.User, bool) {
	cookie, err := r.Cookie("session_id")
	if err != nil || cookie.Value == "" {
		return nil, false
	}
	sessionID := cookie.Value

	cacheKey := "session:" + sessionID
	if raw, err := rdb.Get(ctx, cacheKey); err == nil {
		var cs cachedSession
		if json.Unmarshal([]byte(raw), &cs) == nil {
			return &models.User{ID: cs.UserID, Email: cs.Email, Plan: cs.Plan}, true
		}
	}

	session, err := models.FindSession(ctx, pool, sessionID)
	if err != nil || session.ExpiresAt.Before(time.Now()) {
		return nil, false
	}

	user, err := models.FindUserByID(ctx, pool, session.UserID)
	if err != nil {
		return nil, false
	}

	if payload, err := json.Marshal(cachedSession{UserID: user.ID, Email: user.Email, Plan: user.Plan}); err == nil {
		_ = rdb.Set(ctx, cacheKey, payload, sessionCacheTTL)
	}

	return user, true
}
