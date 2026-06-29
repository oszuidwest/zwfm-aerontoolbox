package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRespondErrorSanitizesServerErrors(t *testing.T) {
	rec := httptest.NewRecorder()

	respondError(rec, http.StatusInternalServerError, "get table statistics failed: pq: relation aeron.artist does not exist")

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
