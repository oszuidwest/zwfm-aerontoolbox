package api

import "net/http"

func (s *Server) handleFileMonitorStatus(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, s.service.FileMonitor.Status())
}

func (s *Server) handleFileMonitorCheck(w http.ResponseWriter, r *http.Request) {
	runID, err := s.service.FileMonitor.TriggerCheck()
	if err != nil {
		respondServiceError(w, err)
		return
	}
	respondJSON(w, http.StatusAccepted, AsyncStartResponse{
		Message: "File monitor check started",
		RunID:   runID,
		Check:   "/api/file-monitor/status",
	})
}
