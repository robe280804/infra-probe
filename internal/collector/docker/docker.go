// Package docker collects information about the containers managed by the
// local Docker daemon.
package docker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// Container holds the facts about a single container: a flat summary for quick
// reading plus the complete inspect payload as returned by the Docker daemon.
type Container struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Image     string    `json:"image"`
	State     string    `json:"state"`  // running, exited, paused...
	Status    string    `json:"status"` // human readable, e.g. "Up 4 days"
	CreatedAt time.Time `json:"created_at"`

	// Inspect is the full configuration of the container (HostConfig, Config,
	// Mounts, NetworkSettings, State, GraphDriver...). Environment variable
	// values are redacted, see redactEnv.
	Inspect *container.InspectResponse `json:"inspect,omitempty"`

	// InspectError is set when the inspect call failed for this container
	// (for example it was removed right after the list call).
	InspectError string `json:"inspect_error,omitempty"`
}

// Result is what the collector returns to the report.
type Result struct {
	Containers []Container `json:"containers"`
}

type Collector struct{}

func New() *Collector { return &Collector{} }

func (c *Collector) Name() string { return "docker" }

// Collect lists every container (running or not) known to the daemon.
// The daemon address comes from the environment (DOCKER_HOST), falling back
// to the default local socket.
func (c *Collector) Collect(ctx context.Context) (any, error) {
	apiClient, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	defer apiClient.Close()

	list, err := apiClient.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	result := Result{Containers: make([]Container, 0, len(list.Items))}
	for _, item := range list.Items {
		entry := Container{
			ID:        shortID(item.ID),
			Name:      firstName(item.Names),
			Image:     item.Image,
			State:     string(item.State),
			Status:    item.Status,
			CreatedAt: time.Unix(item.Created, 0).UTC(),
		}

		// A single failing inspect must not abort the whole collection.
		inspected, err := apiClient.ContainerInspect(ctx, item.ID, client.ContainerInspectOptions{})
		if err != nil {
			entry.InspectError = err.Error()
		} else {
			resp := inspected.Container
			resp.Config = redactConfig(resp.Config)
			entry.Inspect = &resp
		}

		result.Containers = append(result.Containers, entry)
	}
	return result, nil
}

// redactConfig returns a copy of cfg whose environment values are masked.
// Env vars often hold secrets (passwords, tokens), and the report may be
// stored or shared, so only the variable names are kept.
func redactConfig(cfg *container.Config) *container.Config {
	if cfg == nil {
		return nil
	}
	masked := *cfg // shallow copy: do not mutate the SDK response
	masked.Env = redactEnv(cfg.Env)
	return &masked
}

// redactEnv turns "KEY=value" entries into "KEY=<redacted>".
func redactEnv(env []string) []string {
	if env == nil {
		return nil
	}
	out := make([]string, len(env))
	for i, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		out[i] = key + "=<redacted>"
	}
	return out
}

// shortID returns the 12-character form of a container ID, like the Docker CLI.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// firstName returns the primary container name without the leading slash
// that the Docker API adds.
func firstName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}
