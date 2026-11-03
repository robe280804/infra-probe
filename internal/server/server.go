// Package server exposes the scan report over a small read-only HTTP API and
// serves the embedded dashboard UI.
package server

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/netseven/infra-probe/internal/report"
)

//go:embed web
var webFS embed.FS

func init() {
	// The Content-Type of static files comes from the OS mime database, which
	// on Windows can map .js to text/plain. Combined with "nosniff" the browser
	// would then refuse to run the scripts, so pin the types we serve.
	_ = mime.AddExtensionType(".js", "text/javascript; charset=utf-8")
	_ = mime.AddExtensionType(".css", "text/css; charset=utf-8")
	_ = mime.AddExtensionType(".svg", "image/svg+xml")
}

// ScanFunc runs a fresh scan.
type ScanFunc func(ctx context.Context) *report.Report

// Server serves the dashboard and the report API.
type Server struct {
	scan        ScanFunc
	cacheTTL    time.Duration
	scanTimeout time.Duration
	now         func() time.Time // replaced in tests

	mu       sync.Mutex // also serializes scans, so a burst of requests triggers one
	cached   *report.Report
	cachedAt time.Time
}

// New builds a Server. A report is reused for cacheTTL so that refreshing the
// page does not hammer the Docker daemon.
func New(scan ScanFunc, cacheTTL, scanTimeout time.Duration) *Server {
	return &Server{
		scan:        scan,
		cacheTTL:    cacheTTL,
		scanTimeout: scanTimeout,
		now:         time.Now,
	}
}

// Handler returns the HTTP handler. Only GET requests are accepted.
func (s *Server) Handler() http.Handler {
	ui, err := fs.Sub(webFS, "web")
	if err != nil {
		// Cannot happen: the directory is embedded at compile time.
		panic(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /api/report", s.handleReport)
	mux.Handle("GET /", http.FileServerFS(ui))

	return securityHeaders(mux)
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	rep := s.report(r.Context())

	// Encode first so an encoding failure can still produce a proper 500.
	var buf bytes.Buffer
	if err := rep.WriteJSON(&buf); err != nil {
		http.Error(w, "cannot encode report", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}

// report returns the cached report or runs a new scan when it has expired.
func (s *Server) report(ctx context.Context) *report.Report {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cached != nil && s.now().Sub(s.cachedAt) < s.cacheTTL {
		return s.cached
	}

	ctx, cancel := context.WithTimeout(ctx, s.scanTimeout)
	defer cancel()

	s.cached = s.scan(ctx)
	s.cachedAt = s.now()
	return s.cached
}

// securityHeaders adds defensive headers. The CSP forbids inline scripts,
// inline styles and any external resource: the UI is fully self-contained.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// ListenAndServe serves on addr until ctx is cancelled, then shuts down
// gracefully.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      s.scanTimeout + 10*time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// IsLoopback reports whether addr (host:port) only listens on the local
// machine. An empty host means "all interfaces" and is NOT loopback.
func IsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
