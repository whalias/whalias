// Package docker watches the Engine API. Events are read from Action.
// This client's event message has no Status field.
package docker

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// IsNotFound reports whether an Engine API error indicates a missing object.
func IsNotFound(err error) bool {
	return errdefs.IsNotFound(err)
}

// Event is one Engine event. Action is the field the engine supplies.
type Event struct {
	Type       string
	Action     string
	ID         string
	Attributes map[string]string
}

// Endpoint is one container address on a Docker network.
type Endpoint struct {
	NetworkID string
	IP        string
	Gateway   string
}

// Container is the inspect data the controller needs. Addresses come from
// inspect, not from the event payload.
type Container struct {
	ID       string
	Name     string
	Labels   map[string]string
	State    string
	Networks map[string]Endpoint
}

// Network is a Docker network. ContainerIDs is the live member list from
// inspect. That list omits stopped containers and is not a reason to leave.
type Network struct {
	ID           string
	Name         string
	Driver       string
	Subnet       string
	Subnets      []string
	Gateway      string
	ContainerIDs []string
}

// EventFromMessage copies Action and Actor. It does not read a status field.
func EventFromMessage(msg events.Message) Event {
	attrs := map[string]string{}
	for k, v := range msg.Actor.Attributes {
		attrs[k] = v
	}
	return Event{
		Type:       string(msg.Type),
		Action:     string(msg.Action),
		ID:         msg.Actor.ID,
		Attributes: attrs,
	}
}

// Client talks to the Engine over the mounted socket.
type Client struct {
	api *client.Client
}

// New connects using the process environment. Inside the container that is
// the mounted Docker socket. The client negotiates the API version.
func New(ctx context.Context) (*Client, error) {
	api, err := client.New(client.FromEnv)
	if err != nil {
		return nil, err
	}
	return &Client{api: api}, nil
}

// Close releases the client.
func (c *Client) Close() error {
	return c.api.Close()
}

// Events streams container and network events until ctx is cancelled.
func (c *Client) Events(ctx context.Context) (<-chan Event, <-chan error) {
	res := c.api.Events(ctx, client.EventsListOptions{
		Filters: make(client.Filters).Add("type", string(events.ContainerEventType), string(events.NetworkEventType)),
	})
	out := make(chan Event)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-res.Messages:
				if !ok {
					return
				}
				ev := EventFromMessage(msg)
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, res.Err
}

// ListContainers returns every container, including stopped ones.
func (c *Client) ListContainers(ctx context.Context) ([]Container, error) {
	res, err := c.api.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, err
	}
	out := make([]Container, 0, len(res.Items))
	for _, sum := range res.Items {
		ctr, err := c.InspectContainer(ctx, sum.ID)
		if err != nil {
			if IsNotFound(err) {
				continue
			}
			return nil, err
		}
		out = append(out, ctr)
	}
	return out, nil
}

// InspectContainer reads one container by ID or name.
func (c *Client) InspectContainer(ctx context.Context, id string) (Container, error) {
	res, err := c.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return Container{}, err
	}
	return ContainerFromInspect(res.Container), nil
}

// ListNetworks returns every network.
func (c *Client) ListNetworks(ctx context.Context) ([]Network, error) {
	res, err := c.api.NetworkList(ctx, client.NetworkListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]Network, 0, len(res.Items))
	for _, n := range res.Items {
		out = append(out, NetworkFromInspect(network.Inspect{Network: n.Network}))
	}
	return out, nil
}

// InspectNetwork reads one network by ID or name.
func (c *Client) InspectNetwork(ctx context.Context, id string) (Network, error) {
	res, err := c.api.NetworkInspect(ctx, id, client.NetworkInspectOptions{})
	if err != nil {
		return Network{}, err
	}
	return NetworkFromInspect(res.Network), nil
}

// ConnectNetwork attaches a container. Docker chooses the address.
func (c *Client) ConnectNetwork(ctx context.Context, networkID, containerID string) error {
	if _, err := c.api.NetworkConnect(ctx, networkID, client.NetworkConnectOptions{Container: containerID}); err != nil {
		return fmt.Errorf("connect %s to %s: %w", containerID, networkID, err)
	}
	return nil
}

// DisconnectNetwork detaches a container. The join controller does not call
// this because a network left the member list.
func (c *Client) DisconnectNetwork(ctx context.Context, networkID, containerID string) error {
	if _, err := c.api.NetworkDisconnect(ctx, networkID, client.NetworkDisconnectOptions{Container: containerID}); err != nil {
		return fmt.Errorf("disconnect %s from %s: %w", containerID, networkID, err)
	}
	return nil
}

// ContainerFromInspect copies the fields the controller uses.
func ContainerFromInspect(resp container.InspectResponse) Container {
	c := Container{
		ID:       resp.ID,
		Name:     strings.TrimPrefix(resp.Name, "/"),
		Labels:   map[string]string{},
		Networks: map[string]Endpoint{},
	}
	if resp.State != nil {
		c.State = string(resp.State.Status)
	}
	if resp.Config != nil && resp.Config.Labels != nil {
		c.Labels = resp.Config.Labels
	}
	if resp.NetworkSettings == nil {
		return c
	}
	for name, ep := range resp.NetworkSettings.Networks {
		if ep == nil || !ep.IPAddress.IsValid() || !ep.IPAddress.Is4() {
			continue
		}
		end := Endpoint{NetworkID: ep.NetworkID, IP: ep.IPAddress.String()}
		if ep.Gateway.IsValid() {
			end.Gateway = ep.Gateway.String()
		}
		c.Networks[name] = end
	}
	return c
}

// NetworkFromInspect copies the IPv4 subnets and the live member IDs.
func NetworkFromInspect(n network.Inspect) Network {
	out := Network{
		ID:     n.ID,
		Name:   n.Name,
		Driver: n.Driver,
	}
	for _, cfg := range n.IPAM.Config {
		if !cfg.Subnet.IsValid() || !cfg.Subnet.Addr().Is4() {
			continue
		}
		s := cfg.Subnet.String()
		out.Subnets = append(out.Subnets, s)
		if out.Subnet == "" {
			out.Subnet = s
			if cfg.Gateway.IsValid() {
				out.Gateway = cfg.Gateway.String()
			}
		}
	}
	for id := range n.Containers {
		out.ContainerIDs = append(out.ContainerIDs, id)
	}
	sort.Strings(out.ContainerIDs)
	return out
}
