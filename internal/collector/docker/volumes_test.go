package docker

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/volume"
)

const anonVolume = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func volumeFixture() *fakeAPI {
	return &fakeAPI{
		containers: []container.Summary{
			{ID: "cccccccccccc0000", Names: []string{"/db"}, State: container.StateExited},
			{ID: "dddddddddddd0000", Names: []string{"/backup"}, State: container.StateExited},
		},
		mountsFor: map[string][]container.MountPoint{
			"cccccccccccc0000": {
				{Type: mount.TypeVolume, Name: "pgdata", Destination: "/var/lib/postgresql/data", RW: true},
				{Type: mount.TypeBind, Source: "/etc/app", Destination: "/etc/app"}, // not a volume
			},
			"dddddddddddd0000": {
				{Type: mount.TypeVolume, Name: "pgdata", Destination: "/backup/src", RW: false},
			},
		},
		volumes: []volume.Volume{
			{Name: "pgdata", Driver: "local", Scope: "local", Mountpoint: "/var/lib/docker/volumes/pgdata/_data",
				CreatedAt: "2026-01-02T03:04:05Z", Labels: map[string]string{"com.docker.compose.project": "app"}},
			{Name: anonVolume, Driver: "local", Scope: "local"},
			{Name: "share", Driver: "local", Scope: "local",
				Options: map[string]string{"type": "cifs", "device": "//nas/backup", "o": "addr=10.0.0.5,username=bk,password=s3cret"}},
		},
		diskUsage: []volume.Volume{
			{Name: "pgdata", UsageData: &volume.UsageData{Size: 4096, RefCount: 2}},
			{Name: anonVolume, UsageData: &volume.UsageData{Size: 0, RefCount: 0}},
		},
	}
}

func collectVolumesFrom(t *testing.T, api *fakeAPI) ([]Volume, error) {
	t.Helper()
	got, err := newTestCollector(api, nil).Collect(context.Background())
	res, ok := got.(Result)
	if !ok {
		t.Fatalf("Collect() returned %T, want Result", got)
	}
	return res.Volumes, err
}

func TestCollectVolumes(t *testing.T) {
	vols, err := collectVolumesFrom(t, volumeFixture())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(vols) != 3 {
		t.Fatalf("got %d volumes, want 3", len(vols))
	}

	// Sorted by name: the anonymous (hex) volume first.
	anon, pg, share := vols[0], vols[1], vols[2]
	if anon.Name != anonVolume || pg.Name != "pgdata" || share.Name != "share" {
		t.Fatalf("unexpected order: %s, %s, %s", anon.Name, pg.Name, share.Name)
	}

	if !anon.Anonymous || pg.Anonymous {
		t.Error("anonymous flag is wrong")
	}
	if pg.Size != 4096 || pg.RefCount != 2 {
		t.Errorf("pgdata size/refs = %d/%d, want 4096/2", pg.Size, pg.RefCount)
	}
	if share.Size != sizeUnknown || share.RefCount != sizeUnknown {
		t.Errorf("share size/refs = %d/%d, want unknown", share.Size, share.RefCount)
	}
	if want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC); !pg.CreatedAt.Equal(want) {
		t.Errorf("created = %v, want %v", pg.CreatedAt, want)
	}
	if pg.Labels["com.docker.compose.project"] != "app" || share.Labels == nil {
		t.Error("labels not kept (or nil instead of empty)")
	}

	wantMounts := []VolumeMount{
		{ContainerID: "dddddddddddd", ContainerName: "backup", Destination: "/backup/src", RW: false},
		{ContainerID: "cccccccccccc", ContainerName: "db", Destination: "/var/lib/postgresql/data", RW: true},
	}
	if !reflect.DeepEqual(pg.Containers, wantMounts) {
		t.Errorf("pgdata containers = %+v, want %+v", pg.Containers, wantMounts)
	}
	if anon.Containers == nil || len(anon.Containers) != 0 {
		t.Error("unused volume must have an empty, non-nil container list")
	}
	if pg.Inspect == nil || pg.Inspect.UsageData != nil {
		t.Error("inspect missing, or usage data duplicated in it")
	}
}

func TestCollectVolumes_OptionsAreRedacted(t *testing.T) {
	vols, err := collectVolumesFrom(t, volumeFixture())
	if err != nil {
		t.Fatal(err)
	}
	share := vols[2]
	if got, want := share.Inspect.Options["o"], "addr=10.0.0.5,username=bk,password=<redacted>"; got != want {
		t.Errorf("o = %q, want %q", got, want)
	}
	if share.Inspect.Options["device"] != "//nas/backup" {
		t.Error("non-secret option changed")
	}
}

func TestCollectVolumes_DiskUsageFailureKeepsVolumes(t *testing.T) {
	api := volumeFixture()
	api.diskUsageErr = errors.New("df timeout")

	vols, err := collectVolumesFrom(t, api)
	if err == nil || !strings.Contains(err.Error(), "volume sizes: df timeout") {
		t.Fatalf("error = %v, want the disk usage failure", err)
	}
	if len(vols) != 3 {
		t.Fatalf("got %d volumes, want 3", len(vols))
	}
	for _, v := range vols {
		if v.Size != sizeUnknown {
			t.Errorf("%s size = %d, want unknown", v.Name, v.Size)
		}
	}
	// Usage from mounts does not depend on disk usage.
	if len(vols[1].Containers) != 2 {
		t.Error("containers lost on disk usage failure")
	}
}

func TestCollectVolumes_ListErrorFailsOnlyVolumes(t *testing.T) {
	api := volumeFixture()
	api.volumeListErr = errors.New("denied")

	got, err := newTestCollector(api, nil).Collect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "list volumes: denied") {
		t.Fatalf("error = %v", err)
	}
	res := got.(Result)
	if res.Volumes == nil || len(res.Volumes) != 0 {
		t.Error("volumes must be an empty, non-nil list")
	}
	if len(res.Containers) != 2 {
		t.Error("containers must still be collected")
	}
}

func TestRedactVolumeOptions(t *testing.T) {
	in := map[string]string{"o": "user=a,pass=b,credentials=/x", "secret-key": "v", "type": "nfs"}
	want := map[string]string{"o": "user=a,pass=<redacted>,credentials=<redacted>", "secret-key": "<redacted>", "type": "nfs"}
	if got := redactVolumeOptions(in); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if redactVolumeOptions(nil) != nil {
		t.Error("nil options must stay nil")
	}
}
