package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"
)

// KeyValue is an ordered pair, used where the daemon reports a list of
// key/value lines whose keys are driver specific.
type KeyValue struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Daemon describes how the Docker daemon is set up on the server.
type Daemon struct {
	Version   *DaemonVersion `json:"version,omitempty"`
	Info      *DaemonInfo    `json:"info,omitempty"`
	DiskUsage *DiskUsage     `json:"disk_usage,omitempty"`

	// Config is the content of daemon.json, nil when there is none (or the
	// daemon is not on this machine).
	Config *DaemonConfig `json:"config,omitempty"`
}

// DaemonVersion is the engine version as reported by /version.
type DaemonVersion struct {
	Version       string `json:"version"`
	APIVersion    string `json:"api_version"`
	MinAPIVersion string `json:"min_api_version,omitempty"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	Platform      string `json:"platform,omitempty"`
	Experimental  bool   `json:"experimental"`

	// Components are the engine, containerd, runc, docker-init... each with
	// its version and build details (git commit, Go version, build time).
	Components []DaemonComponent `json:"components"`
}

// DaemonComponent is one piece of the engine stack.
type DaemonComponent struct {
	Name    string            `json:"name"`
	Version string            `json:"version"`
	Details map[string]string `json:"details,omitempty"`
}

// DaemonInfo is the configuration and capabilities of the daemon, as reported
// by /info. Credentials in proxy URLs and secret-looking daemon labels are
// redacted.
type DaemonInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"` // host name as seen by the daemon
	ServerVersion string `json:"server_version"`

	OperatingSystem string `json:"operating_system"`
	OSVersion       string `json:"os_version,omitempty"`
	OSType          string `json:"os_type"`
	Architecture    string `json:"architecture"`
	KernelVersion   string `json:"kernel_version"`
	NCPU            int    `json:"ncpu"`
	MemTotal        int64  `json:"mem_total"`

	// DockerRootDir is where images, containers and volumes live on disk.
	DockerRootDir string `json:"docker_root_dir"`

	StorageDriver       string     `json:"storage_driver"`
	StorageDriverStatus []KeyValue `json:"storage_driver_status,omitempty"`
	LoggingDriver       string     `json:"logging_driver"`
	CgroupDriver        string     `json:"cgroup_driver"`
	CgroupVersion       string     `json:"cgroup_version,omitempty"`
	Isolation           string     `json:"isolation,omitempty"`

	DefaultRuntime string   `json:"default_runtime"`
	Runtimes       []string `json:"runtimes"`

	// LiveRestore keeps containers running while the daemon is stopped.
	LiveRestore  bool `json:"live_restore"`
	Debug        bool `json:"debug"`
	Experimental bool `json:"experimental"`

	// Rootless is true when the daemon itself runs without root privileges.
	Rootless        bool     `json:"rootless"`
	SecurityOptions []string `json:"security_options"` // e.g. name=seccomp,profile=builtin

	IPv4Forwarding bool `json:"ipv4_forwarding"`
	MemoryLimit    bool `json:"memory_limit"`
	SwapLimit      bool `json:"swap_limit"`
	PidsLimit      bool `json:"pids_limit"`

	// FirewallBackend is the driver the daemon uses to publish ports
	// (iptables, nftables), with its details.
	FirewallBackend string     `json:"firewall_backend,omitempty"`
	FirewallInfo    []KeyValue `json:"firewall_info,omitempty"`

	// DefaultAddressPools are the ranges new networks are carved from.
	DefaultAddressPools []AddressPool `json:"default_address_pools,omitempty"`

	Plugins  Plugins  `json:"plugins"`
	Registry Registry `json:"registry"`
	Proxy    Proxy    `json:"proxy"`
	Swarm    Swarm    `json:"swarm"`

	// Labels are the daemon labels ("key=value"), the same ones used by
	// swarm constraints.
	Labels []string `json:"labels"`

	Containers        int `json:"containers"`
	ContainersRunning int `json:"containers_running"`
	ContainersPaused  int `json:"containers_paused"`
	ContainersStopped int `json:"containers_stopped"`
	Images            int `json:"images"`

	// Warnings are the daemon's own remarks about its configuration, e.g.
	// "No swap limit support" or "API is accessible on http://0.0.0.0:2375".
	Warnings []string `json:"warnings"`
}

// AddressPool is a base network and the size of the subnets cut from it.
type AddressPool struct {
	Base string `json:"base"`
	Size int    `json:"size"`
}

// Plugins lists the installed plugins by type.
type Plugins struct {
	Volume        []string `json:"volume"`
	Network       []string `json:"network"`
	Log           []string `json:"log"`
	Authorization []string `json:"authorization"`
}

// Registry is the daemon registry configuration. It never holds credentials:
// those live in the client's config.json, which is not read.
type Registry struct {
	// InsecureCIDRs are the networks whose registries may use plain HTTP or
	// untrusted TLS.
	InsecureCIDRs []string        `json:"insecure_cidrs"`
	Mirrors       []string        `json:"mirrors"`
	Indexes       []RegistryIndex `json:"indexes"`
}

// RegistryIndex is one configured registry.
type RegistryIndex struct {
	Name     string   `json:"name"`
	Secure   bool     `json:"secure"`
	Official bool     `json:"official"`
	Mirrors  []string `json:"mirrors,omitempty"`
}

// Proxy is the proxy configuration of the daemon (image pulls).
type Proxy struct {
	HTTP    string `json:"http,omitempty"`
	HTTPS   string `json:"https,omitempty"`
	NoProxy string `json:"no_proxy,omitempty"`
}

// Swarm is the swarm state of the node. Join tokens and keys are never read.
type Swarm struct {
	State            string `json:"state"` // inactive, active, pending, error, locked
	NodeID           string `json:"node_id,omitempty"`
	NodeAddr         string `json:"node_addr,omitempty"`
	ControlAvailable bool   `json:"control_available"` // true on a manager
	Nodes            int    `json:"nodes,omitempty"`
	Managers         int    `json:"managers,omitempty"`
	Error            string `json:"error,omitempty"`
}

// DiskUsage is the space used by Docker objects, like `docker system df`.
type DiskUsage struct {
	Images     UsageTotals `json:"images"`
	Containers UsageTotals `json:"containers"`
	Volumes    UsageTotals `json:"volumes"`
	BuildCache UsageTotals `json:"build_cache"`

	// Reclaimable is the sum of what a prune of each kind would free.
	Reclaimable int64 `json:"reclaimable"`
	TotalSize   int64 `json:"total_size"`
}

// UsageTotals are the totals of one kind of object.
type UsageTotals struct {
	Total       int64 `json:"total"`
	Active      int64 `json:"active"`
	Size        int64 `json:"size"`
	Reclaimable int64 `json:"reclaimable"`
}

// DaemonConfig is the daemon configuration file, with secrets redacted.
type DaemonConfig struct {
	Path    string         `json:"path"`
	Content map[string]any `json:"content"`
}

// collectDaemon gathers version, info, disk usage and daemon.json. Each part
// is independent: a failure is returned together with the parts that worked.
func (c *Collector) collectDaemon(ctx context.Context, api dockerAPI, du *client.DiskUsageResult) (*Daemon, error) {
	var errs []error
	d := &Daemon{}

	if v, err := api.ServerVersion(ctx, client.ServerVersionOptions{}); err != nil {
		errs = append(errs, fmt.Errorf("daemon version: %w", err))
	} else {
		d.Version = versionOf(v)
	}

	rootless := false
	if r, err := api.Info(ctx, client.InfoOptions{}); err != nil {
		errs = append(errs, fmt.Errorf("daemon info: %w", err))
	} else {
		d.Info = infoOf(r.Info)
		rootless = d.Info.Rootless
	}

	if du != nil {
		d.DiskUsage = diskUsageOf(*du)
	}

	cfg, err := c.readDaemonConfig(rootless)
	if err != nil {
		errs = append(errs, err)
	}
	d.Config = cfg

	return d, errors.Join(errs...)
}

func versionOf(v client.ServerVersionResult) *DaemonVersion {
	out := &DaemonVersion{
		Version:       v.Version,
		APIVersion:    v.APIVersion,
		MinAPIVersion: v.MinAPIVersion,
		OS:            v.Os,
		Arch:          v.Arch,
		Platform:      v.Platform.Name,
		Experimental:  v.Experimental,
		Components:    []DaemonComponent{},
	}
	for _, comp := range v.Components {
		out.Components = append(out.Components, DaemonComponent{Name: comp.Name, Version: comp.Version, Details: comp.Details})
	}
	return out
}

func infoOf(i system.Info) *DaemonInfo {
	out := &DaemonInfo{
		ID:                  i.ID,
		Name:                i.Name,
		ServerVersion:       i.ServerVersion,
		OperatingSystem:     i.OperatingSystem,
		OSVersion:           i.OSVersion,
		OSType:              i.OSType,
		Architecture:        i.Architecture,
		KernelVersion:       i.KernelVersion,
		NCPU:                i.NCPU,
		MemTotal:            i.MemTotal,
		DockerRootDir:       i.DockerRootDir,
		StorageDriver:       i.Driver,
		StorageDriverStatus: pairs(i.DriverStatus),
		LoggingDriver:       i.LoggingDriver,
		CgroupDriver:        i.CgroupDriver,
		CgroupVersion:       i.CgroupVersion,
		Isolation:           string(i.Isolation),
		DefaultRuntime:      i.DefaultRuntime,
		Runtimes:            sortedKeys(i.Runtimes),
		LiveRestore:         i.LiveRestoreEnabled,
		Debug:               i.Debug,
		Experimental:        i.ExperimentalBuild,
		SecurityOptions:     nonNil(i.SecurityOptions),
		IPv4Forwarding:      i.IPv4Forwarding,
		MemoryLimit:         i.MemoryLimit,
		SwapLimit:           i.SwapLimit,
		PidsLimit:           i.PidsLimit,
		Plugins: Plugins{
			Volume:        nonNil(i.Plugins.Volume),
			Network:       nonNil(i.Plugins.Network),
			Log:           nonNil(i.Plugins.Log),
			Authorization: nonNil(i.Plugins.Authorization),
		},
		Registry: registryOf(i),
		Proxy: Proxy{
			HTTP:    redactURLCredentials(i.HTTPProxy),
			HTTPS:   redactURLCredentials(i.HTTPSProxy),
			NoProxy: i.NoProxy,
		},
		Swarm:             swarmOf(i),
		Labels:            redactLabels(i.Labels),
		Containers:        i.Containers,
		ContainersRunning: i.ContainersRunning,
		ContainersPaused:  i.ContainersPaused,
		ContainersStopped: i.ContainersStopped,
		Images:            i.Images,
		Warnings:          nonNil(i.Warnings),
	}
	for _, opt := range i.SecurityOptions {
		if opt == "name=rootless" {
			out.Rootless = true
		}
	}
	if fw := i.FirewallBackend; fw != nil {
		out.FirewallBackend = fw.Driver
		out.FirewallInfo = pairs(fw.Info)
	}
	for _, p := range i.DefaultAddressPools {
		out.DefaultAddressPools = append(out.DefaultAddressPools, AddressPool{Base: p.Base.String(), Size: p.Size})
	}
	return out
}

func registryOf(i system.Info) Registry {
	out := Registry{InsecureCIDRs: []string{}, Mirrors: []string{}, Indexes: []RegistryIndex{}}
	rc := i.RegistryConfig
	if rc == nil {
		return out
	}
	for _, p := range rc.InsecureRegistryCIDRs {
		out.InsecureCIDRs = append(out.InsecureCIDRs, p.String())
	}
	for _, m := range rc.Mirrors {
		out.Mirrors = append(out.Mirrors, redactURLCredentials(m))
	}
	for _, name := range sortedKeys(rc.IndexConfigs) {
		idx := rc.IndexConfigs[name]
		if idx == nil {
			continue
		}
		entry := RegistryIndex{Name: idx.Name, Secure: idx.Secure, Official: idx.Official}
		for _, m := range idx.Mirrors {
			entry.Mirrors = append(entry.Mirrors, redactURLCredentials(m))
		}
		out.Indexes = append(out.Indexes, entry)
	}
	return out
}

// swarmOf keeps the node-level facts only: the cluster spec and the join
// tokens are not copied.
func swarmOf(i system.Info) Swarm {
	s := i.Swarm
	state := string(s.LocalNodeState)
	if state == "" {
		state = "inactive"
	}
	return Swarm{
		State:            state,
		NodeID:           s.NodeID,
		NodeAddr:         s.NodeAddr,
		ControlAvailable: s.ControlAvailable,
		Nodes:            s.Nodes,
		Managers:         s.Managers,
		Error:            s.Error,
	}
}

// diskUsageOf summarizes the disk usage result. The per-object items are not
// kept here: volumes carry their own size, and image sizes are in the images.
func diskUsageOf(du client.DiskUsageResult) *DiskUsage {
	out := &DiskUsage{
		Images:     UsageTotals{du.Images.TotalCount, du.Images.ActiveCount, du.Images.TotalSize, du.Images.Reclaimable},
		Containers: UsageTotals{du.Containers.TotalCount, du.Containers.ActiveCount, du.Containers.TotalSize, du.Containers.Reclaimable},
		Volumes:    UsageTotals{du.Volumes.TotalCount, du.Volumes.ActiveCount, du.Volumes.TotalSize, du.Volumes.Reclaimable},
		BuildCache: UsageTotals{du.BuildCache.TotalCount, du.BuildCache.ActiveCount, du.BuildCache.TotalSize, du.BuildCache.Reclaimable},
	}
	for _, t := range []UsageTotals{out.Images, out.Containers, out.Volumes, out.BuildCache} {
		out.Reclaimable += t.Reclaimable
		out.TotalSize += t.Size
	}
	return out
}

func pairs(in [][2]string) []KeyValue {
	if len(in) == 0 {
		return nil
	}
	out := make([]KeyValue, len(in))
	for i, p := range in {
		out[i] = KeyValue{Key: p[0], Value: p[1]}
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ---------- daemon.json ----------

// defaultConfigPaths returns where the daemon configuration file may be, in
// order of preference. A daemon reached over the network has its file on
// another machine, so there is nothing to read.
func defaultConfigPaths(rootless bool) []string {
	if !daemonIsLocal() {
		return nil
	}
	if runtime.GOOS == "windows" {
		return []string{filepath.Join(os.Getenv("ProgramData"), "docker", "config", "daemon.json")}
	}
	systemPath := "/etc/docker/daemon.json"

	var user string
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		user = filepath.Join(dir, "docker", "daemon.json")
	} else if home, err := os.UserHomeDir(); err == nil {
		user = filepath.Join(home, ".config", "docker", "daemon.json")
	}

	if rootless && user != "" {
		return []string{user, systemPath}
	}
	return []string{systemPath}
}

// readDaemonConfig reads the first daemon.json that exists. A missing file is
// normal (defaults apply) and is not an error; an unreadable or invalid one is.
func (c *Collector) readDaemonConfig(rootless bool) (*DaemonConfig, error) {
	for _, path := range c.configPaths(rootless) {
		data, err := c.readFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("daemon config: %w", err)
		}

		var content map[string]any
		if err := json.Unmarshal(data, &content); err != nil {
			// The error is deliberately not wrapped: it can quote file content.
			return nil, fmt.Errorf("daemon config %s: invalid JSON", path)
		}
		redacted, _ := redactConfig(content).(map[string]any)
		return &DaemonConfig{Path: path, Content: redacted}, nil
	}
	return nil, nil
}

var (
	// secretKey matches config keys holding a credential. "key" alone is not
	// matched: tlskey is a file path, not a secret.
	secretKey = regexp.MustCompile(`(?i)(pass|secret|token|credential|api[-_]?key|private[-_]?key|access[-_]?key)`)

	// urlCredentials matches the password of "scheme://user:password@host".
	urlCredentials = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^/\s:@]*:)[^@\s/]+@`)
)

func redactURLCredentials(s string) string {
	return urlCredentials.ReplaceAllString(s, "${1}"+redacted+"@")
}

// redactConfig walks the parsed daemon.json and masks values under secret-like
// keys (log driver options such as splunk-token, tokens...) and passwords
// embedded in URLs (proxies, mirrors).
func redactConfig(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if secretKey.MatchString(k) && val != nil && val != "" {
				out[k] = redacted
				continue
			}
			out[k] = redactConfig(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = redactConfig(val)
		}
		return out
	case string:
		return redactURLCredentials(t)
	default:
		return v
	}
}

// redactLabels masks the value of secret-looking daemon labels.
func redactLabels(labels []string) []string {
	out := make([]string, len(labels))
	for i, l := range labels {
		key, _, ok := strings.Cut(l, "=")
		if ok && secretKey.MatchString(key) {
			out[i] = key + "=" + redacted
			continue
		}
		out[i] = redactURLCredentials(l)
	}
	return out
}
