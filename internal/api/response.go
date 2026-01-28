package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// Response is the standard response format for all API endpoints.
type Response struct {
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

// AsyncStartResponse is the response for async operations (backup, vacuum, analyze).
type AsyncStartResponse struct {
	Message string `json:"message"`
	Check   string `json:"check"`
}

func respondJSON(w http.ResponseWriter, statusCode int, data any) {
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(Response{
		Success: true,
		Data:    data,
	}); err != nil {
		slog.Debug("Failed to write JSON response to client", "error", err)
	}
}

func respondError(w http.ResponseWriter, statusCode int, errorMsg string) {
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(Response{
		Success: false,
		Error:   errorMsg,
	}); err != nil {
		slog.Debug("Failed to write error response to client", "error", err)
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
