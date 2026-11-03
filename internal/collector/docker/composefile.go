package docker

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/netseven/infra-probe/internal/redact"
)

// Status of a compose file read attempt.
const (
	fileOK         = "ok"
	fileMissing    = "missing"    // the file no longer exists
	fileUnreadable = "unreadable" // permission denied or other I/O error
	fileInvalid    = "invalid"    // not a (reasonable) compose file
	fileSkipped    = "skipped"    // the file is not on this machine
)

// maxComposeFileSize bounds what is read: a compose file is a few KiB.
const maxComposeFileSize = 1 << 20

var (
	errNotLocal   = errors.New("the Docker daemon is not on this machine")
	errNotRegular = errors.New("not a regular file")
	errTooLarge   = errors.New("file too large")
)

// ComposeFile is a compose file of a project, read from disk and sanitized.
type ComposeFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`

	// ModifiedAt is the modification time of the file.
	ModifiedAt *time.Time `json:"modified_at,omitempty"`

	// Content is the parsed file, with secrets removed: environment and build
	// argument values are masked (variable names and ${VAR} references are
	// kept), as are secret-looking keys and passwords in URLs. Variables are
	// not interpolated and env files are not read.
	Content map[string]any `json:"content,omitempty"`
}

// daemonIsLocal reports whether the Docker daemon runs on this machine, so
// that paths recorded by Compose can be read here. A remote daemon (tcp, ssh)
// has its files elsewhere.
func daemonIsLocal() bool {
	host := os.Getenv("DOCKER_HOST")
	return host == "" || strings.HasPrefix(host, "unix://") || strings.HasPrefix(host, "npipe://")
}

// readLocalComposeFile reads a compose file. The path comes from a container
// label, which whoever can create containers controls, so it is only read if
// it is a regular file of reasonable size.
func readLocalComposeFile(path string) ([]byte, time.Time, error) {
	if !daemonIsLocal() {
		return nil, time.Time{}, errNotLocal
	}
	// Stat before Open: opening a FIFO or a device could block.
	info, err := os.Stat(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	if !info.Mode().IsRegular() {
		return nil, time.Time{}, errNotRegular
	}
	if info.Size() > maxComposeFileSize {
		return nil, time.Time{}, errTooLarge
	}

	f, err := os.Open(path) //nolint:gosec // validated by the caller: absolute, .yml/.yaml, regular file, size-capped
	if err != nil {
		return nil, time.Time{}, err
	}
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(io.LimitReader(f, maxComposeFileSize+1))
	if err != nil {
		return nil, time.Time{}, err
	}
	if len(data) > maxComposeFileSize {
		return nil, time.Time{}, errTooLarge
	}
	return data, info.ModTime(), nil
}

// attachComposeFiles reads the files of every project. A file that cannot be
// read for a reason other than being gone or being elsewhere is returned as an
// error, so it reaches the report and the exit code.
func (c *Collector) attachComposeFiles(projects []ComposeProject, containers []Container) []error {
	created := map[string]time.Time{}
	for _, ct := range containers {
		created[ct.ID] = ct.CreatedAt
	}

	var errs []error
	for i := range projects {
		p := &projects[i]
		p.Files = make([]ComposeFile, 0, len(p.ConfigFiles))
		for _, path := range p.ConfigFiles {
			f := c.loadComposeFile(path)
			if f.Status == fileUnreadable || f.Status == fileInvalid {
				errs = append(errs, fmt.Errorf("compose project %s: %s: %s", p.Name, path, f.Error))
			}
			p.Files = append(p.Files, f)
		}
		p.ConfigChangedSinceStart = changedSinceStart(p, created)
	}
	return errs
}

// changedSinceStart is true when a compose file was modified after the oldest
// container of the project was created: the running configuration may differ
// from the file. It is a hint, not proof (the edit may be a comment).
func changedSinceStart(p *ComposeProject, created map[string]time.Time) bool {
	var oldest time.Time
	for _, s := range p.Services {
		for _, ct := range s.Containers {
			if ct.OneOff {
				continue
			}
			if t := created[ct.ID]; !t.IsZero() && (oldest.IsZero() || t.Before(oldest)) {
				oldest = t
			}
		}
	}
	if oldest.IsZero() {
		return false
	}
	for _, f := range p.Files {
		if f.ModifiedAt != nil && f.ModifiedAt.After(oldest) {
			return true
		}
	}
	return false
}

func (c *Collector) loadComposeFile(path string) ComposeFile {
	f := ComposeFile{Path: path}

	if !filepath.IsAbs(path) {
		f.Status, f.Error = fileSkipped, "the path is not valid on this machine"
		return f
	}
	if ext := strings.ToLower(filepath.Ext(path)); ext != ".yml" && ext != ".yaml" {
		f.Status, f.Error = fileInvalid, "not a .yml/.yaml file"
		return f
	}

	data, modTime, err := c.readComposeFile(path)
	switch {
	case errors.Is(err, errNotLocal):
		f.Status, f.Error = fileSkipped, err.Error()
		return f
	case errors.Is(err, fs.ErrNotExist):
		f.Status, f.Error = fileMissing, "the file no longer exists"
		return f
	case errors.Is(err, errNotRegular), errors.Is(err, errTooLarge):
		f.Status, f.Error = fileInvalid, err.Error()
		return f
	case err != nil:
		f.Status, f.Error = fileUnreadable, err.Error()
		return f
	}
	if !modTime.IsZero() {
		t := modTime.UTC()
		f.ModifiedAt = &t
	}

	content, err := parseComposeFile(data)
	if err != nil {
		f.Status, f.Error = fileInvalid, err.Error()
		return f
	}
	f.Status, f.Content = fileOK, content
	return f
}

// parseComposeFile parses and sanitizes a compose file. The errors are
// deliberately generic: a parser message could quote the content.
func parseComposeFile(data []byte) (map[string]any, error) {
	var doc any
	// Only the first document: compose files have one. The decoder rejects
	// documents with excessive aliasing.
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
		return nil, errors.New("not valid YAML")
	}
	root, ok := normalize(doc).(map[string]any)
	if !ok {
		return nil, errors.New("not a compose file (no services section)")
	}
	// The path of the file is untrusted, so only something that looks like a
	// compose file is exported: anything else could be an unrelated secret.
	if _, ok := root["services"].(map[string]any); !ok {
		return nil, errors.New("not a compose file (no services section)")
	}
	out, _ := sanitizeCompose(root, nil).(map[string]any)
	return out, nil
}

// normalize converts what the YAML decoder returns into JSON friendly values:
// string keys, finite numbers, no raw bytes.
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = normalize(val)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[fmt.Sprint(k)] = normalize(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalize(val)
		}
		return out
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return fmt.Sprint(t)
		}
		return t
	case []byte:
		return redact.Mask // !!binary: could be anything
	default:
		return v
	}
}

var (
	// envName matches a conventional variable name. A mapping whose keys all
	// look like this is treated as an environment, wherever it appears (for
	// example under an x- extension field reused through a YAML anchor).
	envName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

	// envEntry matches a "NAME=value" list item.
	envEntry = regexp.MustCompile(`^([A-Z][A-Z0-9_]*)=`)

	// labelSecret matches label and flag names holding credentials, including
	// the reverse proxy ones (basic auth user lists carry password hashes).
	labelSecret = regexp.MustCompile(`(?i)(pass|secret|token|credential|api[-_]?key|private[-_]?key|access[-_]?key|basicauth|digestauth|htpasswd)`)
)

// keysKeptAsIs are names that match the secret pattern but only hold names or
// paths: "secrets" lists which secrets a service uses, "env_file" paths.
var keysKeptAsIs = map[string]bool{"secrets": true, "env_file": true}

// namedSections are the top-level sections whose keys are names chosen by the
// user (a service called "password-manager" is not a secret).
var namedSections = map[string]bool{"services": true, "networks": true, "volumes": true, "configs": true, "secrets": true}

// sanitizeCompose walks the parsed file. path is the chain of keys leading to
// v. Rules, in short: values of environment variables and build args are
// masked; a ${VAR} reference is a name, so it is kept; secret-looking keys are
// masked; passwords in URLs and "--password=x" pairs are masked everywhere.
func sanitizeCompose(v any, path []string) any {
	switch t := v.(type) {
	case map[string]any:
		return sanitizeMapping(t, path)
	case []any:
		return sanitizeList(t, path)
	case string:
		return redact.String(t)
	default:
		return v
	}
}

func parentKey(path []string) string {
	if len(path) == 0 {
		return ""
	}
	return path[len(path)-1]
}

func hasKey(path []string, key string) bool {
	for _, p := range path {
		if p == key {
			return true
		}
	}
	return false
}

func sanitizeMapping(m map[string]any, path []string) any {
	// A flat mapping of VARIABLE_NAMES is an environment, whatever its key.
	if looksLikeEnv(m) {
		return maskEnvMapping(m)
	}

	out := make(map[string]any, len(m))
	for k, val := range m {
		sub := append(append([]string(nil), path...), k)
		switch {
		case k == "environment", k == "args" && parentKey(path) == "build":
			out[k] = maskEnvValues(val, sub)
		case k == "labels":
			out[k] = sanitizeLabels(val)
		case k == "content" && hasKey(path, "configs"):
			// Inline config content can hold anything, including secrets.
			out[k] = maskLiteral(val)
		case labelSecret.MatchString(k) && !keysKeptAsIs[k] && !(len(path) == 1 && namedSections[path[0]]):
			out[k] = maskSecretValue(val)
		default:
			out[k] = sanitizeCompose(val, sub)
		}
	}
	return out
}

// looksLikeEnv is true for a non-empty mapping of scalars whose keys all look
// like environment variable names.
func looksLikeEnv(m map[string]any) bool {
	if len(m) == 0 {
		return false
	}
	for k, v := range m {
		if !envName.MatchString(k) {
			return false
		}
		switch v.(type) {
		case map[string]any, []any:
			return false
		}
	}
	return true
}

func sanitizeList(list []any, path []string) any {
	out := make([]any, len(list))
	maskNext := false
	command := parentKey(path) == "command" || parentKey(path) == "entrypoint"
	for i, item := range list {
		s, isString := item.(string)
		switch {
		case !isString:
			out[i] = sanitizeCompose(item, path)
		case maskNext:
			out[i] = redact.Mask
			maskNext = false
		case envEntry.MatchString(s):
			out[i] = maskEnvEntry(s)
		case command && strings.HasPrefix(s, "-") && !strings.ContainsAny(s, "= ") && labelSecret.MatchString(s):
			// "--password" carries its value in the next argument.
			out[i] = s
			maskNext = true
		default:
			out[i] = redact.String(s)
		}
	}
	return out
}

// maskEnvValues masks the values of an environment (mapping or list form).
// Names are kept: they tell which settings the service receives.
func maskEnvValues(v any, path []string) any {
	switch t := v.(type) {
	case map[string]any:
		return maskEnvMapping(t)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			s, ok := item.(string)
			if !ok {
				out[i] = redact.Mask
				continue
			}
			if name, _, found := strings.Cut(s, "="); found {
				out[i] = name + "=" + maskValue(s[len(name)+1:])
			} else {
				out[i] = s // "NAME": passed through from the environment
			}
		}
		return out
	default:
		return sanitizeCompose(v, path)
	}
}

func maskEnvMapping(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = maskScalar(v)
	}
	return out
}

func maskEnvEntry(s string) string {
	name, value, _ := strings.Cut(s, "=")
	return name + "=" + maskValue(value)
}

// maskScalar keeps null (a variable passed through) and variable references,
// and masks every literal.
func maskScalar(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		return maskValue(t)
	default:
		return redact.Mask
	}
}

// maskValue keeps a value made only of ${VAR} references (with their defaults
// masked) and masks any literal.
func maskValue(s string) string {
	if s == "" {
		return ""
	}
	if masked, only := redact.Reference(s); only {
		return masked
	}
	return redact.Mask
}

// maskSecretValue masks the value of a secret-looking key. Empty values and
// plain flags (booleans, numbers) hold no secret and are kept.
func maskSecretValue(v any) any {
	switch t := v.(type) {
	case nil, bool, int, int64, uint64, float64:
		return v
	case string:
		if t == "" {
			return t
		}
		return maskValue(t)
	default:
		return redact.Mask
	}
}

// maskLiteral masks a value unless it is nil or empty.
func maskLiteral(v any) any {
	if s, ok := v.(string); ok && s == "" {
		return v
	}
	if v == nil {
		return nil
	}
	return redact.Mask
}

// sanitizeLabels masks the labels whose name suggests a credential, and
// passwords inside the others.
func sanitizeLabels(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if s, ok := val.(string); ok {
				out[k] = sanitizeLabelValue(k, s)
			} else {
				out[k] = val
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			s, ok := item.(string)
			if !ok {
				out[i] = item
				continue
			}
			if name, value, found := strings.Cut(s, "="); found {
				out[i] = name + "=" + sanitizeLabelValue(name, value)
			} else {
				out[i] = redact.String(s)
			}
		}
		return out
	default:
		return sanitizeCompose(v, nil)
	}
}

func sanitizeLabelValue(name, value string) string {
	if value != "" && labelSecret.MatchString(name) {
		return maskValue(value)
	}
	return redact.String(value)
}
