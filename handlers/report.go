package handlers

import "net/http"

// ReportView is a stub for Phase 1. Full report rendering lands in Phase 2.
func ReportView(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not implemented", http.StatusNotImplemented)
	}
}
