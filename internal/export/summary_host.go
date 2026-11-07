package export

import (
	"fmt"
	"sort"
	"strings"
)

// writeHost summarizes the machine: system, memory, disks, listening ports
// (traced to the containers behind them), services and scheduled jobs. The
// containers may be nil when they are not part of the export.
func writeHost(b *strings.Builder, host map[string]any, containers []map[string]any) {
	b.WriteString("## Host\n\n")

	fmt.Fprintf(b, "- **Name**: %s (%s/%s, %g CPUs)\n", cell(host["hostname"]), cell(host["os"]), cell(host["arch"]), num(host["num_cpu"]))

	if s := mapAt(host, "system"); s != nil {
		fmt.Fprintf(b, "- **System**: %s, kernel %s\n", cell(firstString(s["pretty_name"], s["distro"])), cell(s["kernel"]))
		if model := str(s["cpu_model"]); model != "" {
			fmt.Fprintf(b, "- **CPU**: %s\n", oneLine(model))
		}
		fmt.Fprintf(b, "- **Uptime**: %s (booted %s UTC), load %.2f %.2f %.2f (1, 5, 15 minutes)\n",
			uptimeText(num(s["uptime_seconds"])), dateTime(str(s["boot_time"])), num(s["load_1"]), num(s["load_5"]), num(s["load_15"]))
	}
	if m := mapAt(host, "memory"); m != nil {
		fmt.Fprintf(b, "- **Memory**: %s total, %s available", bytesHuman(num(m["total"])), bytesHuman(num(m["available"])))
		if swap := num(m["swap_total"]); swap > 0 {
			fmt.Fprintf(b, "; swap %s (%s free)", bytesHuman(swap), bytesHuman(num(m["swap_free"])))
		} else {
			b.WriteString("; no swap")
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(b, "- **Probe runs as**: uid %g\n", num(host["uid"]))
	b.WriteString("\n")

	if notes := stringsOf(host["notes"]); len(notes) > 0 {
		b.WriteString("What the probe could not see:\n\n")
		for _, n := range notes {
			fmt.Fprintf(b, "- %s\n", oneLine(n))
		}
		b.WriteString("\n")
	}

	writeDisks(b, objects(host["filesystems"]))
	writeListeners(b, objects(host["listeners"]), containers)
	writeSystemd(b, mapAt(host, "systemd"))
	writeCron(b, objects(host["cron"]))
}

func writeDisks(b *strings.Builder, disks []map[string]any) {
	if len(disks) == 0 {
		return
	}
	b.WriteString("### Disks\n\n| Mount point | Device | Type | Size | Used | Available | Used % | Inodes free |\n|---|---|---|---|---|---|---|---|\n")
	for _, d := range disks {
		inodes := "—"
		// Some file systems (9p, FUSE) report nonsense: more free inodes than
		// inodes. Better no number than a wrong one.
		if total, free := num(d["inodes_total"]), num(d["inodes_free"]); total > 0 && free <= total {
			inodes = fmt.Sprintf("%.0f%%", free/total*100)
		}
		mode := ""
		if d["read_only"] == true {
			mode = " (read-only)"
		}
		fmt.Fprintf(b, "| %s | %s | %s%s | %s | %s | %s | %.1f | %s |\n",
			cell(d["mountpoint"]), cell(d["device"]), cell(d["type"]), mode,
			bytesHuman(num(d["total"])), bytesHuman(num(d["used"])), bytesHuman(num(d["avail"])), num(d["used_percent"]), inodes)
	}
	b.WriteString("\n")
}

// writeListeners lists what accepts connections on the host. A port is traced
// to a container when it is one of the ports the container publishes.
func writeListeners(b *strings.Builder, listeners, containers []map[string]any) {
	if len(listeners) == 0 {
		return
	}
	published := publishedByPort(containers)

	b.WriteString("### Listening ports\n\n" +
		"`all interfaces` means any network the machine is on can reach the port, unless a firewall blocks it.\n\n" +
		"| Port | Protocol | Address | Reachable from | Process | Container |\n|---|---|---|---|---|---|\n")
	for _, l := range listeners {
		reach := str(l["address"])
		switch {
		case l["all_interfaces"] == true:
			reach = "all interfaces"
		case l["loopback"] == true:
			reach = "local only"
		}
		process := "—"
		if p := str(l["process"]); p != "" {
			process = fmt.Sprintf("%s (%g)", p, num(l["pid"]))
		}
		proto := strings.TrimSuffix(str(l["protocol"]), "6")
		who := strings.Join(published[fmt.Sprintf("%s:%g", proto, num(l["port"]))], ", ")
		fmt.Fprintf(b, "| %g | %s | %s | %s | %s | %s |\n",
			num(l["port"]), cell(l["protocol"]), cell(l["address"]), reach, cell(process), cell(who))
	}
	b.WriteString("\n")
}

// publishedByPort maps "tcp:8080" to the names of the containers publishing
// host port 8080.
func publishedByPort(containers []map[string]any) map[string][]string {
	out := map[string][]string{}
	for _, c := range containers {
		ports := mapAt(c, "inspect", "NetworkSettings", "Ports")
		for key, v := range ports {
			proto := "tcp"
			if _, p, ok := strings.Cut(key, "/"); ok {
				proto = p
			}
			for _, binding := range objects(v) {
				k := proto + ":" + str(binding["HostPort"])
				out[k] = append(out[k], str(c["name"]))
			}
		}
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

func writeSystemd(b *strings.Builder, systemd map[string]any) {
	if systemd == nil {
		return
	}
	var running, failed []string
	for _, u := range objects(systemd["services"]) {
		if str(u["active"]) == "failed" {
			failed = append(failed, str(u["name"]))
		} else {
			running = append(running, str(u["name"]))
		}
	}
	b.WriteString("### Services (systemd)\n\n")
	fmt.Fprintf(b, "- **Running**: %s\n", cell(strings.Join(running, ", ")))
	fmt.Fprintf(b, "- **Failed**: %s\n", cell(strings.Join(failed, ", ")))
	fmt.Fprintf(b, "- **Enabled at boot**: %s\n", cell(strings.Join(stringsOf(systemd["enabled"]), ", ")))
	var timers []string
	for _, t := range objects(systemd["timers"]) {
		timers = append(timers, fmt.Sprintf("%s → %s", str(t["name"]), str(t["activates"])))
	}
	fmt.Fprintf(b, "- **Timers**: %s\n\n", cell(strings.Join(timers, ", ")))
}

func writeCron(b *strings.Builder, jobs []map[string]any) {
	if len(jobs) == 0 {
		return
	}
	b.WriteString("### Scheduled jobs (cron)\n\nSecrets in command arguments are masked.\n\n| Schedule | User | Command | Source |\n|---|---|---|---|\n")
	for _, j := range jobs {
		fmt.Fprintf(b, "| %s | %s | %s | %s |\n", cell(j["schedule"]), cell(j["user"]), cell(j["command"]), cell(j["source"]))
	}
	b.WriteString("\n")
}

func firstString(values ...any) string {
	for _, v := range values {
		if s := str(v); s != "" {
			return s
		}
	}
	return ""
}

// uptimeText formats seconds as "3d 4h", "2h 15m" or "5m".
func uptimeText(seconds float64) string {
	s := int64(seconds)
	d, h, m := s/86400, s%86400/3600, s%3600/60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd %dh", d, h)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	default:
		return fmt.Sprintf("%dm", m)
	}
}

// dateTime formats an RFC 3339 timestamp as "2006-01-02 15:04:05".
func dateTime(s string) string {
	if len(s) >= 19 {
		return strings.Replace(s[:19], "T", " ", 1)
	}
	return orDash(s)
}
