package export

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The same analysis runs in the dashboard (web/data/firewall.js): keep the two
// in step.

// exposure is a way into the machine from the network: a host process
// listening on a non-loopback address, or a port a container publishes.
type exposure struct {
	Proto   string // tcp, udp
	Port    string
	Address string // "0.0.0.0", "::", or a specific address
	Service string // process or container names
	Docker  bool   // published by Docker
	Level   string // bad, warn, ok, neutral
	Verdict string
}

// firewallView is the decoded firewall section with the facts the analysis
// needs.
type firewallView struct {
	fw   map[string]any
	ufw  map[string]any
	ipt4 map[string]any
	nft  map[string]any

	// ufwActive is true when ufw is enabled and not known to be unloaded.
	ufwActive bool

	// dockerNAT is true when Docker publishes ports with its own NAT rules,
	// which send the traffic through FORWARD: the rules of ufw (INPUT) never
	// see it. False with iptables disabled in daemon.json or a rootless
	// daemon, whose ports are plain host sockets.
	dockerNAT      bool
	dockerBackend  string // firewall backend of the daemon: iptables, nftables
	dockerReported bool   // the daemon section is part of the data
}

func newFirewallView(fw, daemon map[string]any) firewallView {
	v := firewallView{fw: fw, ufw: mapAt(fw, "ufw"), ipt4: mapAt(fw, "iptables"), nft: mapAt(fw, "nftables")}
	if v.ufw != nil && v.ufw["enabled"] == true && v.ufw["loaded"] != false {
		v.ufwActive = true
	}
	v.dockerNAT = true
	if daemon != nil {
		v.dockerReported = true
		if mapAt(daemon, "info")["rootless"] == true || mapAt(daemon, "config", "content")["iptables"] == false {
			v.dockerNAT = false
		}
		v.dockerBackend = str(mapAt(daemon, "info")["firewall_backend"])
	}
	return v
}

// writeFirewall summarizes the packet filter and what it lets in. host, daemon
// and containers are nil when they are not part of the export.
func writeFirewall(b *strings.Builder, fw, host, daemon map[string]any, containers []map[string]any) {
	v := newFirewallView(fw, daemon)
	b.WriteString("## Firewall and exposure\n\n")

	if v.ufw == nil {
		b.WriteString("- **ufw**: not installed\n")
	} else {
		state := "disabled"
		if v.ufw["enabled"] == true {
			state = "enabled"
			if v.ufw["loaded"] == false {
				state = "enabled in ufw.conf but its rules are not loaded"
			}
		}
		fmt.Fprintf(b, "- **ufw**: %s; default incoming %s, outgoing %s, routed %s\n", state,
			orDash(str(v.ufw["default_input"])), orDash(str(v.ufw["default_output"])), orDash(str(v.ufw["default_forward"])))
	}
	if fw["can_read_rules"] == true {
		var parts []string
		if v.ipt4 != nil {
			parts = append(parts, fmt.Sprintf("iptables %s (%d tables)", orDash(str(v.ipt4["backend"])), len(objects(v.ipt4["tables"]))))
		}
		if v.nft != nil {
			parts = append(parts, fmt.Sprintf("nftables (%d tables)", len(objects(v.nft["tables"]))))
		}
		fmt.Fprintf(b, "- **Kernel rules**: %s; full rules in `collectors/firewall.json`\n", cell(strings.Join(parts, ", ")))
		if du := v.chain("filter", "DOCKER-USER"); du != nil {
			fmt.Fprintf(b, "- **DOCKER-USER**: %d filtering rules\n", len(filteringRules(du)))
		}
	} else {
		b.WriteString("- **Kernel rules**: not readable (the probe needs CAP_NET_ADMIN)\n")
	}
	b.WriteString("\n")

	if notes := stringsOf(fw["notes"]); len(notes) > 0 {
		b.WriteString("What the probe could not see:\n\n")
		for _, n := range notes {
			fmt.Fprintf(b, "- %s\n", oneLine(n))
		}
		b.WriteString("\n")
	}

	rows := exposures(v, host, containers)
	if findings := firewallFindings(v, rows); len(findings) > 0 {
		b.WriteString("Findings:\n\n")
		for _, f := range findings {
			fmt.Fprintf(b, "- %s\n", f)
		}
		b.WriteString("\n")
	}

	b.WriteString("### Reachable from outside\n\n")
	if host == nil {
		b.WriteString("The host listening ports are not part of this export: only the Docker-published ports are listed.\n\n")
	}
	if containers == nil {
		b.WriteString("The containers are not part of this export: a port Docker publishes shows up as a host port.\n\n")
	}
	if len(rows) == 0 {
		b.WriteString("Nothing listens on a non-loopback address.\n\n")
	} else {
		b.WriteString("Ports bound to a non-loopback address, and what the firewall does with incoming connections. " +
			"Docker-published ports go through Docker's NAT rules: ufw does not filter them, only rules in the DOCKER-USER chain do.\n\n" +
			"| Port | Protocol | Address | Service | Firewall |\n|---|---|---|---|---|\n")
		for _, r := range rows {
			fmt.Fprintf(b, "| %s | %s | %s | %s | %s |\n", r.Port, r.Proto, cell(r.Address), cell(r.Service), levelMark(r.Level)+r.Verdict)
		}
		b.WriteString("\n")
	}

	if v.ufw != nil && v.ufw["rules_readable"] == true {
		rules := objects(v.ufw["rules"])
		b.WriteString("### ufw rules\n\n")
		if len(rules) == 0 {
			b.WriteString("No rule: only the default policies apply.\n\n")
			return
		}
		b.WriteString("| Action | Direction | Port | Protocol | From | To | IPv6 | Comment |\n|---|---|---|---|---|---|---|---|\n")
		for _, r := range rules {
			action := str(r["action"])
			if r["route"] == true {
				action = "route " + action
			}
			dir := str(r["direction"])
			if i := str(r["interface"]); i != "" {
				dir += " on " + i
			}
			port := str(r["port"])
			if app := str(r["app"]); app != "" {
				port += " (" + app + ")"
			}
			ipv6 := ""
			if r["ipv6"] == true {
				ipv6 = "yes"
			}
			fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s | %s | %s |\n",
				cell(action), cell(dir), cell(port), cell(r["protocol"]), cell(r["from"]), cell(r["to"]), cell(ipv6), cell(r["comment"]))
		}
		b.WriteString("\n")
	}
}

func levelMark(level string) string {
	switch level {
	case "bad":
		return "**RISK** "
	case "warn":
		return "**open** "
	}
	return ""
}

// exposures lists the ports reachable from the network with the verdict of
// the firewall, Docker ports first.
func exposures(v firewallView, host map[string]any, containers []map[string]any) []exposure {
	type key struct{ proto, port, addr string }
	docker := map[key][]string{}
	dockerPorts := map[string]bool{} // "tcp:8080"
	for _, c := range containers {
		for portKey, bindings := range mapAt(c, "inspect", "NetworkSettings", "Ports") {
			proto := "tcp"
			if _, p, ok := strings.Cut(portKey, "/"); ok {
				proto = p
			}
			for _, bnd := range objects(bindings) {
				addr := str(bnd["HostIp"])
				if addr == "" {
					addr = "0.0.0.0"
				}
				if isLoopback(addr) {
					continue
				}
				k := key{proto, str(bnd["HostPort"]), addr}
				docker[k] = appendUnique(docker[k], str(c["name"]))
				dockerPorts[proto+":"+k.port] = true
			}
		}
	}

	var rows []exposure
	for k, names := range docker {
		sort.Strings(names)
		level, verdict := v.dockerVerdict(k.proto, k.port)
		rows = append(rows, exposure{Proto: k.proto, Port: k.port, Address: k.addr, Service: "container " + strings.Join(names, ", "), Docker: true, Level: level, Verdict: verdict})
	}

	seen := map[key]bool{}
	for _, l := range objects(host["listeners"]) {
		if l["loopback"] == true {
			continue
		}
		proto := strings.TrimSuffix(str(l["protocol"]), "6")
		port := strconv.FormatFloat(num(l["port"]), 'f', -1, 64)
		// A Docker proxy process holding a published port: already listed.
		if dockerPorts[proto+":"+port] {
			continue
		}
		k := key{proto, port, str(l["address"])}
		if seen[k] {
			continue
		}
		seen[k] = true
		service := str(l["process"])
		if service == "" {
			service = "unknown process"
		}
		level, verdict := v.hostVerdict(proto, port)
		rows = append(rows, exposure{Proto: proto, Port: port, Address: k.addr, Service: service, Level: level, Verdict: verdict})
	}

	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Docker != b.Docker {
			return a.Docker
		}
		pa, _ := strconv.Atoi(a.Port)
		pb, _ := strconv.Atoi(b.Port)
		if pa != pb {
			return pa < pb
		}
		if a.Proto != b.Proto {
			return a.Proto < b.Proto
		}
		return a.Address < b.Address
	})
	return rows
}

// hostVerdict says what the firewall does with a connection to a host port
// (the INPUT path).
func (v firewallView) hostVerdict(proto, port string) (level, verdict string) {
	if v.ufwActive {
		policy := strings.ToUpper(str(v.ufw["default_input"]))
		denyByDefault := policy == "DROP" || policy == "REJECT"
		if v.ufw["rules_readable"] != true {
			if denyByDefault {
				return "neutral", "ufw denies by default; its rules are readable by root only"
			}
			return "warn", "ufw allows by default"
		}
		for _, r := range objects(v.ufw["rules"]) {
			if r["route"] == true || str(r["direction"]) != "in" || !protoMatches(str(r["protocol"]), proto) || !portMatches(str(r["port"]), port) {
				continue
			}
			on := ""
			if i := str(r["interface"]); i != "" {
				on = " on " + i
			}
			switch str(r["action"]) {
			case "allow", "limit":
				what := "allowed"
				if str(r["action"]) == "limit" {
					what = "rate limited"
				}
				if from := str(r["from"]); !isAnywhere(from) {
					return "ok", fmt.Sprintf("ufw: %s only from %s%s", what, from, on)
				}
				return "warn", fmt.Sprintf("ufw: %s from anywhere%s", what, on)
			default:
				return "ok", fmt.Sprintf("ufw: blocked by a %s rule%s", str(r["action"]), on)
			}
		}
		if denyByDefault {
			return "ok", "ufw: blocked (no rule, default deny)"
		}
		return "warn", "ufw: allowed (no rule, default allow)"
	}

	if v.fw["can_read_rules"] != true {
		return "neutral", "unknown: the firewall rules are not readable"
	}
	if v.inputFiltered() {
		return "neutral", "filtered by iptables/nftables rules: review them"
	}
	return "warn", "open: no firewall rule filters incoming traffic"
}

// dockerVerdict says what the firewall does with a connection to a port
// published by Docker.
func (v firewallView) dockerVerdict(proto, port string) (level, verdict string) {
	if !v.dockerNAT {
		level, verdict = v.hostVerdict(proto, port)
		return level, verdict + " (Docker without NAT rules: host rules apply)"
	}

	// Behind an active ufw an open Docker port is worse: the admin believes
	// ufw protects it.
	open, ufwNote := "warn", ""
	if v.ufwActive {
		open = "bad"
		ufwNote = "; ufw does not apply to Docker ports"
	}

	if v.fw["can_read_rules"] != true {
		return open, "open unless DOCKER-USER rules filter it (not readable)" + ufwNote
	}
	if v.dockerBackend == "nftables" {
		if v.nftForwardFiltered() {
			return "neutral", "filtered by your nftables forward rules: review them"
		}
		return open, "open: no nftables forward rule of yours filters it" + ufwNote
	}
	if du := v.chain("filter", "DOCKER-USER"); du != nil && len(filteringRules(du)) > 0 {
		return "neutral", "DOCKER-USER has rules: review them"
	}
	return open, "open: no DOCKER-USER rule" + ufwNote
}

// inputFiltered reports whether the kernel rules filter incoming traffic:
// an INPUT chain with a restrictive policy or with rules.
func (v firewallView) inputFiltered() bool {
	if in := v.chain("filter", "INPUT"); in != nil {
		if p := str(in["policy"]); p == "DROP" || p == "REJECT" || len(stringsOf(in["rules"])) > 0 {
			return true
		}
	}
	for _, t := range objects(v.nft["tables"]) {
		for _, c := range objects(t["chains"]) {
			if str(c["hook"]) == "input" && (str(c["policy"]) == "drop" || len(stringsOf(c["rules"])) > 0) {
				return true
			}
		}
	}
	return false
}

// nftForwardFiltered reports whether a forward chain outside Docker's own
// tables drops or filters traffic.
func (v firewallView) nftForwardFiltered() bool {
	for _, t := range objects(v.nft["tables"]) {
		if strings.HasPrefix(str(t["name"]), "docker") {
			continue
		}
		for _, c := range objects(t["chains"]) {
			if str(c["hook"]) == "forward" && (str(c["policy"]) == "drop" || len(stringsOf(c["rules"])) > 0) {
				return true
			}
		}
	}
	return false
}

// chain finds a chain of the IPv4 iptables rules.
func (v firewallView) chain(table, chain string) map[string]any {
	for _, t := range objects(v.ipt4["tables"]) {
		if str(t["name"]) != table {
			continue
		}
		for _, c := range objects(t["chains"]) {
			if str(c["name"]) == chain {
				return c
			}
		}
	}
	return nil
}

// filteringRules are the rules of a chain other than the "-j RETURN" Docker
// puts in DOCKER-USER, which lets everything through.
func filteringRules(chain map[string]any) []string {
	var out []string
	for _, r := range stringsOf(chain["rules"]) {
		if strings.TrimSpace(r) != "-j RETURN" {
			out = append(out, r)
		}
	}
	return out
}

// firewallFindings are the points worth attention, worst first.
func firewallFindings(v firewallView, rows []exposure) []string {
	var bad, warn []string
	if v.ufw != nil {
		switch {
		case v.ufw["enabled"] != true:
			warn = append(warn, "ufw is installed but disabled.")
		case v.ufw["loaded"] == false:
			warn = append(warn, "ufw is enabled in ufw.conf but its rules are not loaded in the kernel.")
		case strings.EqualFold(str(v.ufw["default_input"]), "ACCEPT"):
			warn = append(warn, "ufw accepts incoming connections by default.")
		}
	}
	if !v.ufwActive && v.fw["can_read_rules"] == true && !v.inputFiltered() {
		warn = append(warn, "No firewall rule filters incoming traffic.")
	}

	var bypass, open []string
	for _, r := range rows {
		switch {
		case r.Level == "bad" && r.Docker:
			bypass = append(bypass, r.Proto+"/"+r.Port)
		case r.Level == "warn":
			open = append(open, r.Proto+"/"+r.Port)
		}
	}
	if len(bypass) > 0 {
		bad = append(bad, fmt.Sprintf("**RISK** Docker publishes %s on the network and ufw does not filter them: add DOCKER-USER rules or bind them to 127.0.0.1.",
			strings.Join(unique(bypass), ", ")))
	}
	if len(open) > 0 {
		warn = append(warn, fmt.Sprintf("Reachable from any network: %s.", strings.Join(unique(open), ", ")))
	}
	return append(bad, warn...)
}

func isLoopback(addr string) bool {
	return strings.HasPrefix(addr, "127.") || addr == "::1" || addr == "localhost"
}

func isAnywhere(addr string) bool {
	return addr == "" || addr == "any" || addr == "0.0.0.0/0" || addr == "::/0"
}

func protoMatches(rule, proto string) bool {
	return rule == "" || rule == "any" || rule == proto
}

// portMatches checks a port against a ufw port list: "any", "22",
// "80,443", "6000:6007".
func portMatches(rule, port string) bool {
	if rule == "" || rule == "any" {
		return true
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return false
	}
	for _, item := range strings.Split(rule, ",") {
		lo, hi, isRange := strings.Cut(item, ":")
		if !isRange {
			hi = lo
		}
		l, err1 := strconv.Atoi(lo)
		h, err2 := strconv.Atoi(hi)
		if err1 == nil && err2 == nil && p >= l && p <= h {
			return true
		}
	}
	return false
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

func unique(list []string) []string {
	var out []string
	for _, s := range list {
		out = appendUnique(out, s)
	}
	return out
}
