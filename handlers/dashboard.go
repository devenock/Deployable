package handlers

import "net/http"

// Dashboard is a stub for Phase 1. Saved reports list lands in Phase 5.
func Dashboard(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not implemented", http.StatusNotImplemented)
	}
}
