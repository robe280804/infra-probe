package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/netseven/infra-probe/internal/collector"
	"github.com/netseven/infra-probe/internal/collector/docker"
	"github.com/netseven/infra-probe/internal/collector/host"
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
	fmt.Fprintln(os.Stderr, "usage: infra-probe <scan|serve|version> [flags]")
}
