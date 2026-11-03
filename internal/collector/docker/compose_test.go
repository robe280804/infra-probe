package docker

import (
	"context"
	"reflect"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
)

func composeContainer(id, name, state, project, service, number string, extra map[string]string) Container {
	labels := map[string]string{labelProject: project, labelService: service, labelNumber: number}
	for k, v := range extra {
		labels[k] = v
	}
	return Container{
		ID: id, Name: name, Image: service + ":1", State: state, Status: state,
		Inspect: &container.InspectResponse{Config: &container.Config{Labels: labels}},
	}
}

func projectLabels() map[string]string {
	return map[string]string{
		labelWorkingDir:  "/srv/app",
		labelConfigFiles: "/srv/app/docker-compose.yml,/srv/app/docker-compose.prod.yml",
		labelEnvFile:     "/srv/app/.env",
		labelVersion:     "2.40.3",
	}
}

func composeFixture() ([]Container, []Network, []Volume) {
	web := projectLabels()
	web[labelDependsOn] = "db:service_healthy:true,cache:service_started:false"

	containers := []Container{
		composeContainer("c2", "app-web-2", "running", "app", "web", "2", web),
		composeContainer("c1", "app-web-1", "running", "app", "web", "1", web),
		composeContainer("c3", "app-db-1", "exited", "app", "db", "1", projectLabels()),
		composeContainer("c4", "app-web-run-1", "exited", "app", "web", "", map[string]string{labelOneOff: "True"}),
		composeContainer("c5", "solo-1", "exited", "solo", "solo", "1", nil),
		{ID: "c6", Name: "plain", State: "running", Inspect: &container.InspectResponse{Config: &container.Config{}}},
		{ID: "c7", Name: "no-inspect", State: "running", InspectError: "gone"},
	}
	networks := []Network{
		{Name: "app_default", Inspect: &network.Inspect{Network: network.Network{Labels: map[string]string{labelProject: "app", labelNetwork: "default"}}}},
		{Name: "bridge", Inspect: &network.Inspect{}},
		{Name: "no-inspect"},
	}
	volumes := []Volume{
		{Name: "app_data", Labels: map[string]string{labelProject: "app", labelVolume: "data"}},
		{Name: "other", Labels: map[string]string{}},
	}
	return containers, networks, volumes
}

func TestCollectCompose(t *testing.T) {
	projects := collectCompose(composeFixture())

	if len(projects) != 2 || projects[0].Name != "app" || projects[1].Name != "solo" {
		t.Fatalf("projects = %+v", projects)
	}
	app := projects[0]

	if app.WorkingDir != "/srv/app" || app.EnvironmentFile != "/srv/app/.env" || app.Version != "2.40.3" {
		t.Errorf("project facts wrong: %+v", app)
	}
	wantFiles := []string{"/srv/app/docker-compose.yml", "/srv/app/docker-compose.prod.yml"}
	if !reflect.DeepEqual(app.ConfigFiles, wantFiles) {
		t.Errorf("config files = %v, want %v", app.ConfigFiles, wantFiles)
	}

	if len(app.Services) != 2 || app.Services[0].Name != "db" || app.Services[1].Name != "web" {
		t.Fatalf("services = %+v", app.Services)
	}
	web := app.Services[1]
	if web.Running != 2 || len(web.Containers) != 3 {
		t.Errorf("web running = %d, containers = %d, want 2 and 3", web.Running, len(web.Containers))
	}
	// Replicas first by number; the one-off container (no number) comes first.
	if got := []string{web.Containers[0].Name, web.Containers[1].Name, web.Containers[2].Name}; !reflect.DeepEqual(got, []string{"app-web-run-1", "app-web-1", "app-web-2"}) {
		t.Errorf("container order = %v", got)
	}
	if !web.Containers[0].OneOff || web.Containers[1].OneOff {
		t.Error("one-off flag wrong")
	}
	wantDeps := []ComposeDependency{{"cache", "service_started"}, {"db", "service_healthy"}}
	if !reflect.DeepEqual(web.DependsOn, wantDeps) {
		t.Errorf("depends_on = %+v, want %+v", web.DependsOn, wantDeps)
	}
	if len(app.Services[0].DependsOn) != 0 || app.Services[0].DependsOn == nil {
		t.Error("a service without dependencies needs an empty, non-nil list")
	}

	if !reflect.DeepEqual(app.Networks, []ComposeRef{{"app_default", "default"}}) {
		t.Errorf("networks = %+v", app.Networks)
	}
	if !reflect.DeepEqual(app.Volumes, []ComposeRef{{"app_data", "data"}}) {
		t.Errorf("volumes = %+v", app.Volumes)
	}

	// db is stopped, web has two running replicas: the one-off does not count.
	if app.State != "partial" {
		t.Errorf("state = %q, want partial", app.State)
	}
	if projects[1].State != "stopped" {
		t.Errorf("solo state = %q, want stopped", projects[1].State)
	}
}

func TestProjectState(t *testing.T) {
	svc := func(states ...string) ComposeService {
		s := ComposeService{}
		for _, st := range states {
			s.Containers = append(s.Containers, ComposeContainer{State: st})
		}
		return s
	}
	tests := []struct {
		name     string
		services []ComposeService
		want     string
	}{
		{"all running", []ComposeService{svc("running"), svc("running", "running")}, "running"},
		{"none running", []ComposeService{svc("exited")}, "stopped"},
		{"mixed", []ComposeService{svc("running"), svc("exited")}, "partial"},
		{"no containers", nil, "down"},
		{"only one-off", []ComposeService{{Containers: []ComposeContainer{{State: "exited", OneOff: true}}}}, "down"},
		{"one-off ignored", []ComposeService{{Containers: []ComposeContainer{{State: "running"}, {State: "exited", OneOff: true}}}}, "running"},
	}
	for _, tt := range tests {
		if got := projectState(tt.services); got != tt.want {
			t.Errorf("%s: state = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestCollectCompose_OnlyNetworkAndVolumeIsDown(t *testing.T) {
	_, networks, volumes := composeFixture()
	projects := collectCompose(nil, networks, volumes)
	if len(projects) != 1 || projects[0].State != "down" || len(projects[0].Services) != 0 {
		t.Errorf("projects = %+v", projects)
	}
}

func TestParseDependsOn(t *testing.T) {
	if got := parseDependsOn(""); got == nil || len(got) != 0 {
		t.Errorf("empty label = %v, want empty non-nil", got)
	}
	got := parseDependsOn("b:service_started, a , :x")
	want := []ComposeDependency{{"a", ""}, {"b", "service_started"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestCollect_ComposeIsNeverNil(t *testing.T) {
	got, err := newTestCollector(&fakeAPI{}, nil).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res := got.(Result); res.Compose == nil || len(res.Compose) != 0 {
		t.Errorf("compose = %v, want empty non-nil", res.Compose)
	}
}
