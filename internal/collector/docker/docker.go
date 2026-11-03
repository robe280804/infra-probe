// Package docker collects information about the containers and images managed
// by the local Docker daemon.
package docker

import (
	"context"
	"fmt"
	"strings"
	"time"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
)

// dockerImageConfig is the type of image.InspectResponse.Config.
type dockerImageConfig = dockerspec.DockerOCIImageConfig

// Container holds the facts about a single container: a flat summary for quick
// reading plus the complete inspect payload as returned by the Docker daemon.
type Container struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Image     string    `json:"image"`    // reference used at creation, e.g. "nginx:1.27"
	ImageID   string    `json:"image_id"` // join key with Image.ID
	State     string    `json:"state"`    // running, exited, paused...
	Status    string    `json:"status"`   // human readable, e.g. "Up 4 days"
	CreatedAt time.Time `json:"created_at"`

	// Inspect is the full configuration of the container (HostConfig, Config,
	// Mounts, NetworkSettings, State, GraphDriver...). Environment variable
	// values are redacted, see redactEnv.
	Inspect *container.InspectResponse `json:"inspect,omitempty"`

	// InspectError is set when the inspect call failed for this container
	// (for example it was removed right after the list call).
	InspectError string `json:"inspect_error,omitempty"`
}

// Image holds the facts about a single local image.
type Image struct {
	ID          string    `json:"id"` // full "sha256:..." ID, join key with Container.ImageID
	RepoTags    []string  `json:"repo_tags"`
	RepoDigests []string  `json:"repo_digests"`
	CreatedAt   time.Time `json:"created_at"`
	Size        int64     `json:"size"`

	// Containers is the number of containers (any state) created from this
	// image. An image with 0 is unused.
	Containers int64 `json:"containers"`

	// Dangling is true for untagged images (no usable repository tag).
	Dangling bool `json:"dangling"`

	// Inspect is the full image metadata (Config, RootFS layers, Os, Arch,
	// GraphDriver...). Environment variable values are redacted.
	Inspect *image.InspectResponse `json:"inspect,omitempty"`

	// InspectError is set when the inspect call failed for this image.
	InspectError string `json:"inspect_error,omitempty"`
}

// Result is what the collector returns to the report.
type Result struct {
	Containers []Container `json:"containers"`
	Images     []Image     `json:"images"`
}

// dockerAPI is the subset of the Docker client used by the collector.
// *client.Client satisfies it; tests provide a fake.
type dockerAPI interface {
	ContainerList(ctx context.Context, options client.ContainerListOptions) (client.ContainerListResult, error)
	ContainerInspect(ctx context.Context, containerID string, options client.ContainerInspectOptions) (client.ContainerInspectResult, error)
	ImageList(ctx context.Context, options client.ImageListOptions) (client.ImageListResult, error)
	ImageInspect(ctx context.Context, imageID string, opts ...client.ImageInspectOption) (client.ImageInspectResult, error)
	Close() error
}

type Collector struct {
	// newClient builds the Docker client; replaced in tests.
	newClient func() (dockerAPI, error)
}

func New() *Collector {
	return &Collector{
		newClient: func() (dockerAPI, error) { return client.New(client.FromEnv) },
	}
}

func (c *Collector) Name() string { return "docker" }

// Collect gathers every container (running or not) and every local image
// known to the daemon. The daemon address comes from the environment
// (DOCKER_HOST), falling back to the default local socket.
func (c *Collector) Collect(ctx context.Context) (any, error) {
	apiClient, err := c.newClient()
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	defer apiClient.Close()

	containers, err := collectContainers(ctx, apiClient)
	if err != nil {
		return nil, err
	}
	images, err := collectImages(ctx, apiClient)
	if err != nil {
		return nil, err
	}
	return Result{Containers: containers, Images: images}, nil
}

func collectContainers(ctx context.Context, apiClient dockerAPI) ([]Container, error) {
	list, err := apiClient.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	containers := make([]Container, 0, len(list.Items))
	for _, item := range list.Items {
		entry := Container{
			ID:        shortID(item.ID),
			Name:      firstName(item.Names),
			Image:     item.Image,
			ImageID:   item.ImageID,
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
			resp.Config = redactContainerConfig(resp.Config)
			entry.Inspect = &resp
		}

		containers = append(containers, entry)
	}
	return containers, nil
}

func collectImages(ctx context.Context, apiClient dockerAPI) ([]Image, error) {
	// SharedSize is required for the daemon to fill the per-image container
	// count; without it the value is reported as -1.
	list, err := apiClient.ImageList(ctx, client.ImageListOptions{All: false, SharedSize: true})
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}

	images := make([]Image, 0, len(list.Items))
	for _, item := range list.Items {
		entry := Image{
			ID:          item.ID,
			RepoTags:    item.RepoTags,
			RepoDigests: item.RepoDigests,
			CreatedAt:   time.Unix(item.Created, 0).UTC(),
			Size:        item.Size,
			Containers:  item.Containers,
			Dangling:    isDangling(item.RepoTags),
		}

		inspected, err := apiClient.ImageInspect(ctx, item.ID)
		if err != nil {
			entry.InspectError = err.Error()
		} else {
			resp := inspected.InspectResponse
			resp.Config = redactImageConfig(resp.Config)
			entry.Inspect = &resp
		}

		images = append(images, entry)
	}
	return images, nil
}

// isDangling reports whether an image has no usable tag. The daemon reports
// untagged images either with an empty list or with "<none>:<none>".
func isDangling(tags []string) bool {
	for _, t := range tags {
		if t != "<none>:<none>" {
			return false
		}
	}
	return true
}

// redactContainerConfig returns a copy of cfg whose environment values are
// masked. Env vars often hold secrets (passwords, tokens), and the report may
// be stored or shared, so only the variable names are kept.
func redactContainerConfig(cfg *container.Config) *container.Config {
	if cfg == nil {
		return nil
	}
	masked := *cfg // shallow copy: do not mutate the SDK response
	masked.Env = redactEnv(cfg.Env)
	return &masked
}

// redactImageConfig does the same as redactContainerConfig for image configs.
func redactImageConfig(cfg *dockerImageConfig) *dockerImageConfig {
	if cfg == nil {
		return nil
	}
	masked := *cfg
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
