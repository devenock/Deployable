package middleware

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"deployable/cache"
)

const rateLimitWindow = time.Hour

// RateLimit enforces a per-IP request cap using a Redis INCR + EXPIRE
// sliding window keyed on the X-Real-IP header (set by Caddy upstream).
// Limit is read from RATE_LIMIT_ANON (default 5/hour).
func RateLimit(rdb *cache.Client) func(http.Handler) http.Handler {
	limit := envInt("RATE_LIMIT_ANON", 5)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := r.Header.Get("X-Real-IP")
			if ip == "" {
				ip = remoteIP(r.RemoteAddr)
			}

			ctx := r.Context()
			key := "ratelimit:analysis:ip:" + ip

			count, err := rdb.Incr(ctx, key)
			if err == nil && count == 1 {
				_ = rdb.Expire(ctx, key, rateLimitWindow)
			}

			if err == nil && count > int64(limit) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "rate limit exceeded, try again later",
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// remoteIP strips the port from a "host:port" RemoteAddr, since without it
// every request would key the rate limiter by a distinct ephemeral port
// rather than the client's actual address.
func remoteIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
