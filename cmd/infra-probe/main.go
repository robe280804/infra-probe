// Command infra-probe inspects the server it runs on (host, firewall, Docker
// daemon, containers, images, networks, volumes, Compose projects) and reports
// what it finds, as JSON, as a web dashboard or as a zip archive for an AI
// assistant.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/netseven/infra-probe/internal/collector"
	"github.com/netseven/infra-probe/internal/collector/docker"
	"github.com/netseven/infra-probe/internal/collector/firewall"
	"github.com/netseven/infra-probe/internal/collector/host"
	"github.com/netseven/infra-probe/internal/export"
	"github.com/netseven/infra-probe/internal/report"
	"github.com/netseven/infra-probe/internal/server"
	"github.com/netseven/infra-probe/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "scan":
		os.Exit(runScan(os.Args[2:]))
	case "export":
		os.Exit(runExport(os.Args[2:]))
	case "serve":
		os.Exit(runServe(os.Args[2:]))
	case "version":
		fmt.Println(version.String())
	default:
		usage()
		os.Exit(2)
	}
}

// collectors returns the data sources used by every command.
func collectors() []collector.Collector {
	return []collector.Collector{
		host.New(),
		firewall.New(),
		docker.New(),
	}
}

func runScan(args []string) int {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	timeout := fs.Duration("timeout", 30*time.Second, "overall scan timeout")
	_ = fs.Parse(args)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	r := report.Build(ctx, version.Version, collectors())
	if err := r.WriteJSON(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if len(r.Errors) > 0 {
		return 1
	}
	return 0
}

// runExport writes the report as a sanitized zip archive, the same one the
// dashboard offers for download.
func runExport(args []string) int {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	out := fs.String("o", "", "output file (default: infra-probe-<host>-<time>.zip in the current directory)")
	timeout := fs.Duration("timeout", 30*time.Second, "overall scan timeout")
	include := fs.String("include", "", "export only these categories, comma separated ("+strings.Join(export.Categories, ", ")+")")
	exclude := fs.String("exclude", "", "export everything except these categories, comma separated")
	_ = fs.Parse(args)

	// Validate before scanning and before creating the file.
	selected, err := export.Select(*include, *exclude)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	r := report.Build(ctx, version.Version, collectors())
	path := *out
	if path == "" {
		path = export.FileNameFor(r, selected)
	}

	// The archive describes the whole server configuration: owner-only access.
	f, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if err := export.WriteOnly(f, r, selected); err != nil {
		_ = f.Close()
		_ = os.Remove(path) // do not leave a truncated archive behind
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if err := f.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}

	fmt.Fprintln(os.Stderr, "export written to", path)
	for name, msg := range r.Errors {
		fmt.Fprintf(os.Stderr, "warning: %s: %s\n", name, msg)
	}
	if len(r.Errors) > 0 {
		return 1
	}
	return 0
}

func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:8080", "address to listen on")
	cacheTTL := fs.Duration("cache-ttl", 5*time.Second, "how long a scan result is reused")
	scanTimeout := fs.Duration("scan-timeout", 30*time.Second, "timeout of a single scan")
	_ = fs.Parse(args)

	if !server.IsLoopback(*addr) {
		fmt.Fprintf(os.Stderr,
			"warning: %s is reachable from the network. The report describes the whole server configuration and this server has no authentication.\n",
			*addr)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	scan := func(ctx context.Context) *report.Report {
		return report.Build(ctx, version.Version, collectors())
	}
	srv := server.New(scan, *cacheTTL, *scanTimeout)

	fmt.Fprintf(os.Stderr, "dashboard on http://%s (Ctrl+C to stop)\n", *addr)
	if err := srv.ListenAndServe(ctx, *addr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: infra-probe <scan|export|serve|version> [flags]")
}
