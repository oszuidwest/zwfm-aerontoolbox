package util

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/doyensec/safeurl"
)

func TestValidators(t *testing.T) {
	// Importing image/png also registers the decoder ValidateImageData needs.
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	pngData := buf.Bytes()

	tests := []struct {
		name    string
		check   func() error
		wantErr bool
	}{
		{
			name:  "entity id accepts uuid v4",
			check: func() error { return ValidateEntityID("9e37ff1f-7823-43ce-93d0-12fc1c2edb8b", "artist") },
		},
		{
			name:  "entity id accepts uppercase uuid v4",
			check: func() error { return ValidateEntityID("9E37FF1F-7823-43CE-93D0-12FC1C2EDB8B", "artist") },
		},
		{
			name:    "entity id rejects empty",
			check:   func() error { return ValidateEntityID("", "artist") },
			wantErr: true,
		},
		{
			name:    "entity id rejects non-uuid",
			check:   func() error { return ValidateEntityID("not-a-uuid", "artist") },
			wantErr: true,
		},
		{
			name:    "entity id rejects non-v4 uuid",
			check:   func() error { return ValidateEntityID("9e37ff1f-7823-13ce-93d0-12fc1c2edb8b", "artist") },
			wantErr: true,
		},
		{
			name:    "entity id rejects non-canonical uuid",
			check:   func() error { return ValidateEntityID("9e37ff1f782343ce93d012fc1c2edb8b", "artist") },
			wantErr: true,
		},
		{
			name:  "content type accepts image",
			check: func() error { return ValidateContentType("image/png") },
		},
		{
			name:  "content type accepts absent header",
			check: func() error { return ValidateContentType("") },
		},
		{
			name:    "content type rejects non-image",
			check:   func() error { return ValidateContentType("text/html") },
			wantErr: true,
		},
		{
			name:  "image data accepts decodable png",
			check: func() error { return ValidateImageData(pngData) },
		},
		{
			name:    "image data rejects empty",
			check:   func() error { return ValidateImageData(nil) },
			wantErr: true,
		},
		{
			name:    "image data rejects undecodable bytes",
			check:   func() error { return ValidateImageData([]byte("not an image")) },
			wantErr: true,
		},
		{
			name:  "image format accepts png",
			check: func() error { return ValidateImageFormat("png") },
		},
		{
			name:    "image format rejects gif",
			check:   func() error { return ValidateImageFormat("gif") },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.check()
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateAndDownloadImageStopsWhenContextEnds(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.(http.Flusher).Flush()
		<-release // stall mid-body
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	// safeurl blocks loopback; allow only this test server.
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	orig := safeHTTPClient
	safeHTTPClient = func() *safeurl.WrappedClient {
		return safeurl.Client(safeurl.GetConfigBuilder().SetAllowedIPs("127.0.0.1").SetAllowedPorts(port).Build())
	}
	t.Cleanup(func() { safeHTTPClient = orig })

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := ValidateAndDownloadImage(ctx, srv.URL, 1<<20)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
			t.Fatalf("ValidateAndDownloadImage error = %v, want %v", err, context.DeadlineExceeded)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ValidateAndDownloadImage kept reading after its context ended")
	}
}

func TestSafeHTTPClientLimitsTLSHandshake(t *testing.T) {
	transport := safeHTTPClient().Client.Transport.(*http.Transport)
	if transport.TLSHandshakeTimeout <= 0 {
		t.Fatalf("TLSHandshakeTimeout = %v, want above 0", transport.TLSHandshakeTimeout)
	}
	// Passing a fresh transport through SetTransport fills this config with HTTP/2 ALPN.
	if transport.TLSClientConfig != nil {
		t.Fatalf("TLSClientConfig = %+v, want nil", transport.TLSClientConfig)
	}
}
