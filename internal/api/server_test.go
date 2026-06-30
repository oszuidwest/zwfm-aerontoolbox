package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/database"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/service"
)

func TestFileMonitorRoutesDisabledReturnNotFound(t *testing.T) {
	cfg := &config.Config{}
	cfg.FileMonitor.Enabled = false

	svc, err := service.New(nil, cfg)
	if err != nil {
		t.Fatalf("service.New: %v", err)
	}
	t.Cleanup(svc.Close)

	handler := New(svc, "test").router()

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

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status code = %d, want %d; body: %s", rec.Code, http.StatusNotFound, rec.Body.String())
			}

			var got Response
			if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if got.Success {
				t.Fatal("success = true, want false")
			}
			if got.Error != "file monitor is not enabled" {
				t.Fatalf("error = %q, want %q", got.Error, "file monitor is not enabled")
			}
		})
	}
}

func TestRateLimiterLimitsAuthenticatedProtectedRequests(t *testing.T) {
	cfg := &config.Config{}
	cfg.API.Enabled = true
	cfg.API.Keys = []string{"test-api-key"}
	cfg.API.RateLimitEnabled = true
	cfg.API.RateLimitRequests = 1
	cfg.API.RateLimitWindowSeconds = 60
	cfg.FileMonitor.Enabled = true

	svc, err := service.New(nil, cfg)
	if err != nil {
		t.Fatalf("service.New: %v", err)
	}
	t.Cleanup(svc.Close)

	handler := New(svc, "test").router()

	req := httptest.NewRequest(http.MethodPost, "/api/file-monitor/check", http.NoBody)
	req.Header.Set("X-API-Key", "test-api-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("first status code = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/file-monitor/check", http.NoBody)
	req.Header.Set("X-API-Key", "test-api-key")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second status code = %d, want %d; body: %s", rec.Code, http.StatusTooManyRequests, rec.Body.String())
	}
	if got, want := rec.Header().Get("Retry-After"), "60"; got != want {
		t.Fatalf("Retry-After = %q, want %q", got, want)
	}
}

func TestRateLimiterLimitsInvalidAPIKeyProbesByRemoteAddress(t *testing.T) {
	cfg := &config.Config{}
	cfg.API.Enabled = true
	cfg.API.Keys = []string{"test-api-key"}
	cfg.API.RateLimitEnabled = true
	cfg.API.RateLimitRequests = 1
	cfg.API.RateLimitWindowSeconds = 60

	svc, err := service.New(nil, cfg)
	if err != nil {
		t.Fatalf("service.New: %v", err)
	}
	t.Cleanup(svc.Close)

	handler := New(svc, "test").router()

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
	cfg := &config.Config{}
	cfg.FileMonitor.Enabled = true

	svc, err := service.New(nil, cfg)
	if err != nil {
		t.Fatalf("service.New: %v", err)
	}
	t.Cleanup(svc.Close)

	handler := New(svc, "test").router()
	req := httptest.NewRequest(http.MethodGet, "/api/file-monitor/status", http.NoBody)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got Response
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.Success {
		t.Fatalf("success = false, want true; error: %s", got.Error)
	}
}

func TestHTTPServerUsesConfiguredTimeouts(t *testing.T) {
	cfg := &config.Config{}
	cfg.API.ReadTimeoutSeconds = 12
	cfg.API.WriteTimeoutSeconds = 34
	cfg.API.IdleTimeoutSeconds = 56

	svc, err := service.New(nil, cfg)
	if err != nil {
		t.Fatalf("service.New: %v", err)
	}
	t.Cleanup(svc.Close)

	server := New(svc, "test").newHTTPServer("8080", http.NotFoundHandler())

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

func TestImageUploadRejectsOversizedRequestBody(t *testing.T) {
	cfg := &config.Config{}
	cfg.API.MaxUploadBodyBytes = 8

	svc, err := service.New(nil, cfg)
	if err != nil {
		t.Fatalf("service.New: %v", err)
	}
	t.Cleanup(svc.Close)

	handler := New(svc, "test").router()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/artists/123e4567-e89b-42d3-a456-426614174000/image",
		strings.NewReader(`{"image":"this request is too large"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status code = %d, want %d; body: %s",
			rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}

	var got Response
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Success {
		t.Fatal("success = true, want false")
	}
	if got.Error != "Request body too large" {
		t.Fatalf("error = %q, want %q", got.Error, "Request body too large")
	}
}

func TestImageUploadAllowsBodyAtConfiguredLimit(t *testing.T) {
	body := `{"image":"not-base64!"}`
	cfg := &config.Config{}
	cfg.API.MaxUploadBodyBytes = int64(len(body))

	svc, err := service.New(nil, cfg)
	if err != nil {
		t.Fatalf("service.New: %v", err)
	}
	t.Cleanup(svc.Close)

	handler := New(svc, "test").router()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/artists/123e4567-e89b-42d3-a456-426614174000/image",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d; body: %s",
			rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	var got Response
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Success {
		t.Fatal("success = true, want false")
	}
	if got.Error != "Invalid base64 image" {
		t.Fatalf("error = %q, want %q", got.Error, "Invalid base64 image")
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

func TestImageUploadReadTimeoutReturnsRequestTimeout(t *testing.T) {
	cfg := &config.Config{}
	cfg.API.MaxUploadBodyBytes = 1024

	svc, err := service.New(nil, cfg)
	if err != nil {
		t.Fatalf("service.New: %v", err)
	}
	t.Cleanup(svc.Close)

	handler := New(svc, "test").router()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/artists/123e4567-e89b-42d3-a456-426614174000/image",
		timeoutReader{},
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestTimeout {
		t.Fatalf("status code = %d, want %d; body: %s",
			rec.Code, http.StatusRequestTimeout, rec.Body.String())
	}

	var got Response
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Success {
		t.Fatal("success = true, want false")
	}
	if got.Error != "Request body read timeout" {
		t.Fatalf("error = %q, want %q", got.Error, "Request body read timeout")
	}
}

func TestBackupDownloadReturnsBackupFile(t *testing.T) {
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

	svc, err := service.New(nil, cfg)
	if err != nil {
		t.Fatalf("service.New: %v", err)
	}
	t.Cleanup(svc.Close)

	handler := New(svc, "test").router()
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
	if err := os.WriteFile(path, []byte("stub"), 0o600); err != nil {
		t.Fatalf("write %s stub: %v", name, err)
	}
	return path
}

func TestIsValidAPIKey(t *testing.T) {
	configuredKeys := []string{"first-test-key", "second-test-key"}
	cfg := &config.Config{}
	cfg.API.Enabled = true

	svc, err := service.New(nil, cfg)
	if err != nil {
		t.Fatalf("service.New: %v", err)
	}
	t.Cleanup(svc.Close)

	server := New(svc, "test")
	tests := []struct {
		name string
		keys []string
		key  string
		want bool
	}{
		{
			name: "first configured key",
			keys: configuredKeys,
			key:  "first-test-key",
			want: true,
		},
		{
			name: "second configured key",
			keys: configuredKeys,
			key:  "second-test-key",
			want: true,
		},
		{
			name: "unknown key",
			keys: configuredKeys,
			key:  "unknown-test-key",
			want: false,
		},
		{
			name: "empty key",
			keys: configuredKeys,
			key:  "",
			want: false,
		},
		{
			name: "no keys configured",
			keys: nil,
			key:  "any-test-key",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg.API.Keys = tt.keys
			if got := server.isValidAPIKey(tt.key); got != tt.want {
				t.Fatalf("isValidAPIKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestPublicHealthOmitsInternalDetails(t *testing.T) {
	stubDBPing(t, nil)

	cfg := healthTestConfig()
	cfg.API.Enabled = true
	cfg.API.Keys = []string{"test-key"}
	cfg.Database.Name = "secret_db_name"
	enableHealthDetailSignals(t, cfg)

	handler := newHealthTestServer(t, cfg).router()
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
	stubDBPing(t, errors.New("db down"))

	cfg := healthTestConfig()
	handler := newHealthTestServer(t, cfg).router()
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
	stubDBPing(t, nil)

	cfg := healthTestConfig()
	handler := newHealthTestServer(t, cfg).router()
	req := httptest.NewRequest(http.MethodHead, "/health", http.NoBody)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d; HEAD probes must not get 405", rec.Code, http.StatusOK)
	}
}

func TestDetailedHealthRequiresAuthAndIncludesOperatorDetails(t *testing.T) {
	stubDBPing(t, nil)

	cfg := healthTestConfig()
	cfg.API.Enabled = true
	cfg.API.Keys = []string{"test-key"}
	cfg.Database.Name = "operator_db_name"
	enableHealthDetailSignals(t, cfg)

	handler := newHealthTestServer(t, cfg).router()

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

func healthTestConfig() *config.Config {
	return &config.Config{
		Database: config.DatabaseConfig{
			Host: "localhost", Port: "5432", Name: "aeron", User: "u",
			Password: "p", Schema: "testschema", SSLMode: "disable",
		},
		Image: config.ImageConfig{TargetWidth: 1, TargetHeight: 1, Quality: 85},
	}
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

func newHealthTestServer(t *testing.T, cfg *config.Config) *Server {
	t.Helper()

	svc, err := service.New(nil, cfg)
	if err != nil {
		t.Fatalf("service.New: %v", err)
	}
	t.Cleanup(svc.Close)

	return New(svc, "test")
}

func stubDBPing(t *testing.T, err error) {
	t.Helper()

	previous := dbPing
	dbPing = func(context.Context, *database.Repository) error {
		return err
	}
	t.Cleanup(func() {
		dbPing = previous
	})
}
