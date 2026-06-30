package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// Response wraps every API payload with a success flag and optional error.
type Response struct {
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

// AsyncStartResponse points clients to the status endpoint for async work.
type AsyncStartResponse struct {
	Message string `json:"message"`
	Check   string `json:"check"`
}

func respondJSON(w http.ResponseWriter, statusCode int, data any) {
	respondEnvelope(w, statusCode, statusCode < http.StatusBadRequest, data, "")
}

func respondError(w http.ResponseWriter, statusCode int, errorMsg string) {
	respondEnvelope(w, statusCode, false, nil, errorMsg)
}

func respondEnvelope(w http.ResponseWriter, statusCode int, success bool, data any, errorMsg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(Response{
		Success: success,
		Data:    data,
		Error:   errorMsg,
	}); err != nil {
		slog.Debug("Failed to write JSON response to client", "error", err)
	}
}

func errorCode(err error) int {
	if err == nil {
		return http.StatusOK
	}

	var httpErr types.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode()
	}

	return http.StatusInternalServerError
}
