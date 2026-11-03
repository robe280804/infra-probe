// Package redact masks secrets in free-form text. It is shared by the
// collectors (what is put in the report) and the export (second pass), so the
// same rules apply wherever data leaves the host.
package redact

import "regexp"

// Mask replaces every value removed from the output.
const Mask = "<redacted>"

// inlineSecret matches "name=value" / "name: value" pairs inside a free-form
// string, e.g. a shell command "mysql --password=s3cret -h db". A colon only
// counts when followed by a space, so image references such as
// "secret-app:1.0" are left alone.
var inlineSecret = regexp.MustCompile(`(?i)([\w.-]*(?:passw|passphrase|secret|token|api[-_]?key|private[-_]?key|access[-_]?key|auth[-_]?key|credential)[\w.-]*\s*(?:=|:\s)\s*)("[^"]*"|'[^']*'|[^\s"']+)`)

// urlCredentials matches the password part of "scheme://user:password@host".
var urlCredentials = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^/\s:@]*:)[^@\s/]+@`)

// String masks secrets embedded in a free-form string: passwords in URLs and
// "password=..." style pairs.
func String(s string) string {
	s = urlCredentials.ReplaceAllString(s, "${1}"+Mask+"@")
	return inlineSecret.ReplaceAllString(s, "${1}"+Mask)
}

// URL masks only the password of "scheme://user:password@host".
func URL(s string) string {
	return urlCredentials.ReplaceAllString(s, "${1}"+Mask+"@")
}

// defaultValue matches the default of a compose style substitution:
// ${VAR:-default}, ${VAR-default}, ${VAR:?error}, ${VAR:+alt}.
var defaultValue = regexp.MustCompile(`(\$\{[A-Za-z_][A-Za-z0-9_]*:?[-?+])[^}]*\}`)

// reference matches a string made only of variable references: "$VAR",
// "${VAR}", "${VAR:-<redacted>}", possibly repeated.
var reference = regexp.MustCompile(`^(\$[A-Za-z_][A-Za-z0-9_]*|\$\{[A-Za-z_][A-Za-z0-9_]*(:?[-?+]` + regexp.QuoteMeta(Mask) + `)?\})+$`)

// Reference returns s with the literal defaults of variable substitutions
// masked (they can be real secrets: "${DB_PASS:-hunter2}"), and whether the
// result contains nothing but variable references. A reference is a name, not
// a value: it is safe to keep, and it tells which variable the stack expects.
func Reference(s string) (masked string, only bool) {
	masked = defaultValue.ReplaceAllString(s, "${1}"+Mask+"}")
	return masked, reference.MatchString(masked)
}
