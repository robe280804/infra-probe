package export

import (
	"strings"
	"testing"
)

// Fixtures in the shape of the decoded report (numbers are float64).

func fwHost() map[string]any {
	return map[string]any{
		"hostname": "web01",
		"listeners": []any{
			map[string]any{"protocol": "tcp", "address": "0.0.0.0", "port": 22.0, "all_interfaces": true, "process": "sshd"},
			map[string]any{"protocol": "tcp6", "address": "::", "port": 22.0, "all_interfaces": true, "process": "sshd"},
			map[string]any{"protocol": "tcp", "address": "127.0.0.1", "port": 5432.0, "loopback": true},
			// The Docker proxy holding the published port 8080.
			map[string]any{"protocol": "tcp", "address": "0.0.0.0", "port": 8080.0, "all_interfaces": true, "process": "docker-proxy"},
			map[string]any{"protocol": "udp", "address": "10.0.0.5", "port": 53.0},
		},
	}
}

func fwContainers() []map[string]any {
	return []map[string]any{
		{"name": "web", "inspect": map[string]any{"NetworkSettings": map[string]any{"Ports": map[string]any{
			"80/tcp": []any{map[string]any{"HostIp": "0.0.0.0", "HostPort": "8080"}, map[string]any{"HostIp": "::", "HostPort": "8080"}},
		}}}},
		{"name": "db", "inspect": map[string]any{"NetworkSettings": map[string]any{"Ports": map[string]any{
			"5432/tcp": []any{map[string]any{"HostIp": "127.0.0.1", "HostPort": "5432"}},
		}}}},
	}
}

func ufwActive(rulesReadable bool, rules ...any) map[string]any {
	return map[string]any{
		"enabled": true, "default_input": "DROP", "default_output": "ACCEPT", "default_forward": "DROP",
		"rules_readable": rulesReadable, "rules": rules,
	}
}

func ufwRule(action, port, from string) map[string]any {
	return map[string]any{"action": action, "direction": "in", "protocol": "tcp", "port": port, "from": from, "to": "0.0.0.0/0", "from_port": "any"}
}

func iptables(dockerUser ...any) map[string]any {
	return map[string]any{"backend": "nf_tables", "tables": []any{
		map[string]any{"name": "filter", "chains": []any{
			map[string]any{"name": "INPUT", "policy": "ACCEPT", "rules": []any{}},
			map[string]any{"name": "DOCKER-USER", "rules": dockerUser},
		}},
	}}
}

func firewallSummary(fw, host, daemon map[string]any, containers []map[string]any) string {
	var b strings.Builder
	writeFirewall(&b, fw, host, daemon, containers)
	return b.String()
}

// rowFor returns the table row of a port: "| 8080 | tcp | ...".
func rowFor(t *testing.T, sum, port, proto, addr string) string {
	t.Helper()
	prefix := "| " + port + " | " + proto + " | " + addr + " |"
	for _, line := range strings.Split(sum, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("no row %q in:\n%s", prefix, sum)
	return ""
}

func TestFirewall_UnprivilegedWithUFW(t *testing.T) {
	fw := map[string]any{
		"can_read_rules": false,
		"ufw":            ufwActive(false),
		"notes":          []any{"The kernel firewall rules can only be listed with CAP_NET_ADMIN."},
	}
	sum := firewallSummary(fw, fwHost(), nil, fwContainers())

	for _, want := range []string{
		"- **ufw**: enabled; default incoming DROP, outgoing ACCEPT, routed DROP",
		"- **Kernel rules**: not readable",
		"CAP_NET_ADMIN",
		"**RISK** Docker publishes tcp/8080 on the network and ufw does not filter them",
	} {
		if !strings.Contains(sum, want) {
			t.Errorf("summary lacks %q:\n%s", want, sum)
		}
	}

	// Docker bypasses ufw; both addresses of the published port are listed.
	for _, addr := range []string{"0.0.0.0", "::"} {
		if row := rowFor(t, sum, "8080", "tcp", addr); !strings.Contains(row, "container web") || !strings.Contains(row, "**RISK**") {
			t.Errorf("docker row = %s", row)
		}
	}
	// A host port behind ufw with unreadable rules: unknown, not open.
	if row := rowFor(t, sum, "22", "tcp", "0.0.0.0"); !strings.Contains(row, "sshd") || !strings.Contains(row, "readable by root only") || strings.Contains(row, "**") {
		t.Errorf("ssh row = %s", row)
	}
	// Loopback ports and the proxy of a published port are not exposures.
	if strings.Contains(sum, "| 5432 |") || strings.Contains(sum, "docker-proxy") {
		t.Errorf("loopback or proxy listed:\n%s", sum)
	}
	// Docker ports come first.
	if strings.Index(sum, "| 8080 |") > strings.Index(sum, "| 22 |") {
		t.Error("Docker ports must be listed first")
	}
	if strings.Contains(sum, "### ufw rules") {
		t.Error("unreadable ufw rules must not be listed as an empty table")
	}
}

func TestFirewall_RootWithRules(t *testing.T) {
	fw := map[string]any{
		"can_read_rules": true,
		"ufw": ufwActive(true,
			ufwRule("allow", "22", "0.0.0.0/0"),
			ufwRule("deny", "53", "0.0.0.0/0"),
			map[string]any{"action": "allow", "direction": "in", "protocol": "udp", "port": "50:60", "from": "10.0.0.0/8", "to": "any"},
		),
		"iptables": iptables("-s 10.0.0.0/8 -j RETURN", "-j DROP", "-j RETURN"),
		"notes":    []any{},
	}
	sum := firewallSummary(fw, fwHost(), nil, fwContainers())

	if row := rowFor(t, sum, "8080", "tcp", "0.0.0.0"); !strings.Contains(row, "DOCKER-USER has rules") {
		t.Errorf("docker row = %s", row)
	}
	if row := rowFor(t, sum, "22", "tcp", "0.0.0.0"); !strings.Contains(row, "**open** ufw: allowed from anywhere") {
		t.Errorf("ssh row = %s", row)
	}
	// The udp rule with a range and a source wins over nothing else.
	if row := rowFor(t, sum, "53", "udp", "10.0.0.5"); !strings.Contains(row, "ufw: allowed only from 10.0.0.0/8") {
		t.Errorf("dns row = %s", row)
	}
	for _, want := range []string{
		"- **Kernel rules**: iptables nf_tables (1 tables); full rules in `collectors/firewall.json`",
		"- **DOCKER-USER**: 2 filtering rules",
		"### ufw rules",
		"| deny | in | 53 | tcp | 0.0.0.0/0 |",
		"Reachable from any network: tcp/22.",
	} {
		if !strings.Contains(sum, want) {
			t.Errorf("summary lacks %q:\n%s", want, sum)
		}
	}
	if strings.Contains(sum, "RISK") {
		t.Errorf("filtered Docker ports flagged as a risk:\n%s", sum)
	}
}

func TestFirewall_NoFirewall(t *testing.T) {
	fw := map[string]any{"can_read_rules": true, "iptables": iptables("-j RETURN"), "notes": []any{}}
	sum := firewallSummary(fw, fwHost(), nil, fwContainers())

	for _, want := range []string{"- **ufw**: not installed", "No firewall rule filters incoming traffic.", "Reachable from any network: tcp/8080, tcp/22, udp/53."} {
		if !strings.Contains(sum, want) {
			t.Errorf("summary lacks %q:\n%s", want, sum)
		}
	}
	if row := rowFor(t, sum, "8080", "tcp", "0.0.0.0"); !strings.Contains(row, "**open** open: no DOCKER-USER rule") || strings.Contains(row, "ufw") {
		t.Errorf("docker row = %s", row)
	}
	if row := rowFor(t, sum, "22", "tcp", "0.0.0.0"); !strings.Contains(row, "open: no firewall rule") {
		t.Errorf("ssh row = %s", row)
	}
}

func TestFirewall_DockerWithoutNAT(t *testing.T) {
	fw := map[string]any{"can_read_rules": false, "ufw": ufwActive(true, ufwRule("allow", "8080", "192.168.0.0/16"))}
	rootless := map[string]any{"info": map[string]any{"rootless": true}}
	noIptables := map[string]any{"info": map[string]any{}, "config": map[string]any{"content": map[string]any{"iptables": false}}}

	for name, daemon := range map[string]map[string]any{"rootless": rootless, "iptables false": noIptables} {
		sum := firewallSummary(fw, nil, daemon, fwContainers())
		row := rowFor(t, sum, "8080", "tcp", "0.0.0.0")
		if !strings.Contains(row, "ufw: allowed only from 192.168.0.0/16") || !strings.Contains(row, "host rules apply") {
			t.Errorf("%s: docker row = %s", name, row)
		}
		if strings.Contains(sum, "RISK") {
			t.Errorf("%s: no bypass without NAT rules:\n%s", name, sum)
		}
		if !strings.Contains(sum, "host listening ports are not part of this export") {
			t.Errorf("%s: missing host note", name)
		}
	}
}

func TestFirewall_DisabledUFWAndNoContainers(t *testing.T) {
	fw := map[string]any{"can_read_rules": false, "ufw": map[string]any{"enabled": false, "default_input": "DROP"}}
	sum := firewallSummary(fw, fwHost(), nil, nil)

	for _, want := range []string{"- **ufw**: disabled", "ufw is installed but disabled.", "containers are not part of this export"} {
		if !strings.Contains(sum, want) {
			t.Errorf("summary lacks %q:\n%s", want, sum)
		}
	}
	// Without the containers, the proxy port is a plain host port.
	if row := rowFor(t, sum, "8080", "tcp", "0.0.0.0"); !strings.Contains(row, "docker-proxy") || !strings.Contains(row, "unknown") {
		t.Errorf("proxy row = %s", row)
	}
}

func TestPortMatches(t *testing.T) {
	tests := []struct {
		rule, port string
		want       bool
	}{
		{"any", "22", true},
		{"", "22", true},
		{"22", "22", true},
		{"22", "2222", false},
		{"80,443", "443", true},
		{"6000:6007", "6005", true},
		{"6000:6007", "6008", false},
		{"80,6000:6007", "80", true},
		{"22", "x", false},
	}
	for _, tt := range tests {
		if got := portMatches(tt.rule, tt.port); got != tt.want {
			t.Errorf("portMatches(%q, %q) = %v", tt.rule, tt.port, got)
		}
	}
}

func TestWrite_FirewallInExport(t *testing.T) {
	r := hostReport()
	r.Results["firewall"] = map[string]any{"can_read_rules": false, "ufw": ufwActive(false), "notes": []any{}}

	files := buildZip(t, r)
	if !strings.Contains(files["SUMMARY.md"], "## Firewall and exposure") {
		t.Error("SUMMARY.md has no firewall section")
	}
	if _, ok := files["collectors/firewall.json"]; !ok {
		t.Error("collectors/firewall.json missing")
	}

	// Excluding the category removes it everywhere.
	files = buildZipOnly(t, r, []string{"host", "containers"})
	if strings.Contains(files["SUMMARY.md"], "Firewall") || strings.Contains(files["report.json"], "can_read_rules") {
		t.Error("firewall data in an export that excludes it")
	}
}
