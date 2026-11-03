package export

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/netseven/infra-probe/internal/report"
)

func sampleReport() *report.Report {
	return &report.Report{
		Version:     "1.0.0",
		GeneratedAt: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC),
		Results: map[string]any{
			"host": map[string]any{"hostname": "web01", "os": "linux", "num_cpu": 4},
			"docker": map[string]any{
				"daemon": map[string]any{
					"version": map[string]any{"version": "27.3.1", "api_version": "1.47", "min_api_version": "1.24", "os": "linux", "arch": "amd64",
						"components": []any{map[string]any{"name": "runc", "version": "1.1.14"}}},
					"info": map[string]any{
						"operating_system": "Ubuntu 24.04", "architecture": "x86_64", "kernel_version": "6.8.0", "ncpu": 4, "mem_total": 8589934592,
						"docker_root_dir": "/var/lib/docker", "storage_driver": "overlay2", "logging_driver": "json-file",
						"cgroup_driver": "systemd", "cgroup_version": "2", "default_runtime": "runc", "runtimes": []any{"runc"},
						"live_restore": false, "security_options": []any{"name=seccomp,profile=builtin"},
						"registry": map[string]any{"insecure_cidrs": []any{"10.0.0.0/8"}, "mirrors": []any{},
							"indexes": []any{map[string]any{"name": "registry.corp", "secure": false}}},
						"proxy":    map[string]any{"http": "http://u:daemonproxypw@proxy:3128"},
						"swarm":    map[string]any{"state": "active", "control_available": true, "nodes": 3},
						"labels":   []any{"env=prod"},
						"warnings": []any{"API is accessible on http://0.0.0.0:2375 without encryption."},
					},
					"disk_usage": map[string]any{
						"images":      map[string]any{"total": 12, "active": 3, "size": 1048576, "reclaimable": 524288},
						"containers":  map[string]any{"total": 5, "active": 3, "size": 0, "reclaimable": 0},
						"volumes":     map[string]any{"total": 2, "active": 1, "size": 0, "reclaimable": 0},
						"build_cache": map[string]any{"total": 0, "active": 0, "size": 0, "reclaimable": 0},
						"total_size":  1048576, "reclaimable": 524288,
					},
					"config": map[string]any{"path": "/etc/docker/daemon.json",
						"content": map[string]any{"log-opts": map[string]any{"splunk-token": "daemontok"}, "live-restore": false}},
				},
				"containers": []any{
					map[string]any{
						"id": "aaaaaaaaaaaa", "name": "db", "image": "postgres:16", "state": "running", "status": "Up 2 days",
						"stats": map[string]any{"cpu_percent": 1.5, "memory_usage": 104857600, "memory_limit": 0},
						"inspect": map[string]any{
							"Config": map[string]any{
								"Env":    []any{"POSTGRES_PASSWORD=hunter2", "PGDATA=/var/lib/postgresql/data"},
								"Cmd":    []any{"postgres", "--password", "hunter2", "--api-key=abc123"},
								"Labels": map[string]any{"com.example.db_token": "tok-xyz", "com.example.team": "infra"},
								"User":   "",
							},
							"HostConfig": map[string]any{
								"Privileged":    true,
								"NetworkMode":   "host",
								"Memory":        0,
								"RestartPolicy": map[string]any{"Name": "always"},
								"PortBindings":  map[string]any{"5432/tcp": []any{map[string]any{"HostIp": "", "HostPort": "5432"}}},
							},
							"Mounts": []any{
								map[string]any{"Type": "bind", "Source": "/var/run/docker.sock", "Destination": "/var/run/docker.sock", "RW": true},
							},
							"Args": []any{"sh", "-c", "psql postgres://admin:s3cr3t@db/app --password=topsecret"},
						},
					},
					// Same name: must still get its own file.
					map[string]any{"id": "bbbbbbbbbbbb", "name": "db", "image": "postgres:16", "state": "exited"},
				},
				"images": []any{
					map[string]any{"id": "sha256:0123456789abcdef", "repo_tags": []any{"secret-app:1.0"}, "size": 2048, "containers": 1},
					map[string]any{"id": "sha256:fedcba9876543210", "repo_tags": []any{}, "dangling": true},
				},
				"networks": []any{
					map[string]any{"id": "nnnnnnnnnnnn", "name": "backend", "driver": "bridge",
						"containers": []any{map[string]any{"container_id": "aaaaaaaaaaaa", "name": "db"}}},
				},
				"volumes": []any{
					map[string]any{"name": "pgdata", "driver": "local", "size": 4096, "anonymous": false,
						"labels":     map[string]any{"com.docker.compose.project": "app"},
						"containers": []any{map[string]any{"container_id": "aaaaaaaaaaaa", "container_name": "db", "destination": "/data"}},
						"inspect":    map[string]any{"Options": map[string]any{"o": "addr=nas,password=nfspass"}}},
					map[string]any{"name": "orphan", "driver": "local", "size": -1, "containers": []any{}},
				},
			},
		},
		Errors: map[string]string{"docker": "container x: stats: timeout"},
	}
}

// readZip returns the archive entries by name.
func readZip(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("invalid zip: %v", err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = string(body)
	}
	return files
}

func buildZip(t *testing.T, r *report.Report) map[string]string {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, r); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return readZip(t, buf.Bytes())
}

func TestWrite_Layout(t *testing.T) {
	files := buildZip(t, sampleReport())

	want := []string{
		"README.md", "SUMMARY.md", "manifest.json", "report.json", "errors.json",
		"collectors/host.json",
		"docker/containers/index.json", "docker/containers/db.json", "docker/containers/db-bbbbbbbbbbbb.json",
		"docker/images/index.json", "docker/images/secret-app_1.0.json", "docker/images/fedcba987654.json",
		"docker/networks/index.json", "docker/networks/backend.json",
		"docker/daemon.json", "docker/volumes/index.json", "docker/volumes/pgdata.json", "docker/volumes/orphan.json",
	}
	for _, name := range want {
		if _, ok := files[name]; !ok {
			t.Errorf("missing %s", name)
		}
	}
	for name, body := range files {
		if strings.HasSuffix(name, ".json") && !json.Valid([]byte(body)) {
			t.Errorf("%s is not valid JSON", name)
		}
	}
}

func TestWrite_IndexHasNoInspectAndPointsToFile(t *testing.T) {
	files := buildZip(t, sampleReport())

	var index []map[string]any
	if err := json.Unmarshal([]byte(files["docker/containers/index.json"]), &index); err != nil {
		t.Fatal(err)
	}
	if len(index) != 2 {
		t.Fatalf("index has %d entries", len(index))
	}
	for _, e := range index {
		if _, ok := e["inspect"]; ok {
			t.Error("index entry contains inspect")
		}
		if _, ok := files[e["file"].(string)]; !ok {
			t.Errorf("file %v not in archive", e["file"])
		}
	}
	if !strings.Contains(files["docker/containers/db.json"], `"inspect"`) {
		t.Error("detail file lacks the inspect payload")
	}
}

func TestWrite_NoSecretLeaks(t *testing.T) {
	files := buildZip(t, sampleReport())

	secrets := []string{"hunter2", "abc123", "tok-xyz", "s3cr3t", "topsecret", "nfspass", "daemontok", "daemonproxypw"}
	for name, body := range files {
		for _, s := range secrets {
			if strings.Contains(body, s) {
				t.Errorf("%s leaks %q", name, s)
			}
		}
	}
	// Names are kept so the reader knows which settings exist.
	if !strings.Contains(files["report.json"], "POSTGRES_PASSWORD=<redacted>") {
		t.Error("env variable name lost")
	}
	// Non-secret values survive.
	for _, keep := range []string{"com.example.team", "infra", "secret-app:1.0", "postgres://admin:<redacted>@db/app"} {
		if !strings.Contains(files["report.json"], keep) {
			t.Errorf("report.json lost %q", keep)
		}
	}
}

func TestWrite_Summary(t *testing.T) {
	sum := buildZip(t, sampleReport())["SUMMARY.md"]

	for _, want := range []string{
		"# Server summary: web01",
		"privileged", "network=host", "runs as root", "no memory limit", "docker socket mounted",
		"0.0.0.0:5432->5432/tcp",
		"`docker/containers/db.json`",
		"| backend | bridge |",
		"- Volumes: 2 (1 unused)",
		"| pgdata | 4.0 KiB | db:/data | app | local | false | `docker/volumes/pgdata.json` |",
		"| orphan | unknown |",
		"## Collection errors",
	} {
		if !strings.Contains(sum, want) {
			t.Errorf("SUMMARY.md lacks %q", want)
		}
	}
}

func TestWrite_DaemonSummary(t *testing.T) {
	files := buildZip(t, sampleReport())
	sum := files["SUMMARY.md"]

	for _, want := range []string{
		"## Docker daemon",
		"**Engine**: 27.3.1 (API 1.47, minimum 1.24), linux/amd64",
		"**Components**: runc 1.1.14",
		"**Storage driver**: overlay2",
		"**Logging driver**: json-file",
		"**Cgroup**: driver systemd, version 2",
		"**Live restore**: false",
		"**Insecure registries/networks**: 10.0.0.0/8, registry.corp",
		"http http://u:<redacted>@proxy:3128",
		"**Swarm**: active (manager, 3 nodes)",
		"API is accessible on http://0.0.0.0:2375 without encryption.",
		"**daemon.json**: `/etc/docker/daemon.json`, keys: live-restore, log-opts",
		"| Images | 12 | 3 | 1.0 MiB | 512.0 KiB |",
	} {
		if !strings.Contains(sum, want) {
			t.Errorf("SUMMARY.md lacks %q", want)
		}
	}
	if !strings.Contains(files["docker/daemon.json"], `"splunk-token": "<redacted>"`) {
		t.Error("daemon.json file does not keep the redacted option")
	}
}

func TestWrite_NoDaemonSection(t *testing.T) {
	r := sampleReport()
	delete(r.Results["docker"].(map[string]any), "daemon")
	files := buildZip(t, r)

	if _, ok := files["docker/daemon.json"]; ok {
		t.Error("daemon.json written without daemon data")
	}
	if strings.Contains(files["SUMMARY.md"], "## Docker daemon") {
		t.Error("summary has a daemon section without daemon data")
	}
}

func TestWrite_IsDeterministic(t *testing.T) {
	var a, b bytes.Buffer
	if err := Write(&a, sampleReport()); err != nil {
		t.Fatal(err)
	}
	if err := Write(&b, sampleReport()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Error("same report produced different archives")
	}
}

func TestWrite_EmptyReport(t *testing.T) {
	files := buildZip(t, &report.Report{Results: map[string]any{}})
	if !strings.Contains(files["SUMMARY.md"], "No Docker data") {
		t.Error("summary does not mention the missing docker data")
	}
	if _, ok := files["errors.json"]; ok {
		t.Error("errors.json written without errors")
	}
}

func TestWrite_UnserializableReport(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, &report.Report{Results: map[string]any{"bad": make(chan int)}}); err == nil {
		t.Error("expected an error")
	}
}

func TestFileName(t *testing.T) {
	r := sampleReport()
	r.Results["host"] = map[string]any{"hostname": "web 01/../x"}
	if got, want := FileName(r), "infra-probe-web_01_.._x-20261103T100000Z.zip"; got != want {
		t.Errorf("FileName = %q, want %q", got, want)
	}
}
