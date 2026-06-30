package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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
