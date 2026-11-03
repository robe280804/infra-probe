// Package report runs collectors and assembles their output.
package report

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/netseven/infra-probe/internal/collector"
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

// WriteJSON writes the report as indented JSON.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}
