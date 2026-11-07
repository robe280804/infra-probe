package host

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// statfsTimeout bounds the query of one mount: a hung network share would
// otherwise block the whole scan. A variable so that tests can shorten it.
var statfsTimeout = 5 * time.Second

// Filesystem is a mounted file system and its usage, in bytes.
type Filesystem struct {
	Device     string `json:"device"`
	Mountpoint string `json:"mountpoint"`
	Type       string `json:"type"`
	ReadOnly   bool   `json:"read_only"`

	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
	Free  uint64 `json:"free"`

	// Avail is what a non-root user can still use: smaller than Free when
	// space is reserved for root (ext4 keeps 5% by default).
	Avail uint64 `json:"avail"`

	// UsedPercent is computed like df: used / (used + avail).
	UsedPercent float64 `json:"used_percent"`

	InodesTotal uint64 `json:"inodes_total"`
	InodesFree  uint64 `json:"inodes_free"`
}

// fsUsage is what statfs reports for a mount.
type fsUsage struct {
	Total, Free, Avail uint64 // bytes
	Inodes, InodesFree uint64
}

type mountEntry struct {
	device, mountpoint, fstype string
	readOnly                   bool
}

func (c *Collector) collectFilesystems(ctx context.Context) ([]Filesystem, error) {
	data, err := c.readFile("proc/self/mounts")
	if err != nil {
		return []Filesystem{}, fmt.Errorf("filesystems: %w", err)
	}

	var errs []error
	out := []Filesystem{}
	for _, m := range filterMounts(parseMounts(data)) {
		u, err := c.statfsWithTimeout(ctx, m.mountpoint)
		if err != nil {
			errs = append(errs, fmt.Errorf("filesystem %s: %w", m.mountpoint, err))
			continue
		}
		used := u.Total - minU64(u.Free, u.Total)
		fs := Filesystem{
			Device: m.device, Mountpoint: m.mountpoint, Type: m.fstype, ReadOnly: m.readOnly,
			Total: u.Total, Used: used, Free: u.Free, Avail: u.Avail,
			InodesTotal: u.Inodes, InodesFree: u.InodesFree,
		}
		if denom := used + u.Avail; denom > 0 {
			fs.UsedPercent = float64(used) / float64(denom) * 100
		}
		out = append(out, fs)
	}
	return out, errors.Join(errs...)
}

func (c *Collector) statfsWithTimeout(ctx context.Context, mountpoint string) (fsUsage, error) {
	type result struct {
		u   fsUsage
		err error
	}
	ch := make(chan result, 1) // buffered: a late answer must not block the goroutine forever
	go func() {
		u, err := c.statfs(mountpoint)
		ch <- result{u, err}
	}()

	select {
	case r := <-ch:
		return r.u, r.err
	case <-time.After(statfsTimeout):
		return fsUsage{}, errors.New("timed out (is the mount hung?)")
	case <-ctx.Done():
		return fsUsage{}, ctx.Err()
	}
}

// parseMounts parses /proc/self/mounts ("device mountpoint type options 0 0").
// Spaces and a few other characters are written as octal escapes (\040).
func parseMounts(data []byte) []mountEntry {
	var out []mountEntry
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 {
			continue
		}
		e := mountEntry{device: unescapeMount(f[0]), mountpoint: unescapeMount(f[1]), fstype: f[2]}
		for _, opt := range strings.Split(f[3], ",") {
			if opt == "ro" {
				e.readOnly = true
			}
		}
		out = append(out, e)
	}
	return out
}

// unescapeMount decodes the \NNN octal escapes of /proc/mounts.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// pseudoTypes are kernel or runtime file systems that hold no user data.
var pseudoTypes = map[string]bool{
	"proc": true, "sysfs": true, "devtmpfs": true, "devpts": true, "cgroup": true, "cgroup2": true,
	"securityfs": true, "debugfs": true, "tracefs": true, "configfs": true, "pstore": true, "bpf": true,
	"mqueue": true, "hugetlbfs": true, "autofs": true, "binfmt_misc": true, "fusectl": true,
	"rpc_pipefs": true, "nsfs": true, "overlay": true, "squashfs": true, "ramfs": true,
	"fuse.lxcfs": true, "fuse.gvfsd-fuse": true, "efivarfs": true, "selinuxfs": true, "rootfs": true,
}

// ignoredPrefixes are mount points that belong to container runtimes and
// package managers: the interesting ones are the disks they live on.
var ignoredPrefixes = []string{"/var/lib/docker/", "/var/lib/containerd/", "/var/lib/kubelet/", "/snap/", "/run/user/"}

// filterMounts keeps the mounts a person cares about when looking at disk
// space: real disks, network shares and /tmp, once each (bind mounts of the
// same device are shown once, at the shortest mount point).
func filterMounts(all []mountEntry) []mountEntry {
	sort.SliceStable(all, func(i, j int) bool {
		if len(all[i].mountpoint) != len(all[j].mountpoint) {
			return len(all[i].mountpoint) < len(all[j].mountpoint)
		}
		return all[i].mountpoint < all[j].mountpoint
	})

	var out []mountEntry
	seen := map[string]bool{}
	for _, m := range all {
		if !keepMount(m) {
			continue
		}
		key := m.device + "\x00" + m.mountpoint
		if strings.HasPrefix(m.device, "/dev/") || strings.HasPrefix(m.fstype, "nfs") || m.fstype == "cifs" {
			key = m.device // the same disk or share mounted twice
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].mountpoint < out[j].mountpoint })
	return out
}

func keepMount(m mountEntry) bool {
	for _, p := range ignoredPrefixes {
		if strings.HasPrefix(m.mountpoint+"/", p) {
			return false
		}
	}
	if m.mountpoint == "/" || m.mountpoint == "/tmp" {
		return true // whatever the type: a root on overlay is still the root
	}
	if m.fstype == "tmpfs" {
		return false
	}
	return !pseudoTypes[m.fstype]
}

func minU64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
