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
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(Response{
		Success: true,
		Data:    data,
	}); err != nil {
		slog.Debug("Failed to write JSON response to client", "error", err)
	}
}

func respondError(w http.ResponseWriter, statusCode int, errorMsg string) {
	clientMsg := clientErrorMessage(statusCode, errorMsg)
	if statusCode >= http.StatusInternalServerError && errorMsg != "" && errorMsg != clientMsg {
		slog.Error("API internal error", "status", statusCode, "error", errorMsg)
	}

	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(Response{
		Success: false,
		Error:   clientMsg,
	}); err != nil {
		slog.Debug("Failed to write error response to client", "error", err)
	}
}

func clientErrorMessage(statusCode int, errorMsg string) string {
	if statusCode >= http.StatusInternalServerError {
		return http.StatusText(statusCode)
	}
	return errorMsg
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
