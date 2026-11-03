// Package host collects basic information about the machine running the probe.
package host

import (
	"context"
	"os"
	"runtime"
)

// Info describes the machine running the probe.
type Info struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	NumCPU   int    `json:"num_cpu"`
	UID      int    `json:"uid"`
}

// Collector gathers the host facts.
type Collector struct{}

// New returns a host Collector.
func New() *Collector { return &Collector{} }

// Name is the key of the collector in the report.
func (c *Collector) Name() string { return "host" }

// Collect returns the host Info. It only fails when the host name is unknown.
func (c *Collector) Collect(_ context.Context) (any, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	return Info{
		Hostname: hostname,
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		NumCPU:   runtime.NumCPU(),
		UID:      os.Getuid(),
	}, nil
}
