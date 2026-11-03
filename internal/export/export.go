// Package export packs a report into a zip archive meant to give an AI
// assistant (or a human) the full context of a server: a README explaining the
// layout, a Markdown summary, one JSON file per resource and the full report.
//
// Everything goes through sanitize first, so secrets (environment values,
// password flags, credentials in URLs...) never leave the host.
package export

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/netseven/infra-probe/internal/report"
)

// docker sections split into one file per item, with their label field.
var dockerSections = []struct {
	name  string // key in the docker result and directory name
	label func(item map[string]any) string
}{
	{"containers", func(m map[string]any) string { return str(m["name"]) }},
	{"images", imageFileLabel},
	{"networks", func(m map[string]any) string { return str(m["name"]) }},
	{"volumes", func(m map[string]any) string { return str(m["name"]) }},
}

// Write encodes r as a zip archive on w.
func Write(w io.Writer, r *report.Report) error {
	root, err := sanitizedReport(r)
	if err != nil {
		return err
	}

	a := &archive{zw: zip.NewWriter(w), modified: r.GeneratedAt}
	if a.modified.IsZero() {
		a.modified = time.Now().UTC()
	}

	results, _ := root["results"].(map[string]any)
	m := manifest{
		Tool:        "infra-probe",
		Version:     str(root["version"]),
		GeneratedAt: r.GeneratedAt,
		Hostname:    hostnameOf(results),
		Redaction:   redactionNote,
		Counts:      map[string]int{},
		Errors:      stringMap(root["errors"]),
	}

	// Collectors other than docker are small: one file each.
	for _, name := range sortedKeys(results) {
		if name == "docker" {
			continue
		}
		path := "collectors/" + safeName(name) + ".json"
		if err := a.writeJSON(path, results[name]); err != nil {
			return err
		}
		m.Files = append(m.Files, fileEntry{path, fmt.Sprintf("output of the %q collector", name)})
	}

	// Docker: an index per section (summaries, no inspect payload) plus one
	// file per item with the full data.
	sum := summaryData{manifest: &m, results: results, files: map[string][]string{}}
	if dockerRes, ok := results["docker"].(map[string]any); ok {
		// How the daemon is set up: version, drivers, registries, daemon.json.
		if daemon, ok := dockerRes["daemon"].(map[string]any); ok {
			if err := a.writeJSON("docker/daemon.json", daemon); err != nil {
				return err
			}
			m.Files = append(m.Files, fileEntry{"docker/daemon.json", "Docker daemon: version, drivers, security options, registries, disk usage and daemon.json (secrets redacted)"})
		}
		for _, sec := range dockerSections {
			items := objects(dockerRes[sec.name])
			m.Counts[sec.name] = len(items)
			paths := assignPaths("docker/"+sec.name, items, sec.label)
			sum.files[sec.name] = paths

			index := make([]any, len(items))
			for i, item := range items {
				index[i] = indexEntry(item, paths[i])
				if err := a.writeJSON(paths[i], item); err != nil {
					return err
				}
			}
			indexPath := "docker/" + sec.name + "/index.json"
			if err := a.writeJSON(indexPath, index); err != nil {
				return err
			}
			m.Files = append(m.Files,
				fileEntry{indexPath, "list of " + sec.name + " without the inspect payload, each with the path of its detail file"},
				fileEntry{"docker/" + sec.name + "/<name>.json", "one file per item: summary + full Docker inspect"},
			)
		}
	}

	if len(m.Errors) > 0 {
		if err := a.writeJSON("errors.json", m.Errors); err != nil {
			return err
		}
		m.Files = append(m.Files, fileEntry{"errors.json", "collectors or items that could not be read: the data is incomplete"})
	}

	if err := a.writeJSON("report.json", root); err != nil {
		return err
	}
	m.Files = append([]fileEntry{
		{"README.md", "how to read this archive"},
		{"SUMMARY.md", "human/AI readable overview of the server, start here"},
		{"manifest.json", "metadata, counts and file list"},
		{"report.json", "the complete sanitized report in a single file"},
	}, m.Files...)

	if err := a.writeFile("README.md", []byte(readme(&m))); err != nil {
		return err
	}
	if err := a.writeFile("SUMMARY.md", []byte(summary(&sum))); err != nil {
		return err
	}
	if err := a.writeJSON("manifest.json", m); err != nil {
		return err
	}
	return a.zw.Close()
}

// FileName is the suggested name of the archive, e.g.
// "infra-probe-web01-20261103T100000Z.zip".
func FileName(r *report.Report) string {
	name := "infra-probe"
	if root, err := sanitizedReport(r); err == nil {
		results, _ := root["results"].(map[string]any)
		if h := hostnameOf(results); h != "" {
			name += "-" + safeName(h)
		}
	}
	ts := r.GeneratedAt
	if ts.IsZero() {
		ts = time.Now()
	}
	return name + "-" + ts.UTC().Format("20060102T150405Z") + ".zip"
}

const redactionNote = "Environment variable values, values of secret-looking keys and labels, " +
	"password/token flags in commands and credentials in URLs are replaced with \"" + Redacted + "\"."

type fileEntry struct {
	Path        string `json:"path"`
	Description string `json:"description"`
}

type manifest struct {
	Tool        string            `json:"tool"`
	Version     string            `json:"version"`
	GeneratedAt time.Time         `json:"generated_at"`
	Hostname    string            `json:"hostname,omitempty"`
	Redaction   string            `json:"redaction"`
	Counts      map[string]int    `json:"counts"`
	Errors      map[string]string `json:"errors,omitempty"`
	Files       []fileEntry       `json:"files"`
}

// sanitizedReport converts the report to generic JSON values and strips
// secrets. Going through JSON makes the export independent of the concrete
// collector types: whatever a collector returns is covered.
func sanitizedReport(r *report.Report) (map[string]any, error) {
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("encode report: %w", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("decode report: %w", err)
	}
	root, _ := sanitize(generic).(map[string]any)
	return root, nil
}

// archive writes entries with a fixed timestamp, so the same report always
// produces the same archive.
type archive struct {
	zw       *zip.Writer
	modified time.Time
}

func (a *archive) writeFile(path string, data []byte) error {
	f, err := a.zw.CreateHeader(&zip.FileHeader{Name: path, Method: zip.Deflate, Modified: a.modified})
	if err != nil {
		return fmt.Errorf("zip %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("zip %s: %w", path, err)
	}
	return nil
}

func (a *archive) writeJSON(path string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false) // keep "<redacted>" readable
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return a.writeFile(path, buf.Bytes())
}

// indexEntry is an item without its bulky inspect payload, plus a pointer to
// the file holding everything.
func indexEntry(item map[string]any, path string) map[string]any {
	out := make(map[string]any, len(item))
	for k, v := range item {
		if k != "inspect" {
			out[k] = v
		}
	}
	out["file"] = path
	return out
}

// assignPaths gives every item a unique, filesystem-safe file name.
func assignPaths(dir string, items []map[string]any, label func(map[string]any) string) []string {
	paths := make([]string, len(items))
	used := map[string]bool{"index": true}
	for i, item := range items {
		base := safeName(label(item))
		id := safeName(str(item["id"]))
		if base == "" {
			base = id
		}
		if base == "" {
			base = fmt.Sprintf("item-%d", i+1)
		}
		name := base
		for n := 2; used[name]; n++ {
			if id != "" && n == 2 {
				name = base + "-" + shortHex(id)
				continue
			}
			name = fmt.Sprintf("%s-%d", base, n)
		}
		used[name] = true
		paths[i] = dir + "/" + name + ".json"
	}
	return paths
}

// imageFileLabel is the first usable tag of an image, or its short ID.
func imageFileLabel(m map[string]any) string {
	for _, t := range stringsOf(m["repo_tags"]) {
		if t != "<none>:<none>" {
			return t
		}
	}
	return shortHex(str(m["id"]))
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// safeName turns any label into a portable file name component.
func safeName(s string) string {
	s = strings.Trim(unsafeChars.ReplaceAllString(s, "_"), "._-")
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

func shortHex(id string) string {
	id = strings.TrimPrefix(strings.TrimPrefix(id, "sha256:"), "sha256_")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func hostnameOf(results map[string]any) string {
	if h, ok := results["host"].(map[string]any); ok {
		return str(h["hostname"])
	}
	return ""
}

// ---------- generic JSON helpers ----------

func str(v any) string {
	s, _ := v.(string)
	return s
}

func objects(v any) []map[string]any {
	list, _ := v.([]any)
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func stringsOf(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func stringMap(v any) map[string]string {
	m, _ := v.(map[string]any)
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, val := range m {
		out[k] = str(val)
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
