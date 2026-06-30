package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
