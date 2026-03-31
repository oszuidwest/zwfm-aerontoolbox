package api

import "net/http"

func (s *Server) handleFileMonitorStatus(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, s.service.FileMonitor.Status())
}
