package handlers

import "net/http"

// APIAnalyze godoc
// @Summary      Submit an analysis (stub)
// @Description  Requires an X-API-Key header. The key is SHA-256 hashed and checked against Redis first, then Postgres (users.api_key_hash) on a cache miss. Subject to Redis-backed rate limiting (default 5/hour, keyed on the X-Real-IP header set by Caddy). Full submission handling lands in Phase 4.
// @Tags         api
// @Security     ApiKeyAuth
// @Produce      json
// @Success      501  {string}  string  "not implemented"
// @Failure      401  {object}  ErrorResponse
// @Failure      429  {object}  ErrorResponse
// @Router       /api/v1/analyze [post]
func APIAnalyze(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not implemented", http.StatusNotImplemented)
	}
}

// APIAnalyzeStatus godoc
// @Summary      Get analysis job status (stub)
// @Description  Requires an X-API-Key header. Job status polling lands in Phase 4.
// @Tags         api
// @Security     ApiKeyAuth
// @Produce      json
// @Param        jobID  path  string  true  "Analysis job ID"
// @Success      501  {string}  string  "not implemented"
// @Failure      401  {object}  ErrorResponse
// @Failure      429  {object}  ErrorResponse
// @Router       /api/v1/analyze/{jobID} [get]
func APIAnalyzeStatus(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not implemented", http.StatusNotImplemented)
	}
}
