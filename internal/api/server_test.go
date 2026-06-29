package api

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
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

func TestDetailedHealthRequiresAuthAndIncludesOperatorDetails(t *testing.T) {
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
	registerHealthTestDriver()

	db, err := sql.Open(healthTestDriverName, "")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	svc, err := service.New(sqlx.NewDb(db, healthTestDriverName), cfg)
	if err != nil {
		t.Fatalf("service.New: %v", err)
	}
	t.Cleanup(svc.Close)

	return New(svc, "test")
}

const healthTestDriverName = "health-test"

var registerHealthTestDriverOnce sync.Once

func registerHealthTestDriver() {
	registerHealthTestDriverOnce.Do(func() {
		sql.Register(healthTestDriverName, healthTestDriver{})
	})
}

type healthTestDriver struct{}

func (healthTestDriver) Open(string) (driver.Conn, error) {
	return healthTestConn{}, nil
}

type healthTestConn struct{}

func (healthTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare not implemented")
}

func (healthTestConn) Close() error {
	return nil
}

func (healthTestConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions not implemented")
}

func (healthTestConn) Ping(context.Context) error {
	return nil
}
