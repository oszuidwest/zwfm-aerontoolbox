package api

import (
	"log/slog"
	"net/http"
)

func (s *Server) handleDatabaseHealth(w http.ResponseWriter, r *http.Request) {
	health, err := s.service.Maintenance.GetHealth(r.Context())
	if err != nil {
		slog.Error("Database health check failed", "error", err)
		respondServiceError(w, err)
		return
	}

	// API-only placeholder; the notifier sees the raw (possibly empty) list.
	if len(health.Recommendations) == 0 {
		health.Recommendations = []string{"No issues detected"}
	}

	respondJSON(w, http.StatusOK, health)
}
