package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/service"
)

func TestFileMonitorRoutesDisabledReturnNotFound(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	cfg.FileMonitor.Enabled = false

	handler := newTestRouter(t, cfg)

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{
			name:   "status",
			method: http.MethodGet,
			path:   "/api/file-monitor/status",
		},
		{
			name:   "check",
			method: http.MethodPost,
			path:   "/api/file-monitor/check",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, http.NoBody)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assertErrorResponse(t, rec, http.StatusNotFound, "file monitor is not enabled")
		})
	}
}

func TestRateLimiterLimitsAuthenticatedProtectedRequests(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	cfg.API.Enabled = true
	cfg.API.Keys = []string{"test-api-key-12345"}
	cfg.API.RateLimitEnabled = true
	cfg.API.RateLimitRequests = 1
	cfg.API.RateLimitWindowSeconds = 60
	cfg.FileMonitor.Enabled = true

	handler := newTestRouter(t, cfg)

	req := httptest.NewRequest(http.MethodPost, "/api/file-monitor/check", http.NoBody)
	req.Header.Set("X-API-Key", "test-api-key-12345")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("first status code = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/file-monitor/check", http.NoBody)
	req.Header.Set("X-API-Key", "test-api-key-12345")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assertErrorResponse(t, rec, http.StatusTooManyRequests, "Rate limit exceeded")
	if got, want := rec.Header().Get("Retry-After"), "60"; got != want {
		t.Fatalf("Retry-After = %q, want %q", got, want)
	}
}

func TestRateLimiterLimitsInvalidAPIKeyProbesByRemoteAddress(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	cfg.API.Enabled = true
	cfg.API.Keys = []string{"test-api-key-12345"}
	cfg.API.RateLimitEnabled = true
	cfg.API.RateLimitRequests = 1
	cfg.API.RateLimitWindowSeconds = 60

	handler := newTestRouter(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/playlist", http.NoBody)
	req.Header.Set("X-API-Key", "wrong-key-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("first status code = %d, want %d; body: %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/playlist", http.NoBody)
	req.Header.Set("X-API-Key", "wrong-key-2")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second status code = %d, want %d; body: %s", rec.Code, http.StatusTooManyRequests, rec.Body.String())
	}
}

func TestFileMonitorRoutesEnabledPassThrough(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	cfg.FileMonitor.Enabled = true

	handler := newTestRouter(t, cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/file-monitor/status", http.NoBody)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if got := decodeResponse(t, rec); !got.Success {
		t.Fatalf("success = false, want true; error: %s", got.Error)
	}
}

func TestHTTPServerUsesConfiguredTimeouts(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	cfg.API.ReadTimeoutSeconds = 12
	cfg.API.WriteTimeoutSeconds = 34
	cfg.API.IdleTimeoutSeconds = 56

	server := newTestServer(t, cfg).newHTTPServer("8080", http.NotFoundHandler())

	if got, want := server.ReadHeaderTimeout, 10*time.Second; got != want {
		t.Fatalf("ReadHeaderTimeout = %s, want %s", got, want)
	}
	if got, want := server.ReadTimeout, 12*time.Second; got != want {
		t.Fatalf("ReadTimeout = %s, want %s", got, want)
	}
	if got, want := server.WriteTimeout, 34*time.Second; got != want {
		t.Fatalf("WriteTimeout = %s, want %s", got, want)
	}
	if got, want := server.IdleTimeout, 56*time.Second; got != want {
		t.Fatalf("IdleTimeout = %s, want %s", got, want)
	}
}

type timeoutReadError struct{}

func (timeoutReadError) Error() string { return "read timeout" }
func (timeoutReadError) Timeout() bool { return true }

// Temporary keeps timeoutReadError compatible with net.Error.
func (timeoutReadError) Temporary() bool { return true }

type timeoutReader struct{}

func (timeoutReader) Read([]byte) (int, error) {
	return 0, timeoutReadError{}
}

func TestImageUploadBodyLimitHandling(t *testing.T) {
	t.Parallel()

	atLimitBody := `{"image":"not-base64!"}`
	tests := []struct {
		name               string
		maxUploadBodyBytes int64
		body               io.Reader
		wantStatus         int
		wantError          string
	}{
		{
			name:               "rejects oversized request body",
			maxUploadBodyBytes: 8,
			body:               strings.NewReader(`{"image":"this request is too large"}`),
			wantStatus:         http.StatusRequestEntityTooLarge,
			wantError:          "Request body too large",
		},
		{
			// The 400 for bad base64 proves the body passed the size gate and
			// was read in full.
			name:               "allows body at configured limit",
			maxUploadBodyBytes: int64(len(atLimitBody)),
			body:               strings.NewReader(atLimitBody),
			wantStatus:         http.StatusBadRequest,
			wantError:          "Invalid base64 image",
		},
		{
			name:               "read timeout returns request timeout",
			maxUploadBodyBytes: 1024,
			body:               timeoutReader{},
			wantStatus:         http.StatusRequestTimeout,
			wantError:          "Request body read timeout",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.API.MaxUploadBodyBytes = tt.maxUploadBodyBytes

			handler := newTestRouter(t, cfg)
			req := httptest.NewRequest(
				http.MethodPost,
				"/api/artists/123e4567-e89b-42d3-a456-426614174000/image",
				tt.body,
			)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assertErrorResponse(t, rec, tt.wantStatus, tt.wantError)
		})
	}
}

func TestBackupDownloadReturnsBackupFile(t *testing.T) {
	t.Parallel()

	backupPath := t.TempDir()
	const filename = "aeron-backup-2026-01-02-030405.dump"
	wantBody := []byte("backup-data")
	if err := os.WriteFile(filepath.Join(backupPath, filename), wantBody, 0o600); err != nil {
		t.Fatalf("write backup file: %v", err)
	}

	cfg := &config.Config{}
	cfg.Backup.Enabled = true
	cfg.Backup.Path = backupPath
	cfg.Backup.PgDumpPath = writeExistingToolFile(t, "pg_dump")
	cfg.Backup.PgRestorePath = writeExistingToolFile(t, "pg_restore")

	handler := newTestRouter(t, cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/db/backups/"+filename, http.NoBody)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), wantBody) {
		t.Fatalf("body = %q, want %q", rec.Body.Bytes(), wantBody)
	}
	if got, want := rec.Header().Get("Content-Disposition"), "attachment; filename="+filename; got != want {
		t.Fatalf("Content-Disposition = %q, want %q", got, want)
	}
}

func writeExistingToolFile(t *testing.T, name string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("stub"), 0o700); err != nil { //nolint:gosec // G306: test tools must be executable.
		t.Fatalf("write %s stub: %v", name, err)
	}
	return path
}

func TestAPIKeyAuthentication(t *testing.T) {
	t.Parallel()

	// send drives a protected route with the given API key; an empty key means
	// no X-API-Key header at all.
	send := func(handler http.Handler, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/file-monitor/status", http.NoBody)
		if key != "" {
			req.Header.Set("X-API-Key", key)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	newAuthRouter := func(t *testing.T, apiEnabled bool, keys ...string) http.Handler {
		t.Helper()

		cfg := &config.Config{}
		cfg.API.Enabled = apiEnabled
		cfg.API.Keys = keys
		cfg.FileMonitor.Enabled = true
		return newTestRouter(t, cfg)
	}

	t.Run("configured keys", func(t *testing.T) {
		handler := newAuthRouter(t, true, "first-test-key", "second-test-key")

		tests := []struct {
			name       string
			key        string
			wantStatus int
		}{
			{"first configured key", "first-test-key", http.StatusOK},
			{"second configured key", "second-test-key", http.StatusOK},
			{"unknown key", "unknown-test-key", http.StatusUnauthorized},
			{"missing key", "", http.StatusUnauthorized},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				rec := send(handler, tt.key)
				if tt.wantStatus == http.StatusUnauthorized {
					assertErrorResponse(t, rec, http.StatusUnauthorized, "Unauthorized: invalid or missing API key")
					return
				}
				if rec.Code != tt.wantStatus {
					t.Fatalf("status code = %d, want %d; body: %s", rec.Code, tt.wantStatus, rec.Body.String())
				}
			})
		}
	})

	t.Run("no keys configured rejects every key", func(t *testing.T) {
		handler := newAuthRouter(t, true)

		rec := send(handler, "any-test-key")
		assertErrorResponse(t, rec, http.StatusUnauthorized, "Unauthorized: invalid or missing API key")
	})

	t.Run("authentication disabled passes without key", func(t *testing.T) {
		handler := newAuthRouter(t, false)

		rec := send(handler, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status code = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
		}
	})
}

func TestPublicHealthOmitsInternalDetails(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	cfg.API.Enabled = true
	cfg.API.Keys = []string{"test-key"}
	cfg.Database.Name = "secret_db_name"
	enableHealthDetailSignals(t, cfg)

	handler := newHealthTestServer(t, cfg, nil).router()
	req := httptest.NewRequest(http.MethodGet, "/health", http.NoBody)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want application/json; charset=utf-8", got)
	}

	data := decodeResponseData(t, rec)
	for _, forbidden := range []string{"version", "database", "database_status", "notifications", "file_monitor", "media_file_check"} {
		if _, ok := data[forbidden]; ok {
			t.Fatalf("public health contains %q: %#v", forbidden, data)
		}
	}
	if got := data["status"]; got != "healthy" {
		t.Fatalf("status = %#v, want healthy", got)
	}
}

func TestPublicHealthReturnsUnavailableWhenDatabaseDisconnected(t *testing.T) {
	t.Parallel()

	handler := newHealthTestServer(t, &config.Config{}, errors.New("db down")).router()
	req := httptest.NewRequest(http.MethodGet, "/health", http.NoBody)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status code = %d, want %d; body: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}

	resp := decodeResponse(t, rec)
	if resp.Success {
		t.Fatal("success = true, want false")
	}
	if resp.Error != "Service unavailable" {
		t.Fatalf("error = %q, want %q", resp.Error, "Service unavailable")
	}

	data := responseData(t, resp)
	if got := data["status"]; got != "unhealthy" {
		t.Fatalf("status = %#v, want unhealthy", got)
	}
	for _, forbidden := range []string{"version", "database_status"} {
		if _, ok := data[forbidden]; ok {
			t.Fatalf("public health contains %q: %#v", forbidden, data)
		}
	}
}

func TestPublicHealthRespondsToHeadProbe(t *testing.T) {
	t.Parallel()

	handler := newHealthTestServer(t, &config.Config{}, nil).router()
	req := httptest.NewRequest(http.MethodHead, "/health", http.NoBody)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d; HEAD probes must not get 405", rec.Code, http.StatusOK)
	}
}

func TestDetailedHealthRequiresAuthAndIncludesOperatorDetails(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	cfg.API.Enabled = true
	cfg.API.Keys = []string{"test-key"}
	cfg.Database.Name = "operator_db_name"
	enableHealthDetailSignals(t, cfg)

	handler := newHealthTestServer(t, cfg, nil).router()

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(
		unauthorized,
		httptest.NewRequest(http.MethodGet, "/api/health", http.NoBody),
	)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d; body: %s",
			unauthorized.Code, http.StatusUnauthorized, unauthorized.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/health", http.NoBody)
	req.Header.Set("X-API-Key", "test-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	data := decodeResponseData(t, rec)
	if got := data["database"]; got != "operator_db_name" {
		t.Fatalf("database = %#v, want operator_db_name", got)
	}
	if got := data["version"]; got != "test" {
		t.Fatalf("version = %#v, want test", got)
	}
	if got := data["database_status"]; got != "connected" {
		t.Fatalf("database_status = %#v, want connected", got)
	}
	if _, ok := data["notifications"]; !ok {
		t.Fatalf("detailed health missing notifications: %#v", data)
	}
	fm, ok := data["file_monitor"].(map[string]any)
	if !ok {
		t.Fatalf("detailed health missing file_monitor: %#v", data)
	}
	if got := fm["checks_total"]; got != float64(1) {
		t.Fatalf("file_monitor.checks_total = %#v, want 1", got)
	}
	if _, ok := data["media_file_check"]; !ok {
		t.Fatalf("detailed health missing media_file_check: %#v", data)
	}
}

// newTestServer builds a Server around a fresh service layer (no database) and
// registers cleanup for the service's background workers.
func newTestServer(t *testing.T, cfg *config.Config) *Server {
	t.Helper()

	svc, err := service.New(nil, cfg)
	if err != nil {
		t.Fatalf("service.New: %v", err)
	}
	t.Cleanup(svc.Close)

	return New(svc, "test")
}

// newTestRouter is newTestServer for tests that only drive the HTTP surface.
func newTestRouter(t *testing.T, cfg *config.Config) http.Handler {
	t.Helper()

	return newTestServer(t, cfg).router()
}

// newHealthTestServer is newTestServer with the database ping stubbed out.
func newHealthTestServer(t *testing.T, cfg *config.Config, pingErr error) *Server {
	t.Helper()

	server := newTestServer(t, cfg)
	server.dbPing = func(context.Context) error { return pingErr }
	return server
}

// assertErrorResponse checks the status code and the error envelope in one go.
func assertErrorResponse(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantError string) {
	t.Helper()

	if rec.Code != wantStatus {
		t.Fatalf("status code = %d, want %d; body: %s", rec.Code, wantStatus, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	if resp.Success {
		t.Fatal("success = true, want false")
	}
	if resp.Error != wantError {
		t.Fatalf("error = %q, want %q", resp.Error, wantError)
	}
}

func decodeResponseData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	resp := decodeResponse(t, rec)
	if !resp.Success {
		t.Fatalf("success = false, error = %q", resp.Error)
	}
	return responseData(t, resp)
}

func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder) Response {
	t.Helper()

	var resp Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func responseData(t *testing.T, resp Response) map[string]any {
	t.Helper()

	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %T, want map[string]any", resp.Data)
	}
	return data
}

func enableHealthDetailSignals(t *testing.T, cfg *config.Config) {
	t.Helper()

	dir := t.TempDir()
	watchedFile := filepath.Join(dir, "watched.txt")
	if err := os.WriteFile(watchedFile, []byte("ok"), 0o600); err != nil {
		t.Fatalf("write watched file: %v", err)
	}

	cfg.FileMonitor.Enabled = true
	cfg.FileMonitor.Checks = []config.FileMonitorCheckConfig{{
		Path:          watchedFile,
		MaxAgeMinutes: 5,
	}}
	cfg.MediaFileCheck.Enabled = true
	cfg.MediaFileCheck.SearchDirs = []string{dir}
}
