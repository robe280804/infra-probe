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
