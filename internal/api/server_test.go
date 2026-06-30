package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

	handler := newHealthTestServer(t, cfg).router()
	req := httptest.NewRequest(http.MethodGet, "/api/health", http.NoBody)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	data := decodeResponseData(t, rec)
	for _, forbidden := range []string{"database", "notifications", "file_monitor", "media_file_check"} {
		if _, ok := data[forbidden]; ok {
			t.Fatalf("public health contains %q: %#v", forbidden, data)
		}
	}
	if got := data["database_status"]; got != "connected" {
		t.Fatalf("database_status = %#v, want connected", got)
	}
}

func TestPublicHealthReturnsUnavailableWhenDatabaseDisconnected(t *testing.T) {
	stubDBPing(t, errors.New("db down"))

	cfg := healthTestConfig()
	handler := newHealthTestServer(t, cfg).router()
	req := httptest.NewRequest(http.MethodGet, "/api/health", http.NoBody)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status code = %d, want %d; body: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}

	data := decodeResponseData(t, rec)
	if got := data["status"]; got != "unhealthy" {
		t.Fatalf("status = %#v, want unhealthy", got)
	}
	if got := data["database_status"]; got != "disconnected" {
		t.Fatalf("database_status = %#v, want disconnected", got)
	}
}

func TestDetailedHealthRequiresAuthAndIncludesOperatorDetails(t *testing.T) {
	stubDBPing(t, nil)

	cfg := healthTestConfig()
	cfg.API.Enabled = true
	cfg.API.Keys = []string{"test-key"}
	cfg.Database.Name = "operator_db_name"

	handler := newHealthTestServer(t, cfg).router()

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(
		unauthorized,
		httptest.NewRequest(http.MethodGet, "/api/health/details", http.NoBody),
	)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d; body: %s",
			unauthorized.Code, http.StatusUnauthorized, unauthorized.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/health/details", http.NoBody)
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
	if _, ok := data["notifications"]; !ok {
		t.Fatalf("detailed health missing notifications: %#v", data)
	}
}

func decodeResponseData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var resp Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("success = false, error = %q", resp.Error)
	}
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
