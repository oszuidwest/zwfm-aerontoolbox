package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

func TestRespondErrorSanitizesServerErrors(t *testing.T) {
	rec := httptest.NewRecorder()

	respondError(
		rec,
		http.StatusInternalServerError,
		"get table statistics failed: pq: relation aeron.artist does not exist",
	)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if strings.Contains(rec.Body.String(), "pq:") || strings.Contains(rec.Body.String(), "aeron.artist") {
		t.Fatalf("response leaked internal error: %s", rec.Body.String())
	}

	var resp Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error != http.StatusText(http.StatusInternalServerError) {
		t.Fatalf("error = %q, want %q", resp.Error, http.StatusText(http.StatusInternalServerError))
	}
}

func TestRespondErrorPreservesClientErrors(t *testing.T) {
	rec := httptest.NewRecorder()

	respondError(rec, http.StatusBadRequest, "id: invalid artist ID: must be a UUID")

	var resp Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error != "id: invalid artist ID: must be a UUID" {
		t.Fatalf("error = %q, want validation message", resp.Error)
	}
}

func TestRespondServiceError(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantMessage string
	}{
		{
			name:        "validation error keeps message",
			err:         types.NewValidationError("image", "is required"),
			wantStatus:  400,
			wantMessage: "image: is required",
		},
		{
			name:        "conflict error keeps message",
			err:         types.NewConflictError("backup", "backup already in progress"),
			wantStatus:  409,
			wantMessage: "backup already in progress",
		},
		{
			name:        "not found error keeps message",
			err:         types.NewNotFoundError("artist", "abc"),
			wantStatus:  404,
			wantMessage: "artist with ID 'abc' not found",
		},
		{
			name: "operation error hides underlying cause",
			err: types.NewOperationError(
				"fetch playlist",
				errors.New("pq: connection refused host=10.0.0.5"),
			),
			wantStatus:  500,
			wantMessage: "fetch playlist failed",
		},
		{
			name:        "untyped error returns generic message",
			err:         errors.New("dial tcp 10.0.0.5:5432: i/o timeout"),
			wantStatus:  500,
			wantMessage: http.StatusText(http.StatusInternalServerError),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			respondServiceError(rec, tt.err)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			var resp Response
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.Success {
				t.Fatal("success = true, want false")
			}
			if resp.Error != tt.wantMessage {
				t.Fatalf("error = %q, want %q", resp.Error, tt.wantMessage)
			}
			if tt.wantStatus >= 500 && strings.Contains(resp.Error, "10.0.0.5") {
				t.Fatalf("error leaks internal details: %q", resp.Error)
			}
		})
	}
}
