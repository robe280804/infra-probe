package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// fakeAPI is an in-memory dockerAPI used to test the collector without a daemon.
type fakeAPI struct {
	containers     []container.Summary
	listErr        error
	inspectFailFor map[string]bool // container IDs whose inspect fails
	statsFn        func(id string) (container.StatsResponse, error)

	images         []image.Summary
	imageListErr   error
	imageInspectFn func(id string) (image.InspectResponse, error)

	networks           []network.Summary
	networkListErr     error
	networkEndpoints   map[string]map[string]network.EndpointResource // network ID -> container ID -> endpoint
	networkInspectFail map[string]bool                                // network IDs whose inspect fails

	closed bool
}

func (f *fakeAPI) ContainerList(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
	return client.ContainerListResult{Items: f.containers}, f.listErr
}

func (f *fakeAPI) ContainerInspect(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	if f.inspectFailFor[id] {
		return client.ContainerInspectResult{}, errors.New("no such container")
	}
	return client.ContainerInspectResult{Container: container.InspectResponse{
		ID:     id,
		Config: &container.Config{User: "app", Env: []string{"DB_PASSWORD=s3cret", "MODE=prod"}},
	}}, nil
}

func (f *fakeAPI) ContainerStats(_ context.Context, id string, _ client.ContainerStatsOptions) (client.ContainerStatsResult, error) {
	var resp container.StatsResponse
	if f.statsFn != nil {
		var err error
		if resp, err = f.statsFn(id); err != nil {
			return client.ContainerStatsResult{}, err
		}
	}
	body, err := json.Marshal(resp)
	if err != nil {
		return client.ContainerStatsResult{}, err
	}
	return client.ContainerStatsResult{Body: io.NopCloser(bytes.NewReader(body))}, nil
}

func (f *fakeAPI) NetworkList(context.Context, client.NetworkListOptions) (client.NetworkListResult, error) {
	return client.NetworkListResult{Items: f.networks}, f.networkListErr
}

func (f *fakeAPI) NetworkInspect(_ context.Context, id string, _ client.NetworkInspectOptions) (client.NetworkInspectResult, error) {
	if f.networkInspectFail[id] {
		return client.NetworkInspectResult{}, errors.New("no such network")
	}
	for _, s := range f.networks {
		if s.ID == id {
			return client.NetworkInspectResult{Network: network.Inspect{
				Network:    s.Network,
				Containers: f.networkEndpoints[id],
			}}, nil
		}
	}
	return client.NetworkInspectResult{}, errors.New("no such network")
}

func (f *fakeAPI) ImageList(context.Context, client.ImageListOptions) (client.ImageListResult, error) {
	return client.ImageListResult{Items: f.images}, f.imageListErr
}

func (f *fakeAPI) ImageInspect(_ context.Context, id string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
	if f.imageInspectFn != nil {
		resp, err := f.imageInspectFn(id)
		return client.ImageInspectResult{InspectResponse: resp}, err
	}
	return client.ImageInspectResult{InspectResponse: image.InspectResponse{
		ID: id,
		Config: &dockerspec.DockerOCIImageConfig{
			ImageConfig: ocispec.ImageConfig{Env: []string{"API_TOKEN=abc", "PATH=/usr/bin"}},
		},
	}}, nil
}

func (f *fakeAPI) Close() error { f.closed = true; return nil }

func newTestCollector(api dockerAPI, newErr error) *Collector {
	return &Collector{newClient: func() (dockerAPI, error) { return api, newErr }}
}

func TestCollectorName(t *testing.T) {
	if got := New().Name(); got != "docker" {
		t.Errorf("Name() = %q, want %q", got, "docker")
	}
}

func TestCollect_ContainersAndImages(t *testing.T) {
	api := &fakeAPI{
		containers: []container.Summary{{
			ID:      "0123456789abcdef0123",
			Names:   []string{"/web"},
			Image:   "nginx:1.27",
			ImageID: "sha256:aaa",
			State:   container.StateRunning,
			Status:  "Up 2 hours",
			Created: 1700000000,
		}},
		images: []image.Summary{{
			ID:         "sha256:aaa",
			RepoTags:   []string{"nginx:1.27"},
			Containers: 1,
			Size:       1234,
			Created:    1700000000,
		}},
	}

	got, err := newTestCollector(api, nil).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	res, ok := got.(Result)
	if !ok {
		t.Fatalf("Collect() returned %T, want Result", got)
	}

	if len(res.Containers) != 1 || len(res.Images) != 1 {
		t.Fatalf("got %d containers and %d images, want 1 and 1", len(res.Containers), len(res.Images))
	}

	c := res.Containers[0]
	if c.ID != "0123456789ab" {
		t.Errorf("container ID = %q, want short 12-char form", c.ID)
	}
	if c.Name != "web" {
		t.Errorf("container Name = %q, want %q", c.Name, "web")
	}
	if c.State != "running" || c.Status != "Up 2 hours" {
		t.Errorf("container state/status = %q/%q", c.State, c.Status)
	}
	if c.ImageID != "sha256:aaa" {
		t.Errorf("container ImageID = %q, want %q", c.ImageID, "sha256:aaa")
	}
	if c.CreatedAt.Unix() != 1700000000 {
		t.Errorf("container CreatedAt = %v", c.CreatedAt)
	}
	if c.Inspect == nil || c.InspectError != "" {
		t.Fatalf("container inspect missing: inspect=%v err=%q", c.Inspect, c.InspectError)
	}

	img := res.Images[0]
	if img.ID != "sha256:aaa" || img.Containers != 1 || img.Size != 1234 {
		t.Errorf("unexpected image summary: %+v", img)
	}
	if img.Dangling {
		t.Error("tagged image reported as dangling")
	}
	if img.Inspect == nil {
		t.Fatal("image inspect missing")
	}

	// The image ID is the join key between the two lists.
	if c.ImageID != img.ID {
		t.Errorf("container ImageID %q does not match image ID %q", c.ImageID, img.ID)
	}

	if !api.closed {
		t.Error("client was not closed")
	}
}

func TestCollect_EnvIsRedacted(t *testing.T) {
	api := &fakeAPI{
		containers: []container.Summary{{ID: "c1"}},
		images:     []image.Summary{{ID: "sha256:aaa"}},
	}

	got, err := newTestCollector(api, nil).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	res := got.(Result)

	wantContainerEnv := []string{"DB_PASSWORD=<redacted>", "MODE=<redacted>"}
	if env := res.Containers[0].Inspect.Config.Env; !reflect.DeepEqual(env, wantContainerEnv) {
		t.Errorf("container env = %v, want %v", env, wantContainerEnv)
	}

	wantImageEnv := []string{"API_TOKEN=<redacted>", "PATH=<redacted>"}
	if env := res.Images[0].Inspect.Config.Env; !reflect.DeepEqual(env, wantImageEnv) {
		t.Errorf("image env = %v, want %v", env, wantImageEnv)
	}
}

func TestCollect_ContainerInspectFailureIsIsolated(t *testing.T) {
	api := &fakeAPI{
		containers:     []container.Summary{{ID: "gone"}, {ID: "ok"}},
		inspectFailFor: map[string]bool{"gone": true},
	}

	got, err := newTestCollector(api, nil).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() must not fail when a single inspect fails: %v", err)
	}
	cs := got.(Result).Containers

	if cs[0].InspectError == "" || cs[0].Inspect != nil {
		t.Errorf("failed container: error=%q inspect=%v", cs[0].InspectError, cs[0].Inspect)
	}
	if cs[1].InspectError != "" || cs[1].Inspect == nil {
		t.Errorf("healthy container affected: error=%q inspect=%v", cs[1].InspectError, cs[1].Inspect)
	}
}

func TestCollect_ImageInspectFailureIsIsolated(t *testing.T) {
	api := &fakeAPI{
		images: []image.Summary{{ID: "bad"}, {ID: "good"}},
		imageInspectFn: func(id string) (image.InspectResponse, error) {
			if id == "bad" {
				return image.InspectResponse{}, errors.New("boom")
			}
			return image.InspectResponse{ID: id}, nil
		},
	}

	got, err := newTestCollector(api, nil).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	imgs := got.(Result).Images

	if imgs[0].InspectError != "boom" || imgs[0].Inspect != nil {
		t.Errorf("failed image: %+v", imgs[0])
	}
	if imgs[1].InspectError != "" || imgs[1].Inspect == nil {
		t.Errorf("healthy image affected: %+v", imgs[1])
	}
}

func TestCollect_Errors(t *testing.T) {
	tests := []struct {
		name string
		col  *Collector
	}{
		{"client creation", newTestCollector(nil, errors.New("no socket"))},
		{"container list", newTestCollector(&fakeAPI{listErr: errors.New("denied")}, nil)},
		{"image list", newTestCollector(&fakeAPI{imageListErr: errors.New("denied")}, nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.col.Collect(context.Background()); err == nil {
				t.Error("Collect() error = nil, want an error")
			}
		})
	}
}

func TestCollect_EmptyDaemonYieldsEmptyListsNotNil(t *testing.T) {
	got, err := newTestCollector(&fakeAPI{}, nil).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	res := got.(Result)
	// Non-nil slices serialize to [] instead of null, which is friendlier
	// for any consumer of the JSON.
	if res.Containers == nil || res.Images == nil || res.Networks == nil {
		t.Errorf("expected empty non-nil slices, got containers=%v images=%v networks=%v", res.Containers, res.Images, res.Networks)
	}
}

func TestRedactContainerConfig_DoesNotMutateInput(t *testing.T) {
	orig := &container.Config{Env: []string{"A=1"}}

	masked := redactContainerConfig(orig)

	if orig.Env[0] != "A=1" {
		t.Errorf("input was mutated: %v", orig.Env)
	}
	if masked.Env[0] != "A=<redacted>" {
		t.Errorf("masked env = %v", masked.Env)
	}
}

func TestRedactConfig_Nil(t *testing.T) {
	if redactContainerConfig(nil) != nil {
		t.Error("redactContainerConfig(nil) should return nil")
	}
	if redactImageConfig(nil) != nil {
		t.Error("redactImageConfig(nil) should return nil")
	}
}

func TestRedactEnv(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, nil},
		{"empty", []string{}, []string{}},
		{"simple", []string{"KEY=value"}, []string{"KEY=<redacted>"}},
		{"value with equals sign", []string{"URL=http://x?a=b"}, []string{"URL=<redacted>"}},
		{"empty value", []string{"EMPTY="}, []string{"EMPTY=<redacted>"}},
		{"no equals sign", []string{"FLAG"}, []string{"FLAG=<redacted>"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redactEnv(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("redactEnv(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestShortID(t *testing.T) {
	tests := map[string]string{
		"":                       "",
		"abc":                    "abc",
		"123456789012":           "123456789012",
		"1234567890123456789012": "123456789012",
	}
	for in, want := range tests {
		if got := shortID(in); got != want {
			t.Errorf("shortID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFirstName(t *testing.T) {
	tests := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"/web"}, "web"},
		{[]string{"/web", "/alias"}, "web"},
		{[]string{"noslash"}, "noslash"},
	}
	for _, tt := range tests {
		if got := firstName(tt.in); got != tt.want {
			t.Errorf("firstName(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestIsDangling(t *testing.T) {
	tests := []struct {
		name string
		tags []string
		want bool
	}{
		{"nil tags", nil, true},
		{"empty tags", []string{}, true},
		{"none placeholder", []string{"<none>:<none>"}, true},
		{"tagged", []string{"nginx:latest"}, false},
		{"placeholder plus real tag", []string{"<none>:<none>", "app:1"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDangling(tt.tags); got != tt.want {
				t.Errorf("isDangling(%v) = %v, want %v", tt.tags, got, tt.want)
			}
		})
	}
}
