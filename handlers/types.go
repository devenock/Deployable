package handlers

// HealthResponse is returned by GET /health.
type HealthResponse struct {
	Status   string `json:"status" example:"ok"`
	Postgres string `json:"postgres" example:"ok"`
	Redis    string `json:"redis" example:"ok"`
	Version  string `json:"version" example:"dev"`
}

// ErrorResponse is the standard JSON error shape returned by API routes.
type ErrorResponse struct {
	Error string `json:"error" example:"invalid or missing API key"`
}
