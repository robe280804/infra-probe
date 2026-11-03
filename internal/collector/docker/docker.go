// Package docker collects information about the containers managed by the
// local Docker daemon.
package docker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/moby/moby/client"
)

// Container holds the basic, read-only facts about a single container.
// It is intentionally small: richer data (mounts, capabilities, networks...)
// will come from ContainerInspect in a later step.
type Container struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Image     string    `json:"image"`
	State     string    `json:"state"`  // running, exited, paused...
	Status    string    `json:"status"` // human readable, e.g. "Up 4 days"
	CreatedAt time.Time `json:"created_at"`
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
		result.Containers = append(result.Containers, Container{
			ID:        shortID(item.ID),
			Name:      firstName(item.Names),
			Image:     item.Image,
			State:     string(item.State),
			Status:    item.Status,
			CreatedAt: time.Unix(item.Created, 0).UTC(),
		})
	}
	return result, nil
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
