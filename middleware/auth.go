package middleware

import (
	"context"
	"encoding/json"
	"errors"
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
			cookie, err := r.Cookie("session_id")
			if err != nil || cookie.Value == "" {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			sessionID := cookie.Value
			ctx := r.Context()

			cacheKey := "session:" + sessionID
			if raw, err := rdb.Get(ctx, cacheKey); err == nil {
				var cs cachedSession
				if json.Unmarshal([]byte(raw), &cs) == nil {
					user := &models.User{ID: cs.UserID, Email: cs.Email, Plan: cs.Plan}
					next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, UserContextKey, user)))
					return
				}
			}

			session, err := models.FindSession(ctx, pool, sessionID)
			if err != nil || session.ExpiresAt.Before(time.Now()) {
				if err != nil && !errors.Is(err, models.ErrNotFound) {
					// DB error: still fail closed to /login rather than 500
				}
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			user, err := models.FindUserByID(ctx, pool, session.UserID)
			if err != nil {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			if payload, err := json.Marshal(cachedSession{UserID: user.ID, Email: user.Email, Plan: user.Plan}); err == nil {
				_ = rdb.Set(ctx, cacheKey, payload, sessionCacheTTL)
			}

			next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, UserContextKey, user)))
		})
	}
}
