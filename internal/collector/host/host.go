// Package host collects information about the machine running the probe:
// system, memory, disks, listening ports, systemd units and cron jobs.
//
// On Linux everything is read from /proc, /etc and /var (through an fs.FS, so
// the parsers are tested with fake files on any OS), plus a few read-only
// systemctl queries. Each part is independent: one that fails does not hide
// the others. On other systems only the basic facts are returned.
package host

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// Info describes the machine running the probe.
type Info struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`   // kernel family the binary runs on: linux, windows...
	Arch     string `json:"arch"` // architecture the binary was built for
	NumCPU   int    `json:"num_cpu"`
	UID      int    `json:"uid"` // user running the probe, -1 on Windows

	System      *System      `json:"system,omitempty"`
	Memory      *Memory      `json:"memory,omitempty"`
	Filesystems []Filesystem `json:"filesystems"`
	Listeners   []Listener   `json:"listeners"`
	Systemd     *Systemd     `json:"systemd,omitempty"`
	Cron        []CronEntry  `json:"cron"`

	// Notes say what the probe could not see and why, so a missing piece of
	// data is not mistaken for an empty one.
	Notes []string `json:"notes"`
}

// Collector gathers the host facts.
type Collector struct {
	linux bool // read /proc and friends

	// fsys is the root of the machine's file system: names have no leading
	// slash ("proc/meminfo").
	fsys fs.FS

	// readlink resolves a symbolic link of fsys (fs.FS cannot).
	readlink func(name string) (string, error)

	// statfs returns the usage of the file system mounted at a path.
	statfs func(mountpoint string) (fsUsage, error)

	// systemctl runs the systemctl command with the given arguments and returns
	// its standard output. It is the only command the probe ever executes.
	systemctl func(ctx context.Context, args ...string) ([]byte, error)

	now func() time.Time
}

// New returns a host Collector reading the real machine.
func New() *Collector {
	return &Collector{
		linux:    runtime.GOOS == "linux",
		fsys:     os.DirFS("/"),
		readlink: func(name string) (string, error) { return os.Readlink("/" + name) },
		statfs:   statfsOf,
		systemctl: func(ctx context.Context, args ...string) ([]byte, error) {
			// The program is fixed and the arguments are constants of this package.
			return exec.CommandContext(ctx, "systemctl", args...).Output() //nolint:gosec // G204: fixed program, fixed arguments
		},
		now: time.Now,
	}
}

// Name is the key of the collector in the report.
func (c *Collector) Name() string { return "host" }

// Collect returns the host Info. Parts that cannot be read are left out and
// reported together in the returned error, the rest is kept.
func (c *Collector) Collect(ctx context.Context) (any, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	info := Info{
		Hostname:    hostname,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		NumCPU:      runtime.NumCPU(),
		UID:         os.Getuid(),
		Filesystems: []Filesystem{},
		Listeners:   []Listener{},
		Cron:        []CronEntry{},
		Notes:       []string{},
	}
	if !c.linux {
		info.Notes = append(info.Notes, "System details (memory, disks, ports, services, cron) are only collected on Linux.")
		return info, nil
	}

	// Each part is independent: a failure is reported with the others and the
	// parts that worked are kept. errors.Join ignores the nil ones.
	var errs []error
	var notes []string

	info.System, err = c.collectSystem()
	errs = append(errs, err)
	info.Memory, err = c.collectMemory()
	errs = append(errs, err)
	info.Filesystems, err = c.collectFilesystems(ctx)
	errs = append(errs, err)
	info.Listeners, notes, err = c.collectListeners()
	errs = append(errs, err)
	info.Notes = append(info.Notes, notes...)
	info.Systemd, err = c.collectSystemd(ctx)
	errs = append(errs, err)
	info.Cron, notes, err = c.collectCron()
	errs = append(errs, err)
	info.Notes = append(info.Notes, notes...)

	return info, errors.Join(errs...)
}

// readFile reads a file of the machine, by name without leading slash.
func (c *Collector) readFile(name string) ([]byte, error) {
	return fs.ReadFile(c.fsys, name)
}

// exists reports whether a name exists in the machine's file system.
func (c *Collector) exists(name string) bool {
	_, err := fs.Stat(c.fsys, name)
	return err == nil
}
