// Package collector defines the contract every data source must implement.
package collector

import "context"

// Collector gathers one category of information (host, docker, ...).
// The returned value must be JSON-serializable.
type Collector interface {
	Name() string
	Collect(ctx context.Context) (any, error)
}
