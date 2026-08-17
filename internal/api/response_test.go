package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// TestRespondClientErrorPreservesSafeMessage covers the direct respondClientError
// path used by handlers that pick a 5xx status with a caller-vetted message
// (e.g. 502 from the test-email handler). This is not reachable through
// respondServiceError, which routes every >=500 status to respondInternalError.
func TestRespondClientErrorPreservesSafeMessage(t *testing.T) {
	rec := httptest.NewRecorder()

	respondClientError(rec, http.StatusBadGateway, "Authentication with mail provider failed")

	assertErrorResponse(t, rec, http.StatusBadGateway, "Authentication with mail provider failed")
}

func TestRespondServiceError(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantMessage string
		// wantBodyExcludes lists internal fragments that must never reach the
		// client anywhere in the response body.
		wantBodyExcludes []string
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
			name:             "operation error hides underlying cause",
			err:              types.NewOperationError("fetch playlist", errors.New("pq: connection refused host=10.0.0.5")),
			wantStatus:       500,
			wantMessage:      "fetch playlist failed",
			wantBodyExcludes: []string{"pq:", "10.0.0.5"},
		},
		{
			name:             "untyped error returns generic message",
			err:              errors.New("dial tcp 10.0.0.5:5432: i/o timeout"),
			wantStatus:       500,
			wantMessage:      http.StatusText(http.StatusInternalServerError),
			wantBodyExcludes: []string{"10.0.0.5"},
		},
		{
			name:             "database error is sanitized",
			err:              errors.New("get table statistics failed: pq: relation aeron.artist does not exist at 10.0.0.5"),
			wantStatus:       500,
			wantMessage:      http.StatusText(http.StatusInternalServerError),
			wantBodyExcludes: []string{"pq:", "aeron.artist", "10.0.0.5"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			respondServiceError(rec, tt.err)

			body := rec.Body.String()
			assertErrorResponse(t, rec, tt.wantStatus, tt.wantMessage)
			for _, fragment := range tt.wantBodyExcludes {
				if strings.Contains(body, fragment) {
					t.Fatalf("response leaked internal detail %q: %s", fragment, body)
				}
			}
		})
	}
}
