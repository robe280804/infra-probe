//go:build integration

package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The probe promises that secrets never leave the host. These tests check it
// end to end: a real container and a real compose file hold secrets, the
// compiled binary produces its outputs, and no output may contain any of them.
//
// The secrets are generated at run time, so they cannot be mistaken for real
// credentials by a scanner, and a leak cannot be a coincidence.

// fixture is a running container that looks like part of a Compose project,
// plus the secrets hidden in it.
type fixture struct {
	container string
	project   string

	// Secrets that must appear in no output, by where they are hidden.
	envValue     string // container environment
	cmdFlag      string // --token=... in the container command
	labelValue   string // label with a secret-looking name
	composeEnv   string // environment of a service in the compose file
	composeAnch  string // environment shared through a YAML anchor
	composeFlag  string // --password=... in a compose command
	composeURL   string // password in a URL of a compose command
	composeBuild string // build argument
}

// secrets maps where each secret is hidden to its value, so that a failure
// says which place leaks.
func (f fixture) secrets() map[string]string {
	return map[string]string{
		"container environment":        f.envValue,
		"container command (--token)":  f.cmdFlag,
		"label with a secret name":     f.labelValue,
		"compose service environment":  f.composeEnv,
		"compose environment (anchor)": f.composeAnch,
		"compose command (--password)": f.composeFlag,
		"compose command (URL)":        f.composeURL,
		"compose build argument":       f.composeBuild,
	}
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// dockerCLI runs the docker CLI.
func dockerCLI(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	return cmd.CombinedOutput()
}

// startFixture starts a container labelled as a Compose project whose compose
// file is written to a temp dir. The Compose labels are set by hand, so the
// docker compose plugin is not needed.
func startFixture(t *testing.T) fixture {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	out, err := dockerCLI(ctx, "info", "--format", "{{.ServerVersion}}")
	requireDocker(t, err)
	t.Logf("docker %s", strings.TrimSpace(string(out)))

	id := randomHex(t, 4)
	f := fixture{
		container:    "infra-probe-it-" + id,
		project:      "infra-probe-it-" + id,
		envValue:     "itsecret" + randomHex(t, 8),
		cmdFlag:      "itsecret" + randomHex(t, 8),
		labelValue:   "itsecret" + randomHex(t, 8),
		composeEnv:   "itsecret" + randomHex(t, 8),
		composeAnch:  "itsecret" + randomHex(t, 8),
		composeFlag:  "itsecret" + randomHex(t, 8),
		composeURL:   "itsecret" + randomHex(t, 8),
		composeBuild: "itsecret" + randomHex(t, 8),
	}

	dir := t.TempDir()
	composePath := filepath.Join(dir, "docker-compose.yml")
	compose := fmt.Sprintf(`name: %s
x-env: &env
  API_TOKEN: %s
services:
  app:
    image: alpine:3
    environment:
      DB_PASSWORD: %s
      DB_USER: ${DB_USER}
    command: ["sh", "-c", "mysql --password=%s postgres://admin:%s@db/app"]
    build:
      context: .
      args:
        NPM_TOKEN: %s
  worker:
    image: alpine:3
    environment: *env
`, f.project, f.composeAnch, f.composeEnv, f.composeFlag, f.composeURL, f.composeBuild)
	if err := os.WriteFile(composePath, []byte(compose), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = dockerCLI(ctx, "rm", "-f", f.container)
	})

	// ": --token=..." is a no-op for the shell, but it is part of the command
	// line that docker inspect reports.
	out, err = dockerCLI(ctx, "run", "-d", "--name", f.container,
		"-e", "DB_PASSWORD="+f.envValue,
		"-e", "PLAIN_SETTING=visible",
		"-l", "com.example.api_key="+f.labelValue,
		"-l", "com.example.team=infra",
		"-l", "com.docker.compose.project="+f.project,
		"-l", "com.docker.compose.service=app",
		"-l", "com.docker.compose.container-number=1",
		"-l", "com.docker.compose.project.working_dir="+dir,
		"-l", "com.docker.compose.project.config_files="+composePath,
		"alpine:3", "sh", "-c", ": --token="+f.cmdFlag+"; sleep 300")
	if err != nil {
		t.Fatalf("docker run failed: %v\n%s", err, out)
	}
	return f
}

func assertNoSecrets(t *testing.T, where string, data []byte, secrets map[string]string) {
	t.Helper()
	for place, value := range secrets {
		if bytes.Contains(data, []byte(value)) {
			t.Errorf("%s leaks the secret hidden in the %s", where, place)
		}
	}
}

func readZip(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("not a valid zip: %v", err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = string(body)
	}
	return files
}

// runExport runs `infra-probe export` and returns the archive entries. Exit
// code 1 (a collector reported a problem, e.g. an unreadable container) is
// fine: the archive is written anyway. Anything else is a failure.
func runExport(t *testing.T, bin string, args ...string) map[string]string {
	t.Helper()

	out := filepath.Join(t.TempDir(), "out.zip")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, append([]string{"export", "-o", out, "-timeout", "60s"}, args...)...)
	output, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if err != nil && (!errors.As(err, &exitErr) || exitErr.ExitCode() != 1) {
		t.Fatalf("export failed: %v\n%s", err, output)
	}
	data, readErr := os.ReadFile(out)
	if readErr != nil {
		t.Fatalf("export wrote no archive: %v\n%s", readErr, output)
	}
	return readZip(t, data)
}

func TestBinary_ExportNeverLeaksSecrets(t *testing.T) {
	f := startFixture(t)
	bin := buildBinary(t)

	files := runExport(t, bin)

	// The test is only meaningful if the fixture really is in the archive.
	containerFile, ok := files["docker/containers/"+f.container+".json"]
	if !ok {
		t.Fatalf("the fixture container is not in the export (files: %d)", len(files))
	}
	composeJSON, ok := files["docker/compose/"+f.project+".json"]
	if !ok {
		t.Fatal("the fixture compose project is not in the export")
	}
	composeYAML, ok := files["docker/compose-files/"+f.project+"/docker-compose.yml"]
	if !ok {
		t.Fatalf("the compose file was not read and exported (project json: %s)", composeJSON)
	}

	for name, body := range files {
		assertNoSecrets(t, name, []byte(body), f.secrets())
	}

	// What is useful is kept: names, structure, non-secret values.
	for _, want := range []string{"DB_PASSWORD=<redacted>", "PLAIN_SETTING=<redacted>", "com.example.team", "infra"} {
		if !strings.Contains(containerFile, want) {
			t.Errorf("container file lacks %q", want)
		}
	}
	for _, want := range []string{"DB_PASSWORD: <redacted>", "DB_USER: ${DB_USER}", "API_TOKEN: <redacted>", "image: alpine:3"} {
		if !strings.Contains(composeYAML, want) {
			t.Errorf("compose file lacks %q:\n%s", want, composeYAML)
		}
	}
	if !strings.Contains(composeJSON, `"status": "ok"`) {
		t.Errorf("compose file status is not ok:\n%s", composeJSON)
	}
}

func TestBinary_PartialExportNeverLeaksSecrets(t *testing.T) {
	f := startFixture(t)
	bin := buildBinary(t)

	files := runExport(t, bin, "-include", "containers,compose")

	if _, ok := files["docker/containers/"+f.container+".json"]; !ok {
		t.Fatal("the fixture container is not in the export")
	}
	for name, body := range files {
		assertNoSecrets(t, name, []byte(body), f.secrets())
		for _, excluded := range []string{"docker/images/", "docker/networks/", "docker/volumes/", "docker/daemon.json", "collectors/"} {
			if strings.HasPrefix(name, excluded) {
				t.Errorf("%s exported although its category was not selected", name)
			}
		}
	}
}

func TestBinary_ScanNeverLeaksSecrets(t *testing.T) {
	f := startFixture(t)
	bin := buildBinary(t)

	// The report is what the dashboard API serves: it must be as clean as the
	// export. Exit code 1 only means a collector reported a problem.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, "scan", "--timeout", "60s")
	cmd.Stdout = &stdout
	_ = cmd.Run()

	if !bytes.Contains(stdout.Bytes(), []byte(f.container)) {
		t.Fatal("the fixture container is not in the report")
	}
	assertNoSecrets(t, "scan output", stdout.Bytes(), f.secrets())
}

// freePort returns a TCP port nobody listens on.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

func httpGet(ctx context.Context, url string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

func TestBinary_ServeNeverLeaksSecrets(t *testing.T) {
	f := startFixture(t)
	bin := buildBinary(t)

	addr := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := exec.CommandContext(ctx, bin, "serve", "-addr", addr, "-scan-timeout", "60s")
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = srv.Wait()
	})

	base := "http://" + addr
	deadline := time.Now().Add(30 * time.Second)
	for {
		reqCtx, reqCancel := context.WithTimeout(ctx, 2*time.Second)
		code, _, err := httpGet(reqCtx, base+"/healthz")
		reqCancel()
		if err == nil && code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server did not come up on %s: %v", addr, err)
		}
		time.Sleep(200 * time.Millisecond)
	}

	reqCtx, reqCancel := context.WithTimeout(ctx, 90*time.Second)
	defer reqCancel()

	code, report, err := httpGet(reqCtx, base+"/api/report")
	if err != nil || code != http.StatusOK {
		t.Fatalf("/api/report: %d %v", code, err)
	}
	if !bytes.Contains(report, []byte(f.container)) {
		t.Fatal("the fixture container is not in /api/report")
	}
	assertNoSecrets(t, "/api/report", report, f.secrets())

	code, zipped, err := httpGet(reqCtx, base+"/api/export?include=containers,compose")
	if err != nil || code != http.StatusOK {
		t.Fatalf("/api/export: %d %v", code, err)
	}
	files := readZip(t, zipped)
	if _, ok := files["docker/containers/"+f.container+".json"]; !ok {
		t.Fatal("the fixture container is not in /api/export")
	}
	for name, body := range files {
		assertNoSecrets(t, "/api/export "+name, []byte(body), f.secrets())
	}

	if code, _, _ := httpGet(reqCtx, base+"/api/export?include=nope"); code != http.StatusBadRequest {
		t.Errorf("an unknown category: status = %d, want 400", code)
	}
}
