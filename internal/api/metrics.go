package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

var httpDurationBuckets = []float64{
	0.005,
	0.01,
	0.025,
	0.05,
	0.1,
	0.25,
	0.5,
	1,
	2.5,
	5,
	10,
}

type httpMetricKey struct {
	Method string
	Route  string
	Status string
}

type httpMetricSeries struct {
	Count   uint64
	Sum     float64
	Buckets []uint64
}

type httpMetricSample struct {
	Key     httpMetricKey
	Count   uint64
	Sum     float64
	Buckets []uint64
}

type httpMetrics struct {
	mu     sync.Mutex
	series map[httpMetricKey]*httpMetricSeries
}

func newHTTPMetrics() *httpMetrics {
	return &httpMetrics{
		series: map[httpMetricKey]*httpMetricSeries{},
	}
}

func (m *httpMetrics) middleware(next http.Handler) http.Handler {
	if m == nil {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &metricsResponseWriter{
			ResponseWriter: w,
			status:         http.StatusOK,
		}

		defer func() {
			if recovered := recover(); recovered != nil {
				m.record(r, http.StatusInternalServerError, time.Since(start))
				panic(recovered)
			}
			m.record(r, rw.status, time.Since(start))
		}()

		next.ServeHTTP(rw, r)
	})
}

func (m *httpMetrics) record(r *http.Request, status int, duration time.Duration) {
	route := chi.RouteContext(r.Context()).RoutePattern()
	if route == "" {
		route = "unmatched"
	}

	key := httpMetricKey{
		Method: r.Method,
		Route:  route,
		Status: strconv.Itoa(status),
	}

	seconds := duration.Seconds()

	m.mu.Lock()
	defer m.mu.Unlock()

	series := m.series[key]
	if series == nil {
		series = &httpMetricSeries{
			Buckets: make([]uint64, len(httpDurationBuckets)),
		}
		m.series[key] = series
	}

	series.Count++
	series.Sum += seconds
	for i, bucket := range httpDurationBuckets {
		if seconds <= bucket {
			series.Buckets[i]++
		}
	}
}

func (m *httpMetrics) snapshot() []httpMetricSample {
	if m == nil {
		return []httpMetricSample{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	samples := make([]httpMetricSample, 0, len(m.series))
	for key, series := range m.series {
		samples = append(samples, httpMetricSample{
			Key:     key,
			Count:   series.Count,
			Sum:     series.Sum,
			Buckets: slices.Clone(series.Buckets),
		})
	}

	sort.Slice(samples, func(i, j int) bool {
		if samples[i].Key.Route != samples[j].Key.Route {
			return samples[i].Key.Route < samples[j].Key.Route
		}
		if samples[i].Key.Method != samples[j].Key.Method {
			return samples[i].Key.Method < samples[j].Key.Method
		}
		return samples[i].Key.Status < samples[j].Key.Status
	})

	return samples
}

type metricsResponseWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *metricsResponseWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.status = status
	w.wrote = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *metricsResponseWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *metricsResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

type metricsSnapshot struct {
	HealthStatus        string
	DatabaseConnected   bool
	BackupEnabled       bool
	BackupRunning       bool
	BackupCompleted     bool
	BackupLastSuccess   bool
	BackupLastDuration  float64
	FileMonitorEnabled  bool
	FileMonitorRunning  bool
	FileMonitorDone     bool
	FileMonitorAlerting int
	MediaCheckEnabled   bool
	MediaCheckRunning   bool
	MediaCheckDone      bool
	MediaCheckSuccess   bool
	MediaCheckProblems  int
	NotifyConfigured    bool
	NotifyLastError     bool
	HTTPSamples         []httpMetricSample
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(formatPrometheusMetrics(s.collectMetrics(r.Context()))))
}

func (s *Server) collectMetrics(ctx context.Context) metricsSnapshot {
	health := s.collectHealth(ctx)
	cfg := s.service.Config()
	snapshot := metricsSnapshot{
		HealthStatus:      health.Status,
		DatabaseConnected: health.DatabaseStatus == "connected",
		NotifyConfigured:  health.Notifications != nil && health.Notifications.Configured,
		NotifyLastError:   health.Notifications != nil && health.Notifications.LastError != "",
		HTTPSamples:       s.httpMetrics.snapshot(),
	}

	if cfg.Backup.Enabled {
		status := s.service.Backup.Status()
		snapshot.BackupEnabled = true
		snapshot.BackupRunning = status.Running
		snapshot.BackupCompleted = status.EndedAt != nil
		snapshot.BackupLastSuccess = status.EndedAt != nil && status.Success
		if status.StartedAt != nil && status.EndedAt != nil {
			snapshot.BackupLastDuration = status.EndedAt.Sub(*status.StartedAt).Seconds()
		}
	}

	if cfg.FileMonitor.Enabled {
		status := s.service.FileMonitor.Status()
		snapshot.FileMonitorEnabled = true
		snapshot.FileMonitorRunning = status.Running
		snapshot.FileMonitorDone = status.CompletedRunID > 0
		if health.FileMonitor != nil {
			snapshot.FileMonitorAlerting = health.FileMonitor.ChecksAlerting
		}
	}

	if cfg.MediaFileCheck.Enabled {
		status := s.service.MediaFileCheck.Status()
		snapshot.MediaCheckEnabled = true
		snapshot.MediaCheckRunning = status.Running
		snapshot.MediaCheckDone = status.CompletedRunID > 0
		snapshot.MediaCheckSuccess = status.CompletedRunID > 0 && status.Result != nil && status.Result.Error == ""
		if health.MediaFileCheck != nil {
			snapshot.MediaCheckProblems = health.MediaFileCheck.Problems
		}
	}

	return snapshot
}

func formatPrometheusMetrics(snapshot metricsSnapshot) string {
	var b strings.Builder

	writeGauge(&b, "aeron_toolbox_up", "Whether the Aeron Toolbox process is running.", 1)
	writeHealthStatus(&b, snapshot.HealthStatus)
	writeGaugeBool(
		&b,
		"aeron_toolbox_database_connected",
		"Whether the configured database answered the last metrics health probe.",
		snapshot.DatabaseConnected,
	)
	writeGaugeBool(
		&b,
		"aeron_toolbox_backup_enabled",
		"Whether backup support is enabled.",
		snapshot.BackupEnabled,
	)
	writeGaugeBool(
		&b,
		"aeron_toolbox_backup_running",
		"Whether a backup job is currently running.",
		snapshot.BackupRunning,
	)
	writeGaugeBool(
		&b,
		"aeron_toolbox_backup_last_completed",
		"Whether at least one backup job has completed since process start.",
		snapshot.BackupCompleted,
	)
	writeGaugeBool(
		&b,
		"aeron_toolbox_backup_last_success",
		"Whether the last completed backup job succeeded.",
		snapshot.BackupLastSuccess,
	)
	writeGauge(
		&b,
		"aeron_toolbox_backup_last_duration_seconds",
		"Duration of the last completed backup job in seconds.",
		nonNegative(snapshot.BackupLastDuration),
	)
	writeGaugeBool(
		&b,
		"aeron_toolbox_file_monitor_enabled",
		"Whether file monitoring is enabled.",
		snapshot.FileMonitorEnabled,
	)
	writeGaugeBool(
		&b,
		"aeron_toolbox_file_monitor_running",
		"Whether a file-monitor job is currently running.",
		snapshot.FileMonitorRunning,
	)
	writeGaugeBool(
		&b,
		"aeron_toolbox_file_monitor_last_completed",
		"Whether at least one file-monitor job has completed since process start.",
		snapshot.FileMonitorDone,
	)
	writeGauge(
		&b,
		"aeron_toolbox_file_monitor_checks_alerting",
		"Number of monitored files currently alerting in their active window.",
		float64(snapshot.FileMonitorAlerting),
	)
	writeGaugeBool(
		&b,
		"aeron_toolbox_media_file_check_enabled",
		"Whether media-file checking is enabled.",
		snapshot.MediaCheckEnabled,
	)
	writeGaugeBool(
		&b,
		"aeron_toolbox_media_file_check_running",
		"Whether a media-file check job is currently running.",
		snapshot.MediaCheckRunning,
	)
	writeGaugeBool(
		&b,
		"aeron_toolbox_media_file_check_last_completed",
		"Whether at least one media-file check has completed since process start.",
		snapshot.MediaCheckDone,
	)
	writeGaugeBool(
		&b,
		"aeron_toolbox_media_file_check_last_success",
		"Whether the last completed media-file check succeeded.",
		snapshot.MediaCheckSuccess,
	)
	writeGauge(
		&b,
		"aeron_toolbox_media_file_check_problems",
		"Number of missing, ambiguous, or errored files in the latest scheduled media-file check.",
		float64(snapshot.MediaCheckProblems),
	)
	writeGaugeBool(
		&b,
		"aeron_toolbox_notifications_configured",
		"Whether email notifications are configured.",
		snapshot.NotifyConfigured,
	)
	writeGaugeBool(
		&b,
		"aeron_toolbox_notifications_last_error",
		"Whether the notification service has recorded a send error since process start.",
		snapshot.NotifyLastError,
	)
	writeHTTPMetrics(&b, snapshot.HTTPSamples)

	return b.String()
}

func writeGaugeBool(b *strings.Builder, name, help string, value bool) {
	if value {
		writeGauge(b, name, help, 1)
		return
	}
	writeGauge(b, name, help, 0)
}

func writeGauge(b *strings.Builder, name, help string, value float64) {
	fmt.Fprintf(b, "# HELP %s %s\n", name, help)
	fmt.Fprintf(b, "# TYPE %s gauge\n", name)
	fmt.Fprintf(b, "%s %s\n", name, prometheusFloat(value))
}

func writeHealthStatus(b *strings.Builder, active string) {
	const name = "aeron_toolbox_health_status"
	fmt.Fprintf(b, "# HELP %s Current health status as one sample per status label.\n", name)
	fmt.Fprintf(b, "# TYPE %s gauge\n", name)
	for _, status := range []string{"healthy", "degraded", "unhealthy"} {
		value := 0.0
		if active == status {
			value = 1
		}
		fmt.Fprintf(
			b,
			"%s{status=%q} %s\n",
			name,
			status,
			prometheusFloat(value),
		)
	}
}

func writeHTTPMetrics(b *strings.Builder, samples []httpMetricSample) {
	const requestsName = "aeron_toolbox_http_requests_total"
	fmt.Fprintf(b, "# HELP %s Total HTTP requests by method, route pattern, and status.\n", requestsName)
	fmt.Fprintf(b, "# TYPE %s counter\n", requestsName)
	for _, sample := range samples {
		fmt.Fprintf(
			b,
			"%s%s %d\n",
			requestsName,
			httpMetricLabels(sample.Key, ""),
			sample.Count,
		)
	}

	const durationName = "aeron_toolbox_http_request_duration_seconds"
	fmt.Fprintf(b, "# HELP %s HTTP request duration by method, route pattern, and status.\n", durationName)
	fmt.Fprintf(b, "# TYPE %s histogram\n", durationName)
	for _, sample := range samples {
		for i, bucket := range httpDurationBuckets {
			fmt.Fprintf(
				b,
				"%s_bucket%s %d\n",
				durationName,
				httpMetricLabels(sample.Key, prometheusFloat(bucket)),
				sample.Buckets[i],
			)
		}
		fmt.Fprintf(
			b,
			"%s_bucket%s %d\n",
			durationName,
			httpMetricLabels(sample.Key, "+Inf"),
			sample.Count,
		)
		fmt.Fprintf(
			b,
			"%s_sum%s %s\n",
			durationName,
			httpMetricLabels(sample.Key, ""),
			prometheusFloat(sample.Sum),
		)
		fmt.Fprintf(
			b,
			"%s_count%s %d\n",
			durationName,
			httpMetricLabels(sample.Key, ""),
			sample.Count,
		)
	}
}

func httpMetricLabels(key httpMetricKey, le string) string {
	labels := []string{
		`method="` + prometheusLabelValue(key.Method) + `"`,
		`route="` + prometheusLabelValue(key.Route) + `"`,
		`status="` + prometheusLabelValue(key.Status) + `"`,
	}
	if le != "" {
		labels = append(labels, `le="`+prometheusLabelValue(le)+`"`)
	}
	return "{" + strings.Join(labels, ",") + "}"
}

func prometheusLabelValue(value string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		"\n", `\n`,
		`"`, `\"`,
	)
	return replacer.Replace(value)
}

func prometheusFloat(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "0"
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func nonNegative(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}
