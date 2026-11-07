package host

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Memory is the RAM and swap of the machine, in bytes.
type Memory struct {
	Total uint64 `json:"total"`

	// Available is what can be given to new work without swapping: the free
	// memory plus what the kernel can reclaim (page cache...). It is the
	// number to watch, not Free.
	Available uint64 `json:"available"`
	Free      uint64 `json:"free"`

	SwapTotal uint64 `json:"swap_total"`
	SwapFree  uint64 `json:"swap_free"`
}

func (c *Collector) collectMemory() (*Memory, error) {
	data, err := c.readFile("proc/meminfo")
	if err != nil {
		return nil, fmt.Errorf("memory: %w", err)
	}
	m, err := parseMeminfo(data)
	if err != nil {
		return nil, fmt.Errorf("memory: %w", err)
	}
	return m, nil
}

// parseMeminfo parses /proc/meminfo ("MemTotal:  7970004 kB"). MemTotal is
// required; MemAvailable is missing on kernels older than 3.14, where
// MemFree is used instead.
func parseMeminfo(data []byte) (*Memory, error) {
	values := map[string]uint64{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		key, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		n, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		if len(fields) > 1 && strings.EqualFold(fields[1], "kB") {
			n *= 1024
		}
		values[key] = n
	}

	total, ok := values["MemTotal"]
	if !ok {
		return nil, errors.New("MemTotal not found")
	}
	m := &Memory{
		Total:     total,
		Free:      values["MemFree"],
		SwapTotal: values["SwapTotal"],
		SwapFree:  values["SwapFree"],
	}
	if avail, ok := values["MemAvailable"]; ok {
		m.Available = avail
	} else {
		m.Available = m.Free
	}
	return m, nil
}
