package docker

import (
	"context"
	"errors"
	"io/fs"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/registry"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"
)

func daemonFixture() *fakeAPI {
	return &fakeAPI{
		version: client.ServerVersionResult{
			Version: "27.3.1", APIVersion: "1.47", MinAPIVersion: "1.24", Os: "linux", Arch: "amd64",
			Platform: client.PlatformInfo{Name: "Docker Engine - Community"},
			Components: []system.ComponentVersion{
				{Name: "Engine", Version: "27.3.1", Details: map[string]string{"GitCommit": "41ca978", "GoVersion": "go1.22.7"}},
				{Name: "runc", Version: "1.1.14"},
			},
		},
		info: system.Info{
			ID: "ABCD", Name: "web01", ServerVersion: "27.3.1",
			OperatingSystem: "Ubuntu 24.04 LTS", OSType: "linux", Architecture: "x86_64", KernelVersion: "6.8.0",
			NCPU: 4, MemTotal: 8 << 30, DockerRootDir: "/var/lib/docker",
			Driver: "overlay2", DriverStatus: [][2]string{{"Backing Filesystem", "extfs"}},
			LoggingDriver: "json-file", CgroupDriver: "systemd", CgroupVersion: "2",
			DefaultRuntime:     "runc",
			Runtimes:           map[string]system.RuntimeWithStatus{"runc": {}, "io.containerd.runc.v2": {}},
			LiveRestoreEnabled: true,
			SecurityOptions:    []string{"name=apparmor", "name=seccomp,profile=builtin", "name=rootless"},
			HTTPProxy:          "http://user:proxypw@proxy:3128", NoProxy: "localhost",
			Labels:     []string{"env=prod", "vault_token=abc"},
			Warnings:   []string{"API is accessible on http://0.0.0.0:2375 without encryption."},
			Containers: 5, ContainersRunning: 3, ContainersPaused: 0, ContainersStopped: 2, Images: 12,
			Plugins: system.PluginsInfo{Volume: []string{"local"}, Log: []string{"json-file"}},
			RegistryConfig: &registry.ServiceConfig{
				InsecureRegistryCIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
				Mirrors:               []string{"https://mirror:secret@mirror.example.com/"},
				IndexConfigs: map[string]*registry.IndexInfo{
					"docker.io":     {Name: "docker.io", Secure: true, Official: true},
					"registry.corp": {Name: "registry.corp", Secure: false},
				},
			},
			Swarm: swarm.Info{LocalNodeState: swarm.LocalNodeStateActive, NodeID: "n1", ControlAvailable: true, Managers: 1, Nodes: 3},
			DefaultAddressPools: []system.NetworkAddressPool{
				{Base: netip.MustParsePrefix("172.30.0.0/16"), Size: 24},
			},
			FirewallBackend: &system.FirewallInfo{Driver: "iptables+firewalld", Info: [][2]string{{"ReloadedAt", "x"}}},
		},
		dfTotals: &client.DiskUsageResult{
			Images:     client.ImagesDiskUsage{TotalCount: 12, ActiveCount: 3, TotalSize: 1000, Reclaimable: 700},
			Containers: client.ContainersDiskUsage{TotalCount: 5, ActiveCount: 3, TotalSize: 50, Reclaimable: 10},
			Volumes:    client.VolumesDiskUsage{TotalCount: 2, ActiveCount: 1, TotalSize: 200, Reclaimable: 150},
			BuildCache: client.BuildCacheDiskUsage{TotalCount: 4, ActiveCount: 0, TotalSize: 300, Reclaimable: 300},
		},
	}
}

func collectDaemonFrom(t *testing.T, c *Collector) (*Daemon, error) {
	t.Helper()
	got, err := c.Collect(context.Background())
	res, ok := got.(Result)
	if !ok {
		t.Fatalf("Collect() returned %T, want Result", got)
	}
	return res.Daemon, err
}

func TestCollectDaemon(t *testing.T) {
	d, err := collectDaemonFrom(t, newTestCollector(daemonFixture(), nil))
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	v := d.Version
	if v.Version != "27.3.1" || v.APIVersion != "1.47" || v.MinAPIVersion != "1.24" || v.Platform != "Docker Engine - Community" {
		t.Errorf("version = %+v", v)
	}
	if len(v.Components) != 2 || v.Components[0].Details["GitCommit"] != "41ca978" {
		t.Errorf("components = %+v", v.Components)
	}

	i := d.Info
	if i.StorageDriver != "overlay2" || i.LoggingDriver != "json-file" || i.CgroupDriver != "systemd" || i.CgroupVersion != "2" {
		t.Errorf("drivers wrong: %+v", i)
	}
	if !i.LiveRestore || !i.Rootless {
		t.Errorf("live restore = %v, rootless = %v, want both true", i.LiveRestore, i.Rootless)
	}
	if want := []string{"io.containerd.runc.v2", "runc"}; !reflect.DeepEqual(i.Runtimes, want) {
		t.Errorf("runtimes = %v, want sorted %v", i.Runtimes, want)
	}
	if i.StorageDriverStatus[0] != (KeyValue{"Backing Filesystem", "extfs"}) {
		t.Errorf("driver status = %+v", i.StorageDriverStatus)
	}
	if i.ContainersRunning != 3 || i.Images != 12 || i.NCPU != 4 || i.DockerRootDir != "/var/lib/docker" {
		t.Errorf("counts wrong: %+v", i)
	}
	if len(i.Warnings) != 1 || !strings.Contains(i.Warnings[0], "2375") {
		t.Errorf("warnings = %v", i.Warnings)
	}
	if i.FirewallBackend != "iptables+firewalld" || len(i.DefaultAddressPools) != 1 || i.DefaultAddressPools[0] != (AddressPool{"172.30.0.0/16", 24}) {
		t.Errorf("firewall/pools wrong: %+v %+v", i.FirewallBackend, i.DefaultAddressPools)
	}

	if i.Swarm.State != "active" || !i.Swarm.ControlAvailable || i.Swarm.Nodes != 3 {
		t.Errorf("swarm = %+v", i.Swarm)
	}

	reg := i.Registry
	if !reflect.DeepEqual(reg.InsecureCIDRs, []string{"10.0.0.0/8"}) {
		t.Errorf("insecure cidrs = %v", reg.InsecureCIDRs)
	}
	if len(reg.Indexes) != 2 || reg.Indexes[0].Name != "docker.io" || reg.Indexes[1].Secure {
		t.Errorf("indexes = %+v", reg.Indexes)
	}

	du := d.DiskUsage
	if du.Images.Reclaimable != 700 || du.BuildCache.Size != 300 || du.Reclaimable != 1160 || du.TotalSize != 1550 {
		t.Errorf("disk usage = %+v", du)
	}
	if d.Config != nil {
		t.Error("config must be nil when daemon.json does not exist")
	}
}

func TestCollectDaemon_SecretsAreRedacted(t *testing.T) {
	d, err := collectDaemonFrom(t, newTestCollector(daemonFixture(), nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Info.Proxy.HTTP; got != "http://user:<redacted>@proxy:3128" {
		t.Errorf("proxy = %q", got)
	}
	if got := d.Info.Registry.Mirrors[0]; got != "https://mirror:<redacted>@mirror.example.com/" {
		t.Errorf("mirror = %q", got)
	}
	if want := []string{"env=prod", "vault_token=<redacted>"}; !reflect.DeepEqual(d.Info.Labels, want) {
		t.Errorf("labels = %v, want %v", d.Info.Labels, want)
	}
}

func TestCollectDaemon_SwarmInactiveByDefault(t *testing.T) {
	api := daemonFixture()
	api.info.Swarm = swarm.Info{}
	d, _ := collectDaemonFrom(t, newTestCollector(api, nil))
	if d.Info.Swarm.State != "inactive" {
		t.Errorf("state = %q, want inactive", d.Info.Swarm.State)
	}
}

func TestCollectDaemon_Config(t *testing.T) {
	c := newTestCollector(daemonFixture(), nil)
	c.readFile = func(path string) ([]byte, error) {
		if path != "/etc/docker/daemon.json" {
			t.Errorf("read %q", path)
		}
		return []byte(`{
			"live-restore": true,
			"tlskey": "/etc/docker/key.pem",
			"proxies": {"http-proxy": "http://u:pw@p:1"},
			"log-driver": "splunk",
			"log-opts": {"splunk-token": "TOKEN123", "max-size": "10m"},
			"registry-mirrors": ["https://m:mpw@mirror/"]
		}`), nil
	}

	d, err := collectDaemonFrom(t, c)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if d.Config == nil || d.Config.Path != "/etc/docker/daemon.json" {
		t.Fatalf("config = %+v", d.Config)
	}
	want := map[string]any{
		"live-restore":     true,
		"tlskey":           "/etc/docker/key.pem", // a path, not a secret
		"proxies":          map[string]any{"http-proxy": "http://u:<redacted>@p:1"},
		"log-driver":       "splunk",
		"log-opts":         map[string]any{"splunk-token": redacted, "max-size": "10m"},
		"registry-mirrors": []any{"https://m:<redacted>@mirror/"},
	}
	if !reflect.DeepEqual(d.Config.Content, want) {
		t.Errorf("content = %v\nwant     %v", d.Config.Content, want)
	}
}

func TestCollectDaemon_ConfigErrors(t *testing.T) {
	tests := []struct {
		name    string
		read    func(string) ([]byte, error)
		wantErr string
	}{
		{"unreadable", func(string) ([]byte, error) { return nil, fs.ErrPermission }, "daemon config:"},
		{"invalid json", func(string) ([]byte, error) { return []byte(`{"token": "SECRETVALUE"`), nil }, "invalid JSON"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestCollector(daemonFixture(), nil)
			c.readFile = tt.read

			d, err := collectDaemonFrom(t, c)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), "SECRETVALUE") {
				t.Error("error leaks file content")
			}
			if d.Info == nil || d.Version == nil {
				t.Error("the other parts must still be collected")
			}
		})
	}
}

func TestCollectDaemon_FirstExistingConfigWins(t *testing.T) {
	c := newTestCollector(daemonFixture(), nil)
	c.configPaths = func(bool) []string { return []string{"/a.json", "/b.json", "/c.json"} }
	c.readFile = func(path string) ([]byte, error) {
		if path == "/a.json" {
			return nil, fs.ErrNotExist
		}
		return []byte(`{"debug": true}`), nil
	}
	d, _ := collectDaemonFrom(t, c)
	if d.Config == nil || d.Config.Path != "/b.json" {
		t.Errorf("config = %+v, want /b.json", d.Config)
	}
}

func TestCollectDaemon_RootlessIsPassedToConfigLookup(t *testing.T) {
	var gotRootless bool
	c := newTestCollector(daemonFixture(), nil)
	c.configPaths = func(rootless bool) []string { gotRootless = rootless; return nil }
	if _, err := collectDaemonFrom(t, c); err != nil {
		t.Fatal(err)
	}
	if !gotRootless {
		t.Error("rootless daemon not detected before looking for the config")
	}
}

func TestCollectDaemon_PartialFailures(t *testing.T) {
	api := daemonFixture()
	api.infoErr = errors.New("info denied")
	api.versionErr = errors.New("version denied")
	api.diskUsageErr = errors.New("df timeout")

	d, err := collectDaemonFrom(t, newTestCollector(api, nil))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"daemon info: info denied", "daemon version: version denied", "disk usage: df timeout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if strings.Count(err.Error(), "df timeout") != 1 {
		t.Error("the disk usage failure must be reported once")
	}
	if d == nil || d.Info != nil || d.Version != nil || d.DiskUsage != nil {
		t.Errorf("daemon = %+v, want an empty daemon", d)
	}
}

func TestDefaultConfigPaths(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://10.0.0.1:2375")
	if got := defaultConfigPaths(false); got != nil {
		t.Errorf("remote daemon: got %v, want none", got)
	}
}
