package host

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"
)

// System is the operating system and its current load.
type System struct {
	PrettyName string `json:"pretty_name,omitempty"` // "Ubuntu 24.04.1 LTS"
	Distro     string `json:"distro,omitempty"`      // ID: ubuntu, debian, alpine...
	Version    string `json:"version,omitempty"`     // VERSION_ID: 24.04
	Codename   string `json:"codename,omitempty"`
	Kernel     string `json:"kernel,omitempty"` // 6.8.0-45-generic
	CPUModel   string `json:"cpu_model,omitempty"`

	UptimeSeconds int64     `json:"uptime_seconds"`
	BootTime      time.Time `json:"boot_time"`

	// Load is the number of runnable tasks averaged over 1, 5 and 15 minutes;
	// compare it with the number of CPUs.
	Load1  float64 `json:"load_1"`
	Load5  float64 `json:"load_5"`
	Load15 float64 `json:"load_15"`
}

// collectSystem reads os-release, the kernel version, uptime and load. A file
// that does not exist is skipped silently (minimal containers lack some); one
// that exists but cannot be read or parsed is an error.
func (c *Collector) collectSystem() (*System, error) {
	var errs []error
	s := &System{}

	for _, name := range []string{"etc/os-release", "usr/lib/os-release"} {
		data, err := c.readFile(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("system: %w", err))
			break
		}
		fields := parseOSRelease(data)
		s.PrettyName = fields["PRETTY_NAME"]
		s.Distro = fields["ID"]
		s.Version = fields["VERSION_ID"]
		s.Codename = fields["VERSION_CODENAME"]
		break
	}

	if data, err := c.readFile("proc/sys/kernel/osrelease"); err == nil {
		s.Kernel = strings.TrimSpace(string(data))
	} else if !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, fmt.Errorf("system: kernel version: %w", err))
	}

	if data, err := c.readFile("proc/cpuinfo"); err == nil {
		s.CPUModel = parseCPUModel(data)
	}

	if data, err := c.readFile("proc/uptime"); err != nil {
		errs = append(errs, fmt.Errorf("system: uptime: %w", err))
	} else if up, err := parseUptime(data); err != nil {
		errs = append(errs, fmt.Errorf("system: uptime: %w", err))
	} else {
		s.UptimeSeconds = up
		s.BootTime = c.now().UTC().Add(-time.Duration(up) * time.Second).Truncate(time.Second)
	}

	if data, err := c.readFile("proc/loadavg"); err != nil {
		errs = append(errs, fmt.Errorf("system: load: %w", err))
	} else if l1, l5, l15, err := parseLoadAvg(data); err != nil {
		errs = append(errs, fmt.Errorf("system: load: %w", err))
	} else {
		s.Load1, s.Load5, s.Load15 = l1, l5, l15
	}

	return s, joinErrors(errs)
}

// parseOSRelease parses the KEY=value lines of os-release. Values may be
// quoted; comments and blank lines are skipped.
func parseOSRelease(data []byte) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return out
}

// parseUptime reads the seconds since boot from /proc/uptime ("123.45 678.90").
func parseUptime(data []byte) (int64, error) {
	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return 0, errors.New("empty")
	}
	up, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || up < 0 {
		return 0, fmt.Errorf("unexpected value %q", fields[0])
	}
	return int64(up), nil
}

// parseLoadAvg reads the three load averages from /proc/loadavg
// ("0.12 0.34 0.56 1/234 5678").
func parseLoadAvg(data []byte) (l1, l5, l15 float64, err error) {
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return 0, 0, 0, errors.New("unexpected format")
	}
	var out [3]float64
	for i := range out {
		if out[i], err = strconv.ParseFloat(fields[i], 64); err != nil {
			return 0, 0, 0, fmt.Errorf("unexpected value %q", fields[i])
		}
	}
	return out[0], out[1], out[2], nil
}

// parseCPUModel returns the model of the first CPU listed in /proc/cpuinfo.
func parseCPUModel(data []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "model name", "Model Name", "Hardware", "Model":
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// joinErrors is errors.Join that returns nil for an empty list.
func joinErrors(errs []error) error {
	return errors.Join(errs...)
}
