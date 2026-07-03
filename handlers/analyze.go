package handlers

import "net/http"

// AnalyzePage is a stub for Phase 1. Full input handling (zip/GitHub/CLI)
// and the processing pipeline land in Phase 2.
func AnalyzePage(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not implemented", http.StatusNotImplemented)
	}
}
