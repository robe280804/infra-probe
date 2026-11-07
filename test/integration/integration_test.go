//go:build integration

// Package integration holds tests that need the outside world: a real Docker
// daemon and a compiled binary. They are excluded from `go test ./...` and run
// with: go test -tags=integration ./test/...
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/netseven/infra-probe/internal/collector/docker"
)

// requireDocker skips the test when no daemon is reachable, unless
// INFRA_PROBE_REQUIRE_DOCKER is set (as in CI), where it must fail instead.
func requireDocker(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if os.Getenv("INFRA_PROBE_REQUIRE_DOCKER") != "" {
		t.Fatalf("docker daemon required but unavailable: %v", err)
	}
	t.Skipf("docker daemon not reachable: %v", err)
}

func TestDockerCollector_RealDaemon(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	got, err := docker.New().Collect(ctx)
	requireDocker(t, err)

	res, ok := got.(docker.Result)
	if !ok {
		t.Fatalf("Collect() returned %T, want docker.Result", got)
	}
	for _, c := range res.Containers {
		if c.ID == "" {
			t.Errorf("container without ID: %+v", c)
		}
		if c.InspectError == "" && c.Inspect == nil {
			t.Errorf("container %s has neither inspect nor inspect_error", c.ID)
		}
	}
	for _, img := range res.Images {
		if img.ID == "" {
			t.Errorf("image without ID: %+v", img)
		}
	}
}

// buildBinary compiles the CLI into a temp dir and returns its path.
func buildBinary(t *testing.T) string {
	t.Helper()

	name := "infra-probe"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out := filepath.Join(t.TempDir(), name)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", out, "../../cmd/infra-probe")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, output)
	}
	return out
}

func TestBinary_Version(t *testing.T) {
	bin := buildBinary(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").Output()
	if err != nil {
		t.Fatalf("version failed: %v", err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		t.Error("version printed nothing")
	}
}

func TestBinary_ScanProducesValidReport(t *testing.T) {
	bin := buildBinary(t)

	// The exit code is 1 when a collector fails (e.g. no Docker daemon), but
	// stdout must be a valid report either way.
	var stdout bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "scan", "--timeout", "20s")
	cmd.Stdout = &stdout
	_ = cmd.Run()

	var report struct {
		Version string                     `json:"version"`
		Results map[string]json.RawMessage `json:"results"`
		Errors  map[string]string          `json:"errors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("scan output is not valid JSON: %v\n%s", err, stdout.String())
	}
	rawHost, ok := report.Results["host"]
	if !ok {
		t.Fatalf("host section missing: results=%v errors=%v", report.Results, report.Errors)
	}

	// On Linux the host section must carry the system details, not just the
	// name: that is what the collector exists for.
	if runtime.GOOS == "linux" {
		var host struct {
			System      json.RawMessage   `json:"system"`
			Memory      json.RawMessage   `json:"memory"`
			Filesystems []json.RawMessage `json:"filesystems"`
		}
		if err := json.Unmarshal(rawHost, &host); err != nil {
			t.Fatal(err)
		}
		if len(host.System) == 0 || len(host.Memory) == 0 || len(host.Filesystems) == 0 {
			t.Errorf("host details missing on Linux: %s", rawHost)
		}
	}
}
