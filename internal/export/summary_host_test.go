package export

import (
	"strings"
	"testing"
	"time"

	"github.com/netseven/infra-probe/internal/report"
)

func hostReport() *report.Report {
	return &report.Report{
		Version:     "1.0.0",
		GeneratedAt: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC),
		Results: map[string]any{
			"host": map[string]any{
				"hostname": "web01", "os": "linux", "arch": "amd64", "num_cpu": 4, "uid": 1000,
				"system": map[string]any{
					"pretty_name": "Ubuntu 24.04.1 LTS", "distro": "ubuntu", "kernel": "6.8.0-45-generic", "cpu_model": "Test CPU",
					"uptime_seconds": 273600, "boot_time": "2026-10-31T06:00:00Z", "load_1": 0.5, "load_5": 0.4, "load_15": 0.3,
				},
				"memory": map[string]any{"total": 8589934592, "available": 4294967296, "free": 1073741824, "swap_total": 2147483648, "swap_free": 1073741824},
				"filesystems": []any{
					map[string]any{"device": "/dev/sda1", "mountpoint": "/", "type": "ext4", "total": 107374182400, "used": 64424509440, "avail": 37580963840,
						"used_percent": 63.2, "inodes_total": 1000, "inodes_free": 900},
					map[string]any{"device": "/dev/sdb1", "mountpoint": "/mnt/data", "type": "xfs", "read_only": true, "total": 1073741824, "used": 0, "avail": 1073741824},
					// A 9p share reporting more free inodes than inodes.
					map[string]any{"device": "C:", "mountpoint": "/mnt/c", "type": "9p", "total": 1073741824, "used": 0, "avail": 1073741824, "inodes_total": 1000, "inodes_free": 1001000},
				},
				"listeners": []any{
					map[string]any{"protocol": "tcp", "address": "0.0.0.0", "port": 22, "all_interfaces": true, "pid": 200, "process": "sshd"},
					map[string]any{"protocol": "tcp", "address": "127.0.0.1", "port": 5432, "loopback": true},
					map[string]any{"protocol": "tcp6", "address": "::", "port": 8080, "all_interfaces": true},
				},
				"systemd": map[string]any{
					"services": []any{
						map[string]any{"name": "nginx.service", "active": "active", "sub": "running"},
						map[string]any{"name": "broken.service", "active": "failed", "sub": "failed"},
					},
					"enabled": []any{"nginx.service"},
					"timers":  []any{map[string]any{"name": "logrotate.timer", "activates": "logrotate.service"}},
				},
				"cron":  []any{map[string]any{"source": "/etc/crontab", "schedule": "17 * * * *", "user": "root", "command": "run-parts /etc/cron.hourly"}},
				"notes": []any{"2 of 3 listening sockets have no process: run the probe as root."},
			},
			"docker": map[string]any{
				"containers": []any{
					map[string]any{"id": "aaaaaaaaaaaa", "name": "web",
						"inspect": map[string]any{"NetworkSettings": map[string]any{"Ports": map[string]any{
							"80/tcp":  []any{map[string]any{"HostIp": "0.0.0.0", "HostPort": "8080"}},
							"443/tcp": nil,
						}}}},
				},
			},
		},
	}
}

func TestWrite_HostSummary(t *testing.T) {
	sum := buildZip(t, hostReport())["SUMMARY.md"]

	for _, want := range []string{
		"- **Name**: web01 (linux/amd64, 4 CPUs)",
		"- **System**: Ubuntu 24.04.1 LTS, kernel 6.8.0-45-generic",
		"- **CPU**: Test CPU",
		"- **Uptime**: 3d 4h (booted 2026-10-31 06:00:00 UTC), load 0.50 0.40 0.30",
		"- **Memory**: 8.0 GiB total, 4.0 GiB available; swap 2.0 GiB (1.0 GiB free)",
		"- **Probe runs as**: uid 1000",
		"What the probe could not see:",
		"- 2 of 3 listening sockets have no process: run the probe as root.",
		"| / | /dev/sda1 | ext4 | 100.0 GiB | 60.0 GiB | 35.0 GiB | 63.2 | 90% |",
		"| /mnt/data | /dev/sdb1 | xfs (read-only) |",
		"| /mnt/c | C: | 9p | 1.0 GiB | 0 B | 1.0 GiB | 0.0 | — |",
		"| 22 | tcp | 0.0.0.0 | all interfaces | sshd (200) | — |",
		"| 5432 | tcp | 127.0.0.1 | local only | — | — |",
		// tcp6 8080 is traced to the container publishing 8080/tcp.
		"| 8080 | tcp6 | :: | all interfaces | — | web |",
		"- **Running**: nginx.service",
		"- **Failed**: broken.service",
		"- **Timers**: logrotate.timer → logrotate.service",
		"| 17 * * * * | root | run-parts /etc/cron.hourly | /etc/crontab |",
	} {
		if !strings.Contains(sum, want) {
			t.Errorf("SUMMARY.md lacks %q\n%s", want, sum)
		}
	}
}

func TestWrite_HostSummaryWithoutDetails(t *testing.T) {
	r := &report.Report{Results: map[string]any{"host": map[string]any{
		"hostname": "laptop", "os": "windows", "arch": "amd64", "num_cpu": 12, "uid": -1,
		"notes": []any{"System details are only collected on Linux."},
	}}}
	sum := buildZip(t, r)["SUMMARY.md"]

	if !strings.Contains(sum, "- **Name**: laptop (windows/amd64, 12 CPUs)") || !strings.Contains(sum, "only collected on Linux") {
		t.Errorf("SUMMARY.md:\n%s", sum)
	}
	for _, unwanted := range []string{"### Disks", "### Listening ports", "### Services", "### Scheduled jobs", "map["} {
		if strings.Contains(sum, unwanted) {
			t.Errorf("SUMMARY.md contains %q:\n%s", unwanted, sum)
		}
	}
}

func TestWrite_HostOnlyExportKeepsListenersWithoutContainers(t *testing.T) {
	files := buildZipOnly(t, hostReport(), []string{CategoryHost})
	sum := files["SUMMARY.md"]

	if !strings.Contains(sum, "| 8080 | tcp6 | :: | all interfaces | — | — |") {
		t.Errorf("without the containers the listener must have no container:\n%s", sum)
	}
	if strings.Contains(sum, "web") && strings.Contains(sum, "| web |") {
		t.Error("a container leaked into a host-only export")
	}
}
