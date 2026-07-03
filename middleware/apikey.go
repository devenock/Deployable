package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"deployable/cache"
	"deployable/models"
)

const apiKeyCacheTTL = 5 * time.Minute

type cachedAPIKey struct {
	UserID uuid.UUID `json:"user_id"`
	Plan   string    `json:"plan"`
}

// RequireAPIKey authenticates CLI/API requests via the X-API-Key header.
// The key is SHA-256 hashed, checked against a Redis cache, and falls back
// to a Postgres lookup against users.api_key_hash on a cache miss.
func RequireAPIKey(pool *pgxpool.Pool, rdb *cache.Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := r.Header.Get("X-API-Key")
			if apiKey == "" {
				unauthorized(w)
				return
			}

			sum := sha256.Sum256([]byte(apiKey))
			hash := hex.EncodeToString(sum[:])
			ctx := r.Context()
			cacheKey := "apikey:" + hash

			if raw, err := rdb.Get(ctx, cacheKey); err == nil {
				var ck cachedAPIKey
				if json.Unmarshal([]byte(raw), &ck) == nil {
					user := &models.User{ID: ck.UserID, Plan: ck.Plan}
					next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, UserContextKey, user)))
					return
				}
			}

			var u models.User
			err := pool.QueryRow(ctx, `
				SELECT id, email, name, plan, analyses_count, created_at, updated_at
				FROM users WHERE api_key_hash = $1
			`, hash).Scan(&u.ID, &u.Email, &u.Name, &u.Plan, &u.AnalysesCount, &u.CreatedAt, &u.UpdatedAt)
			if errors.Is(err, pgx.ErrNoRows) {
				unauthorized(w)
				return
			}
			if err != nil {
				unauthorized(w)
				return
			}

			if payload, err := json.Marshal(cachedAPIKey{UserID: u.ID, Plan: u.Plan}); err == nil {
				_ = rdb.Set(ctx, cacheKey, payload, apiKeyCacheTTL)
			}

			next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, UserContextKey, &u)))
		})
	}
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid or missing API key"})
}
