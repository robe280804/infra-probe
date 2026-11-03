package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/netseven/infra-probe/internal/collector"
	"github.com/netseven/infra-probe/internal/collector/host"
	"github.com/netseven/infra-probe/internal/report"
)

// Overridden at build time: -ldflags "-X main.version=1.0.0"
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "scan":
		os.Exit(runScan(os.Args[2:]))
	case "version":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
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

	collectors := []collector.Collector{
		host.New(),
		// docker.New() comes next
	}

	r := report.Build(ctx, version, collectors)
	if err := r.WriteJSON(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if len(r.Errors) > 0 {
		return 1
	}
	return 0
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: infra-probe <scan|version> [flags]")
}
