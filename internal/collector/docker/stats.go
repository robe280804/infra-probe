package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// maxConcurrentStats bounds how many stats requests run at once. Each request
// takes about a second (the daemon samples twice), so they run in parallel,
// but not so many as to hammer the daemon.
const maxConcurrentStats = 8

// Stats is a point-in-time snapshot of the resources used by a running
// container, already reduced to the numbers a dashboard needs.
type Stats struct {
	// CPUPercent is the CPU used, where 100 means one full core.
	CPUPercent float64 `json:"cpu_percent"`

	// MemoryUsage is the working set in bytes: total usage minus the
	// reclaimable page cache, the same figure `docker stats` shows.
	MemoryUsage uint64 `json:"memory_usage"`

	// MemoryLimit is the limit configured on the container, 0 if unlimited.
	// The daemon reports the host memory as "limit" for unlimited containers,
	// which would be misleading, so the configured value is used instead.
	MemoryLimit uint64 `json:"memory_limit"`

	// MemoryPercent is MemoryUsage over MemoryLimit, 0 when there is no limit.
	MemoryPercent float64 `json:"memory_percent"`

	PIDs            uint64 `json:"pids"`
	NetRxBytes      uint64 `json:"net_rx_bytes"`
	NetTxBytes      uint64 `json:"net_tx_bytes"`
	BlockReadBytes  uint64 `json:"block_read_bytes"`
	BlockWriteBytes uint64 `json:"block_write_bytes"`

	SampledAt time.Time `json:"sampled_at"`
}

// collectStats fills Stats for every running container, in parallel. A failure
// is recorded on the container and never aborts the others.
func collectStats(ctx context.Context, api dockerAPI, containers []Container) {
	sem := make(chan struct{}, maxConcurrentStats)
	var wg sync.WaitGroup

	for i := range containers {
		c := &containers[i]
		if c.State != string(container.StateRunning) {
			continue // stats of a stopped container are meaningless
		}

		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			stats, err := readStats(ctx, api, c.ID, configuredMemoryLimit(c))
			if err != nil {
				c.StatsError = err.Error()
				return
			}
			c.Stats = stats
		}()
	}
	wg.Wait()
}

// configuredMemoryLimit returns the memory limit set on the container, or 0.
func configuredMemoryLimit(c *Container) uint64 {
	if c.Inspect == nil || c.Inspect.HostConfig == nil || c.Inspect.HostConfig.Memory <= 0 {
		return 0
	}
	return uint64(c.Inspect.HostConfig.Memory)
}

func readStats(ctx context.Context, api dockerAPI, id string, memoryLimit uint64) (*Stats, error) {
	// IncludePreviousSample makes the daemon fill the "previous" CPU figures,
	// which are needed to compute a CPU percentage.
	res, err := api.ContainerStats(ctx, id, client.ContainerStatsOptions{IncludePreviousSample: true})
	if err != nil {
		return nil, fmt.Errorf("container stats: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	var raw container.StatsResponse
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode stats: %w", err)
	}

	stats := computeStats(raw, memoryLimit)
	return &stats, nil
}

// computeStats reduces the raw daemon response to a Stats snapshot.
func computeStats(raw container.StatsResponse, memoryLimit uint64) Stats {
	s := Stats{
		CPUPercent:  cpuPercent(raw),
		MemoryUsage: workingSet(raw.MemoryStats),
		MemoryLimit: memoryLimit,
		PIDs:        raw.PidsStats.Current,
		SampledAt:   raw.Read.UTC(),
	}
	if memoryLimit > 0 {
		s.MemoryPercent = float64(s.MemoryUsage) / float64(memoryLimit) * 100
	}

	for _, n := range raw.Networks {
		s.NetRxBytes += n.RxBytes
		s.NetTxBytes += n.TxBytes
	}
	for _, e := range raw.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(e.Op) {
		case "read":
			s.BlockReadBytes += e.Value
		case "write":
			s.BlockWriteBytes += e.Value
		}
	}
	return s
}

// cpuPercent uses the same formula as the Docker CLI: the container CPU time
// delta over the system CPU time delta, scaled by the number of CPUs.
func cpuPercent(raw container.StatsResponse) float64 {
	cpuDelta := float64(raw.CPUStats.CPUUsage.TotalUsage) - float64(raw.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(raw.CPUStats.SystemUsage) - float64(raw.PreCPUStats.SystemUsage)

	cpus := float64(raw.CPUStats.OnlineCPUs)
	if cpus == 0 {
		cpus = float64(len(raw.CPUStats.CPUUsage.PercpuUsage))
	}
	if cpuDelta <= 0 || sysDelta <= 0 || cpus == 0 {
		return 0
	}
	return cpuDelta / sysDelta * cpus * 100
}

// workingSet subtracts the inactive page cache from the memory usage, so the
// figure matches `docker stats`. The cache key differs between cgroup v2
// ("inactive_file") and v1 ("total_inactive_file").
func workingSet(m container.MemoryStats) uint64 {
	inactive, ok := m.Stats["inactive_file"]
	if !ok {
		inactive, ok = m.Stats["total_inactive_file"]
	}
	if ok && inactive < m.Usage {
		return m.Usage - inactive
	}
	return m.Usage
}
