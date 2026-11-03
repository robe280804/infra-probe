// Package collector defines the contract every data source must implement.
package collector

import "context"

// Collector gathers one category of information (host, docker, ...).
// The returned value must be JSON-serializable.
//
// Errors are never swallowed. When only part of the collection failed, Collect
// returns the data it did gather together with a non-nil error describing
// every failure; the report keeps both. Return nil data only when nothing
// could be collected.
type Collector interface {
	Name() string
	Collect(ctx context.Context) (any, error)
}
