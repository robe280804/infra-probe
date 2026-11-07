// Package firewall collects the packet filter of the machine running the
// probe: the ufw configuration and, when the probe is allowed to read them,
// the kernel rules (iptables, ip6tables, nftables).
//
// ufw is read from its files under /etc (through an fs.FS, so the parsers are
// tested with fake files on any OS). The kernel rules can only be listed with
// CAP_NET_ADMIN: without it the commands are not even started and a note says
// what is missing. The commands only list rules, they never change them.
package firewall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// Info is the packet filter configuration of the machine.
type Info struct {
	// CanReadRules is true when the probe has CAP_NET_ADMIN, the capability
	// the kernel requires to list the firewall rules.
	CanReadRules bool `json:"can_read_rules"`

	// UFW is nil when ufw is not installed.
	UFW *UFW `json:"ufw,omitempty"`

	// The kernel rules, nil when they were not read (no permission, or the
	// command is not installed).
	Iptables  *Ruleset  `json:"iptables,omitempty"`
	IP6tables *Ruleset  `json:"ip6tables,omitempty"`
	Nftables  *Nftables `json:"nftables,omitempty"`

	// Notes say what the probe could not see and why, so a missing piece of
	// data is not mistaken for an empty one.
	Notes []string `json:"notes"`
}

// errNotInstalled is returned by the command runner when the program is not
// on this machine.
var errNotInstalled = errors.New("not installed")

// capNetAdmin is the bit of CAP_NET_ADMIN in the capability sets of
// /proc/<pid>/status (linux/capability.h).
const capNetAdmin = 12

// Collector gathers the firewall configuration.
type Collector struct {
	linux bool

	// fsys is the root of the machine's file system: names have no leading
	// slash ("etc/ufw/ufw.conf").
	fsys fs.FS

	// run executes one of the listing commands (iptables-save, ip6tables-save,
	// nft) and returns its standard output, or errNotInstalled.
	run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// New returns a firewall Collector reading the real machine.
func New() *Collector {
	return &Collector{
		linux: runtime.GOOS == "linux",
		fsys:  os.DirFS("/"),
		run:   runCommand,
	}
}

// Name is the key of the collector in the report.
func (c *Collector) Name() string { return "firewall" }

// Collect returns the firewall Info. Parts that cannot be read are left out
// and reported together in the returned error, the rest is kept.
func (c *Collector) Collect(ctx context.Context) (any, error) {
	info := Info{Notes: []string{}}
	if !c.linux {
		info.Notes = append(info.Notes, "The firewall is only inspected on Linux.")
		return info, nil
	}

	var errs []error
	var notes []string
	var err error

	info.UFW, notes, err = c.collectUFW()
	errs = append(errs, err)
	info.Notes = append(info.Notes, notes...)

	info.CanReadRules = c.hasNetAdmin()
	if !info.CanReadRules {
		info.Notes = append(info.Notes,
			"The kernel firewall rules (iptables, nftables) can only be listed with CAP_NET_ADMIN: run the probe as root to see them, and to know whether Docker ports are filtered by DOCKER-USER rules.")
		return info, errors.Join(errs...)
	}

	// Each listing is independent; a refusal of the kernel (a capability
	// other than CAP_NET_ADMIN is missing) is a note, not an error.
	var out []byte
	if out, err = c.list(ctx, &info.Notes, "iptables-save"); out != nil {
		info.Iptables = parseIptablesSave(out)
	}
	errs = append(errs, err)
	if out, err = c.list(ctx, &info.Notes, "ip6tables-save"); out != nil {
		info.IP6tables = parseIptablesSave(out)
	}
	errs = append(errs, err)
	// iptables-nft rules show up in nftables too, translated: they are the
	// same rules seen from the other tool.
	if out, err = c.list(ctx, &info.Notes, "nft", "list", "ruleset"); out != nil {
		info.Nftables = parseNftRuleset(out)
	}
	errs = append(errs, err)

	// Mark ufw as loaded when its chains are in the kernel: ufw.conf only says
	// what happens at boot.
	if info.UFW != nil && info.Iptables != nil {
		loaded := info.Iptables.HasChain("filter", "ufw-user-input")
		info.UFW.Loaded = &loaded
	}

	return info, errors.Join(errs...)
}

// hasNetAdmin reports whether the probe holds CAP_NET_ADMIN, from the
// effective capability set of its own process.
func (c *Collector) hasNetAdmin() bool {
	data, err := fs.ReadFile(c.fsys, "proc/self/status")
	if err != nil {
		return false
	}
	caps, ok := parseCapEff(data)
	return ok && caps&(1<<capNetAdmin) != 0
}

// parseCapEff extracts the effective capability mask ("CapEff:\t000001ffffffffff").
func parseCapEff(data []byte) (uint64, bool) {
	for _, line := range strings.Split(string(data), "\n") {
		rest, ok := strings.CutPrefix(line, "CapEff:")
		if !ok {
			continue
		}
		mask, err := strconv.ParseUint(strings.TrimSpace(rest), 16, 64)
		return mask, err == nil
	}
	return 0, false
}

// list runs one of the listing commands. It returns nil output, and no
// error, when the command is not installed (a machine may use nftables only)
// or when the kernel refuses it: then a note is added.
func (c *Collector) list(ctx context.Context, notes *[]string, command string, args ...string) ([]byte, error) {
	out, err := c.run(ctx, command, args...)
	switch {
	case err == nil:
		return out, nil
	case errors.Is(err, errNotInstalled):
		return nil, nil
	case isPermissionError(err):
		*notes = append(*notes, fmt.Sprintf("%s was refused by the kernel (%v): run the probe as root to see these rules.", command, err))
		return nil, nil
	default:
		return nil, fmt.Errorf("%s: %w", command, err)
	}
}

// isPermissionError recognizes the messages of the firewall tools when a
// capability is missing (iptables-legacy also needs CAP_NET_RAW).
func isPermissionError(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"permission denied", "operation not permitted", "must be root"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// commandDirs are searched when a command is not on the PATH: the firewall
// tools live in sbin, which is often missing from the PATH of a service user.
var commandDirs = []string{"/usr/sbin", "/sbin", "/usr/bin", "/bin"}

// allowedCommands are the only programs this package runs: each one lists
// rules and never changes them.
var allowedCommands = map[string]bool{"iptables-save": true, "ip6tables-save": true, "nft": true}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	if !allowedCommands[name] {
		return nil, fmt.Errorf("command %q is not allowed", name)
	}
	path, err := exec.LookPath(name)
	if err != nil {
		path = ""
		for _, dir := range commandDirs {
			if st, err := os.Stat(dir + "/" + name); err == nil && !st.IsDir() {
				path = dir + "/" + name
				break
			}
		}
	}
	if path == "" {
		return nil, errNotInstalled
	}

	// The program is one of allowedCommands, the arguments are constants of
	// this package.
	cmd := exec.CommandContext(ctx, path, args...) //nolint:gosec // G204: fixed programs, fixed arguments
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, firstLine(msg))
		}
		return nil, err
	}
	return out, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
