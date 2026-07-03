package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"deployable/cache"
)

// HealthHandler pings Postgres and Redis and reports overall status.
func HealthHandler(pool *pgxpool.Pool, rdb *cache.Client, version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		postgresStatus := "ok"
		if err := pool.Ping(ctx); err != nil {
			postgresStatus = "error"
		}

		redisStatus := "ok"
		if err := rdb.Ping(ctx); err != nil {
			redisStatus = "error"
		}

		status := "ok"
		httpStatus := http.StatusOK
		if postgresStatus != "ok" || redisStatus != "ok" {
			status = "degraded"
			httpStatus = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(httpStatus)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":   status,
			"postgres": postgresStatus,
			"redis":    redisStatus,
			"version":  version,
		})
	}
}
