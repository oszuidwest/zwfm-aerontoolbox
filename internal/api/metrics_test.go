package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestFormatPrometheusMetrics(t *testing.T) {
	buckets := make([]uint64, len(httpDurationBuckets))
	for i := range buckets {
		buckets[i] = 3
	}

	got := formatPrometheusMetrics(metricsSnapshot{
		HealthStatus:        "degraded",
		DatabaseConnected:   true,
		BackupEnabled:       true,
		BackupCompleted:     true,
		BackupLastDuration:  2.5,
		FileMonitorEnabled:  true,
		FileMonitorDone:     true,
		FileMonitorAlerting: 2,
		MediaCheckEnabled:   true,
		MediaCheckDone:      true,
		MediaCheckProblems:  1,
		NotifyConfigured:    true,
		NotifyLastError:     true,
		HTTPSamples: []httpMetricSample{
			{
				Key: httpMetricKey{
					Method: http.MethodGet,
					Route:  "/api/health",
					Status: "200",
				},
				Count:   3,
				Sum:     0.12,
				Buckets: buckets,
			},
		},
	})

	for _, want := range []string{
		"aeron_toolbox_up 1\n",
		`aeron_toolbox_health_status{status="degraded"} 1`,
		"aeron_toolbox_database_connected 1\n",
		"aeron_toolbox_backup_last_success 0\n",
		"aeron_toolbox_file_monitor_checks_alerting 2\n",
		"aeron_toolbox_media_file_check_problems 1\n",
		"aeron_toolbox_notifications_last_error 1\n",
		`aeron_toolbox_http_requests_total{method="GET",route="/api/health",status="200"} 3`,
		`aeron_toolbox_http_request_duration_seconds_bucket{method="GET",route="/api/health",status="200",le="+Inf"} 3`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("metrics output missing %q:\n%s", want, got)
		}
	}

	for _, forbidden := range []string{"super-secret", "raw database error", "aeron_prod"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("metrics output leaked %q:\n%s", forbidden, got)
		}
	}
}

func TestHTTPMetricsUsesRoutePattern(t *testing.T) {
	metrics := newHTTPMetrics()
	router := chi.NewRouter()
	router.Use(metrics.middleware)
	router.Get("/api/items/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/items/sensitive-id", http.NoBody)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusCreated)
	}

	samples := metrics.snapshot()
	if len(samples) != 1 {
		t.Fatalf("sample count = %d, want 1: %#v", len(samples), samples)
	}
	got := samples[0].Key
	if got.Route != "/api/items/{id}" {
		t.Fatalf("route = %q, want route pattern", got.Route)
	}
	if strings.Contains(got.Route, "sensitive-id") {
		t.Fatalf("route label contains concrete URL value: %q", got.Route)
	}
	if got.Status != "201" {
		t.Fatalf("status = %q, want 201", got.Status)
	}
}

func TestPrometheusLabelValueEscapesSpecialCharacters(t *testing.T) {
	got := prometheusLabelValue("line\nquote\"slash\\")
	want := `line\nquote\"slash\\`
	if got != want {
		t.Fatalf("prometheusLabelValue() = %q, want %q", got, want)
	}
}
