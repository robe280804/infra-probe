package redact

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestString_InlineSecrets(t *testing.T) {
	tests := []struct{ in, want string }{
		{"API_KEY=abc run", "API_KEY=<redacted> run"},
		{`token: "a b c"`, "token: <redacted>"},
		{"nginx -g daemon off;", "nginx -g daemon off;"},
	}
	for _, tt := range tests {
		if got := String(tt.in); got != tt.want {
			t.Errorf("String(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestArgs(t *testing.T) {
	in := []any{"app", "--db-password", "x1", "--port", "80", "--token=x2", "SECRET_KEY=x3"}
	want := []any{"app", "--db-password", Mask, "--port", "80", "--token=" + Mask, "SECRET_KEY=" + Mask}
	if got := args(in); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTree_KeysAndNesting(t *testing.T) {
	in := map[string]any{
		"Env":          []any{"A=1", "NOVALUE"},
		"Labels":       map[string]any{"vault.token": "t", "app": "web"},
		"LogConfig":    map[string]any{"Config": map[string]any{"splunk-token": "t2", "tag": "x"}},
		"Password":     "", // empty values are left alone
		"Healthcheck":  map[string]any{"Test": []any{"CMD", "check", "--password", "p"}},
		"OtherNumbers": []any{1.0, true, nil},
	}
	want := map[string]any{
		"Env":          []any{"A=" + Mask, "NOVALUE=" + Mask},
		"Labels":       map[string]any{"vault.token": Mask, "app": "web"},
		"LogConfig":    map[string]any{"Config": map[string]any{"splunk-token": Mask, "tag": "x"}},
		"Password":     "",
		"Healthcheck":  map[string]any{"Test": []any{"CMD", "check", "--password", Mask}},
		"OtherNumbers": []any{1.0, true, nil},
	}
	if got := Tree(in); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}

func TestTree_DoesNotModifyItsInput(t *testing.T) {
	in := map[string]any{"Env": []any{"A=secret"}, "Labels": map[string]any{"api_key": "k"}}
	Tree(in)
	if in["Env"].([]any)[0] != "A=secret" || in["Labels"].(map[string]any)["api_key"] != "k" {
		t.Errorf("input was modified: %v", in)
	}
}

func TestTree_KeepsJSONNumbersExact(t *testing.T) {
	// A decoder with UseNumber keeps integers beyond 2^53 exact; Tree must pass them through.
	dec := json.NewDecoder(strings.NewReader(`{"bytes": 9007199254740993}`))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	got := Tree(v).(map[string]any)["bytes"]
	if n, ok := got.(json.Number); !ok || n.String() != "9007199254740993" {
		t.Errorf("number = %#v", got)
	}
}
