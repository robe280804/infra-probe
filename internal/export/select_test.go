package export

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/netseven/infra-probe/internal/report"
)

func buildZipOnly(t *testing.T, r *report.Report, selected []string) map[string]string {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteOnly(&buf, r, selected); err != nil {
		t.Fatalf("WriteOnly(%v): %v", selected, err)
	}
	return readZip(t, buf.Bytes())
}

func hasFilesUnder(files map[string]string, prefix string) bool {
	for name := range files {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func TestParseCategories(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", []string{""}, nil},
		{"no values", nil, nil},
		{"canonical order", []string{"images,containers"}, []string{"containers", "images"}},
		{"duplicates, spaces and case", []string{" Containers , images,containers,, "}, []string{"containers", "images"}},
		{"several values", []string{"volumes", "host,compose"}, []string{"host", "compose", "volumes"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCategories(tt.in...)
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got (%v, %v), want %v", got, err, tt.want)
			}
		})
	}

	_, err := ParseCategories("containers,kubernetes")
	if err == nil || !strings.Contains(err.Error(), `unknown category "kubernetes"`) || !strings.Contains(err.Error(), "volumes") {
		t.Errorf("unknown category error = %v, want it to name the category and list the valid ones", err)
	}
}

func TestWithout(t *testing.T) {
	got, err := Without("volumes", "images")
	if err != nil || !reflect.DeepEqual(got, []string{"host", "firewall", "daemon", "compose", "containers", "networks"}) {
		t.Errorf("Without = (%v, %v)", got, err)
	}
	if got, err := Without(); err != nil || len(got) != len(Categories) {
		t.Errorf("Without() = (%v, %v), want everything", got, err)
	}
	if _, err := Without(Categories...); !errors.Is(err, ErrNoCategory) {
		t.Errorf("excluding everything: err = %v, want ErrNoCategory", err)
	}
	if _, err := Without("nope"); err == nil {
		t.Error("an unknown category must be an error")
	}
}

func TestSelect(t *testing.T) {
	tests := []struct {
		name, include, exclude string
		want                   []string
		wantErr                bool
	}{
		{"neither", "", "", nil, false},
		{"blank options", "  ", " ", nil, false},
		{"include", "images,containers", "", []string{"containers", "images"}, false},
		{"exclude", "", "host,firewall,daemon,compose,containers,images", []string{"networks", "volumes"}, false},
		{"both", "images", "volumes", nil, true},
		{"include only separators", ",", "", nil, true},
		{"exclude everything", "", strings.Join(Categories, ","), nil, true},
		{"unknown include", "nope", "", nil, true},
		{"unknown exclude", "", "nope", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Select(tt.include, tt.exclude)
			if (err != nil) != tt.wantErr || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Select(%q, %q) = (%v, %v), want (%v, error %v)", tt.include, tt.exclude, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestWriteOnly_Containers(t *testing.T) {
	files := buildZipOnly(t, sampleReport(), []string{CategoryContainers})

	for _, want := range []string{"docker/containers/index.json", "docker/containers/db.json", "SUMMARY.md", "README.md", "manifest.json", "report.json"} {
		if _, ok := files[want]; !ok {
			t.Errorf("missing %s", want)
		}
	}
	for _, prefix := range []string{"docker/images/", "docker/networks/", "docker/volumes/", "docker/compose/", "docker/compose-files/", "collectors/", "docker/daemon.json"} {
		if hasFilesUnder(files, prefix) {
			t.Errorf("%s must not be exported", prefix)
		}
	}

	sum := files["SUMMARY.md"]
	for _, want := range []string{"## Containers", "## Mounts", "- Containers: 2"} {
		if !strings.Contains(sum, want) {
			t.Errorf("SUMMARY.md lacks %q", want)
		}
	}
	for _, unwanted := range []string{"## Images", "## Networks", "## Volumes", "## Compose", "## Docker daemon", "## Host", "- Images:", "- Networks:", "- Volumes:", "No Docker data"} {
		if strings.Contains(sum, unwanted) {
			t.Errorf("SUMMARY.md must not contain %q:\n%s", unwanted, sum)
		}
	}
	if !strings.Contains(files["README.md"], "Partial export") || !strings.Contains(files["README.md"], "containers") {
		t.Error("README.md does not say the export is partial")
	}

	var m manifest
	if err := json.Unmarshal([]byte(files["manifest.json"]), &m); err != nil {
		t.Fatal(err)
	}
	if !m.Partial || !reflect.DeepEqual(m.Categories, []string{"containers"}) || !reflect.DeepEqual(m.Counts, map[string]int{"containers": 2}) {
		t.Errorf("manifest = %+v", m)
	}

	var rep struct {
		Results map[string]map[string]any `json:"results"`
	}
	if err := json.Unmarshal([]byte(files["report.json"]), &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) != 1 || len(rep.Results["docker"]) != 1 || rep.Results["docker"]["containers"] == nil {
		t.Errorf("report.json results = %v, want only docker.containers", rep.Results)
	}
}

func TestWriteOnly_ExcludedDataAppearsNowhere(t *testing.T) {
	files := buildZipOnly(t, sampleReport(), []string{CategoryContainers})

	// "web01" is the host name, "secret-app:1.0" an image tag, "pgdata" a volume,
	// "registry.corp" a registry of the daemon: none of them is in a selected category.
	for name, body := range files {
		for _, excluded := range []string{"web01", "secret-app:1.0", "pgdata-only-in-volumes", "registry.corp", "Ubuntu 24.04"} {
			if strings.Contains(body, excluded) {
				t.Errorf("%s contains %q although its category was not selected", name, excluded)
			}
		}
	}
}

func TestWriteOnly_HostAndDaemon(t *testing.T) {
	files := buildZipOnly(t, sampleReport(), []string{CategoryHost, CategoryDaemon})

	for _, want := range []string{"collectors/host.json", "docker/daemon.json"} {
		if _, ok := files[want]; !ok {
			t.Errorf("missing %s", want)
		}
	}
	if hasFilesUnder(files, "docker/containers/") || hasFilesUnder(files, "docker/compose") {
		t.Error("container or compose files exported")
	}
	sum := files["SUMMARY.md"]
	for _, want := range []string{"## Host", "## Docker daemon", "# Server summary: web01"} {
		if !strings.Contains(sum, want) {
			t.Errorf("SUMMARY.md lacks %q", want)
		}
	}
	if strings.Contains(sum, "## Docker at a glance") || strings.Contains(sum, "No Docker data") {
		t.Errorf("SUMMARY.md has an empty or misleading Docker overview:\n%s", sum)
	}
}

func TestWriteOnly_HostOnlyDoesNotClaimThereIsNoDocker(t *testing.T) {
	sum := buildZipOnly(t, sampleReport(), []string{CategoryHost})["SUMMARY.md"]
	if strings.Contains(sum, "No Docker data") {
		t.Error("a host-only export must not say that Docker data is missing")
	}
}

func TestWriteOnly_ComposeKeepsItsFiles(t *testing.T) {
	files := buildZipOnly(t, sampleReport(), []string{CategoryCompose})

	if _, ok := files["docker/compose-files/app/docker-compose.yml"]; !ok {
		t.Error("sanitized compose file missing")
	}
	if hasFilesUnder(files, "docker/containers/") {
		t.Error("containers exported")
	}
	if strings.Contains(files["SUMMARY.md"], "Containers outside Compose") {
		t.Error("cannot know which containers are outside Compose without the containers")
	}
}

func TestWriteOnly_FullSelectionEqualsWrite(t *testing.T) {
	var full, all bytes.Buffer
	if err := Write(&full, sampleReport()); err != nil {
		t.Fatal(err)
	}
	if err := WriteOnly(&all, sampleReport(), Categories); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(full.Bytes(), all.Bytes()) {
		t.Error("selecting every category must give the same archive as Write")
	}

	var m manifest
	if err := json.Unmarshal([]byte(readZip(t, full.Bytes())["manifest.json"]), &m); err != nil {
		t.Fatal(err)
	}
	if m.Partial || !reflect.DeepEqual(m.Categories, Categories) {
		t.Errorf("manifest of a full export = %+v", m)
	}
}

func TestWriteOnly_InvalidSelectionWritesNothing(t *testing.T) {
	for name, sel := range map[string][]string{"empty": {}, "unknown": {"containers", "kubernetes"}} {
		var buf bytes.Buffer
		err := WriteOnly(&buf, sampleReport(), sel)
		if err == nil {
			t.Errorf("%s: expected an error", name)
		}
		if buf.Len() != 0 {
			t.Errorf("%s: %d bytes written before the error", name, buf.Len())
		}
	}
	if err := WriteOnly(&bytes.Buffer{}, sampleReport(), []string{}); !errors.Is(err, ErrNoCategory) {
		t.Errorf("empty selection: err = %v, want ErrNoCategory", err)
	}
}

func TestFileNameFor(t *testing.T) {
	r := sampleReport()
	tests := []struct {
		sel  []string
		want string
	}{
		{nil, "infra-probe-web01-20261103T100000Z.zip"},
		{Categories, "infra-probe-web01-20261103T100000Z.zip"},
		{[]string{"images", "containers"}, "infra-probe-web01-20261103T100000Z-containers-images.zip"},
		{[]string{"host", "daemon", "compose", "containers"}, "infra-probe-web01-20261103T100000Z-partial.zip"},
	}
	for _, tt := range tests {
		if got := FileNameFor(r, tt.sel); got != tt.want {
			t.Errorf("FileNameFor(%v) = %q, want %q", tt.sel, got, tt.want)
		}
	}
}
