package export

import (
	"fmt"
	"sort"
	"strings"
)

// summaryData is what SUMMARY.md is built from.
type summaryData struct {
	manifest *manifest
	results  map[string]any
	files    map[string][]string // docker section -> detail file of each item, same order
}

func readme(m *manifest) string {
	var b strings.Builder
	b.WriteString("# infra-probe export\n\n")
	fmt.Fprintf(&b, "Snapshot of the server `%s` taken by infra-probe %s at %s (UTC).\n\n",
		orDash(m.Hostname), orDash(m.Version), m.GeneratedAt.UTC().Format("2006-01-02 15:04:05"))
	b.WriteString("It describes the host and everything managed by its Docker daemon: containers\n" +
		"(full configuration, mounts, ports, networks, live resource usage), images, networks\n" +
		"and volumes.\n" +
		"It is meant to be given to an AI assistant or a reviewer as complete context of the server.\n\n")

	b.WriteString("## How to read it\n\n")
	b.WriteString("1. `SUMMARY.md`: overview with tables, start here.\n")
	b.WriteString("2. `docker/<section>/index.json`: compact list of every item, each with a `file` field.\n")
	b.WriteString("3. `docker/<section>/<name>.json`: everything about one item, including the raw\n" +
		"   Docker inspect payload under `inspect` (field names as in the Docker Engine API).\n")
	b.WriteString("4. `report.json`: the same data in a single file.\n\n")
	b.WriteString("Items are linked by ID: `container.image_id` = `image.id`, " +
		"`network.containers[].container_id` = `container.id`, " +
		"`volume.containers[].container_id` = `container.id`, " +
		"`compose[].services[].containers[].id` = `container.id`, " +
		"`compose[].networks[].name` / `compose[].volumes[].name` = `network.name` / `volume.name`.\n\n")

	b.WriteString("## Files\n\n")
	for _, f := range m.Files {
		fmt.Fprintf(&b, "- `%s`: %s\n", f.Path, f.Description)
	}

	b.WriteString("\n## Redaction\n\n")
	b.WriteString(m.Redaction + " Variable names are kept, so it is still possible to see which\n" +
		"settings a container receives, but not their values. Do not ask for the missing values.\n")

	if len(m.Errors) > 0 {
		b.WriteString("\n## Incomplete data\n\nSome information could not be collected, see `errors.json`.\n")
	}

	b.WriteString("\n## Suggested prompt\n\n")
	b.WriteString("> You are reviewing a Linux server running Docker. The attached archive is a snapshot\n" +
		"> produced by infra-probe; read README.md and SUMMARY.md first, then the detail files as\n" +
		"> needed. Answer my questions about this server based only on this data.\n")
	return b.String()
}

func summary(s *summaryData) string {
	m := s.manifest
	var b strings.Builder

	fmt.Fprintf(&b, "# Server summary: %s\n\n", orDash(m.Hostname))
	fmt.Fprintf(&b, "Generated %s UTC by infra-probe %s.\n\n",
		m.GeneratedAt.UTC().Format("2006-01-02 15:04:05"), orDash(m.Version))

	if host, ok := s.results["host"].(map[string]any); ok {
		b.WriteString("## Host\n\n")
		for _, k := range sortedKeys(host) {
			fmt.Fprintf(&b, "- **%s**: %s\n", k, cell(host[k]))
		}
		b.WriteString("\n")
	}

	if len(m.Errors) > 0 {
		b.WriteString("## Collection errors\n\nThe data below is incomplete:\n\n")
		for _, k := range sortedKeys(m.Errors) {
			fmt.Fprintf(&b, "- **%s**: %s\n", k, oneLine(m.Errors[k]))
		}
		b.WriteString("\n")
	}

	dockerRes, ok := s.results["docker"].(map[string]any)
	if !ok {
		b.WriteString("No Docker data was collected.\n")
		return b.String()
	}
	containers := objects(dockerRes["containers"])
	images := objects(dockerRes["images"])
	networks := objects(dockerRes["networks"])
	volumes := objects(dockerRes["volumes"])
	unusedVolume := func(v map[string]any) bool { return len(objects(v["containers"])) == 0 }

	fmt.Fprintf(&b, "## Docker at a glance\n\n- Containers: %d (%d running)\n- Images: %d (%d unused)\n- Networks: %d\n- Volumes: %d (%d unused)\n\n",
		len(containers), countWhere(containers, func(c map[string]any) bool { return str(c["state"]) == "running" }),
		len(images), countWhere(images, func(i map[string]any) bool { return num(i["containers"]) == 0 }),
		len(networks), len(volumes), countWhere(volumes, unusedVolume))

	writeDaemon(&b, mapAt(dockerRes, "daemon"))
	writeCompose(&b, objects(dockerRes["compose"]), containers, s.files["compose"])
	writeContainers(&b, containers, s.files["containers"])
	writeMounts(&b, containers)
	writeImages(&b, images, s.files["images"])
	writeNetworks(&b, networks, s.files["networks"])
	writeVolumes(&b, volumes, s.files["volumes"])
	return b.String()
}

// writeDaemon summarizes how the Docker daemon is set up. The full data is in
// docker/daemon.json.
func writeDaemon(b *strings.Builder, daemon map[string]any) {
	if daemon == nil {
		return
	}
	b.WriteString("## Docker daemon\n\nFull data: `docker/daemon.json`.\n\n")

	if v := mapAt(daemon, "version"); v != nil {
		fmt.Fprintf(b, "- **Engine**: %s (API %s, minimum %s), %s/%s\n",
			cell(v["version"]), cell(v["api_version"]), cell(v["min_api_version"]), cell(v["os"]), cell(v["arch"]))
		var comps []string
		for _, c := range objects(v["components"]) {
			comps = append(comps, str(c["name"])+" "+str(c["version"]))
		}
		if len(comps) > 0 {
			fmt.Fprintf(b, "- **Components**: %s\n", strings.Join(comps, ", "))
		}
	}

	if i := mapAt(daemon, "info"); i != nil {
		fmt.Fprintf(b, "- **Host as seen by Docker**: %s, %s, kernel %s, %g CPUs, %s RAM\n",
			cell(i["operating_system"]), cell(i["architecture"]), cell(i["kernel_version"]), num(i["ncpu"]), bytesHuman(num(i["mem_total"])))
		fmt.Fprintf(b, "- **Docker root dir**: `%s`\n", str(i["docker_root_dir"]))
		fmt.Fprintf(b, "- **Storage driver**: %s\n", cell(i["storage_driver"]))
		fmt.Fprintf(b, "- **Logging driver**: %s\n", cell(i["logging_driver"]))
		fmt.Fprintf(b, "- **Cgroup**: driver %s, version %s\n", cell(i["cgroup_driver"]), cell(i["cgroup_version"]))
		fmt.Fprintf(b, "- **Runtimes**: %s (default %s)\n", cell(strings.Join(stringsOf(i["runtimes"]), ", ")), cell(i["default_runtime"]))
		fmt.Fprintf(b, "- **Live restore**: %t, **rootless**: %t, **debug**: %t, **experimental**: %t\n",
			i["live_restore"] == true, i["rootless"] == true, i["debug"] == true, i["experimental"] == true)
		fmt.Fprintf(b, "- **Security options**: %s\n", cell(strings.Join(stringsOf(i["security_options"]), "; ")))
		if fw := str(i["firewall_backend"]); fw != "" {
			fmt.Fprintf(b, "- **Firewall backend**: %s\n", fw)
		}

		reg := mapAt(i, "registry")
		var insecure []string
		insecure = append(insecure, stringsOf(reg["insecure_cidrs"])...)
		for _, idx := range objects(reg["indexes"]) {
			if idx["secure"] == false {
				insecure = append(insecure, str(idx["name"]))
			}
		}
		fmt.Fprintf(b, "- **Insecure registries/networks**: %s\n", cell(strings.Join(insecure, ", ")))
		fmt.Fprintf(b, "- **Registry mirrors**: %s\n", cell(strings.Join(stringsOf(reg["mirrors"]), ", ")))
		if p := mapAt(i, "proxy"); len(p) > 0 {
			fmt.Fprintf(b, "- **Proxy**: http %s, https %s, no_proxy %s\n", cell(p["http"]), cell(p["https"]), cell(p["no_proxy"]))
		}
		sw := mapAt(i, "swarm")
		fmt.Fprintf(b, "- **Swarm**: %s", cell(sw["state"]))
		if str(sw["state"]) == "active" {
			role := "worker"
			if sw["control_available"] == true {
				role = "manager"
			}
			fmt.Fprintf(b, " (%s, %g nodes)", role, num(sw["nodes"]))
		}
		b.WriteString("\n")
		fmt.Fprintf(b, "- **Daemon labels**: %s\n", cell(strings.Join(stringsOf(i["labels"]), ", ")))
		if warns := stringsOf(i["warnings"]); len(warns) > 0 {
			b.WriteString("- **Daemon warnings**:\n")
			for _, w := range warns {
				fmt.Fprintf(b, "  - %s\n", oneLine(w))
			}
		}
	}

	if cfg := mapAt(daemon, "config"); cfg != nil {
		fmt.Fprintf(b, "- **daemon.json**: `%s`, keys: %s\n", str(cfg["path"]), cell(strings.Join(sortedKeys(mapAt(cfg, "content")), ", ")))
	} else {
		b.WriteString("- **daemon.json**: not found (defaults apply, or the daemon is not on this machine)\n")
	}

	if du := mapAt(daemon, "disk_usage"); du != nil {
		b.WriteString("\n### Docker disk usage\n\n| Kind | Total | Active | Size | Reclaimable |\n|---|---|---|---|---|\n")
		for _, k := range []struct{ key, label string }{{"images", "Images"}, {"containers", "Containers"}, {"volumes", "Volumes"}, {"build_cache", "Build cache"}} {
			t := mapAt(du, k.key)
			fmt.Fprintf(b, "| %s | %g | %g | %s | %s |\n", k.label, num(t["total"]), num(t["active"]), bytesHuman(num(t["size"])), bytesHuman(num(t["reclaimable"])))
		}
		fmt.Fprintf(b, "\nTotal %s, of which %s reclaimable.\n", bytesHuman(num(du["total_size"])), bytesHuman(num(du["reclaimable"])))
	}
	b.WriteString("\n")
}

// writeCompose lists the Compose projects. Paths are the ones recorded in the
// labels when the project was started: the files themselves are not read.
func writeCompose(b *strings.Builder, projects []map[string]any, containers []map[string]any, files []string) {
	b.WriteString("## Compose projects\n\n")
	if len(projects) == 0 {
		b.WriteString("None.\n\n")
		return
	}
	b.WriteString("State `down` means no container is left, only networks or volumes.\n\n")
	b.WriteString("| Project | State | Services | Directory | Compose files | Networks | Volumes | File |\n|---|---|---|---|---|---|---|---|\n")

	inProject := map[string]bool{}
	for i, p := range projects {
		var services []string
		for _, svc := range objects(p["services"]) {
			conts := objects(svc["containers"])
			for _, c := range conts {
				inProject[str(c["id"])] = true
			}
			services = append(services, fmt.Sprintf("%s (%g/%d)", str(svc["name"]), num(svc["running"]), len(conts)))
		}
		var networks, volumes []string
		for _, n := range objects(p["networks"]) {
			networks = append(networks, str(n["name"]))
		}
		for _, v := range objects(p["volumes"]) {
			volumes = append(volumes, str(v["name"]))
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s | %s | %s |\n",
			cell(p["name"]), cell(p["state"]), cell(strings.Join(services, ", ")), cell(p["working_dir"]),
			cell(strings.Join(stringsOf(p["config_files"]), ", ")), cell(strings.Join(networks, ", ")),
			cell(strings.Join(volumes, ", ")), fileLink(files, i))
	}
	b.WriteString("\n")
	writeComposeFileNotes(b, projects)

	var outside []string
	for _, c := range containers {
		if !inProject[str(c["id"])] {
			outside = append(outside, str(c["name"]))
		}
	}
	if len(outside) > 0 {
		sort.Strings(outside)
		fmt.Fprintf(b, "Containers outside Compose (started with `docker run` or another tool): %s.\n\n", strings.Join(outside, ", "))
	}
}

// writeComposeFileNotes reports what the reader should know about the compose
// files: possible drift, and files that were not read.
func writeComposeFileNotes(b *strings.Builder, projects []map[string]any) {
	var drift, unread []string
	for _, p := range projects {
		if p["config_changed_since_start"] == true {
			drift = append(drift, str(p["name"]))
		}
		for _, f := range objects(p["files"]) {
			if status := str(f["status"]); status != "ok" {
				unread = append(unread, fmt.Sprintf("%s: %s (%s)", str(p["name"]), oneLine(str(f["path"])), status))
			}
		}
	}
	if len(drift) > 0 {
		fmt.Fprintf(b, "Compose files modified after the containers were created (what runs may differ from the file): %s.\n\n", strings.Join(drift, ", "))
	}
	if len(unread) > 0 {
		b.WriteString("Compose files that were not read (`missing`: no longer on disk; `skipped`: not on this machine):\n\n")
		for _, u := range unread {
			fmt.Fprintf(b, "- %s\n", u)
		}
		b.WriteString("\n")
	}
}

func writeContainers(b *strings.Builder, containers []map[string]any, files []string) {
	b.WriteString("## Containers\n\n")
	if len(containers) == 0 {
		b.WriteString("None.\n\n")
		return
	}
	b.WriteString("| Name | Image | State | Status | Ports | Networks | CPU % | Memory | Notes | File |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|\n")
	for i, c := range containers {
		cpu, mem := "—", "—"
		if st, ok := c["stats"].(map[string]any); ok {
			cpu = fmt.Sprintf("%.1f", num(st["cpu_percent"]))
			mem = bytesHuman(num(st["memory_usage"]))
			if limit := num(st["memory_limit"]); limit > 0 {
				mem += " / " + bytesHuman(limit)
			}
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
			cell(c["name"]), cell(c["image"]), cell(c["state"]), cell(c["status"]),
			cell(strings.Join(containerPorts(c), ", ")),
			cell(strings.Join(sortedKeys(mapAt(c, "inspect", "NetworkSettings", "Networks")), ", ")),
			cpu, mem, cell(strings.Join(containerNotes(c), ", ")), fileLink(files, i))
	}
	b.WriteString("\n")
}

// containerNotes lists configuration facts relevant to security and
// operations. They are facts, not verdicts: the reader decides.
func containerNotes(c map[string]any) []string {
	var notes []string
	hc := mapAt(c, "inspect", "HostConfig")
	if hc == nil {
		return notes
	}
	if b, _ := hc["Privileged"].(bool); b {
		notes = append(notes, "privileged")
	}
	for _, k := range []string{"NetworkMode", "PidMode", "IpcMode"} {
		if str(hc[k]) == "host" {
			notes = append(notes, strings.ToLower(strings.TrimSuffix(k, "Mode"))+"=host")
		}
	}
	if caps := stringsOf(hc["CapAdd"]); len(caps) > 0 {
		notes = append(notes, "cap_add="+strings.Join(caps, "+"))
	}
	if user := str(mapAt(c, "inspect", "Config")["User"]); user == "" || user == "root" || user == "0" || strings.HasPrefix(user, "0:") {
		notes = append(notes, "runs as root")
	}
	if num(hc["Memory"]) == 0 {
		notes = append(notes, "no memory limit")
	}
	if rp := str(mapAt(hc, "RestartPolicy")["Name"]); rp == "" || rp == "no" {
		notes = append(notes, "no restart policy")
	}
	if b, _ := hc["ReadonlyRootfs"].(bool); b {
		notes = append(notes, "read-only rootfs")
	}
	for _, mnt := range objects(at(c, "inspect", "Mounts")) {
		if strings.HasSuffix(str(mnt["Source"]), "docker.sock") {
			notes = append(notes, "docker socket mounted")
			break
		}
	}
	return notes
}

// containerPorts lists published ports as "hostIP:hostPort->port/proto".
// Runtime bindings are used when available, configured ones otherwise.
func containerPorts(c map[string]any) []string {
	ports := mapAt(c, "inspect", "NetworkSettings", "Ports")
	if len(ports) == 0 {
		ports = mapAt(c, "inspect", "HostConfig", "PortBindings")
	}
	var out []string
	for _, port := range sortedKeys(ports) {
		bindings := objects(ports[port])
		if len(bindings) == 0 {
			continue // exposed but not published
		}
		for _, bnd := range bindings {
			host := str(bnd["HostIp"])
			if host == "" {
				host = "0.0.0.0"
			}
			out = append(out, fmt.Sprintf("%s:%s->%s", host, str(bnd["HostPort"]), port))
		}
	}
	return out
}

func writeMounts(b *strings.Builder, containers []map[string]any) {
	type row struct{ container, typ, source, dest, mode string }
	var rows []row
	for _, c := range containers {
		for _, mnt := range objects(at(c, "inspect", "Mounts")) {
			mode := "ro"
			if rw, _ := mnt["RW"].(bool); rw {
				mode = "rw"
			}
			source := str(mnt["Source"])
			if name := str(mnt["Name"]); name != "" && str(mnt["Type"]) == "volume" {
				source = name
			}
			rows = append(rows, row{str(c["name"]), str(mnt["Type"]), source, str(mnt["Destination"]), mode})
		}
	}
	if len(rows) == 0 {
		return
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].container < rows[j].container })
	b.WriteString("## Mounts\n\n| Container | Type | Source | Destination | Mode |\n|---|---|---|---|---|\n")
	for _, r := range rows {
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s |\n", cell(r.container), cell(r.typ), cell(r.source), cell(r.dest), r.mode)
	}
	b.WriteString("\n")
}

func writeImages(b *strings.Builder, images []map[string]any, files []string) {
	b.WriteString("## Images\n\n")
	if len(images) == 0 {
		b.WriteString("None.\n\n")
		return
	}
	b.WriteString("| Tags | Size | Containers | Dangling | Created | File |\n|---|---|---|---|---|---|\n")
	for i, img := range images {
		tags := strings.Join(stringsOf(img["repo_tags"]), ", ")
		if tags == "" {
			tags = shortHex(str(img["id"]))
		}
		fmt.Fprintf(b, "| %s | %s | %d | %t | %s | %s |\n",
			cell(tags), bytesHuman(num(img["size"])), int64(num(img["containers"])),
			img["dangling"] == true, cell(dateOnly(str(img["created_at"]))), fileLink(files, i))
	}
	b.WriteString("\n")
}

func writeNetworks(b *strings.Builder, networks []map[string]any, files []string) {
	b.WriteString("## Networks\n\n")
	if len(networks) == 0 {
		b.WriteString("None.\n\n")
		return
	}
	b.WriteString("| Name | Driver | Scope | Subnets | Internal | Containers | File |\n|---|---|---|---|---|---|---|\n")
	for i, n := range networks {
		var members []string
		for _, ep := range objects(n["containers"]) {
			members = append(members, str(ep["name"]))
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %t | %s | %s |\n",
			cell(n["name"]), cell(n["driver"]), cell(n["scope"]),
			cell(strings.Join(stringsOf(n["subnets"]), ", ")), n["internal"] == true,
			cell(strings.Join(members, ", ")), fileLink(files, i))
	}
	b.WriteString("\n")
}

func writeVolumes(b *strings.Builder, volumes []map[string]any, files []string) {
	b.WriteString("## Volumes\n\n")
	if len(volumes) == 0 {
		b.WriteString("None.\n\n")
		return
	}
	b.WriteString("| Name | Size | Containers | Compose project | Driver | Anonymous | File |\n|---|---|---|---|---|---|---|\n")
	for i, v := range volumes {
		size := "unknown" // -1 or missing: non-local driver or disk usage failed
		if n, ok := v["size"].(float64); ok && n >= 0 {
			size = bytesHuman(n)
		}
		var users []string
		for _, m := range objects(v["containers"]) {
			users = append(users, str(m["container_name"])+":"+str(m["destination"]))
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %t | %s |\n",
			cell(v["name"]), size, cell(strings.Join(users, ", ")),
			cell(mapAt(v, "labels")["com.docker.compose.project"]), cell(v["driver"]),
			v["anonymous"] == true, fileLink(files, i))
	}
	b.WriteString("\n")
}

// ---------- formatting helpers ----------

// at follows a path of object keys and returns the value found, or nil.
func at(v any, path ...string) any {
	for _, k := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

func mapAt(v any, path ...string) map[string]any {
	m, _ := at(v, path...).(map[string]any)
	return m
}

func num(v any) float64 {
	f, _ := v.(float64) // encoding/json decodes every number as float64
	return f
}

func countWhere(items []map[string]any, pred func(map[string]any) bool) int {
	n := 0
	for _, it := range items {
		if pred(it) {
			n++
		}
	}
	return n
}

// cell renders a value for a Markdown table cell.
func cell(v any) string {
	var s string
	switch t := v.(type) {
	case nil:
		s = ""
	case string:
		s = t
	case float64:
		s = fmt.Sprintf("%g", t)
	default:
		s = fmt.Sprint(t)
	}
	s = strings.ReplaceAll(oneLine(s), "|", `\|`)
	if s == "" {
		return "—"
	}
	return s
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func fileLink(files []string, i int) string {
	if i >= len(files) {
		return "—"
	}
	return "`" + files[i] + "`"
}

func dateOnly(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

func bytesHuman(n float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for n >= 1024 && i < len(units)-1 {
		n /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f %s", n, units[i])
	}
	return fmt.Sprintf("%.1f %s", n, units[i])
}
