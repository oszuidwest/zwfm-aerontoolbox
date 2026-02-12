package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/service"
)

// BackupDeleteResponse represents the response format for backup delete operations.
type BackupDeleteResponse struct {
	Message  string `json:"message"`
	Filename string `json:"filename"`
}

func (s *Server) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	var req service.BackupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		respondError(w, http.StatusBadRequest, "Invalid request content")
		return
	}

	if err := s.service.Backup.Start(req); err != nil {
		respondError(w, errorCode(err), err.Error())
		return
	}

	respondJSON(w, http.StatusAccepted, AsyncStartResponse{
		Message: "Backup started in background",
		Check:   "/api/db/backup/status",
	})
}

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	result, err := s.service.Backup.List()
	if err != nil {
		statusCode := errorCode(err)
		respondError(w, statusCode, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, result)
}

func (s *Server) handleBackupStatus(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, s.service.Backup.Status())
}

func (s *Server) handleDownloadBackupFile(w http.ResponseWriter, r *http.Request) {
	filename := chi.URLParam(r, "filename")

	filePath, err := s.service.Backup.GetFilePath(filename)
	if err != nil {
		statusCode := errorCode(err)
		respondError(w, statusCode, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)

	http.ServeFile(w, r, filePath)
}

func (s *Server) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	filename := chi.URLParam(r, "filename")

	// Require confirmation header
	const confirmHeader = "X-Confirm-Delete"
	if r.Header.Get(confirmHeader) != filename {
		respondError(w, http.StatusBadRequest, "Confirmation header missing: "+confirmHeader+" must contain the filename")
		return
	}

	if err := s.service.Backup.Delete(filename); err != nil {
		statusCode := errorCode(err)
		respondError(w, statusCode, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, BackupDeleteResponse{
		Message:  "Backup deleted successfully",
		Filename: filename,
	})
}

func (s *Server) handleValidateBackup(w http.ResponseWriter, r *http.Request) {
	filename := chi.URLParam(r, "filename")

	result, err := s.service.Backup.Validate(filename)
	if err != nil {
		respondError(w, errorCode(err), err.Error())
		return
	}

	respondJSON(w, http.StatusOK, result)
}
