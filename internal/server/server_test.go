package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/netseven/infra-probe/internal/report"
)

// countingScan returns a ScanFunc that counts how many times it ran.
func countingScan(calls *int) ScanFunc {
	return func(context.Context) *report.Report {
		*calls++
		return &report.Report{
			Version:     "test",
			GeneratedAt: time.Unix(1700000000, 0).UTC(),
			Results:     map[string]any{"host": map[string]string{"hostname": "h"}},
		}
	}
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealthz(t *testing.T) {
	var calls int
	rec := get(t, New(countingScan(&calls), time.Second, time.Second).Handler(), "/healthz")

	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Errorf("got %d %q", rec.Code, rec.Body.String())
	}
	if calls != 0 {
		t.Error("healthz must not trigger a scan")
	}
}

func TestAPIReport(t *testing.T) {
	var calls int
	rec := get(t, New(countingScan(&calls), time.Second, time.Second).Handler(), "/api/report")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}

	var got struct {
		Version string         `json:"version"`
		Results map[string]any `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got.Version != "test" || got.Results["host"] == nil {
		t.Errorf("unexpected report: %+v", got)
	}
}

func TestAPIReport_UnserializableReportReturns500(t *testing.T) {
	scan := func(context.Context) *report.Report {
		return &report.Report{Results: map[string]any{"bad": make(chan int)}}
	}
	rec := get(t, New(scan, time.Second, time.Second).Handler(), "/api/report")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestReportIsCachedUntilTTLExpires(t *testing.T) {
	var calls int
	s := New(countingScan(&calls), time.Minute, time.Second)
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }
	h := s.Handler()

	get(t, h, "/api/report")
	get(t, h, "/api/report")
	if calls != 1 {
		t.Fatalf("scans within TTL = %d, want 1", calls)
	}

	now = now.Add(2 * time.Minute)
	get(t, h, "/api/report")
	if calls != 2 {
		t.Errorf("scans after TTL = %d, want 2", calls)
	}
}

func TestScanReceivesTimeout(t *testing.T) {
	var hasDeadline bool
	scan := func(ctx context.Context) *report.Report {
		_, hasDeadline = ctx.Deadline()
		return &report.Report{Results: map[string]any{}}
	}
	get(t, New(scan, 0, time.Second).Handler(), "/api/report")

	if !hasDeadline {
		t.Error("scan context has no deadline")
	}
}

func TestOnlyGETIsAllowed(t *testing.T) {
	var calls int
	h := New(countingScan(&calls), time.Second, time.Second).Handler()

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, "/api/report", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s status = %d, want 405", method, rec.Code)
		}
	}
	if calls != 0 {
		t.Error("rejected requests must not trigger a scan")
	}
}

func TestEmbeddedUIIsServed(t *testing.T) {
	var calls int
	h := New(countingScan(&calls), time.Second, time.Second).Handler()

	tests := []struct {
		path     string
		contains string
	}{
		{"/", "infra-probe"},
		{"/app.js", "api/report"},
		{"/style.css", "--bg"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := get(t, h, tt.path)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tt.contains) {
				t.Errorf("body does not contain %q", tt.contains)
			}
		})
	}
}

func TestUnknownPathIs404(t *testing.T) {
	var calls int
	rec := get(t, New(countingScan(&calls), time.Second, time.Second).Handler(), "/nope")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	var calls int
	h := New(countingScan(&calls), time.Second, time.Second).Handler()

	for _, path := range []string{"/", "/api/report", "/healthz"} {
		rec := get(t, h, path)
		if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
			t.Errorf("%s: CSP = %q", path, csp)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: missing nosniff", path)
		}
		if rec.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s: missing X-Frame-Options", path)
		}
	}
}

func TestUIDoesNotUseInnerHTML(t *testing.T) {
	// Report data is untrusted (container names, labels...). The UI must only
	// use textContent. This guards against reintroducing an XSS sink.
	var calls int
	rec := get(t, New(countingScan(&calls), time.Second, time.Second).Handler(), "/app.js")

	if strings.Contains(rec.Body.String(), "innerHTML") {
		t.Error("app.js must not use innerHTML")
	}
}

func TestListenAndServe_StopsOnContextCancel(t *testing.T) {
	var calls int
	s := New(countingScan(&calls), time.Second, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.ListenAndServe(ctx, "127.0.0.1:0") }()

	time.Sleep(100 * time.Millisecond) // let the listener start
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("ListenAndServe() = %v, want nil after graceful shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop after context cancellation")
	}
}

func TestIsLoopback(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8080", true},
		{"[::1]:8080", true},
		{"localhost:8080", true},
		{"0.0.0.0:8080", false},
		{":8080", false},
		{"192.168.1.10:8080", false},
		{"[::]:8080", false},
		{"not-an-address", false},
	}
	for _, tt := range tests {
		if got := IsLoopback(tt.addr); got != tt.want {
			t.Errorf("IsLoopback(%q) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}
