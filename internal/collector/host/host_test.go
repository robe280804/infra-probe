package host

import (
	"context"
	"os"
	"runtime"
	"testing"
)

func TestName(t *testing.T) {
	if got := New().Name(); got != "host" {
		t.Errorf("Name() = %q, want %q", got, "host")
	}
}

func TestCollect(t *testing.T) {
	// On the real machine some part may legitimately fail (a mount the user
	// cannot read...): the collector then returns what it could read together
	// with the error, so only the data is checked here.
	got, err := New().Collect(context.Background())
	if err != nil {
		t.Logf("Collect() reported (partial data is expected): %v", err)
	}
	info, ok := got.(Info)
	if !ok {
		t.Fatalf("Collect() returned %T, want Info", got)
	}

	wantHost, _ := os.Hostname()
	if info.Hostname != wantHost {
		t.Errorf("Hostname = %q, want %q", info.Hostname, wantHost)
	}
	if info.OS != runtime.GOOS {
		t.Errorf("OS = %q, want %q", info.OS, runtime.GOOS)
	}
	if info.Arch != runtime.GOARCH {
		t.Errorf("Arch = %q, want %q", info.Arch, runtime.GOARCH)
	}
	if info.NumCPU < 1 {
		t.Errorf("NumCPU = %d, want >= 1", info.NumCPU)
	}
	if info.UID != os.Getuid() {
		t.Errorf("UID = %d, want %d", info.UID, os.Getuid())
	}
}
