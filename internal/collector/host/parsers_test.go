package host

import (
	"net"
	"reflect"
	"strings"
	"testing"
)

func TestParseOSRelease(t *testing.T) {
	got := parseOSRelease([]byte(`# a comment
PRETTY_NAME="Ubuntu 24.04.1 LTS"
NAME='Ubuntu'
VERSION_ID="24.04"
VERSION_CODENAME=noble

not a pair
ID=ubuntu
`))
	want := map[string]string{"PRETTY_NAME": "Ubuntu 24.04.1 LTS", "NAME": "Ubuntu", "VERSION_ID": "24.04", "VERSION_CODENAME": "noble", "ID": "ubuntu"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseUptimeAndLoad(t *testing.T) {
	if up, err := parseUptime([]byte("12345.67 54321.00\n")); err != nil || up != 12345 {
		t.Errorf("uptime = (%d, %v)", up, err)
	}
	for _, bad := range []string{"", "abc 1", "-5 1"} {
		if _, err := parseUptime([]byte(bad)); err == nil {
			t.Errorf("parseUptime(%q) must fail", bad)
		}
	}

	l1, l5, l15, err := parseLoadAvg([]byte("0.12 0.34 0.56 1/234 5678\n"))
	if err != nil || l1 != 0.12 || l5 != 0.34 || l15 != 0.56 {
		t.Errorf("load = (%v %v %v, %v)", l1, l5, l15, err)
	}
	for _, bad := range []string{"", "0.1 0.2", "a b c"} {
		if _, _, _, err := parseLoadAvg([]byte(bad)); err == nil {
			t.Errorf("parseLoadAvg(%q) must fail", bad)
		}
	}
}

func TestParseCPUModel(t *testing.T) {
	data := "processor\t: 0\nvendor_id\t: GenuineIntel\nmodel name\t: Intel(R) Xeon(R) CPU @ 2.20GHz\nprocessor\t: 1\nmodel name\t: other\n"
	if got := parseCPUModel([]byte(data)); got != "Intel(R) Xeon(R) CPU @ 2.20GHz" {
		t.Errorf("model = %q", got)
	}
	if got := parseCPUModel([]byte("Hardware\t: BCM2835\n")); got != "BCM2835" {
		t.Errorf("arm model = %q", got)
	}
	if parseCPUModel(nil) != "" {
		t.Error("no model must give an empty string")
	}
}

func TestParseMeminfo(t *testing.T) {
	m, err := parseMeminfo([]byte("MemTotal: 8000 kB\nMemFree: 1000 kB\nMemAvailable: 5000 kB\nSwapTotal: 2000 kB\nSwapFree: 1500 kB\nHugePages_Total: 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := &Memory{Total: 8000 * 1024, Available: 5000 * 1024, Free: 1000 * 1024, SwapTotal: 2000 * 1024, SwapFree: 1500 * 1024}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("got %+v, want %+v", m, want)
	}

	old, err := parseMeminfo([]byte("MemTotal: 8000 kB\nMemFree: 1000 kB\n"))
	if err != nil || old.Available != 1000*1024 {
		t.Errorf("without MemAvailable: %+v, %v (want Free as fallback)", old, err)
	}
	if _, err := parseMeminfo([]byte("MemFree: 1 kB\n")); err == nil {
		t.Error("a file without MemTotal must be an error")
	}
}

func TestParseMountsAndUnescape(t *testing.T) {
	mounts := parseMounts([]byte("/dev/sdb1 /mnt/data\\040disk xfs ro,relatime 0 0\nshort line\n/dev/sda1 / ext4 rw 0 0\n"))
	if len(mounts) != 2 {
		t.Fatalf("got %d mounts", len(mounts))
	}
	if mounts[0].mountpoint != "/mnt/data disk" || !mounts[0].readOnly || mounts[0].fstype != "xfs" {
		t.Errorf("mount 0 = %+v", mounts[0])
	}
	if mounts[1].readOnly {
		t.Error("rw mount flagged read-only")
	}
	for in, want := range map[string]string{"plain": "plain", "a\\040b": "a b", "a\\011b": "a\tb", "back\\134slash": "back\\slash", "bad\\zzz": "bad\\zzz", "end\\04": "end\\04"} {
		if got := unescapeMount(in); got != want {
			t.Errorf("unescapeMount(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFilterMounts(t *testing.T) {
	var in []mountEntry
	for _, line := range []string{
		"/dev/sda1 / ext4",
		"proc /proc proc",
		"sysfs /sys sysfs",
		"tmpfs /run tmpfs",
		"tmpfs /tmp tmpfs",
		"/dev/sda1 /srv/bind ext4",
		"/dev/sda1 /var/lib/docker/plugins ext4",
		"overlay /var/lib/docker/overlay2/x/merged overlay",
		"/dev/loop0 /snap/core/1 squashfs",
		"/dev/sdb1 /mnt/data xfs",
		"server:/export /srv/nfs nfs4",
		"server:/export /srv/nfs2 nfs4",
		"cgroup2 /sys/fs/cgroup cgroup2",
		"rootfs /init rootfs",
	} {
		f := strings.Fields(line)
		in = append(in, mountEntry{device: f[0], mountpoint: f[1], fstype: f[2]})
	}

	var got []string
	for _, m := range filterMounts(in) {
		got = append(got, m.mountpoint)
	}
	want := []string{"/", "/mnt/data", "/srv/nfs", "/tmp"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("kept %v, want %v", got, want)
	}
}

func TestFilterMounts_RootIsKeptWhateverItsType(t *testing.T) {
	got := filterMounts([]mountEntry{{device: "overlay", mountpoint: "/", fstype: "overlay"}})
	if len(got) != 1 {
		t.Errorf("root on overlay was dropped: %v", got)
	}
}

func TestParseSocketAddr(t *testing.T) {
	tests := []struct {
		in   string
		ip   string
		port int
		ok   bool
	}{
		{"0100007F:1F90", "127.0.0.1", 8080, true},
		{"00000000:0016", "0.0.0.0", 22, true},
		{"0A00A8C0:01BB", "192.168.0.10", 443, true},
		{"00000000000000000000000001000000:0050", "::1", 80, true},
		{"00000000000000000000000000000000:1F40", "::", 8000, true},
		{"0000000000000000FFFF00000100007F:0035", "::ffff:127.0.0.1", 53, true},
		{"nonsense", "", 0, false},
		{"0100007:1F90", "", 0, false},
		{"0100007F:ZZZZ", "", 0, false},
		{"0100007F:10000", "", 0, false},
	}
	for _, tt := range tests {
		ip, port, ok := parseSocketAddr(tt.in)
		if ok != tt.ok {
			t.Errorf("parseSocketAddr(%q) ok = %v", tt.in, ok)
			continue
		}
		if ok && (ip.String() != net.ParseIP(tt.ip).String() || port != tt.port) {
			t.Errorf("parseSocketAddr(%q) = %s:%d, want %s:%d", tt.in, ip, port, tt.ip, tt.port)
		}
	}
}

const tcpTable = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1001 1 0000000000000000 100 0 0 10 0
   1: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1002 1 0000000000000000 100 0 0 10 0
   2: 0100007F:1F90 0100007F:D2A0 01 00000000:00000000 00:00000000 00000000     0        0 1099 1 0000000000000000 20 4 30 10 -1
   3: broken
`

func TestParseSocketTable(t *testing.T) {
	got := parseSocketTable([]byte(tcpTable), "tcp")
	if len(got) != 2 {
		t.Fatalf("got %d sockets, want only the 2 in LISTEN state: %+v", len(got), got)
	}
	if got[0].port != 8080 || got[0].inode != 1001 || got[1].port != 22 || got[1].inode != 1002 {
		t.Errorf("sockets = %+v", got)
	}

	udp := "  sl  local_address rem_address   st\n   0: 00000000:0035 00000000:0000 07 0:0 0:0 0 0 0 2001 2 0\n   1: 0100007F:0044 0100007F:0035 01 0:0 0:0 0 0 0 2002 2 0\n"
	if got := parseSocketTable([]byte(udp), "udp"); len(got) != 1 || got[0].port != 53 {
		t.Errorf("udp sockets = %+v, want only the bound one", got)
	}
}

func TestParseSocketLink(t *testing.T) {
	if inode, ok := parseSocketLink("socket:[12345]"); !ok || inode != 12345 {
		t.Errorf("got (%d, %v)", inode, ok)
	}
	for _, bad := range []string{"anon_inode:[eventpoll]", "pipe:[1]", "socket:[x]", "/dev/null", ""} {
		if _, ok := parseSocketLink(bad); ok {
			t.Errorf("parseSocketLink(%q) must fail", bad)
		}
	}
}

func TestParseUnitsAndTimers(t *testing.T) {
	units := parseUnits([]byte("nginx.service loaded active running A high performance web server\nbroken.service loaded failed failed Some failing job\nshort\n"))
	want := []Unit{
		{Name: "nginx.service", Active: "active", Sub: "running", Description: "A high performance web server"},
		{Name: "broken.service", Active: "failed", Sub: "failed", Description: "Some failing job"},
	}
	if !reflect.DeepEqual(units, want) {
		t.Errorf("units = %+v", units)
	}

	if got := parseUnitFiles([]byte("nginx.service enabled enabled\nssh.service enabled enabled\n")); !reflect.DeepEqual(got, []string{"nginx.service", "ssh.service"}) {
		t.Errorf("unit files = %v", got)
	}

	timers := parseTimers([]byte(`Tue 2026-11-03 12:00:00 UTC 29min left Tue 2026-11-03 11:00:01 UTC 30min ago apt-daily.timer apt-daily.service
n/a n/a Mon 2026-11-02 06:00:00 UTC 1 day 5h ago logrotate.timer logrotate.service

2 timers listed.
`))
	wantTimers := []Timer{{Name: "apt-daily.timer", Activates: "apt-daily.service"}, {Name: "logrotate.timer", Activates: "logrotate.service"}}
	if !reflect.DeepEqual(timers, wantTimers) {
		t.Errorf("timers = %+v", timers)
	}
}

func TestParseCrontab(t *testing.T) {
	pw := "pw" + "-value"
	system := "SHELL=/bin/sh\nAPI_TOKEN=" + pw + "\n# comment\n\n" +
		"17 * * * * root cd / && run-parts --report /etc/cron.hourly\n" +
		"25 6 * * * root backup --password " + pw + " --dest /mnt\n" +
		"@reboot root /usr/local/bin/startup\n" +
		"too short\n" +
		"* * * * * onlyuser\n"
	got := parseCrontab([]byte(system), "/etc/crontab", "")
	want := []CronEntry{
		{Source: "/etc/crontab", Schedule: "17 * * * *", User: "root", Command: "cd / && run-parts --report /etc/cron.hourly"},
		{Source: "/etc/crontab", Schedule: "25 6 * * *", User: "root", Command: "backup --password <redacted> --dest /mnt"},
		{Source: "/etc/crontab", Schedule: "@reboot", User: "root", Command: "/usr/local/bin/startup"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("system crontab:\n got %+v\nwant %+v", got, want)
	}
	for _, e := range got {
		if strings.Contains(e.Command, pw) {
			t.Errorf("secret kept in %q", e.Command)
		}
	}

	user := parseCrontab([]byte("0 3 * * * /home/alice/backup.sh --token="+pw+"\n@daily /home/alice/clean.sh\n"), "/var/spool/cron/alice", "alice")
	if len(user) != 2 || user[0].User != "alice" || user[0].Command != "/home/alice/backup.sh --token=<redacted>" || user[1].Schedule != "@daily" {
		t.Errorf("user crontab = %+v", user)
	}
}
