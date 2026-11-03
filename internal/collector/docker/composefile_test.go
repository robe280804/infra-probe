package docker

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

const sampleCompose = `
name: app
x-env: &env
  API_TOKEN: abc123
  REGION: eu
services:
  web:
    image: ${REGISTRY:-docker.io}/web:1
    environment:
      DB_PASSWORD: hunter2
      DB_USER: ${DB_USER}
      DB_PASS_DEFAULT: ${DB_PASS:-fallback}
      DEBUG: true
      PASSTHROUGH:
      lower_case: litvalue
    command: ["run", "--password", "p4ss", "--port=80"]
    env_file: [.env, secrets.env]
    labels:
      - "traefik.http.middlewares.auth.basicauth.users=admin:apr1hash"
      - "traefik.enable=true"
    build:
      context: .
      args:
        NPM_TOKEN: npmsecret
        VERSION: "1"
    ports: ["8080:80"]
    healthcheck:
      test: ["CMD", "curl", "http://user:urlpw@localhost"]
  password-manager:
    image: vault:1
    environment:
      - MODE=prod
      - TOKEN_FILE=/run/secrets/t
      - FROM_ENV
      - REF=${FROM_HOST}
    secrets: [db_pass]
    entrypoint: sh -c "mysql --password=mysecret"
  worker:
    image: worker:1
    environment: *env
    api_key: ${WORKER_KEY}
    token: inline-token
secrets:
  db_pass:
    file: ./secrets/db_pass.txt
configs:
  site:
    content: "password=zzzconfig"
`

func parseSample(t *testing.T) (map[string]any, string) {
	t.Helper()
	content, err := parseComposeFile([]byte(sampleCompose))
	if err != nil {
		t.Fatalf("parseComposeFile: %v", err)
	}
	raw, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("content is not JSON serializable: %v", err)
	}
	return content, string(raw)
}

func TestParseComposeFile_NoSecretLeaks(t *testing.T) {
	_, raw := parseSample(t)

	for _, secret := range []string{"hunter2", "abc123", "fallback", "p4ss", "npmsecret", "mysecret", "urlpw", "apr1hash", "zzzconfig", "litvalue", "inline-token", "prod", `"eu"`} {
		if strings.Contains(raw, secret) {
			t.Errorf("output leaks %q:\n%s", secret, raw)
		}
	}
}

func TestParseComposeFile_KeepsStructure(t *testing.T) {
	content, raw := parseSample(t)

	for _, keep := range []string{
		`"DB_PASSWORD":"\u003credacted\u003e"`, // names kept, value masked
		`"DB_USER":"${DB_USER}"`,               // references are names, not values
		`"DB_PASS_DEFAULT":"${DB_PASS:-\u003credacted\u003e}"`,
		`"REF=${FROM_HOST}"`,
		`"FROM_ENV"`,
		`"env_file":[".env","secrets.env"]`, // paths only
		`"secrets":["db_pass"]`,
		`"traefik.enable=true"`,
		`"ports":["8080:80"]`,
		`"--port=80"`,
		`"image":"${REGISTRY:-docker.io}/web:1"`, // defaults outside env stay readable
		`"VERSION":"\u003credacted\u003e"`,
		`"file":"./secrets/db_pass.txt"`,
		`"api_key":"${WORKER_KEY}"`,
	} {
		if !strings.Contains(raw, keep) {
			t.Errorf("output lacks %s:\n%s", keep, raw)
		}
	}

	services := content["services"].(map[string]any)
	if _, ok := services["password-manager"].(map[string]any)["image"]; !ok {
		t.Error("a service named after a secret was masked as a whole")
	}
	env := services["web"].(map[string]any)["environment"].(map[string]any)
	if v, present := env["PASSTHROUGH"]; !present || v != nil {
		t.Errorf("pass-through variable = %v (present %v), want null", v, present)
	}
	if env["DEBUG"] != "<redacted>" {
		t.Errorf("DEBUG = %v, want masked: literals are never kept", env["DEBUG"])
	}
	// The anchor reused through a YAML alias is masked wherever it appears.
	if !reflect.DeepEqual(services["worker"].(map[string]any)["environment"], map[string]any{"API_TOKEN": "<redacted>", "REGION": "<redacted>"}) {
		t.Errorf("worker environment = %v", services["worker"].(map[string]any)["environment"])
	}
	if content["x-env"].(map[string]any)["API_TOKEN"] != "<redacted>" {
		t.Error("the x- extension field holding the anchor leaks")
	}
}

func TestParseComposeFile_Rejects(t *testing.T) {
	tests := []struct{ name, in string }{
		{"not yaml", "{{{ not: yaml: ["},
		{"empty", ""},
		{"list", "- a\n- b\n"},
		{"scalar", "just text"},
		{"no services", "db_password: hunter2\nversion: '3'\n"},
		{"services is not a mapping", "services: [a, b]\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, err := parseComposeFile([]byte(tt.in))
			if err == nil || content != nil {
				t.Errorf("got (%v, %v), want an error", content, err)
			}
			if err != nil && strings.Contains(err.Error(), "hunter2") {
				t.Error("error quotes the content")
			}
		})
	}
}

func TestParseComposeFile_AliasBombIsRejected(t *testing.T) {
	var b strings.Builder
	b.WriteString("services: {a: {image: x}}\nx0: &a0 [lol, lol, lol, lol, lol, lol, lol, lol, lol]\n")
	for i := 1; i < 12; i++ {
		b.WriteString("x" + string(rune('0'+i%10)) + string(rune('a'+i)) + ": &a" + string(rune('0'+i)) + " [")
		for j := 0; j < 9; j++ {
			if j > 0 {
				b.WriteString(", ")
			}
			b.WriteString("*a" + string(rune('0'+i-1)))
		}
		b.WriteString("]\n")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = parseComposeFile([]byte(b.String())) // an error or a result, but it must finish
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("parsing an alias bomb did not finish")
	}
}

func TestParseComposeFile_JSONSafeValues(t *testing.T) {
	content, err := parseComposeFile([]byte("services:\n  a:\n    image: x\n    cpus: .inf\n    n: .nan\n    when: 2024-01-02\n    bin: !!binary c2VjcmV0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := json.Marshal(content); err != nil {
		t.Errorf("not serializable: %v", err)
	}
}

func fileCollector(read func(string) ([]byte, time.Time, error)) *Collector {
	c := newTestCollector(&fakeAPI{}, nil)
	c.readComposeFile = read
	return c
}

func TestLoadComposeFile_Statuses(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "docker-compose.yml")
	mod := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	valid := []byte("services:\n  a:\n    image: x\n")

	tests := []struct {
		name   string
		path   string
		read   func(string) ([]byte, time.Time, error)
		status string
	}{
		{"ok", abs, func(string) ([]byte, time.Time, error) { return valid, mod, nil }, fileOK},
		{"relative path", "docker-compose.yml", nil, fileSkipped},
		{"wrong extension", filepath.Join(t.TempDir(), "passwd"), nil, fileInvalid},
		{"missing", abs, func(string) ([]byte, time.Time, error) { return nil, time.Time{}, fs.ErrNotExist }, fileMissing},
		{"remote daemon", abs, func(string) ([]byte, time.Time, error) { return nil, time.Time{}, errNotLocal }, fileSkipped},
		{"too large", abs, func(string) ([]byte, time.Time, error) { return nil, time.Time{}, errTooLarge }, fileInvalid},
		{"not regular", abs, func(string) ([]byte, time.Time, error) { return nil, time.Time{}, errNotRegular }, fileInvalid},
		{"permission", abs, func(string) ([]byte, time.Time, error) { return nil, time.Time{}, fs.ErrPermission }, fileUnreadable},
		{"not compose", abs, func(string) ([]byte, time.Time, error) { return []byte("a: b\n"), mod, nil }, fileInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			read := tt.read
			if read == nil {
				read = func(string) ([]byte, time.Time, error) {
					t.Error("file must not be read")
					return nil, time.Time{}, nil
				}
			}
			f := fileCollector(read).loadComposeFile(tt.path)
			if f.Status != tt.status {
				t.Errorf("status = %q (%s), want %q", f.Status, f.Error, tt.status)
			}
			if (f.Status == fileOK) != (f.Content != nil) {
				t.Errorf("content presence wrong for status %q", f.Status)
			}
		})
	}

	f := fileCollector(func(string) ([]byte, time.Time, error) { return valid, mod, nil }).loadComposeFile(abs)
	if f.ModifiedAt == nil || !f.ModifiedAt.Equal(mod) {
		t.Errorf("modified = %v, want %v", f.ModifiedAt, mod)
	}
}

func TestAttachComposeFiles_ErrorsAndDrift(t *testing.T) {
	dir := t.TempDir()
	okPath := filepath.Join(dir, "docker-compose.yml")
	badPath := filepath.Join(dir, "broken.yml")
	gonePath := filepath.Join(dir, "gone.yml")

	started := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	containers := []Container{
		{ID: "c1", CreatedAt: started},
		{ID: "c2", CreatedAt: started.Add(time.Hour)},
		{ID: "c3", CreatedAt: started.Add(-24 * time.Hour)}, // one-off: ignored for drift
	}
	project := func(name string, files ...string) ComposeProject {
		return ComposeProject{Name: name, ConfigFiles: files, Services: []ComposeService{{Name: "s", Containers: []ComposeContainer{
			{ID: "c1"}, {ID: "c2"}, {ID: "c3", OneOff: true},
		}}}}
	}

	readAt := func(mod time.Time) func(string) ([]byte, time.Time, error) {
		return func(path string) ([]byte, time.Time, error) {
			switch path {
			case okPath:
				return []byte("services: {a: {image: x}}"), mod, nil
			case badPath:
				return []byte("nope"), mod, nil
			}
			return nil, time.Time{}, fs.ErrNotExist
		}
	}

	t.Run("changed after start", func(t *testing.T) {
		projects := []ComposeProject{project("p", okPath)}
		errs := fileCollector(readAt(started.Add(30*time.Minute))).attachComposeFiles(projects, containers)
		if len(errs) != 0 {
			t.Errorf("errors = %v", errs)
		}
		if !projects[0].ConfigChangedSinceStart {
			t.Error("a file modified after the oldest container was created must be flagged")
		}
	})

	t.Run("unchanged", func(t *testing.T) {
		projects := []ComposeProject{project("p", okPath)}
		fileCollector(readAt(started.Add(-time.Hour))).attachComposeFiles(projects, containers)
		if projects[0].ConfigChangedSinceStart {
			t.Error("flagged although the file is older than the containers")
		}
	})

	t.Run("errors only for unreadable or invalid files", func(t *testing.T) {
		projects := []ComposeProject{project("p", okPath, badPath, gonePath)}
		errs := fileCollector(readAt(started)).attachComposeFiles(projects, containers)
		if len(errs) != 1 || !strings.Contains(errs[0].Error(), "compose project p: "+badPath) {
			t.Fatalf("errors = %v, want only the invalid file", errs)
		}
		statuses := []string{projects[0].Files[0].Status, projects[0].Files[1].Status, projects[0].Files[2].Status}
		if !reflect.DeepEqual(statuses, []string{fileOK, fileInvalid, fileMissing}) {
			t.Errorf("statuses = %v", statuses)
		}
	})

	t.Run("no containers", func(t *testing.T) {
		projects := []ComposeProject{{Name: "down", ConfigFiles: []string{okPath}}}
		fileCollector(readAt(started)).attachComposeFiles(projects, nil)
		if projects[0].ConfigChangedSinceStart || len(projects[0].Files) != 1 {
			t.Errorf("project = %+v", projects[0])
		}
	})
}

func TestReadLocalComposeFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, size int) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(strings.Repeat("a", size)), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("regular file", func(t *testing.T) {
		data, mod, err := readLocalComposeFile(write("ok.yml", 10))
		if err != nil || len(data) != 10 || mod.IsZero() {
			t.Errorf("got (%d bytes, %v, %v)", len(data), mod, err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		if _, _, err := readLocalComposeFile(filepath.Join(dir, "none.yml")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("directory", func(t *testing.T) {
		if _, _, err := readLocalComposeFile(dir); !errors.Is(err, errNotRegular) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("too large", func(t *testing.T) {
		if _, _, err := readLocalComposeFile(write("big.yml", maxComposeFileSize+1)); !errors.Is(err, errTooLarge) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("exactly at the limit", func(t *testing.T) {
		if _, _, err := readLocalComposeFile(write("limit.yml", maxComposeFileSize)); err != nil {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("remote daemon", func(t *testing.T) {
		t.Setenv("DOCKER_HOST", "tcp://10.0.0.1:2375")
		if _, _, err := readLocalComposeFile(write("remote.yml", 1)); !errors.Is(err, errNotLocal) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("symlink to a regular file is followed, to a directory refused", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks need privileges on Windows")
		}
		target := write("target.yml", 5)
		link := filepath.Join(dir, "link.yml")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readLocalComposeFile(link); err != nil {
			t.Errorf("symlink to file: %v", err)
		}
		dirLink := filepath.Join(dir, "dirlink.yml")
		if err := os.Symlink(dir, dirLink); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readLocalComposeFile(dirLink); !errors.Is(err, errNotRegular) {
			t.Errorf("symlink to directory: %v", err)
		}
	})
}
