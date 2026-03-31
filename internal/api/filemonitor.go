package api

import "net/http"

func (s *Server) handleFileMonitorStatus(w http.ResponseWriter, r *http.Request) {
	if !s.service.Config().FileMonitor.Enabled {
		respondError(w, http.StatusNotFound, "file monitor is not enabled")
		return
	}
	respondJSON(w, http.StatusOK, s.service.FileMonitor.Status())
}
