package firewall

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

// UFW is the configuration of ufw, the firewall front end of Ubuntu and Debian.
type UFW struct {
	// Enabled is ENABLED in /etc/ufw/ufw.conf: whether ufw starts at boot,
	// which is also its state unless someone stopped it by hand.
	Enabled bool `json:"enabled"`

	// Loaded is true when the ufw chains are in the kernel right now. It is
	// only known when the probe can read the kernel rules (nil otherwise).
	Loaded *bool `json:"loaded,omitempty"`

	// Default policies from /etc/default/ufw: ACCEPT, DROP or REJECT.
	DefaultInput   string `json:"default_input,omitempty"`
	DefaultOutput  string `json:"default_output,omitempty"`
	DefaultForward string `json:"default_forward,omitempty"`
	IPv6           bool   `json:"ipv6"`

	// RulesReadable is false when the rule files are readable by root only;
	// Rules is then empty, which does not mean there are none.
	RulesReadable bool      `json:"rules_readable"`
	Rules         []UFWRule `json:"rules"`
}

// UFWRule is a rule added with "ufw allow|deny|reject|limit ...".
type UFWRule struct {
	Action string `json:"action"` // allow, deny, reject, limit

	// Route is true for "ufw route" rules, which filter forwarded traffic
	// (containers, VMs) instead of traffic to the host.
	Route bool `json:"route,omitempty"`
	Log   bool `json:"log,omitempty"`

	Direction string `json:"direction"`           // in, out
	Interface string `json:"interface,omitempty"` // "on eth0"

	Protocol string `json:"protocol"` // tcp, udp, any
	Port     string `json:"port"`     // destination: "22", "80,443", "6000:6007", "any"
	To       string `json:"to"`       // destination address
	From     string `json:"from"`     // source address
	FromPort string `json:"from_port"`
	App      string `json:"app,omitempty"` // application profile ("OpenSSH")

	Comment string `json:"comment,omitempty"`
	IPv6    bool   `json:"ipv6"`
}

// The ufw files. ufw.conf and the defaults are world readable, the rules are
// readable by root only on Debian and Ubuntu.
const (
	ufwConf     = "etc/ufw/ufw.conf"
	ufwDefaults = "etc/default/ufw"
	ufwRules4   = "etc/ufw/user.rules"
	ufwRules6   = "etc/ufw/user6.rules"
)

// collectUFW reads the ufw configuration. ufw is considered not installed,
// without an error, when ufw.conf does not exist.
func (c *Collector) collectUFW() (*UFW, []string, error) {
	conf, err := fs.ReadFile(c.fsys, ufwConf)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("ufw: %w", err)
	}

	var errs []error
	var notes []string
	u := &UFW{Rules: []UFWRule{}}
	u.Enabled = strings.EqualFold(parseShellVars(conf)["ENABLED"], "yes")

	if data, err := fs.ReadFile(c.fsys, ufwDefaults); err == nil {
		vars := parseShellVars(data)
		u.DefaultInput = vars["DEFAULT_INPUT_POLICY"]
		u.DefaultOutput = vars["DEFAULT_OUTPUT_POLICY"]
		u.DefaultForward = vars["DEFAULT_FORWARD_POLICY"]
		u.IPv6 = strings.EqualFold(vars["IPV6"], "yes")
	} else if !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, fmt.Errorf("ufw: %w", err))
	}

	u.RulesReadable = true
	for _, f := range []struct {
		name string
		ipv6 bool
	}{{ufwRules4, false}, {ufwRules6, true}} {
		data, err := fs.ReadFile(c.fsys, f.name)
		switch {
		case err == nil:
			u.Rules = append(u.Rules, parseUFWRules(data, f.ipv6)...)
		case errors.Is(err, fs.ErrNotExist):
			// No rule of this family has ever been added.
		case errors.Is(err, fs.ErrPermission):
			u.RulesReadable = false
		default:
			errs = append(errs, fmt.Errorf("ufw: %w", err))
		}
	}
	if !u.RulesReadable {
		u.Rules = []UFWRule{}
		notes = append(notes, "The ufw rules (/etc/ufw/user.rules) are readable by root only: run the probe as root to see which ports ufw allows.")
	}
	return u, notes, errors.Join(errs...)
}

// parseShellVars parses the KEY=value lines of a shell variables file (values
// may be quoted). Comments and other lines are skipped.
func parseShellVars(data []byte) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return out
}

// parseUFWRules reads the "### tuple ###" comments ufw writes above the
// iptables rules of each user rule. They hold the rule as the user gave it:
//
//	### tuple ### ACTION PROTO DPORT DST SPORT SRC [DAPP SAPP] DIRECTION[_IFACE] [comment=HEX]
//
// ACTION may be prefixed by "route:" and suffixed by "_log" or "_log-all";
// application names have their spaces written as %20.
func parseUFWRules(data []byte, ipv6 bool) []UFWRule {
	var rules []UFWRule
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		rest, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "### tuple ###")
		if !ok {
			continue
		}
		f := strings.Fields(rest)

		var r UFWRule
		r.IPv6 = ipv6
		if n := len(f); n > 0 && strings.HasPrefix(f[n-1], "comment=") {
			if raw, err := hex.DecodeString(strings.TrimPrefix(f[n-1], "comment=")); err == nil {
				r.Comment = string(raw)
			}
			f = f[:n-1]
		}
		if len(f) != 7 && len(f) != 9 {
			continue // not a format this parser knows
		}

		action := f[0]
		if a, ok := strings.CutPrefix(action, "route:"); ok {
			r.Route, action = true, a
		}
		if a, _, ok := strings.Cut(action, "_log"); ok {
			r.Log, action = true, a
		}
		r.Action = action
		r.Protocol, r.Port, r.To, r.FromPort, r.From = f[1], f[2], f[3], f[4], f[5]
		if len(f) == 9 && f[6] != "-" {
			r.App = strings.ReplaceAll(f[6], "%20", " ")
		}
		r.Direction, r.Interface, _ = strings.Cut(f[len(f)-1], "_")
		rules = append(rules, r)
	}
	return rules
}
