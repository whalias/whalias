package docker

import (
	"net/netip"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/network"
)

func TestEventUsesAction(t *testing.T) {
	msg := events.Message{
		Type:   events.ContainerEventType,
		Action: events.ActionStart,
		Actor: events.Actor{
			ID:         "abc",
			Attributes: map[string]string{"name": "web"},
		},
	}
	ev := EventFromMessage(msg)
	if ev.Action != "start" {
		t.Fatalf("action %s", ev.Action)
	}
	if ev.ID != "abc" {
		t.Fatalf("id %s", ev.ID)
	}
	if ev.Type != "container" || ev.Attributes["name"] != "web" {
		t.Fatalf("event %#v", ev)
	}
}

func TestEmptyActionStaysEmpty(t *testing.T) {
	ev := EventFromMessage(events.Message{Action: ""})
	if ev.Action != "" {
		t.Fatalf("action %q", ev.Action)
	}
}

func TestContainerFromInspect(t *testing.T) {
	resp := container.InspectResponse{
		ID:    "abc",
		Name:  "/web",
		State: &container.State{Status: "running"},
		Config: &container.Config{
			Labels: map[string]string{"com.docker.compose.service": "web"},
		},
		NetworkSettings: &container.NetworkSettings{
			Networks: map[string]*network.EndpointSettings{
				"proj_default": {
					NetworkID: "nid",
					IPAddress: netip.MustParseAddr("172.18.0.3"),
					Gateway:   netip.MustParseAddr("172.18.0.1"),
				},
				"whalias": {
					NetworkID: "hid",
					IPAddress: netip.MustParseAddr("172.19.0.2"),
					Gateway:   netip.MustParseAddr("172.19.0.1"),
				},
				"v6only": {IPAddress: netip.MustParseAddr("fd00::2")},
			},
		},
	}
	got := ContainerFromInspect(resp)
	if got.Name != "web" || got.ID != "abc" || got.State != "running" {
		t.Fatalf("container %#v", got)
	}
	if got.Labels["com.docker.compose.service"] != "web" {
		t.Fatalf("labels %#v", got.Labels)
	}
	if got.Networks["proj_default"].IP != "172.18.0.3" {
		t.Fatalf("bridge %#v", got.Networks)
	}
	if _, ok := got.Networks["v6only"]; ok {
		t.Fatal("kept an IPv6 endpoint")
	}
	if ContainerFromInspect(container.InspectResponse{}).Name != "" {
		t.Fatal("empty inspect invented a name")
	}
}

func TestNetworkFromInspect(t *testing.T) {
	n := network.Inspect{
		Network: network.Network{
			Name:   "proj_default",
			ID:     "nid",
			Driver: "bridge",
			IPAM: network.IPAM{Config: []network.IPAMConfig{
				{Subnet: netip.MustParsePrefix("fd00::/64"), Gateway: netip.MustParseAddr("fd00::1")},
				{Subnet: netip.MustParsePrefix("10.1.0.0/16"), Gateway: netip.MustParseAddr("10.1.0.1")},
				{Subnet: netip.MustParsePrefix("10.2.0.0/16"), Gateway: netip.MustParseAddr("10.2.0.1")},
			}},
		},
		Containers: map[string]network.EndpointResource{
			"stopped-is-absent": {},
		},
	}
	got := NetworkFromInspect(n)
	if got.Subnet != "10.1.0.0/16" || got.Gateway != "10.1.0.1" {
		t.Fatalf("network %#v", got)
	}
	if len(got.Subnets) != 2 || got.Subnets[0] != "10.1.0.0/16" || got.Subnets[1] != "10.2.0.0/16" {
		t.Fatalf("subnets %#v", got.Subnets)
	}
	if len(got.ContainerIDs) != 1 || got.ContainerIDs[0] != "stopped-is-absent" {
		t.Fatalf("members %#v", got.ContainerIDs)
	}
	empty := NetworkFromInspect(network.Inspect{Network: network.Network{Name: "none", Driver: "null"}})
	if empty.Subnet != "" || len(empty.Subnets) != 0 || len(empty.ContainerIDs) != 0 {
		t.Fatalf("empty network %#v", empty)
	}
}
