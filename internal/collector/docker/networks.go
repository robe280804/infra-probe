package docker

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// builtinNetworks are the networks Docker creates on every host.
var builtinNetworks = map[string]bool{"bridge": true, "host": true, "none": true}

// NetworkEndpoint is one container attached to a network.
type NetworkEndpoint struct {
	ContainerID string `json:"container_id"` // short form, join key with Container.ID
	Name        string `json:"name"`         // container name as seen on the network
	IPv4        string `json:"ipv4,omitempty"`
	IPv6        string `json:"ipv6,omitempty"`
	MAC         string `json:"mac,omitempty"`
}

// Network holds the facts about a Docker network: a flat summary plus the
// complete inspect payload.
type Network struct {
	ID        string    `json:"id"` // short form
	Name      string    `json:"name"`
	Driver    string    `json:"driver"`
	Scope     string    `json:"scope"`
	CreatedAt time.Time `json:"created_at"`

	// Internal networks have no route to the outside world.
	Internal   bool `json:"internal"`
	Attachable bool `json:"attachable"`
	Ingress    bool `json:"ingress"`
	IPv6       bool `json:"ipv6"`

	// Builtin is true for the networks Docker always creates (bridge, host, none).
	Builtin bool `json:"builtin"`

	Subnets  []string `json:"subnets"`
	Gateways []string `json:"gateways"`

	// Containers lists the attached containers, sorted by name.
	Containers []NetworkEndpoint `json:"containers"`

	// Inspect is the full network configuration (IPAM, options, labels, peers...).
	Inspect *network.Inspect `json:"inspect,omitempty"`

	// InspectError is set when the inspect call failed for this network.
	InspectError string `json:"inspect_error,omitempty"`
}

func collectNetworks(ctx context.Context, api dockerAPI) ([]Network, error) {
	list, err := api.NetworkList(ctx, client.NetworkListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list networks: %w", err)
	}

	networks := make([]Network, 0, len(list.Items))
	for _, item := range list.Items {
		entry := networkFromSummary(item.Network)

		// The list call does not report attached containers: inspect does.
		// A single failing inspect must not abort the whole collection.
		inspected, err := api.NetworkInspect(ctx, item.ID, client.NetworkInspectOptions{})
		if err != nil {
			entry.InspectError = err.Error()
		} else {
			resp := inspected.Network
			entry.Containers = endpointsOf(resp.Containers)
			entry.Inspect = &resp
		}

		networks = append(networks, entry)
	}
	return networks, nil
}

// networkFromSummary builds the flat part of a Network from the daemon data.
func networkFromSummary(n network.Network) Network {
	entry := Network{
		ID:         shortID(n.ID),
		Name:       n.Name,
		Driver:     n.Driver,
		Scope:      n.Scope,
		CreatedAt:  n.Created.UTC(),
		Internal:   n.Internal,
		Attachable: n.Attachable,
		Ingress:    n.Ingress,
		IPv6:       n.EnableIPv6,
		Builtin:    builtinNetworks[n.Name],
		Subnets:    []string{},
		Gateways:   []string{},
		Containers: []NetworkEndpoint{},
	}
	for _, cfg := range n.IPAM.Config {
		// Unset prefixes/addresses are the zero value, which is not valid.
		if cfg.Subnet.IsValid() {
			entry.Subnets = append(entry.Subnets, cfg.Subnet.String())
		}
		if cfg.Gateway.IsValid() {
			entry.Gateways = append(entry.Gateways, cfg.Gateway.String())
		}
	}
	return entry
}

// endpointsOf flattens the container map of an inspected network. The map is
// keyed by full container ID.
func endpointsOf(m map[string]network.EndpointResource) []NetworkEndpoint {
	out := make([]NetworkEndpoint, 0, len(m))
	for id, ep := range m {
		e := NetworkEndpoint{ContainerID: shortID(id), Name: ep.Name}
		if ep.IPv4Address.IsValid() {
			e.IPv4 = ep.IPv4Address.String()
		}
		if ep.IPv6Address.IsValid() {
			e.IPv6 = ep.IPv6Address.String()
		}
		if len(ep.MacAddress) > 0 {
			e.MAC = ep.MacAddress.String()
		}
		out = append(out, e)
	}
	// Map iteration order is random: sort so the report is stable.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ContainerID < out[j].ContainerID
	})
	return out
}
