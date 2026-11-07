package host

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strings"
)

// Systemd is the state of the service manager, read with systemctl.
type Systemd struct {
	// Services are the units that are running or have failed.
	Services []Unit `json:"services"`

	// Enabled are the services started at boot.
	Enabled []string `json:"enabled"`

	Timers []Timer `json:"timers"`
}

// Unit is a systemd unit.
type Unit struct {
	Name        string `json:"name"`
	Active      string `json:"active"` // active, failed...
	Sub         string `json:"sub"`    // running, exited, failed...
	Description string `json:"description,omitempty"`
}

// Timer is a systemd timer and the unit it starts.
type Timer struct {
	Name      string `json:"name"`
	Activates string `json:"activates"`
}

// collectSystemd lists services and timers. It is skipped, without an error,
// when the machine does not run systemd (containers, other init systems): the
// directory systemd creates at boot is the standard way to tell.
func (c *Collector) collectSystemd(ctx context.Context) (*Systemd, error) {
	if !c.exists("run/systemd/system") {
		return nil, nil
	}

	s := &Systemd{Services: []Unit{}, Enabled: []string{}, Timers: []Timer{}}
	var errs []error

	run := func(args ...string) ([]byte, bool) {
		out, err := c.systemctl(ctx, append(args, "--no-pager", "--plain", "--no-legend")...)
		if err != nil {
			errs = append(errs, fmt.Errorf("systemd: systemctl %s: %w", args[0], err))
			return nil, false
		}
		return out, true
	}

	if out, ok := run("list-units", "--type=service", "--state=running,failed"); ok {
		s.Services = parseUnits(out)
	}
	if out, ok := run("list-unit-files", "--type=service", "--state=enabled"); ok {
		s.Enabled = parseUnitFiles(out)
	}
	if out, ok := run("list-timers", "--all"); ok {
		s.Timers = parseTimers(out)
	}
	return s, joinErrors(errs)
}

// parseUnits parses `systemctl list-units --plain --no-legend`:
// "UNIT LOAD ACTIVE SUB DESCRIPTION...".
func parseUnits(data []byte) []Unit {
	units := []Unit{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 {
			continue
		}
		units = append(units, Unit{Name: f[0], Active: f[2], Sub: f[3], Description: strings.Join(f[4:], " ")})
	}
	return units
}

// parseUnitFiles parses `systemctl list-unit-files --plain --no-legend`:
// "UNIT STATE PRESET", keeping the unit names.
func parseUnitFiles(data []byte) []string {
	names := []string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) >= 1 {
			names = append(names, f[0])
		}
	}
	return names
}

// parseTimers parses `systemctl list-timers --plain --no-legend`. The leading
// columns (NEXT LEFT LAST PASSED) contain dates of variable width, so only the
// last two fields, UNIT and ACTIVATES, are used: they never contain spaces.
func parseTimers(data []byte) []Timer {
	timers := []Timer{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 || !strings.HasSuffix(f[len(f)-2], ".timer") {
			continue // not a timer line (e.g. the "N timers listed." footer)
		}
		timers = append(timers, Timer{Name: f[len(f)-2], Activates: f[len(f)-1]})
	}
	return timers
}
