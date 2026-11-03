package docker

import (
	"sort"
	"strconv"
	"strings"
)

// Labels written by Docker Compose on every object it creates.
const (
	labelProject     = "com.docker.compose.project"
	labelService     = "com.docker.compose.service"
	labelNumber      = "com.docker.compose.container-number"
	labelOneOff      = "com.docker.compose.oneoff"
	labelDependsOn   = "com.docker.compose.depends_on"
	labelVersion     = "com.docker.compose.version"
	labelWorkingDir  = "com.docker.compose.project.working_dir"
	labelConfigFiles = "com.docker.compose.project.config_files"
	labelEnvFile     = "com.docker.compose.project.environment_file"
	labelNetwork     = "com.docker.compose.network"
	labelVolume      = "com.docker.compose.volume"
)

// ComposeProject is a Docker Compose project reconstructed from the labels
// Compose leaves on containers, networks and volumes. No file is read: the
// paths are the ones recorded when the project was started.
type ComposeProject struct {
	Name string `json:"name"`

	// State is "running" when every container is running, "stopped" when none
	// is, "partial" otherwise, and "down" when the project has no container
	// left, only networks or volumes (typically after `compose down` without
	// -v). One-off containers (compose run) do not count.
	State string `json:"state"`

	// WorkingDir is the project directory on the machine running the daemon.
	WorkingDir string `json:"working_dir,omitempty"`

	// ConfigFiles are the compose files combined at startup, in order
	// (base file first, then overrides). Only the paths: nothing is read.
	ConfigFiles []string `json:"config_files"`

	// EnvironmentFile is the .env file passed to Compose. Only its path is
	// recorded: environment files hold secrets and are never read.
	EnvironmentFile string `json:"environment_file,omitempty"`

	// Version is the Compose version that created the containers.
	Version string `json:"version,omitempty"`

	// Files are the compose files read from disk, sanitized. Empty when the
	// daemon is not on this machine.
	Files []ComposeFile `json:"files"`

	// ConfigChangedSinceStart is true when a compose file was modified after
	// the project's oldest container was created: what runs may differ from
	// what the file says. A hint, not proof.
	ConfigChangedSinceStart bool `json:"config_changed_since_start"`

	Services []ComposeService `json:"services"`
	Networks []ComposeRef     `json:"networks"`
	Volumes  []ComposeRef     `json:"volumes"`
}

// ComposeService is one service of a project and its containers.
type ComposeService struct {
	Name       string             `json:"name"`
	Containers []ComposeContainer `json:"containers"`
	Running    int                `json:"running"`

	// DependsOn are the services this one waits for.
	DependsOn []ComposeDependency `json:"depends_on"`
}

// ComposeContainer is a container of a service.
type ComposeContainer struct {
	ID     string `json:"id"` // short form, join key with Container.ID
	Name   string `json:"name"`
	State  string `json:"state"`
	Status string `json:"status"`
	Image  string `json:"image"`
	Number int    `json:"number"` // replica number, 1-based, 0 when unknown

	// OneOff is true for containers started with `compose run`.
	OneOff bool `json:"one_off"`
}

// ComposeDependency is a depends_on entry.
type ComposeDependency struct {
	Service   string `json:"service"`
	Condition string `json:"condition"` // service_started, service_healthy, ...
}

// ComposeRef is a network or volume of a project. Name is the Docker object
// name (join key with Network.Name / Volume.Name), Logical the key used in
// the compose file.
type ComposeRef struct {
	Name    string `json:"name"`
	Logical string `json:"logical,omitempty"`
}

// collectCompose groups containers, networks and volumes by project. It works
// on data already collected, so it cannot fail; objects without inspect data
// cannot be attributed and are left out.
func collectCompose(containers []Container, networks []Network, volumes []Volume) []ComposeProject {
	projects := map[string]*ComposeProject{}
	get := func(name string) *ComposeProject {
		p := projects[name]
		if p == nil {
			p = &ComposeProject{Name: name, ConfigFiles: []string{}, Files: []ComposeFile{}, Services: []ComposeService{}, Networks: []ComposeRef{}, Volumes: []ComposeRef{}}
			projects[name] = p
		}
		return p
	}

	services := map[string]map[string]*ComposeService{} // project -> service name -> service
	for _, c := range containers {
		if c.Inspect == nil || c.Inspect.Config == nil {
			continue
		}
		labels := c.Inspect.Config.Labels
		name := labels[labelProject]
		if name == "" {
			continue
		}
		p := get(name)

		// Project level facts are identical on every container: keep the
		// first non-empty value.
		p.WorkingDir = firstNonEmpty(p.WorkingDir, labels[labelWorkingDir])
		p.EnvironmentFile = firstNonEmpty(p.EnvironmentFile, labels[labelEnvFile])
		p.Version = firstNonEmpty(p.Version, labels[labelVersion])
		if len(p.ConfigFiles) == 0 {
			p.ConfigFiles = splitList(labels[labelConfigFiles])
		}

		if services[name] == nil {
			services[name] = map[string]*ComposeService{}
		}
		svcName := labels[labelService]
		svc := services[name][svcName]
		if svc == nil {
			svc = &ComposeService{Name: svcName, Containers: []ComposeContainer{}, DependsOn: parseDependsOn(labels[labelDependsOn])}
			services[name][svcName] = svc
		}
		number, _ := strconv.Atoi(labels[labelNumber])
		svc.Containers = append(svc.Containers, ComposeContainer{
			ID: c.ID, Name: c.Name, State: c.State, Status: c.Status, Image: c.Image,
			Number: number, OneOff: strings.EqualFold(labels[labelOneOff], "true"),
		})
		if c.State == "running" {
			svc.Running++
		}
	}

	for _, n := range networks {
		if n.Inspect == nil {
			continue
		}
		if name := n.Inspect.Labels[labelProject]; name != "" {
			p := get(name)
			p.Networks = append(p.Networks, ComposeRef{Name: n.Name, Logical: n.Inspect.Labels[labelNetwork]})
		}
	}
	for _, v := range volumes {
		if name := v.Labels[labelProject]; name != "" {
			p := get(name)
			p.Volumes = append(p.Volumes, ComposeRef{Name: v.Name, Logical: v.Labels[labelVolume]})
		}
	}

	out := make([]ComposeProject, 0, len(projects))
	for name, p := range projects {
		for _, svc := range services[name] {
			sort.Slice(svc.Containers, func(i, j int) bool {
				a, b := svc.Containers[i], svc.Containers[j]
				if a.Number != b.Number {
					return a.Number < b.Number
				}
				return a.Name < b.Name
			})
			p.Services = append(p.Services, *svc)
		}
		sort.Slice(p.Services, func(i, j int) bool { return p.Services[i].Name < p.Services[j].Name })
		sort.Slice(p.Networks, func(i, j int) bool { return p.Networks[i].Name < p.Networks[j].Name })
		sort.Slice(p.Volumes, func(i, j int) bool { return p.Volumes[i].Name < p.Volumes[j].Name })
		p.State = projectState(p.Services)
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// projectState is running when every regular container runs, stopped when
// none does, and down when there is no regular container at all.
func projectState(services []ComposeService) string {
	total, running := 0, 0
	for _, s := range services {
		for _, c := range s.Containers {
			if c.OneOff {
				continue
			}
			total++
			if c.State == "running" {
				running++
			}
		}
	}
	switch {
	case total == 0:
		return "down"
	case running == total:
		return "running"
	case running == 0:
		return "stopped"
	default:
		return "partial"
	}
}

// parseDependsOn parses "db:service_healthy:true,cache:service_started:false"
// (service:condition:restart).
func parseDependsOn(label string) []ComposeDependency {
	deps := []ComposeDependency{}
	for _, item := range splitList(label) {
		parts := strings.Split(item, ":")
		dep := ComposeDependency{Service: parts[0]}
		if len(parts) > 1 {
			dep.Condition = parts[1]
		}
		if dep.Service != "" {
			deps = append(deps, dep)
		}
	}
	sort.Slice(deps, func(i, j int) bool { return deps[i].Service < deps[j].Service })
	return deps
}

// splitList splits a comma separated label, dropping empty items.
func splitList(s string) []string {
	out := []string{}
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
