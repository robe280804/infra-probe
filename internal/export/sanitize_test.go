package export

import (
	"reflect"
	"testing"
)

func TestSanitizeString(t *testing.T) {
	tests := []struct{ in, want string }{
		{"mysql --password=s3cret -h db", "mysql --password=<redacted> -h db"},
		{"API_KEY=abc run", "API_KEY=<redacted> run"},
		{`token: "a b c"`, "token: <redacted>"},
		{"redis://:pw@cache:6379", "redis://:<redacted>@cache:6379"},
		{"https://user:pw@example.com/x", "https://user:<redacted>@example.com/x"},
		// Not secrets.
		{"secret-app:1.0", "secret-app:1.0"},
		{"https://example.com:8443/path", "https://example.com:8443/path"},
		{"nginx -g daemon off;", "nginx -g daemon off;"},
	}
	for _, tt := range tests {
		if got := sanitizeString(tt.in); got != tt.want {
			t.Errorf("sanitizeString(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRedactArgs(t *testing.T) {
	in := []any{"app", "--db-password", "x1", "--port", "80", "--token=x2", "SECRET_KEY=x3"}
	want := []any{"app", "--db-password", Redacted, "--port", "80", "--token=" + Redacted, "SECRET_KEY=" + Redacted}
	if got := redactArgs(in); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSanitize_KeysAndNesting(t *testing.T) {
	in := map[string]any{
		"Env":          []any{"A=1", "NOVALUE"},
		"Labels":       map[string]any{"vault.token": "t", "app": "web"},
		"LogConfig":    map[string]any{"Config": map[string]any{"splunk-token": "t2", "tag": "x"}},
		"Password":     "", // empty values are left alone
		"Healthcheck":  map[string]any{"Test": []any{"CMD", "check", "--password", "p"}},
		"OtherNumbers": []any{1.0, true, nil},
	}
	want := map[string]any{
		"Env":          []any{"A=" + Redacted, "NOVALUE=" + Redacted},
		"Labels":       map[string]any{"vault.token": Redacted, "app": "web"},
		"LogConfig":    map[string]any{"Config": map[string]any{"splunk-token": Redacted, "tag": "x"}},
		"Password":     "",
		"Healthcheck":  map[string]any{"Test": []any{"CMD", "check", "--password", Redacted}},
		"OtherNumbers": []any{1.0, true, nil},
	}
	if got := sanitize(in); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}
