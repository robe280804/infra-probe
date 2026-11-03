package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/netseven/infra-probe/internal/export"
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

func newHandler(calls *int) http.Handler {
	return New(countingScan(calls), time.Second, time.Second).Handler()
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
	return rec
}

func TestHealthz(t *testing.T) {
	var calls int
	rec := get(t, newHandler(&calls), "/healthz")

	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Errorf("got %d %q", rec.Code, rec.Body.String())
	}
	if calls != 0 {
		t.Error("healthz must not trigger a scan")
	}
}

func TestAPIReport(t *testing.T) {
	var calls int
	rec := get(t, newHandler(&calls), "/api/report")

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

func TestAPIExport(t *testing.T) {
	var calls int
	rec := get(t, newHandler(&calls), "/api/export")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="infra-probe-h-20231114T221320Z.zip"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if _, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len())); err != nil {
		t.Errorf("invalid zip: %v", err)
	}
}

func zipFiles(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("invalid zip: %v", err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		_, err = b.ReadFrom(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = b.String()
	}
	return files
}

func TestAPIExport_IncludeSelectsCategories(t *testing.T) {
	var calls int
	rec := get(t, newHandler(&calls), "/api/export?include=host,images")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="infra-probe-h-20231114T221320Z-host-images.zip"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	files := zipFiles(t, rec)
	if _, ok := files["collectors/host.json"]; !ok {
		t.Error("the selected host category is missing")
	}
	if !strings.Contains(files["manifest.json"], `"partial": true`) {
		t.Error("manifest does not say the export is partial")
	}
}

func TestAPIExport_IncludeOmittedMeansEverything(t *testing.T) {
	var calls int
	files := zipFiles(t, get(t, newHandler(&calls), "/api/export"))
	if !strings.Contains(files["manifest.json"], `"partial": false`) {
		t.Error("an export without include must be complete")
	}
}

func TestAPIExport_InvalidIncludeIsRejectedBeforeScanning(t *testing.T) {
	for _, path := range []string{
		"/api/export?include=nope",
		"/api/export?include=host,nope",
		"/api/export?include=",
		"/api/export?include=,",
	} {
		var calls int
		rec := get(t, newHandler(&calls), path)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", path, rec.Code)
		}
		if calls != 0 {
			t.Errorf("%s: a rejected request must not trigger a scan", path)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("%s: Content-Type = %q, an error message must be plain text", path, ct)
		}
	}
}

// TestExportCategoriesMatchTheUI keeps the checkboxes of the dashboard in step
// with the categories the server understands: a category missing from the UI
// could never be selected on its own.
func TestExportCategoriesMatchTheUI(t *testing.T) {
	js, err := fs.ReadFile(webFS, "web/data/export.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range export.Categories {
		if !strings.Contains(string(js), `key: "`+c+`"`) {
			t.Errorf("web/data/export.js has no entry for the %q category", c)
		}
	}
}

func TestAPIExport_UnserializableReportReturns500(t *testing.T) {
	scan := func(context.Context) *report.Report {
		return &report.Report{Results: map[string]any{"bad": make(chan int)}}
	}
	rec := get(t, New(scan, time.Second, time.Second).Handler(), "/api/export")

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
	h := newHandler(&calls)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, "/api/report", nil))
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
	h := newHandler(&calls)

	tests := []struct {
		path        string
		contains    string
		contentType string
	}{
		{"/", "infra-probe", "text/html"},
		{"/app.js", "api/report", "text/javascript"},
		{"/lib/dom.js", "export", "text/javascript"},
		{"/lib/ui.js", "export", "text/javascript"},
		{"/data/findings.js", "export", "text/javascript"},
		{"/views/containers.js", "export", "text/javascript"},
		{"/views/networks.js", "export", "text/javascript"},
		{"/views/volumes.js", "export", "text/javascript"},
		{"/views/daemon.js", "export", "text/javascript"},
		{"/views/compose.js", "export", "text/javascript"},
		{"/data/compose.js", "export", "text/javascript"},
		{"/data/export.js", "export", "text/javascript"},
		{"/views/export-menu.js", "export", "text/javascript"},
		{"/data/daemon.js", "export", "text/javascript"},
		{"/style.css", "--bg", "text/css"},
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
			// With "nosniff" a wrong type makes the browser refuse to run the script.
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, tt.contentType) {
				t.Errorf("Content-Type = %q, want prefix %q", ct, tt.contentType)
			}
		})
	}
}

func TestUnknownPathIs404(t *testing.T) {
	var calls int
	rec := get(t, newHandler(&calls), "/nope")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	var calls int
	h := newHandler(&calls)

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

// TestUIHasNoUnsafeSinks guards the two rules the UI must keep. Report data is
// untrusted (container names, labels...), so it may only reach the page through
// textContent, and the CSP forbids inline styles and scripts.
func TestUIHasNoUnsafeSinks(t *testing.T) {
	forbidden := []string{
		"innerHTML",
		"outerHTML",
		"insertAdjacentHTML",
		"document.write",
		"eval(",
		"new Function",
		`setAttribute("style"`,
		"setAttribute('style'",
	}

	err := fs.WalkDir(webFS, "web", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".js") {
			return nil
		}
		src, err := fs.ReadFile(webFS, path)
		if err != nil {
			return err
		}
		for _, bad := range forbidden {
			if strings.Contains(string(src), bad) {
				t.Errorf("%s uses forbidden construct %q", path, bad)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIndexHasNoInlineScriptOrStyle(t *testing.T) {
	var calls int
	body := get(t, newHandler(&calls), "/").Body.String()

	if strings.Contains(body, "style=") {
		t.Error("index.html must not use inline style attributes (blocked by the CSP)")
	}
	for _, tag := range strings.Split(body, "<script")[1:] {
		head, _, _ := strings.Cut(tag, ">")
		if !strings.Contains(head, "src=") {
			t.Error("index.html must not contain inline scripts (blocked by the CSP)")
		}
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
