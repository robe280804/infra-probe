package redact

import (
	"regexp"
	"strings"
)

// secretName matches names (map keys, flags, VAR names) that usually hold a
// secret: passwords, tokens, API keys, private keys, credentials.
var secretName = regexp.MustCompile(`(?i)(passw|passphrase|pwd|secret|token|api[-_]?key|private[-_]?key|access[-_]?key|auth[-_]?key|credential)`)

// commandKeys are the fields holding an argv-like list (Docker inspect).
var commandKeys = map[string]bool{"Cmd": true, "Entrypoint": true, "Args": true, "Test": true}

// Tree walks a decoded JSON value (maps, lists, strings, numbers...) and
// returns a copy with secrets removed. It does not depend on what produced the
// value, so it covers whatever a collector returns, now and in the future: it
// is the single choke point every output (scan, dashboard API, export) goes
// through, on top of the redaction the collectors do themselves.
//
// It masks environment values, the value of keys named like a secret, the
// value following a "--password" style flag, "password=..." pairs inside any
// string and passwords in URLs.
func Tree(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = field(k, val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = Tree(val)
		}
		return out
	case string:
		return String(t)
	default:
		return v
	}
}

func field(key string, val any) any {
	switch {
	case key == "Env":
		if list, ok := val.([]any); ok {
			return envList(list)
		}
	case commandKeys[key]:
		if list, ok := val.([]any); ok {
			return args(list)
		}
	case secretName.MatchString(key):
		if s, ok := val.(string); ok && s != "" {
			return Mask
		}
	}
	return Tree(val)
}

// envList keeps only variable names: "KEY=value" becomes "KEY=<redacted>".
func envList(list []any) []any {
	out := make([]any, len(list))
	for i, item := range list {
		s, ok := item.(string)
		if !ok {
			out[i] = Tree(item)
			continue
		}
		key, _, _ := strings.Cut(s, "=")
		out[i] = key + "=" + Mask
	}
	return out
}

// args masks secrets in a command line given as a list of arguments:
// "--password=x", "DB_TOKEN=x", the value following "--password", and secrets
// inside shell strings ("sh -c '...'").
func args(list []any) []any {
	out := make([]any, len(list))
	maskNext := false
	for i, item := range list {
		s, ok := item.(string)
		if !ok {
			out[i] = Tree(item)
			maskNext = false
			continue
		}
		if maskNext {
			out[i] = Mask
			maskNext = false
			continue
		}
		// A bare flag such as "--password" carries its value in the next argument.
		if strings.HasPrefix(s, "-") && !strings.ContainsAny(s, "= ") && secretName.MatchString(s) {
			out[i] = s
			maskNext = true
			continue
		}
		out[i] = String(s)
	}
	return out
}

// Args masks secrets in a command line given as a list of arguments, the way
// Tree does for the "Cmd" and "Entrypoint" fields of a Docker inspect.
func Args(list []string) []string {
	generic := make([]any, len(list))
	for i, s := range list {
		generic[i] = s
	}
	masked := args(generic)
	out := make([]string, len(masked))
	for i, v := range masked {
		out[i], _ = v.(string)
	}
	return out
}

// Command masks secrets in a shell command written as one string (a cron
// line, for example): "--password x", "--token=x", "PASSWORD=x"... The words
// are separated by single spaces in the result.
func Command(s string) string {
	return strings.Join(Args(strings.Fields(String(s))), " ")
}
