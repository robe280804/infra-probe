package host

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// permFS is an fs.FS where some directories cannot be listed, like the
// crontabs of other users or the fd directory of another user's process.
type permFS struct {
	fs.FS
	denied map[string]bool
}

func (p permFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if p.denied[name] {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return fs.ReadDir(p.FS, name)
}

// machine is a fake Linux host.
type machine struct {
	files   fstest.MapFS
	denied  map[string]bool
	links   map[string]string  // symbolic links (/proc/<pid>/fd/<n>)
	usage   map[string]fsUsage // statfs by mount point
	cmds    map[string]string  // systemctl output by sub-command
	runs    []string           // sub-commands executed
	statErr map[string]error
}

func (m *machine) collector() *Collector {
	return &Collector{
		linux: true,
		fsys:  permFS{FS: m.files, denied: m.denied},
		readlink: func(name string) (string, error) {
			if target, ok := m.links[name]; ok {
				return target, nil
			}
			return "", fs.ErrNotExist
		},
		statfs: func(mp string) (fsUsage, error) {
			if err := m.statErr[mp]; err != nil {
				return fsUsage{}, err
			}
			return m.usage[mp], nil
		},
		systemctl: func(_ context.Context, args ...string) ([]byte, error) {
			m.runs = append(m.runs, args[0])
			out, ok := m.cmds[args[0]]
			if !ok {
				return nil, errors.New("exit status 1")
			}
			return []byte(out), nil
		},
		now: func() time.Time { return time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC) },
	}
}

func file(content string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(content)} }

func newMachine(t *testing.T) *machine {
	t.Helper()
	pw := "pw" + "-value"
	return &machine{
		files: fstest.MapFS{
			"etc/os-release":              file("PRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\nID=ubuntu\nVERSION_ID=\"24.04\"\nVERSION_CODENAME=noble\n"),
			"proc/sys/kernel/osrelease":   file("6.8.0-45-generic\n"),
			"proc/cpuinfo":                file("model name\t: Test CPU\n"),
			"proc/uptime":                 file("7200.50 100.00\n"),
			"proc/loadavg":                file("0.50 0.40 0.30 1/200 999\n"),
			"proc/meminfo":                file("MemTotal: 8000 kB\nMemFree: 1000 kB\nMemAvailable: 5000 kB\nSwapTotal: 2000 kB\nSwapFree: 1500 kB\n"),
			"proc/self/mounts":            file("/dev/sda1 / ext4 rw 0 0\nproc /proc proc rw 0 0\n/dev/sdb1 /mnt/data xfs ro 0 0\n"),
			"proc/net/tcp":                file(tcpTable),
			"proc/net/tcp6":               file("  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n   0: 00000000000000000000000000000000:1F40 00000000000000000000000000000000:0000 0A 0:0 0:0 0 0 0 1003 1 0\n"),
			"proc/net/udp":                file("  sl  local_address rem_address   st\n   0: 00000000:0035 00000000:0000 07 0:0 0:0 0 0 0 2001 2 0\n"),
			"proc/100/fd/3":               file(""),
			"proc/100/fd/4":               file(""),
			"proc/100/comm":               file("myapp\n"),
			"proc/200/fd/5":               file(""),
			"proc/200/comm":               file("sshd\n"),
			"proc/300/comm":               file("other\n"),
			"run/systemd/system/.keep":    file(""),
			"etc/crontab":                 file("SHELL=/bin/sh\n17 * * * * root run-parts /etc/cron.hourly\n"),
			"etc/cron.d/app":              file("API_TOKEN=" + pw + "\n*/5 * * * * www-data /srv/app/run --password " + pw + "\n"),
			"etc/cron.daily/logrotate":    file("#!/bin/sh\n"),
			"etc/cron.daily/.placeholder": file(""),
		},
		denied: map[string]bool{"proc/300/fd": true, "var/spool/cron/crontabs": true},
		links: map[string]string{
			"proc/100/fd/3": "socket:[1001]",
			"proc/100/fd/4": "anon_inode:[eventpoll]",
			"proc/200/fd/5": "socket:[1002]",
		},
		usage: map[string]fsUsage{
			"/":         {Total: 100 << 30, Free: 40 << 30, Avail: 35 << 30, Inodes: 1000, InodesFree: 900},
			"/mnt/data": {Total: 10 << 30, Free: 10 << 30, Avail: 10 << 30},
		},
		cmds: map[string]string{
			"list-units":      "nginx.service loaded active running A web server\nbroken.service loaded failed failed Failing\n",
			"list-unit-files": "nginx.service enabled enabled\n",
			"list-timers":     "n/a n/a Mon 2026-11-02 06:00:00 UTC 1 day ago logrotate.timer logrotate.service\n",
		},
	}
}

// The userland of the fake machine reuses the tcp table of the parser tests,
// whose sockets 1001 (127.0.0.1:8080) and 1002 (0.0.0.0:22) have owners.
func collectInfo(t *testing.T, m *machine) (Info, error) {
	t.Helper()
	got, err := m.collector().Collect(context.Background())
	info, ok := got.(Info)
	if !ok {
		t.Fatalf("Collect returned %T", got)
	}
	return info, err
}

func TestCollect_FakeMachine(t *testing.T) {
	m := newMachine(t)
	info, err := collectInfo(t, m)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	s := info.System
	if s.PrettyName != "Ubuntu 24.04.1 LTS" || s.Distro != "ubuntu" || s.Version != "24.04" || s.Codename != "noble" || s.Kernel != "6.8.0-45-generic" || s.CPUModel != "Test CPU" {
		t.Errorf("system = %+v", s)
	}
	if s.UptimeSeconds != 7200 || !s.BootTime.Equal(time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("uptime = %d, boot = %v", s.UptimeSeconds, s.BootTime)
	}
	if s.Load1 != 0.5 || s.Load15 != 0.3 {
		t.Errorf("load = %v %v %v", s.Load1, s.Load5, s.Load15)
	}
	if info.Memory.Total != 8000*1024 || info.Memory.Available != 5000*1024 {
		t.Errorf("memory = %+v", info.Memory)
	}

	if len(info.Filesystems) != 2 {
		t.Fatalf("filesystems = %+v", info.Filesystems)
	}
	root := info.Filesystems[0]
	if root.Mountpoint != "/" || root.Total != 100<<30 || root.Used != 60<<30 || root.Avail != 35<<30 || root.InodesTotal != 1000 {
		t.Errorf("root = %+v", root)
	}
	// df: used / (used + avail) = 60 / 95
	if root.UsedPercent < 63.1 || root.UsedPercent > 63.2 {
		t.Errorf("used percent = %v, want about 63.16", root.UsedPercent)
	}
	if data := info.Filesystems[1]; !data.ReadOnly || data.UsedPercent != 0 {
		t.Errorf("data = %+v", data)
	}

	byPort := map[int]Listener{}
	for _, l := range info.Listeners {
		byPort[l.Port] = l
	}
	if l := byPort[8080]; l.PID != 100 || l.Process != "myapp" || !l.Loopback || l.AllInterfaces {
		t.Errorf("8080 = %+v", l)
	}
	if l := byPort[22]; l.PID != 200 || l.Process != "sshd" || !l.AllInterfaces {
		t.Errorf("22 = %+v", l)
	}
	if l := byPort[8000]; l.Protocol != "tcp6" || l.Address != "::" || !l.AllInterfaces || l.PID != 0 {
		t.Errorf("8000 = %+v", l)
	}
	if l := byPort[53]; l.Protocol != "udp" || !l.AllInterfaces {
		t.Errorf("53 = %+v", l)
	}
	if len(info.Listeners) != 4 || info.Listeners[0].Port != 22 {
		t.Errorf("listeners = %+v (want 4, sorted by port)", info.Listeners)
	}

	if info.Systemd == nil || len(info.Systemd.Services) != 2 || info.Systemd.Services[1].Active != "failed" ||
		len(info.Systemd.Enabled) != 1 || len(info.Systemd.Timers) != 1 || info.Systemd.Timers[0].Name != "logrotate.timer" {
		t.Errorf("systemd = %+v", info.Systemd)
	}

	var sources []string
	for _, e := range info.Cron {
		sources = append(sources, e.Source)
	}
	if got := strings.Join(sources, ","); got != "/etc/cron.d/app,/etc/cron.daily,/etc/crontab" {
		t.Errorf("cron sources = %s (the .placeholder file must be ignored)", got)
	}

	// 3 sockets (8000, 53 and the ones of process 300) cannot be attributed.
	joined := strings.Join(info.Notes, "\n")
	if !strings.Contains(joined, "2 of 4 listening sockets have no process") || !strings.Contains(joined, "1 processes") {
		t.Errorf("notes = %q", info.Notes)
	}
	if !strings.Contains(joined, "crontabs of the users are not readable") {
		t.Errorf("notes = %q, want the crontab one", info.Notes)
	}
}

func TestCollect_NoSecretFromCronReachesTheReport(t *testing.T) {
	info, _ := collectInfo(t, newMachine(t))
	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "pw-value") {
		t.Errorf("a secret of a cron job is in the report: %s", raw)
	}
	masked := false
	for _, e := range info.Cron {
		masked = masked || e.Command == "/srv/app/run --password <redacted>"
	}
	if !masked {
		t.Errorf("the cron job lost its masked form: %+v", info.Cron)
	}
}

func TestCollect_NotLinuxReturnsTheBasicsOnly(t *testing.T) {
	c := newMachine(t).collector()
	c.linux = false
	c.fsys = fstest.MapFS{} // must not be read

	got, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	info := got.(Info)
	if info.System != nil || info.Memory != nil || info.Systemd != nil || len(info.Filesystems) != 0 {
		t.Errorf("details collected on a non-Linux system: %+v", info)
	}
	if len(info.Notes) != 1 || !strings.Contains(info.Notes[0], "only collected on Linux") {
		t.Errorf("notes = %v", info.Notes)
	}
	if info.Listeners == nil || info.Cron == nil || info.Filesystems == nil {
		t.Error("lists must be empty, not null, in the JSON")
	}
}

func TestCollect_APartFailingKeepsTheOthers(t *testing.T) {
	m := newMachine(t)
	delete(m.files, "proc/meminfo")
	m.files["proc/uptime"] = file("garbage\n")
	m.statErr = map[string]error{"/mnt/data": errors.New("permission denied")}
	delete(m.cmds, "list-timers")

	info, err := collectInfo(t, m)
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"memory:", "system: uptime:", "filesystem /mnt/data: permission denied", "systemd: systemctl list-timers"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}

	if info.Memory != nil {
		t.Error("memory must be absent when /proc/meminfo is missing")
	}
	if info.System == nil || info.System.Kernel == "" || info.System.UptimeSeconds != 0 {
		t.Errorf("the rest of the system data must survive: %+v", info.System)
	}
	if len(info.Filesystems) != 1 || info.Filesystems[0].Mountpoint != "/" {
		t.Errorf("filesystems = %+v", info.Filesystems)
	}
	if len(info.Listeners) == 0 || len(info.Cron) == 0 {
		t.Error("listeners and cron must be unaffected")
	}
	if info.Systemd == nil || len(info.Systemd.Services) != 2 || len(info.Systemd.Timers) != 0 {
		t.Errorf("systemd = %+v: services must be kept, timers empty", info.Systemd)
	}
}

func TestCollect_NoSystemdMeansNoCommandsAndNoError(t *testing.T) {
	m := newMachine(t)
	delete(m.files, "run/systemd/system/.keep")

	info, err := collectInfo(t, m)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if info.Systemd != nil || len(m.runs) != 0 {
		t.Errorf("systemd = %+v, commands run = %v", info.Systemd, m.runs)
	}
}

func TestCollect_HungMountTimesOut(t *testing.T) {
	old := statfsTimeout
	statfsTimeout = 30 * time.Millisecond
	defer func() { statfsTimeout = old }()

	m := newMachine(t)
	c := m.collector()
	release := make(chan struct{})
	defer close(release)
	c.statfs = func(mp string) (fsUsage, error) {
		if mp == "/mnt/data" {
			<-release // a hung NFS share never answers
		}
		return m.usage[mp], nil
	}

	start := time.Now()
	got, err := c.Collect(context.Background())
	if time.Since(start) > 5*time.Second {
		t.Fatal("a hung mount blocked the scan")
	}
	info := got.(Info)
	if err == nil || !strings.Contains(err.Error(), "filesystem /mnt/data: timed out") {
		t.Errorf("error = %v", err)
	}
	if len(info.Filesystems) != 1 {
		t.Errorf("filesystems = %+v, want only the root", info.Filesystems)
	}
}

func TestCollect_UserCrontabsWhenReadable(t *testing.T) {
	m := newMachine(t)
	delete(m.denied, "var/spool/cron/crontabs")
	m.files["var/spool/cron/crontabs/alice"] = file("0 3 * * * /home/alice/backup.sh\n")

	info, err := collectInfo(t, m)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range info.Cron {
		if e.User == "alice" && e.Command == "/home/alice/backup.sh" {
			found = true
		}
	}
	if !found {
		t.Errorf("alice's crontab is missing: %+v", info.Cron)
	}
	for _, n := range info.Notes {
		if strings.Contains(n, "crontabs") {
			t.Errorf("unexpected note %q", n)
		}
	}
}

func TestCollect_SocketsOfAnotherNamespaceAreExplained(t *testing.T) {
	m := newMachine(t)
	delete(m.denied, "proc/300/fd") // as root: nothing is off limits, yet some sockets have no visible owner

	info, _ := collectInfo(t, m)
	// The first note is about sockets; it must not blame permissions.
	if len(info.Notes) == 0 || !strings.Contains(info.Notes[0], "2 of 4 listening sockets have no process the probe can see") || strings.Contains(info.Notes[0], "Run it as root") {
		t.Errorf("notes = %q", info.Notes)
	}
}

func TestCollect_FirstProcessToHoldASocketOwnsIt(t *testing.T) {
	m := newMachine(t)
	// A forked child shares the listening socket with its parent.
	m.files["proc/101/fd/3"] = file("")
	m.files["proc/101/comm"] = file("myapp-worker\n")
	m.links["proc/101/fd/3"] = "socket:[1001]"

	info, _ := collectInfo(t, m)
	for _, l := range info.Listeners {
		if l.Port == 8080 && l.PID != 100 {
			t.Errorf("8080 owned by %d (%s), want the first process found (100)", l.PID, l.Process)
		}
	}
}
