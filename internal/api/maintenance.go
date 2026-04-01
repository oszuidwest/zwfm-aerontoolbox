package api

import (
	"log/slog"
	"net/http"
)

func (s *Server) handleDatabaseHealth(w http.ResponseWriter, r *http.Request) {
	health, err := s.service.Maintenance.GetHealth(r.Context())
	if err != nil {
		slog.Error("Database health check failed", "error", err)
		respondError(w, errorCode(err), err.Error())
		return
	}

	respondJSON(w, http.StatusOK, health)
}
