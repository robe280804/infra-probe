package docker

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 0.0001 }

func TestCPUPercent(t *testing.T) {
	cpu := func(total, system uint64, online uint32) container.CPUStats {
		return container.CPUStats{
			CPUUsage:    container.CPUUsage{TotalUsage: total},
			SystemUsage: system,
			OnlineCPUs:  online,
		}
	}

	tests := []struct {
		name string
		raw  container.StatsResponse
		want float64
	}{
		{
			name: "20 percent of one core on a 2 CPU host",
			// cpu delta 100 over system delta 1000, x2 CPUs = 20%
			raw:  container.StatsResponse{PreCPUStats: cpu(100, 1000, 2), CPUStats: cpu(200, 2000, 2)},
			want: 20,
		},
		{
			name: "idle container",
			raw:  container.StatsResponse{PreCPUStats: cpu(100, 1000, 2), CPUStats: cpu(100, 2000, 2)},
			want: 0,
		},
		{
			name: "no previous sample",
			raw:  container.StatsResponse{CPUStats: cpu(200, 2000, 2)},
			want: 20, // 200 / 2000 * 2 CPUs * 100
		},
		{
			name: "counters going backwards are ignored",
			raw:  container.StatsResponse{PreCPUStats: cpu(500, 5000, 2), CPUStats: cpu(100, 1000, 2)},
			want: 0,
		},
		{
			name: "online CPUs missing falls back to the per-CPU list",
			raw: container.StatsResponse{
				PreCPUStats: cpu(100, 1000, 0),
				CPUStats: container.CPUStats{
					CPUUsage:    container.CPUUsage{TotalUsage: 200, PercpuUsage: []uint64{1, 1}},
					SystemUsage: 2000,
				},
			},
			want: 20,
		},
		{
			name: "no CPU information at all",
			raw:  container.StatsResponse{},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cpuPercent(tt.raw); !approx(got, tt.want) {
				t.Errorf("cpuPercent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWorkingSet(t *testing.T) {
	tests := []struct {
		name string
		mem  container.MemoryStats
		want uint64
	}{
		{"no cache info", container.MemoryStats{Usage: 1000}, 1000},
		{"cgroup v2 inactive_file", container.MemoryStats{Usage: 1000, Stats: map[string]uint64{"inactive_file": 300}}, 700},
		{"cgroup v1 total_inactive_file", container.MemoryStats{Usage: 1000, Stats: map[string]uint64{"total_inactive_file": 400}}, 600},
		{"cache larger than usage is ignored", container.MemoryStats{Usage: 100, Stats: map[string]uint64{"inactive_file": 500}}, 100},
		{"v2 key wins over v1 key", container.MemoryStats{Usage: 1000, Stats: map[string]uint64{"inactive_file": 100, "total_inactive_file": 900}}, 900},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workingSet(tt.mem); got != tt.want {
				t.Errorf("workingSet() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestComputeStats(t *testing.T) {
	read := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	raw := container.StatsResponse{
		Read:        read,
		MemoryStats: container.MemoryStats{Usage: 600, Limit: 99999 /* host memory, must be ignored */},
		PidsStats:   container.PidsStats{Current: 7},
		Networks: map[string]container.NetworkStats{
			"eth0": {RxBytes: 10, TxBytes: 20},
			"eth1": {RxBytes: 1, TxBytes: 2},
		},
		BlkioStats: container.BlkioStats{IoServiceBytesRecursive: []container.BlkioStatEntry{
			{Op: "read", Value: 100},
			{Op: "Write", Value: 50},
			{Op: "read", Value: 25},
			{Op: "total", Value: 175}, // not counted
		}},
	}

	s := computeStats(raw, 1000)

	if s.MemoryUsage != 600 || s.MemoryLimit != 1000 {
		t.Errorf("memory = %d / %d", s.MemoryUsage, s.MemoryLimit)
	}
	if !approx(s.MemoryPercent, 60) {
		t.Errorf("MemoryPercent = %v, want 60", s.MemoryPercent)
	}
	if s.PIDs != 7 {
		t.Errorf("PIDs = %d", s.PIDs)
	}
	if s.NetRxBytes != 11 || s.NetTxBytes != 22 {
		t.Errorf("network = rx %d tx %d, want 11 and 22", s.NetRxBytes, s.NetTxBytes)
	}
	if s.BlockReadBytes != 125 || s.BlockWriteBytes != 50 {
		t.Errorf("block = read %d write %d, want 125 and 50", s.BlockReadBytes, s.BlockWriteBytes)
	}
	if !s.SampledAt.Equal(read) {
		t.Errorf("SampledAt = %v", s.SampledAt)
	}
}

func TestComputeStats_NoLimitMeansNoPercent(t *testing.T) {
	raw := container.StatsResponse{MemoryStats: container.MemoryStats{Usage: 600, Limit: 99999}}

	s := computeStats(raw, 0)

	if s.MemoryLimit != 0 || s.MemoryPercent != 0 {
		t.Errorf("unlimited container: limit=%d percent=%v, want 0 and 0", s.MemoryLimit, s.MemoryPercent)
	}
}

func TestConfiguredMemoryLimit(t *testing.T) {
	withLimit := &Container{Inspect: &container.InspectResponse{HostConfig: &container.HostConfig{}}}
	withLimit.Inspect.HostConfig.Memory = 512

	tests := []struct {
		name string
		c    *Container
		want uint64
	}{
		{"no inspect", &Container{}, 0},
		{"no host config", &Container{Inspect: &container.InspectResponse{}}, 0},
		{"unlimited", &Container{Inspect: &container.InspectResponse{HostConfig: &container.HostConfig{}}}, 0},
		{"limited", withLimit, 512},
	}
	for _, tt := range tests {
		if got := configuredMemoryLimit(tt.c); got != tt.want {
			t.Errorf("%s: configuredMemoryLimit() = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestCollectStats_OnlyRunningContainers(t *testing.T) {
	api := &fakeAPI{statsFn: func(id string) (container.StatsResponse, error) {
		return container.StatsResponse{MemoryStats: container.MemoryStats{Usage: 42}}, nil
	}}
	containers := []Container{
		{ID: "run", State: "running"},
		{ID: "stop", State: "exited"},
	}

	collectStats(context.Background(), api, containers)

	if containers[0].Stats == nil || containers[0].Stats.MemoryUsage != 42 {
		t.Errorf("running container stats = %+v", containers[0].Stats)
	}
	if containers[1].Stats != nil || containers[1].StatsError != "" {
		t.Errorf("stopped container must be skipped: %+v / %q", containers[1].Stats, containers[1].StatsError)
	}
}

func TestCollectStats_FailureIsIsolated(t *testing.T) {
	api := &fakeAPI{statsFn: func(id string) (container.StatsResponse, error) {
		if id == "bad" {
			return container.StatsResponse{}, errors.New("stats unavailable")
		}
		return container.StatsResponse{}, nil
	}}
	containers := []Container{
		{ID: "bad", State: "running"},
		{ID: "good", State: "running"},
	}

	collectStats(context.Background(), api, containers)

	if containers[0].Stats != nil || containers[0].StatsError == "" {
		t.Errorf("failed container: stats=%v error=%q", containers[0].Stats, containers[0].StatsError)
	}
	if containers[1].Stats == nil || containers[1].StatsError != "" {
		t.Errorf("healthy container affected: stats=%v error=%q", containers[1].Stats, containers[1].StatsError)
	}
}

func TestCollectStats_ManyContainers(t *testing.T) {
	// More containers than the concurrency limit: all of them must be served.
	api := &fakeAPI{}
	containers := make([]Container, maxConcurrentStats*3)
	for i := range containers {
		containers[i] = Container{ID: "c", State: "running"}
	}

	collectStats(context.Background(), api, containers)

	for i, c := range containers {
		if c.Stats == nil {
			t.Fatalf("container %d has no stats", i)
		}
	}
}
