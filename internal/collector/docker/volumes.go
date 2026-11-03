package docker

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
)

// sizeUnknown marks a size or count the daemon could not provide.
const sizeUnknown = -1

// VolumeMount is one container using a volume.
type VolumeMount struct {
	ContainerID   string `json:"container_id"` // short form, join key with Container.ID
	ContainerName string `json:"container_name"`
	Destination   string `json:"destination"` // path inside the container
	RW            bool   `json:"rw"`
}

// Volume holds the facts about a Docker volume: a flat summary plus the
// complete payload returned by the daemon.
type Volume struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Scope      string            `json:"scope"`
	Mountpoint string            `json:"mountpoint"` // path on the host
	CreatedAt  time.Time         `json:"created_at"`
	Labels     map[string]string `json:"labels"`

	// Anonymous volumes have a random 64-hex name: they were created by a
	// VOLUME instruction or an unnamed -v, not on purpose.
	Anonymous bool `json:"anonymous"`

	// Size in bytes, -1 when unknown (non-local driver or disk usage failed).
	Size int64 `json:"size"`

	// RefCount is the number of containers (any state) referencing the
	// volume according to the daemon, -1 when unknown.
	RefCount int64 `json:"ref_count"`

	// Containers lists the containers mounting the volume, sorted by name.
	// An empty list means the volume is unused.
	Containers []VolumeMount `json:"containers"`

	// Inspect is the full volume payload (options, status, labels...).
	// Secrets in driver options are redacted, see redactVolumeOptions.
	Inspect *volume.Volume `json:"inspect,omitempty"`
}

var anonymousName = regexp.MustCompile(`^[0-9a-f]{64}$`)

// collectVolumes lists the volumes and links them to the containers that
// mount them. Sizes come from the disk usage endpoint, which walks the volume
// directories and can be slow or fail: in that case the volumes are still
// returned, with unknown size, together with the error.
func collectVolumes(ctx context.Context, api dockerAPI, containers []Container) ([]Volume, error) {
	list, err := api.VolumeList(ctx, client.VolumeListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}

	usage, usageErr := volumeUsage(ctx, api)
	mounts := volumeMounts(containers)

	volumes := make([]Volume, 0, len(list.Items))
	for _, item := range list.Items {
		entry := volumeFromAPI(item)
		if u, ok := usage[item.Name]; ok {
			entry.Size = u.Size
			entry.RefCount = u.RefCount
		}
		if m := mounts[item.Name]; m != nil {
			entry.Containers = m
		}
		volumes = append(volumes, entry)
	}
	sort.Slice(volumes, func(i, j int) bool { return volumes[i].Name < volumes[j].Name })

	if usageErr != nil {
		return volumes, fmt.Errorf("volume sizes: %w", usageErr)
	}
	return volumes, nil
}

// volumeFromAPI builds a Volume from the daemon data, with sizes unknown and
// no containers.
func volumeFromAPI(v volume.Volume) Volume {
	created, _ := time.Parse(time.RFC3339Nano, v.CreatedAt) // zero when missing
	labels := v.Labels
	if labels == nil {
		labels = map[string]string{}
	}

	inspect := v // shallow copy: do not mutate the SDK response
	inspect.Options = redactVolumeOptions(v.Options)
	inspect.UsageData = nil // reported as Size / RefCount

	return Volume{
		Name:       v.Name,
		Driver:     v.Driver,
		Scope:      v.Scope,
		Mountpoint: v.Mountpoint,
		CreatedAt:  created.UTC(),
		Labels:     labels,
		Anonymous:  anonymousName.MatchString(v.Name),
		Size:       sizeUnknown,
		RefCount:   sizeUnknown,
		Containers: []VolumeMount{},
		Inspect:    &inspect,
	}
}

// volumeUsage returns size and reference count per volume name.
func volumeUsage(ctx context.Context, api dockerAPI) (map[string]volume.UsageData, error) {
	du, err := api.DiskUsage(ctx, client.DiskUsageOptions{Volumes: true, Verbose: true})
	if err != nil {
		return nil, err
	}
	out := make(map[string]volume.UsageData, len(du.Volumes.Items))
	for _, v := range du.Volumes.Items {
		if v.UsageData != nil {
			out[v.Name] = *v.UsageData
		}
	}
	return out, nil
}

// volumeMounts indexes the volume mounts of every inspected container by
// volume name.
func volumeMounts(containers []Container) map[string][]VolumeMount {
	out := map[string][]VolumeMount{}
	for _, c := range containers {
		if c.Inspect == nil {
			continue
		}
		for _, m := range c.Inspect.Mounts {
			if m.Type != mount.TypeVolume || m.Name == "" {
				continue
			}
			out[m.Name] = append(out[m.Name], VolumeMount{
				ContainerID:   c.ID,
				ContainerName: c.Name,
				Destination:   m.Destination,
				RW:            m.RW,
			})
		}
	}
	for _, list := range out {
		sort.Slice(list, func(i, j int) bool {
			if list[i].ContainerName != list[j].ContainerName {
				return list[i].ContainerName < list[j].ContainerName
			}
			return list[i].Destination < list[j].Destination
		})
	}
	return out
}

// secretOption matches option names that hold a credential.
var secretOption = regexp.MustCompile(`(?i)(pass|secret|token|cred|key)`)

// redactVolumeOptions masks credentials in driver options. Network volumes
// (CIFS, NFS...) carry them in the "o" option, e.g.
// "addr=10.0.0.5,username=backup,password=s3cret".
func redactVolumeOptions(opts map[string]string) map[string]string {
	if opts == nil {
		return nil
	}
	out := make(map[string]string, len(opts))
	for k, v := range opts {
		if secretOption.MatchString(k) {
			out[k] = redacted
			continue
		}
		parts := strings.Split(v, ",")
		for i, p := range parts {
			if name, _, ok := strings.Cut(p, "="); ok && secretOption.MatchString(name) {
				parts[i] = name + "=" + redacted
			}
		}
		out[k] = strings.Join(parts, ",")
	}
	return out
}
