package api

import "net/http"

// FileMonitorCheckResponse is returned by POST /file-monitor/check. RunID is
// the monotonic server-side ID of the run just started; clients use it to
// correlate later /status snapshots (run is done when completed_run_id >= RunID
// && running == false).
type FileMonitorCheckResponse struct {
	Message string `json:"message"`
	RunID   uint64 `json:"run_id"`
	Check   string `json:"check"`
}

func (s *Server) handleFileMonitorStatus(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, s.service.FileMonitor.Status())
}

func (s *Server) handleFileMonitorCheck(w http.ResponseWriter, r *http.Request) {
	runID, err := s.service.FileMonitor.TriggerCheck()
	if err != nil {
		respondError(w, errorCode(err), err.Error())
		return
	}
	respondJSON(w, http.StatusAccepted, FileMonitorCheckResponse{
		Message: "File monitor check started",
		RunID:   runID,
		Check:   "/api/file-monitor/status",
	})
}
