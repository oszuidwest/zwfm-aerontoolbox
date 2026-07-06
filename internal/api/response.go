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
	respondEnvelope(w, statusCode, data, "")
}

func respondError(w http.ResponseWriter, statusCode int, errorMsg string) {
	clientMsg := clientErrorMessage(statusCode, errorMsg)
	if statusCode >= http.StatusInternalServerError && errorMsg != "" && errorMsg != clientMsg {
		slog.Error("API internal error", "status", statusCode, "error", errorMsg)
	}

	respondEnvelope(w, statusCode, nil, clientMsg)
}

func respondEnvelope(w http.ResponseWriter, statusCode int, data any, errorMsg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(Response{
		Success: statusCode < http.StatusBadRequest,
		Data:    data,
		Error:   errorMsg,
	}); err != nil {
		slog.Debug("Failed to write JSON response to client", "error", err)
	}
}

// respondServiceError maps a service-layer error onto the API response, using
// the typed HTTPError status when available and 500 otherwise. Internal (5xx)
// errors are logged in full but sent to the client without the underlying
// cause, so database and filesystem details never leave the server.
func respondServiceError(w http.ResponseWriter, err error) {
	statusCode := errorCode(err)
	message := err.Error()
	if statusCode >= http.StatusInternalServerError {
		slog.Error("Request failed with internal error", "error", err)
		message = http.StatusText(statusCode)
		var opErr *types.OperationError
		if errors.As(err, &opErr) {
			message = opErr.Operation + " failed"
		}
	}
	respondEnvelope(w, statusCode, nil, message)
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
