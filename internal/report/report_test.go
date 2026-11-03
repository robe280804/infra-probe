package report

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/netseven/infra-probe/internal/collector"
)

// stubCollector is a collector with a canned outcome.
type stubCollector struct {
	name string
	data any
	err  error
}

func (s stubCollector) Name() string { return s.name }

func (s stubCollector) Collect(context.Context) (any, error) { return s.data, s.err }

var _ collector.Collector = stubCollector{}

func TestBuild_AllSucceed(t *testing.T) {
	r := Build(context.Background(), "1.2.3", []collector.Collector{
		stubCollector{name: "a", data: 1},
		stubCollector{name: "b", data: "two"},
	})

	if r.Version != "1.2.3" {
		t.Errorf("Version = %q, want %q", r.Version, "1.2.3")
	}
	if r.GeneratedAt.IsZero() {
		t.Error("GeneratedAt is zero")
	}
	if r.Results["a"] != 1 || r.Results["b"] != "two" {
		t.Errorf("Results = %v", r.Results)
	}
	if len(r.Errors) != 0 {
		t.Errorf("Errors = %v, want none", r.Errors)
	}
}

func TestBuild_FailureDoesNotStopOthers(t *testing.T) {
	r := Build(context.Background(), "dev", []collector.Collector{
		stubCollector{name: "bad", err: errors.New("boom")},
		stubCollector{name: "good", data: "ok"},
	})

	if got := r.Errors["bad"]; got != "boom" {
		t.Errorf("Errors[bad] = %q, want %q", got, "boom")
	}
	if _, present := r.Results["bad"]; present {
		t.Error("failed collector must not appear in Results")
	}
	if r.Results["good"] != "ok" {
		t.Errorf("healthy collector lost: Results = %v", r.Results)
	}
}

func TestBuild_PartialDataIsKeptWithTheError(t *testing.T) {
	// A collector that gathered something before failing must not lose it,
	// and its error must still be reported.
	r := Build(context.Background(), "dev", []collector.Collector{
		stubCollector{name: "partial", data: "some", err: errors.New("one part failed")},
	})

	if r.Results["partial"] != "some" {
		t.Errorf("partial data dropped: Results = %v", r.Results)
	}
	if r.Errors["partial"] != "one part failed" {
		t.Errorf("error not reported: Errors = %v", r.Errors)
	}
}

func TestBuild_NoCollectors(t *testing.T) {
	r := Build(context.Background(), "dev", nil)
	if r.Results == nil || len(r.Results) != 0 {
		t.Errorf("Results = %v, want empty non-nil map", r.Results)
	}
}

func TestWriteJSON(t *testing.T) {
	r := Build(context.Background(), "dev", []collector.Collector{
		stubCollector{name: "ok", data: map[string]int{"n": 7}},
		stubCollector{name: "bad", err: errors.New("boom")},
	})

	var buf bytes.Buffer
	if err := r.WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}

	var decoded struct {
		Version     string                    `json:"version"`
		GeneratedAt string                    `json:"generated_at"`
		Results     map[string]map[string]int `json:"results"`
		Errors      map[string]string         `json:"errors"`
	}
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if decoded.Version != "dev" || decoded.GeneratedAt == "" {
		t.Errorf("unexpected header: %+v", decoded)
	}
	if decoded.Results["ok"]["n"] != 7 {
		t.Errorf("Results = %v", decoded.Results)
	}
	if decoded.Errors["bad"] != "boom" {
		t.Errorf("Errors = %v", decoded.Errors)
	}
}

func TestWriteJSON_OmitsEmptyErrors(t *testing.T) {
	r := Build(context.Background(), "dev", []collector.Collector{
		stubCollector{name: "ok", data: 1},
	})

	var buf bytes.Buffer
	if err := r.WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `"errors"`) {
		t.Errorf("empty errors should be omitted:\n%s", buf.String())
	}
}

func TestWriteJSON_UnserializableDataReturnsError(t *testing.T) {
	r := Build(context.Background(), "dev", []collector.Collector{
		stubCollector{name: "bad", data: make(chan int)}, // channels cannot be encoded
	})
	if err := r.WriteJSON(&bytes.Buffer{}); err == nil {
		t.Error("WriteJSON() error = nil, want an encoding error")
	}
}

// inspectLike mimics the raw payloads collectors return: a command line, labels
// and options that only the generic redaction can recognize.
type inspectLike struct {
	Config struct {
		Env    []string          `json:"Env"`
		Cmd    []string          `json:"Cmd"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	Counter uint64 `json:"counter"`
}

func TestWriteJSON_RedactsWhatCollectorsLeaveRaw(t *testing.T) {
	var payload inspectLike
	payload.Config.Env = []string{"DB_PASSWORD=envsecret", "MODE=prod"}
	payload.Config.Cmd = []string{"app", "--token=cmdsecret", "--api-key", "nextsecret", "--port=80"}
	payload.Config.Labels = map[string]string{"com.example.api_key": "labelsecret", "com.example.team": "infra"}
	payload.Counter = 9007199254740993 // 2^53+1: a float64 cannot hold it

	r := &Report{Version: "t", Results: map[string]any{"docker": payload},
		Errors: map[string]string{"docker": "dial postgres://admin:errsecret@db failed"}}

	var buf bytes.Buffer
	if err := r.WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	out := buf.String()

	for _, secret := range []string{"envsecret", "cmdsecret", "nextsecret", "labelsecret", "errsecret"} {
		if strings.Contains(out, secret) {
			t.Errorf("output leaks %q:\n%s", secret, out)
		}
	}
	for _, keep := range []string{"DB_PASSWORD=<redacted>", "MODE=<redacted>", "--token=<redacted>", "--port=80", "com.example.team", "infra"} {
		if !strings.Contains(out, keep) {
			t.Errorf("output lacks %q:\n%s", keep, out)
		}
	}
	if !strings.Contains(out, "9007199254740993") {
		t.Errorf("a large integer lost precision:\n%s", out)
	}
	if !json.Valid(buf.Bytes()) {
		t.Error("output is not valid JSON")
	}
}
