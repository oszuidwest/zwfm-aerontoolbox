package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/service"
)

// VacuumRequest represents the JSON request body for vacuum operations.
type VacuumRequest struct {
	Tables  []string `json:"tables"`  // Tables specifies which tables to vacuum
	Analyze bool     `json:"analyze"` // Analyze indicates whether to run ANALYZE after VACUUM
}

// AnalyzeRequest represents the JSON request body for analyze operations.
type AnalyzeRequest struct {
	Tables []string `json:"tables"` // Tables specifies which tables to analyze
}

func (s *Server) handleDatabaseHealth(w http.ResponseWriter, r *http.Request) {
	health, err := s.service.Maintenance.GetHealth(r.Context())
	if err != nil {
		slog.Error("Database health check failed", "error", err)
		respondError(w, errorCode(err), err.Error())
		return
	}

	respondJSON(w, http.StatusOK, health)
}

func (s *Server) handleVacuum(w http.ResponseWriter, r *http.Request) {
	var req VacuumRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		respondError(w, http.StatusBadRequest, "Invalid request content")
		return
	}

	err := s.service.Maintenance.StartVacuum(service.VacuumOptions{
		Tables:  req.Tables,
		Analyze: req.Analyze,
	})
	if err != nil {
		slog.Error("Failed to start vacuum", "tables", req.Tables, "error", err)
		respondError(w, errorCode(err), err.Error())
		return
	}

	msg := "Vacuum started"
	if req.Analyze {
		msg = "Vacuum with analyze started"
	}
	slog.Info(msg, "tables", req.Tables)
	respondJSON(w, http.StatusAccepted, AsyncStartResponse{
		Message: msg,
		Check:   "/api/db/maintenance/status",
	})
}

func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	var req AnalyzeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		respondError(w, http.StatusBadRequest, "Invalid request content")
		return
	}

	err := s.service.Maintenance.StartAnalyze(req.Tables)
	if err != nil {
		slog.Error("Failed to start analyze", "tables", req.Tables, "error", err)
		respondError(w, errorCode(err), err.Error())
		return
	}

	slog.Info("Analyze started", "tables", req.Tables)
	respondJSON(w, http.StatusAccepted, AsyncStartResponse{
		Message: "Analyze started",
		Check:   "/api/db/maintenance/status",
	})
}

func (s *Server) handleMaintenanceStatus(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, s.service.Maintenance.Status())
}
