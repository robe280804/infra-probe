// Package report runs collectors and assembles their output.
package report

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/netseven/infra-probe/internal/collector"
	"github.com/netseven/infra-probe/internal/redact"
)

// Report is the output of a scan: what every collector returned, and what
// went wrong.
type Report struct {
	Version     string            `json:"version"`
	GeneratedAt time.Time         `json:"generated_at"`
	Results     map[string]any    `json:"results"`
	Errors      map[string]string `json:"errors,omitempty"`
}

// Build runs every collector. A failing collector is recorded in Errors and
// does not stop the others. A collector may return partial data together with
// an error: both are kept, so a failure never hides what was collected.
func Build(ctx context.Context, version string, collectors []collector.Collector) *Report {
	r := &Report{
		Version:     version,
		GeneratedAt: time.Now().UTC(),
		Results:     make(map[string]any),
		Errors:      make(map[string]string),
	}
	for _, c := range collectors {
		data, err := c.Collect(ctx)
		if err != nil {
			r.Errors[c.Name()] = err.Error()
		}
		if data != nil {
			r.Results[c.Name()] = data
		}
	}
	return r
}

// WriteJSON writes the report as indented JSON, with secrets removed.
//
// Collectors redact what they know to be secret (environment values), but the
// report also carries raw inspect payloads: command lines, labels, options. So
// it goes through redact.Tree on the way out, whatever the collector returned.
// Everything that serializes a report (scan, the dashboard API) uses this
// method, so none of them can forget it.
func (r *Report) WriteJSON(w io.Writer) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // keep integers exact: byte counters can exceed 2^53
	var generic any
	if err := dec.Decode(&generic); err != nil {
		return err
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false) // keep "<redacted>" readable
	return enc.Encode(redact.Tree(generic))
}
