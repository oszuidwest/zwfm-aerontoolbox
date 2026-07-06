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

// respondClientError writes a caller-supplied, client-safe error message.
func respondClientError(w http.ResponseWriter, statusCode int, errorMsg string) {
	respondEnvelope(w, statusCode, nil, errorMsg)
}

// respondInternalError logs the technical error and sends only safe text.
func respondInternalError(w http.ResponseWriter, statusCode int, err error) {
	slog.Error("Request failed with internal error", "status", statusCode, "error", err)

	message := http.StatusText(statusCode)
	if message == "" {
		message = http.StatusText(http.StatusInternalServerError)
	}
	if opErr, ok := errors.AsType[*types.OperationError](err); ok {
		message = opErr.Operation + " failed"
	}

	respondEnvelope(w, statusCode, nil, message)
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
	if statusCode >= http.StatusInternalServerError {
		respondInternalError(w, statusCode, err)
		return
	}

	respondClientError(w, statusCode, err.Error())
}

func errorCode(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if httpErr, ok := errors.AsType[types.HTTPError](err); ok {
		return httpErr.StatusCode()
	}
	return http.StatusInternalServerError
}
