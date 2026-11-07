package firewall

import (
	"bufio"
	"bytes"
	"regexp"
	"strings"
)

// Nftables is the output of "nft list ruleset".
type Nftables struct {
	Tables []NftTable `json:"tables"`
}

// NftTable is an nftables table.
type NftTable struct {
	Family string     `json:"family"` // ip, ip6, inet, arp, bridge, netdev
	Name   string     `json:"name"`
	Chains []NftChain `json:"chains"`

	// Sets are the names of the named sets and maps (their elements are not
	// kept: they can be long lists of addresses).
	Sets []string `json:"sets"`
}

// NftChain is a chain and its rules, in order.
type NftChain struct {
	Name string `json:"name"`

	// Base chains are attached to a hook of the network stack (input,
	// forward, output, prerouting, postrouting); regular chains are only
	// reached by a jump and have none of these fields.
	Type     string `json:"type,omitempty"` // filter, nat, route
	Hook     string `json:"hook,omitempty"`
	Priority string `json:"priority,omitempty"`
	Policy   string `json:"policy,omitempty"` // accept (the default) or drop

	// Rules are the statements as nft prints them: "tcp dport 22 accept".
	Rules []string `json:"rules"`
}

// parseNftRuleset parses the text output of "nft list ruleset":
//
//	table inet filter {
//		set allowed { type ipv4_addr; elements = { 10.0.0.1, 10.0.0.2 } }
//		chain input {
//			type filter hook input priority filter; policy drop;
//			tcp dport 22 accept
//		}
//	}
//
// Blocks are tracked by counting braces, so the elements of a set may span
// lines and anonymous sets ({ 80, 443 }) may appear inside a rule.
func parseNftRuleset(data []byte) *Nftables {
	out := &Nftables{Tables: []NftTable{}}
	var (
		table *NftTable
		chain *NftChain
		depth int
	)

	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		opens, closes := strings.Count(line, "{"), strings.Count(line, "}")
		f := strings.Fields(line)

		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case depth == 0 && len(f) >= 3 && f[0] == "table":
			t := NftTable{Chains: []NftChain{}, Sets: []string{}}
			if len(f) >= 4 && f[3] == "{" {
				t.Family, t.Name = f[1], f[2]
			} else {
				t.Family, t.Name = "ip", f[1] // "table NAME {": family ip by default
			}
			out.Tables = append(out.Tables, t)
			table = &out.Tables[len(out.Tables)-1]
		case depth == 1 && table != nil && len(f) >= 2 && f[0] == "chain":
			table.Chains = append(table.Chains, NftChain{Name: f[1], Rules: []string{}})
			chain = &table.Chains[len(table.Chains)-1]
		case depth == 1 && table != nil && len(f) >= 2 && (f[0] == "set" || f[0] == "map"):
			table.Sets = append(table.Sets, f[1])
		case depth == 2 && chain != nil && line != "}":
			if strings.HasPrefix(line, "type ") {
				parseChainHeader(chain, line)
			} else if !strings.HasPrefix(line, "comment ") {
				chain.Rules = append(chain.Rules, counters.ReplaceAllString(line, "counter"))
			}
		}

		depth += opens - closes
		if depth < 0 {
			depth = 0
		}
		if depth <= 1 {
			chain = nil
		}
		if depth == 0 {
			table = nil
		}
	}
	return out
}

// counters are the packet and byte counts nft prints after "counter": they
// change all the time and say nothing about the configuration.
var counters = regexp.MustCompile(`counter packets \d+ bytes \d+`)

// parseChainHeader reads "type filter hook input priority filter; policy drop;".
func parseChainHeader(c *NftChain, line string) {
	for _, part := range strings.Split(line, ";") {
		f := strings.Fields(part)
		for i := 0; i+1 < len(f); i++ {
			switch f[i] {
			case "type":
				c.Type = f[i+1]
			case "hook":
				c.Hook = f[i+1]
			case "priority":
				// The priority may be a name with an offset: "filter - 10".
				c.Priority = strings.Join(f[i+1:], " ")
				i = len(f)
			case "policy":
				c.Policy = f[i+1]
			}
		}
	}
}
