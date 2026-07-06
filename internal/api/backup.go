package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/service"
)

const maxCreateBackupBodyBytes = 1 << 20

// BackupDeleteResponse is returned after a backup file is deleted.
type BackupDeleteResponse struct {
	Message  string `json:"message"`
	Filename string `json:"filename"`
}

func (s *Server) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCreateBackupBodyBytes)

	var req service.BackupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			respondClientError(w, http.StatusRequestEntityTooLarge, "Request body too large")
			return
		}
		slog.Warn("Invalid backup request content", "path", r.URL.Path, "remote_addr", r.RemoteAddr, "error", err)
		respondClientError(w, http.StatusBadRequest, "Invalid request content")
		return
	}

	if err := s.service.Backup.Start(req); err != nil {
		respondServiceError(w, err)
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
		respondServiceError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, result)
}

func (s *Server) handleBackupStatus(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, s.service.Backup.Status())
}

func (s *Server) handleDownloadBackupFile(w http.ResponseWriter, r *http.Request) {
	filename := chi.URLParam(r, "filename")

	file, info, err := s.service.Backup.OpenFile(filename)
	if err != nil {
		respondServiceError(w, err)
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.Debug("Failed to close backup download file", "filename", filename, "error", err)
		}
	}()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)

	// Backup downloads can exceed the global WriteTimeout. Clear the socket
	// write deadline so slow clients do not receive silently truncated dumps.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
		slog.Warn("Could not clear write deadline for backup download; download may be truncated by WriteTimeout",
			"filename", filename,
			"error", err)
	}

	http.ServeContent(w, r, filename, info.ModTime(), file)
}

func (s *Server) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	filename := chi.URLParam(r, "filename")

	const confirmHeader = "X-Confirm-Delete"
	if r.Header.Get(confirmHeader) != filename {
		respondClientError(w, http.StatusBadRequest, "Confirmation header missing: "+confirmHeader+" must contain the filename")
		return
	}

	if err := s.service.Backup.Delete(filename); err != nil {
		respondServiceError(w, err)
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
		respondServiceError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, result)
}
