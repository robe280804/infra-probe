package export

import (
	"regexp"
	"strings"

	"github.com/netseven/infra-probe/internal/redact"
)

// Redacted replaces every value removed from the export.
const Redacted = redact.Mask

// secretName matches names (map keys, flags, VAR names) that usually hold a
// secret: passwords, tokens, API keys, private keys, credentials.
var secretName = regexp.MustCompile(`(?i)(passw|passphrase|pwd|secret|token|api[-_]?key|private[-_]?key|access[-_]?key|auth[-_]?key|credential)`)

// commandKeys are the Docker inspect fields holding an argv-like list.
var commandKeys = map[string]bool{"Cmd": true, "Entrypoint": true, "Args": true, "Test": true}

// sanitize walks a decoded JSON value and returns a copy with secrets removed.
// It is a second line of defense on top of the redaction done by collectors:
// the export is meant to be handed to third parties (an AI assistant), so
// anything that looks like a credential is masked.
func sanitize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = sanitizeField(k, val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = sanitize(val)
		}
		return out
	case string:
		return sanitizeString(t)
	default:
		return v
	}
}

func sanitizeField(key string, val any) any {
	switch {
	case key == "Env":
		if list, ok := val.([]any); ok {
			return redactEnvList(list)
		}
	case commandKeys[key]:
		if list, ok := val.([]any); ok {
			return redactArgs(list)
		}
	case secretName.MatchString(key):
		if s, ok := val.(string); ok && s != "" {
			return Redacted
		}
	}
	return sanitize(val)
}

// sanitizeString masks secrets embedded in a free-form string.
func sanitizeString(s string) string {
	return redact.String(s)
}

// redactEnvList keeps only variable names: "KEY=value" becomes "KEY=<redacted>".
// Collectors already do this; repeating it here keeps the guarantee even if a
// future collector forgets.
func redactEnvList(list []any) []any {
	out := make([]any, len(list))
	for i, item := range list {
		s, ok := item.(string)
		if !ok {
			out[i] = sanitize(item)
			continue
		}
		key, _, _ := strings.Cut(s, "=")
		out[i] = key + "=" + Redacted
	}
	return out
}

// redactArgs masks secrets in a command line given as a list of arguments:
// "--password=x", "DB_TOKEN=x", the value following "--password", and secrets
// inside shell strings ("sh -c '...'").
func redactArgs(list []any) []any {
	out := make([]any, len(list))
	maskNext := false
	for i, item := range list {
		s, ok := item.(string)
		if !ok {
			out[i] = sanitize(item)
			maskNext = false
			continue
		}
		if maskNext {
			out[i] = Redacted
			maskNext = false
			continue
		}
		// A bare flag such as "--password" carries its value in the next argument.
		if strings.HasPrefix(s, "-") && !strings.ContainsAny(s, "= ") && secretName.MatchString(s) {
			out[i] = s
			maskNext = true
			continue
		}
		out[i] = sanitizeString(s)
	}
	return out
}
