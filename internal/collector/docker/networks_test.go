package docker

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/moby/moby/api/types/network"
)

const (
	appNetID    = "aaaaaaaaaaaa1111111111111111111111111111111111111111111111111111"
	bridgeNetID = "bbbbbbbbbbbb2222222222222222222222222222222222222222222222222222"
)

func appNetwork() network.Summary {
	return network.Summary{Network: network.Network{
		ID:         appNetID,
		Name:       "appnet",
		Driver:     "bridge",
		Scope:      "local",
		Created:    time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC),
		Internal:   true,
		Attachable: true,
		EnableIPv6: true,
		IPAM: network.IPAM{Config: []network.IPAMConfig{
			{Subnet: netip.MustParsePrefix("172.18.0.0/16"), Gateway: netip.MustParseAddr("172.18.0.1")},
			{Subnet: netip.MustParsePrefix("fd00::/64")},
		}},
	}}
}

func bridgeNetwork() network.Summary {
	return network.Summary{Network: network.Network{ID: bridgeNetID, Name: "bridge", Driver: "bridge", Scope: "local"}}
}

func TestCollectNetworks(t *testing.T) {
	api := &fakeAPI{
		networks: []network.Summary{appNetwork(), bridgeNetwork()},
		networkEndpoints: map[string]map[string]network.EndpointResource{
			appNetID: {
				"ffffffffffff9999999999": {
					Name:        "web",
					IPv4Address: netip.MustParsePrefix("172.18.0.3/16"),
					MacAddress:  network.HardwareAddr{0x02, 0x42, 0xac, 0x12, 0x00, 0x03},
				},
				"eeeeeeeeeeee8888888888": {
					Name:        "db",
					IPv4Address: netip.MustParsePrefix("172.18.0.2/16"),
					IPv6Address: netip.MustParsePrefix("fd00::2/64"),
				},
			},
		},
	}

	got, err := collectNetworks(context.Background(), api)
	if err != nil {
		t.Fatalf("collectNetworks() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d networks, want 2", len(got))
	}

	app := got[0]
	if app.ID != "aaaaaaaaaaaa" || app.Name != "appnet" || app.Driver != "bridge" || app.Scope != "local" {
		t.Errorf("unexpected identity: %+v", app)
	}
	if !app.Internal || !app.Attachable || !app.IPv6 || app.Ingress {
		t.Errorf("unexpected flags: internal=%v attachable=%v ipv6=%v ingress=%v", app.Internal, app.Attachable, app.IPv6, app.Ingress)
	}
	if app.Builtin {
		t.Error("user network reported as builtin")
	}
	if !reflect.DeepEqual(app.Subnets, []string{"172.18.0.0/16", "fd00::/64"}) {
		t.Errorf("Subnets = %v", app.Subnets)
	}
	if !reflect.DeepEqual(app.Gateways, []string{"172.18.0.1"}) {
		t.Errorf("Gateways = %v (an unset gateway must be skipped)", app.Gateways)
	}
	if app.Inspect == nil || app.InspectError != "" {
		t.Fatalf("inspect missing: %v / %q", app.Inspect, app.InspectError)
	}

	// Endpoints are sorted by name and use the short container ID.
	wantEndpoints := []NetworkEndpoint{
		{ContainerID: "eeeeeeeeeeee", Name: "db", IPv4: "172.18.0.2/16", IPv6: "fd00::2/64"},
		{ContainerID: "ffffffffffff", Name: "web", IPv4: "172.18.0.3/16", MAC: "02:42:ac:12:00:03"},
	}
	if !reflect.DeepEqual(app.Containers, wantEndpoints) {
		t.Errorf("Containers = %+v, want %+v", app.Containers, wantEndpoints)
	}

	if !got[1].Builtin {
		t.Error("the bridge network must be reported as builtin")
	}
	if got[1].Containers == nil || len(got[1].Containers) != 0 {
		t.Errorf("empty network Containers = %v, want empty non-nil", got[1].Containers)
	}
}

func TestCollectNetworks_InspectFailureIsIsolated(t *testing.T) {
	api := &fakeAPI{
		networks:           []network.Summary{appNetwork(), bridgeNetwork()},
		networkInspectFail: map[string]bool{appNetID: true},
	}

	got, err := collectNetworks(context.Background(), api)
	if err != nil {
		t.Fatalf("a failing inspect must not fail the collection: %v", err)
	}

	// The summary data survives even when inspect fails.
	if got[0].Name != "appnet" || got[0].InspectError == "" || got[0].Inspect != nil {
		t.Errorf("failed network: %+v", got[0])
	}
	if got[1].InspectError != "" || got[1].Inspect == nil {
		t.Errorf("healthy network affected: %+v", got[1])
	}
}

func TestCollectNetworks_ListError(t *testing.T) {
	api := &fakeAPI{networkListErr: errors.New("denied")}

	if _, err := collectNetworks(context.Background(), api); err == nil {
		t.Error("collectNetworks() error = nil, want an error")
	}
}

func TestCollect_IncludesNetworks(t *testing.T) {
	api := &fakeAPI{networks: []network.Summary{bridgeNetwork()}}

	got, err := newTestCollector(api, nil).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if nets := got.(Result).Networks; len(nets) != 1 || nets[0].Name != "bridge" {
		t.Errorf("Networks = %+v", nets)
	}
}

func TestCollect_NetworkListErrorFailsCollector(t *testing.T) {
	api := &fakeAPI{networkListErr: errors.New("denied")}

	if _, err := newTestCollector(api, nil).Collect(context.Background()); err == nil {
		t.Error("Collect() error = nil, want an error")
	}
}

func TestNetworkFromSummary_ZeroIPAMIsSkipped(t *testing.T) {
	n := networkFromSummary(network.Network{
		Name: "empty",
		IPAM: network.IPAM{Config: []network.IPAMConfig{{}}}, // all zero values
	})

	if len(n.Subnets) != 0 || len(n.Gateways) != 0 {
		t.Errorf("zero IPAM config produced subnets=%v gateways=%v", n.Subnets, n.Gateways)
	}
	if n.Subnets == nil || n.Gateways == nil {
		t.Error("Subnets and Gateways must be non-nil so they serialize to []")
	}
}

func TestNetworkFromSummary_Builtin(t *testing.T) {
	for name, want := range map[string]bool{"bridge": true, "host": true, "none": true, "mynet": false, "bridge2": false} {
		if got := networkFromSummary(network.Network{Name: name}).Builtin; got != want {
			t.Errorf("Builtin(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestEndpointsOf_Empty(t *testing.T) {
	got := endpointsOf(nil)
	if got == nil || len(got) != 0 {
		t.Errorf("endpointsOf(nil) = %v, want empty non-nil", got)
	}
}

func TestEndpointsOf_SameNameIsOrderedByID(t *testing.T) {
	// Two containers can share a name across restarts; the order must still be stable.
	m := map[string]network.EndpointResource{
		"bbbbbbbbbbbb000": {Name: "web"},
		"aaaaaaaaaaaa000": {Name: "web"},
	}
	for i := 0; i < 20; i++ {
		got := endpointsOf(m)
		if got[0].ContainerID != "aaaaaaaaaaaa" {
			t.Fatalf("unstable order: %+v", got)
		}
	}
}
