// Package host collects basic information about the machine running the probe.
package host

import (
	"context"
	"os"
	"runtime"
)

type Info struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	NumCPU   int    `json:"num_cpu"`
	UID      int    `json:"uid"`
}

type Collector struct{}

func New() *Collector { return &Collector{} }

func (c *Collector) Name() string { return "host" }

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
