package handlers

import "net/http"

// APIAnalyze is a stub for Phase 1. CLI-driven analysis submission lands in Phase 4.
func APIAnalyze(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not implemented", http.StatusNotImplemented)
	}
}

// APIAnalyzeStatus is a stub for Phase 1. Job status polling lands in Phase 4.
func APIAnalyzeStatus(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not implemented", http.StatusNotImplemented)
	}
}
