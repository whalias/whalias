# Whalias: OrbStack-style container domains for plain Docker

Design proposal, written 19 November 2026. Nothing here is built yet. Readied for the spike phase on
19 November 2026: the implementation decisions are recorded in section 17, and section 15 is the spike
plan. The macOS path was revised on 20 November 2026. Section 3 is that product. Linux stays on
loopback. Windows is not decided.

The name is Docker's whale plus *alias*, because network aliases are how a container reaches another
container (section 9). That is the naming origin, and it is the in-container plan. On the Mac host, a
name resolves to the stable tunnel address through host DNS (section 3). macOS host traffic does not
use a Docker network alias. The domain is `wha.internal`; section 8 says why. Both were chosen on
19 November 2026. The working name until then, "Dockname", was dropped because three public projects
already use it, two of them for container names on `.localhost`.

The logo was chosen on 25 November 2026. It is [docs/logo.svg](docs/logo.svg): a navy whale
(`#0E4D64`) with three teal arrows (`#00B4D8`) leaving the blowhole, left, up, and right. That file
is the app icon, the menu-bar icon, and the mark in the README. The candidates it was chosen from
stay in `docs/logo-candidates/`.

**Whalias runs in Docker.** On macOS, decided 25 November 2026, the window app is the install.
A DMG from a GitHub release, or a Homebrew cask, puts Whalias.app on the Mac. There is no setup
script in that product. The first launch makes the host changes that need admin rights. On Linux
the plan is still that nothing Whalias ships keeps running on the host, apart from a tunnel helper
where Docker Desktop needs one. On macOS, decided 20 November 2026, a root helper owns the tunnel
interface and one route; section 3 records that. The native data-plane app considered on
19 November 2026 stays rejected (section 14). The menu-bar app does not own DNS, the tunnel, or
HTTPS. The plan is [docs/host-app.md](docs/host-app.md). The app is not built yet. The scripts in
`scripts/macos/` are the development stand-in on this machine.

## Where the project lives

Claimed on 19 November 2026, after a search that found "Whalias" unused on GitHub, Homebrew, winget,
Chocolatey, Snap, Flathub, the AUR, NuGet, npm, crates.io, PyPI, the Go module proxy and Docker Hub,
and no product, company or trademark by that name in a web search. A formal USPTO trademark search has
not been done.

| What | Where | State |
| --- | --- | --- |
| Website, docs | [whalias.dev](https://whalias.dev) | Registered at Cloudflare Registrar, on Cloudflare DNS. A placeholder page for now. `.dev` is HSTS-preloaded, so the site is HTTPS only. macOS installs are the app. Linux still plans an `install.sh`. Windows installs are the tray app, from a GitHub release or winget |
| Source code | [github.com/whalias](https://github.com/whalias) (GitHub organization) | Created, no code yet |
| Homebrew tap | [github.com/whalias/homebrew-tap](https://github.com/whalias/homebrew-tap) | Created, empty. Will hold a cask, `brew install --cask whalias/tap/whalias`, that installs Whalias.app. The app installs the `whalias` command on first launch |
| Container image, primary | `ghcr.io/whalias/whalias` (GitHub Container Registry) | Comes with the GitHub organization; nothing else to claim |
| Container image, mirror | `docker.io/whalias/whalias` (Docker Hub) | Held by the Docker account `whalias`, a personal account. Docker can convert it to an organization later, which needs a paid plan |
| Docker Desktop extension | `docker.io/whalias/extension` | Planned. Docker's extension marketplace lists extensions published on Docker Hub |
| winget | [docs/windows-release.md](docs/windows-release.md) | The Windows install mirrors macOS: the tray app is the product, from a GitHub release or winget. Not built. The tunnel spikes have not been run |
| NuGet | Not claimed | Deliberately skipped: Whalias does not ship as a `dotnet tool` |

`whalias.com` belongs to someone else (registered in 2006 behind a privacy service) and only
redirects to an advertising site. Do not link to it, and expect some people to type it by mistake.

## 1. Goal

OrbStack gives every container a name such as `web.myproject.orb.local`, reachable from the Mac on
**every port the container listens on**, with trusted HTTPS, and with nothing to configure in the
projects themselves. Two containers can both listen on 5432 and never conflict, because each name is
its own machine as far as a client can tell.

Whalias is meant to give the same experience with ordinary Docker:

- Docker on macOS, whichever local engine is current, Docker Desktop or OrbStack. Docker Desktop on Windows, and Docker Engine on Linux.
- On macOS, install the app and finish its first launch. One admin prompt, then nothing to configure. Linux still has a setup command. Windows is not decided.
- One codebase, written in Go, built into one multi-architecture Linux container image. On macOS, launchd runs a helper shell script, and that script runs `wireguard-go`. The helper is not a signed host binary. The menu-bar app in section 14 will be a host binary. Its first copies are unsigned.
- No app signing yet. The helper is not a signed Mac app, and it is not the `lo0` alias job from spike 16. A "Background Items Added" notification can still appear for the LaunchDaemon. Whether it does is not settled. Setup does not install a job that aliases `lo0`.

### In scope

- `<service>.<project>.wha.internal` and `<container>.wha.internal` names, created and removed as
  containers start and stop.
- On macOS, every TCP port the process listens on is reachable on the container's own name from the
  host. DNAT to the current bridge address does that. The container does not have to `EXPOSE` or
  publish the port for the tunnel to work. On Linux, a relay publishes exposed TCP ports only
  (sections 6 and 12). Windows is not decided. Two containers that listen on the same port do not
  conflict, because each name has its own address.
- Trusted HTTPS from the host: `https://web.myproject.wha.internal`. On macOS, termination is inside
  the controller (section 10). The app's first launch adds the public CA to the System keychain.
  The development scripts do not.
- The same names resolving inside containers is the shared-network plan in section 9. That is how
  Linux containers reach each other. It is out of this macOS build. macOS host traffic does not use
  those Docker aliases.
- A `whalias` command the Mac app installs, for listing names and opening one. There is no
  `whalias doctor`. The window is the health view. The read-only status page and the optional
  Docker Desktop extension are later. This macOS build does not serve the status page and does not
  give it `127.87.0.2`.
- A Mac menu-bar app that is the install, decided 25 November 2026. A DMG or a Homebrew cask is
  how it arrives. First launch installs the helper, the login item, the controller, the CA, and
  the `whalias` command. It lists names, opens HTTP and HTTPS targets, and restarts the tunnel
  helper. It does not own the data path. Login Items shows the helper as Whalias once that launch
  has tied the job to the app. The plan is [docs/host-app.md](docs/host-app.md). The app is not
  built yet. Linux stays a CLI, with systemd only where a host tunnel helper must keep running.
  The Windows tray waits on a Windows service. Section 14.

### Out of scope, by decision

- **HTTPS inside containers.** Inside a container, a name reaches the target container directly, and
  no Whalias TLS endpoint sits in front of it. Use `http://name:port` inside. Accepted on
  19 November 2026: containers would not trust Whalias's certificate authority (CA) anyway.
- **A native data-plane app.** Considered on 19 November 2026 and rejected. The menu-bar app in
  section 14 is a control panel on top of the container and the helper. It is not that app.
- **UDP** for application traffic in the first version. TCP only for that traffic. The macOS tunnel
  publishes one UDP port for WireGuard (section 3). That port is not application UDP.
- **Remote and cloud Docker contexts.** One local engine at a time.
- **Docker runtimes other than Docker Desktop, Docker Engine, and OrbStack on macOS** in the first
  version. OrbStack on macOS passed the tunnel checks on 25 November 2026 (section 16). Rancher
  Desktop, Podman, Colima, and rootless Docker may work, but are untested (section 12).

## 2. Why existing tools are not enough

No single existing tool does this on all three operating systems. A Docker Desktop extension alone
cannot either: extensions run only in Docker Desktop, not on Docker Engine on Linux, which is why
Whalias's extension is an optional window onto a core that runs anywhere Docker does.

| Tool | What it gives | What it lacks |
| --- | --- | --- |
| Traefik (Docker provider) | Automatic HTTP routes from container labels, using `defaultRule` | No local CA. It must share a network with each container. HTTP only on its own ports |
| caddy-docker-proxy | Caddy's `tls internal` issues trusted certificates per hostname | Each container needs labels. HTTP only |
| nginx-proxy | Routing from a `VIRTUAL_HOST` environment variable | Labels needed and certificates are manual. HTTP only |
| mkcert | A local CA installed in every OS's trust store | Only makes certificates |
| DDEV, Lando | The whole package for their own project types | Only for projects managed by those tools |
| localias | Trusted HTTPS for `*.test` aliases on Mac, Linux and WSL | Not aware of Docker. Aliases configured by hand |
| docker-mac-net-connect | Routes container IPs from macOS into Docker Desktop's VM over WireGuard | macOS only. Can break on Docker Desktop upgrades. No names or HTTPS |
| Colima `--network-address` | A VM address the Mac can reach, so container subnets can be routed | macOS only, and replaces Docker Desktop |

### Why a reverse proxy on 80 and 443 is not the answer

Every proxy-based tool above publishes ports 80 and 443 and routes by hostname. That has two problems:

- 80 and 443 may already be in use on the host.
- Only HTTP can be routed by name. Postgres, Redis, gRPC on its own port and other raw TCP services have
  no hostname in the connection, so they still need published ports, and published ports conflict.

### Why direct container IPs are not portable

OrbStack resolves names to the container's real IP, and its VM network is routable from the Mac. Plain
Docker only allows that in one setup:

- **Native Linux, rootful Docker:** bridge networks live on the host, so container IPs are reachable
  from the host on any port.
- **Rootless Docker:** containers sit in a separate network namespace that the host cannot reach.
- **Docker Desktop on macOS:** containers live in a Linux VM with no route from the host.
  docker-mac-net-connect or Colima can add one.
- **Docker Desktop on Windows (WSL2):** `route add` to the WSL VM's address is sometimes reported to
  work, but Docker Desktop's internal network layout changes between versions and WSL's mirrored
  networking mode changes it again. Not something to build on.

Docker Desktop's host networking option (`network_mode: host`) is no help: it puts the container on the
host's ports, which brings the conflicts back.

## 3. One address per container

Whalias needs **a separate IP address for each container**. How the host reaches that address depends
on the operating system.

**Linux** uses loopback addresses and Docker port publishing. **macOS**, decided 20 November 2026,
uses the tunnel below. **Windows is not decided.** Sentences in this document that describe a
Windows loopback path are earlier guesses. They are not a Windows product, and they are not a
decision to use the tunnel.

### Linux: one loopback address per container, published by Docker

Loopback addresses provide the separate address on Linux:

- Each container gets its own address in a block of loopback addresses, shared by all of its names:
  `web.myproject.wha.internal` and `myproject-web-1.wha.internal` might both be `127.87.0.6`, and
  `db.myproject.wha.internal` might be `127.87.0.7`.
- **Docker's own port publishing listens on that address** for every port the container exposes. A
  running container cannot gain published ports, so on Linux Whalias starts a small relay container
  per container that publishes them and forwards to the real container (section 6).
- Two containers that both expose 5432 never collide, because they are published on different
  addresses.

On Linux, a scaled compose service is the exception: its service name, which spreads connections
across the replicas, gets an address and relay of its own, besides one per replica (section 5). On
macOS a scaled service is not a relay. Per-connection balancing is not part of this macOS build, and
this document does not define a new way to spread one connection across replicas.

The Linux default block is `127.87.0.0/24`, the `WHALIAS_BLOCK` setting: `127.87.0.1` for Whalias's
DNS responder, `127.87.0.2` for the status page at `wha.internal`, and `127.87.0.3` to `127.87.0.254`
for containers, 252 of them. (87 is the character code of "W".) It avoids `127.0.1.x`, because
Debian and Ubuntu map the machine's own hostname to `127.0.1.1` and older Ubuntu releases ran a DNS
server on `127.0.1.1:53`, and it avoids `127.0.0.53` and `127.0.0.54`, which systemd-resolved uses.
The setup script can configure a larger or different block on Linux. `127.87.0.0/24` is not the macOS
default. On macOS the stable subnet is `10.254.253.0/24`. The status page is later work, not an
address in this macOS build, and it is not `127.87.0.2` on a Mac.

| OS | Extra loopback addresses |
| --- | --- |
| Linux | The whole `127.0.0.0/8` range is already loopback. No setup. This row is the Linux plan |
| Windows | Not decided. Earlier guess, left as written: the whole `127.0.0.0/8` range is already loopback, so no setup |
| macOS | Not used. macOS does not add `ifconfig lo0 alias` addresses and does not install a launchd job for a loopback block. The helper owns a `utun` and one route for `10.254.253.0/24` |

Linux uses the loopback model rather than routing to container IPs. Windows is not decided. The
earlier guess, left here on purpose, was that Windows would use the same model until it is tested.

### macOS, decided 20 November 2026: a tunnel

Spikes 21 and 22 replace the loopback model on macOS. The theory those spikes tested remains in
the local spike notes, which are not published with this repository. This section is the product. The `127.87`
aliases, the per-port host binds, relays, and the root bind of 80 and 443 are not the macOS plan.
There is no `ifconfig lo0 alias` block and no host bind of service ports on `127.87`. Linux is
unchanged and stays on loopback. Windows is not decided. Do not read the addresses and the HTTPS
rule in this subsection as a Linux or Windows product.

- The container is named `whalias`. This build's image tag is `whalias:local`. It runs
  `--privileged`. Kernel WireGuard runs inside the container, not `wireguard-go`: `ip link add wg0
  type wireguard`, address `10.254.254.2/32`, listen UDP 51820, MTU 1280. Forwarding is on
  (`ip_forward=1`). Reverse-path filtering is off on every interface, including ones that appear
  later. `MASQUERADE` applies on every output interface except `wg0`. The image includes `iproute2`,
  `iptables`, and `wireguard-tools`. Docker events are read from `.Action`, not `.Status`.
- It publishes two things on `127.0.0.1` only: DNS on port 5387, TCP and UDP, and one WireGuard UDP
  port, `127.0.0.1:<hostport>:51820/udp`. The helper reads `<hostport>` from Docker after the
  container is up. It does not publish the ports a project container listens on.
- It joins a project network when that network is created, and it stays until the network is deleted
  or the user leaves. It does not leave because `network inspect` omitted a stopped container. If a
  user disconnects it, it remembers that and does not reconnect until the network is created again.
  Every join puts the default route back on Whalias's own network. It does not join `host`, `none`,
  the default `bridge`, or its own `whalias` network.
- On macOS, Whalias's own network is one ordinary bridge named `whalias`. It is not `--internal`,
  not `10.250.0.0/16`, and not `whalias-edge`. Docker picks that bridge's subnet. The subnet must
  not be forced to `10.254.253.0/24` or `10.254.254.0/30`. Linux may still use the two-network
  description in sections 4, 7, and 9. Those Linux addresses are not merged with the stable subnet
  or the `/32` tunnel addresses.
- Each container has one stable address, shared by all of its names, in `10.254.253.0/24`. That
  subnet is the only route the Mac installs, and only the tunnel routes it. The address is not the
  container's Docker bridge address and it is not `127.87`. The Mac's tunnel address is
  `10.254.254.1/32` on the `utun`. The container's is `10.254.254.2/32` on `wg0`. Those three must
  not overlap. The Mac's peer endpoint is `127.0.0.1` plus the published UDP port, which is not
  inside a subnet routed into the tunnel. The Mac's AllowedIPs are `10.254.254.2/32` and
  `10.254.253.0/24`. The container peer's AllowedIPs are `10.254.254.1/32`. MTU is 1280 at both
  ends. Persistent keepalive is 25. The Mac does not route Docker bridge subnets.
- Identity is the compose project, service, and container number when those labels exist, and the
  container name otherwise. One stable address per identity, persisted in the data volume. Recreate
  keeps the address and the DNS record. Whalias changes only the DNAT destination. DNS is published
  as soon as the address is allocated, before that update returns, and it never publishes the bridge
  address. macOS held a missing name for at least 75 seconds in spike 13, so the address has to be
  published before lookup. A one-second SOA minimum does not promise a two-second recovery from
  NXDOMAIN on the Mac.
- On container move, DNAT points at the current bridge address. Do not run `iptables -F
  PREROUTING`. Replace one address's rules by deleting them by line number, then appending. DNAT
  reaches the port the process listens on. A Mac container does not have to `EXPOSE` or publish
  that port. When the HTTPS probe says to redirect, this TCP 443 rule is installed before the DNAT.
  UDP 443 is not redirected.

```
iptables -t nat -A PREROUTING -i wg0 -p tcp -d <stable> --dport 443 -j REDIRECT --to-ports 8443
```

- On macOS a scaled service is not a relay. Per-connection balancing is not part of this build.
  This document does not define a replacement.
- A root helper, a shell script run by launchd, owns the `utun`, the Mac's WireGuard peer, and the
  one route. The app's first launch installs the LaunchDaemon. A Docker Desktop extension does not:
  an extension runs as the desktop user, and Docker Desktop stops it when the dashboard closes. The
  label is `dev.whalias.tunnel`. `KeepAlive` is false or absent, so launchd does not restart the
  helper after it exits. `RunAtLoad` is true. The job runs as root, with no `UserName`. The plist
  is mode 644, `root:wheel`. Check modes with `/usr/bin/stat`, not GNU stat. Dates, if any, use
  `/bin/date`. The app ships `wireguard-go` and `wg` and uses an installed pair when that pair is
  at least as new as the shipped pair. The development scripts look only at
  `/opt/homebrew/bin/wireguard-go` and `/opt/homebrew/bin/wg`. The name `utun` lets the kernel pick
  the interface, and `WG_TUN_NAME_FILE` receives the real name. It routes only `10.254.253.0/24`
  at that interface. It does not enable IP forwarding on the Mac. This repository does not produce
  that script as a signed host binary, and the script is not a signed Mac app.
- The app talks to Docker as the user, through that user's `docker` client and the current context.
  It records the socket. The root helper reads the record. It does not look for
  `/Applications/Docker.app`, it does not run `context inspect desktop-linux`, and it does not use
  `command -v docker`. One local engine at a time. The development scripts still call the Docker
  Desktop binary. That path is not the product.
- Public keys share a bind-mounted directory. The container path is `/keys`. The helper writes
  `/keys/mac.pub` and keeps the private key at `/var/lib/whalias/mac.key`, mode 0600, root. The
  controller writes `/keys/controller.pub` and keeps its private key in the data volume, not in
  `/keys`. After those two public files exist, the only conversation is WireGuard. A stale
  handshake means the helper drops the route and the interface.
- When the container is not running, or Docker cannot be inspected, the helper deletes the route,
  removes `/var/run/wireguard/<utun>.sock`, destroys the `utun` with `ifconfig`, and exits. It does
  not kill processes. Launchd must not restart that exit. The same teardown runs when the helper
  exits and when Docker Desktop quits.
- Host DNS is `/etc/resolver/wha.internal` with `nameserver 127.0.0.1` and `port 5387`. The
  nameserver is not `127.87.0.1`. Answers are addresses in `10.254.253.0/24`, never the bridge
  address.
- HTTPS follows section 10, inside the container. The proxy listens on `127.0.0.1:8443`. If TCP 443
  on the bridge address accepts a connection, the handshake passes through and the client sees the
  container's certificate. Otherwise the controller redirects that TCP connection to its proxy,
  which presents the Whalias certificate and forwards plain HTTP to `dev.whalias.http-port` when
  that label is a port, or to the first of 80, 8080, 3000, 5000, and 8000 that accepts. If none
  accept, there is no proxy. The name has to resolve to the stable tunnel address. The CA is
  name-constrained to `wha.internal`. The app's first launch adds the public certificate to the
  System keychain. TCP port 80 is not a host bind and not a redirect. DNAT
  delivers it like any other listened port. The only redirect is TCP 443, and only when the probe
  says so.

## 4. Architecture

### Components

| Component | Where it runs | Responsibility |
| --- | --- | --- |
| Whalias.app and the `whalias` command | The Mac, as the logged-in user. The command is `/usr/local/bin/whalias`, installed by the app | The app is the macOS install. First launch writes the config, the DNS registration, the tunnel helper, the controller, and the public CA in the System keychain, and installs the command. The command lists names, opens one, and uninstalls. There is no `whalias doctor`. Linux still uses a setup script for its networks and its root certificate. Windows setup is not decided |
| macOS tunnel helper | The Mac, root, under launchd | A shell script, label `dev.whalias.tunnel`. Runs `wireguard-go -f utun` with `WG_TUN_NAME_FILE`. Owns the `utun`, the Mac's WireGuard peer, and the one route for `10.254.253.0/24`. `KeepAlive` is absent. When the controller is gone it deletes the route, removes the WireGuard socket, destroys the `utun`, and exits, and launchd does not restart that exit. Not used on Linux. Not a signed app, and not an `lo0` alias job. Login Items shows it as `sh` until the menu-bar app in section 14 is installed and the plist names that app in `AssociatedBundleIdentifiers` |
| Controller container (`whalias`) | Docker, with the Docker socket mounted | Watches Docker events, keeps the name table and sticky addresses, answers DNS for `wha.internal`, and runs the local CA. On macOS it is privileged, terminates WireGuard, joins project networks, and DNATs to the current bridge address. It does not create relays. On Linux it also creates and removes relays and manages attachments to the shared network (section 9). The status page is later, not this macOS build |
| Relay containers (`whalias-<container>`) | Docker, Linux plan: one per container (plus one per scaled service), no socket and no privileges | Linux only. Publish one container's exposed ports on its loopback address, terminate HTTPS on 443, and copy bytes to the container. Not the macOS data path. On macOS, HTTPS is inside the controller (section 10). Windows is not decided; the earlier guess used relays too |
| `whalias` network on macOS | Docker, one ordinary bridge | The network the controller is created on. Not `--internal`, not `10.250.0.0/16`, and not `whalias-edge`. Docker picks the subnet. It is not `10.254.253.0/24` and not the `/32` tunnel addresses. After every project-network join, the default route returns here |
| `whalias` network on Linux | Docker, `--internal`, `10.250.0.0/16` | The shared-network alias plan (section 9). Out of this macOS build. Every container joins it, carrying its `wha.internal` names as aliases. Relays reach their targets over it. This is also the Linux host path's internal network, not a replacement for loopback addresses |
| `whalias-edge` network | Docker, an ordinary bridge, Linux | Relays and the controller join it, because Docker does not publish ports for a container attached only to an `--internal` network. Not used on macOS. Windows is not decided |
| Docker Desktop extension | Docker Desktop on macOS and Windows (and Linux, where Docker Desktop is used) | Later. Not this macOS build. A tab listing names, with Open and Copy. It does not install or run the macOS tunnel helper. The Mac window is the health view |
| Menu-bar management app | The logged-in user's session on macOS. A Windows tray app is later. Linux uses the CLI | Decided 25 November 2026. Not started. Shows Docker, the controller, the tunnel, and names, and runs the management actions in section 14. Quitting it leaves the helper and the controller running. The plan is [docs/host-app.md](docs/host-app.md) |

### Data path from the host

Linux loopback. This drawing is the Linux plan. It is not the macOS plan. Windows is not decided
and is not drawn here. An earlier guess still describes Windows as a Docker publish of the service
port. That guess is not a decision.

```text
Host (Linux)                                     Docker engine
────────────                                     ─────────────
psql -h db.myproject.wha.internal -p 5432
  │  OS resolver → controller on 127.87.0.1:5387 → 127.87.0.7
  ▼
127.87.0.7:5432  Docker publish of the service port
  │
  ▼
relay whalias-myproject-db-1
  │  resolves myproject-db-1.wha.internal
  │  (Docker DNS → 10.250.0.4), dials :5432
  ▼
db container
```

macOS tunnel, decided 20 November 2026. No relay, no `127.87` address, no root bind of 80 or 443.

```text
Host (macOS)                                          Docker Desktop VM
────────────                                          ─────────────────
psql -h db.myproject.wha.internal -p 5432
  │  /etc/resolver/wha.internal → 127.0.0.1:5387
  │  answer: a stable address in 10.254.253.0/24
  ▼
stable address:5432
  │  route 10.254.253.0/24 at the utun (10.254.254.1/32)
  ▼
WireGuard UDP to 127.0.0.1:<published port>
  │
  ▼
controller (privileged, wg0 at 10.254.254.2/32)
  │  DNAT to the container's current bridge address
  │  TCP 443, when the probe says so, redirects to 127.0.0.1:8443 first
  ▼
db container, on the project network Whalias joined
     the process's own port; nothing publishes that port on the Mac
```

The first drawing is the Linux loopback model. It is not the macOS plan. macOS uses the tunnel in
section 3 and the second drawing. Windows is not decided. It is still described, as a guess, as a
Docker publish of the service port, the way the Linux drawing publishes that port. Linux publishes
natively with no VM. Neither Linux nor Windows has run spike 4.

### Data path between containers

This drawing is the shared-network alias plan (section 9). It is the Linux plan. It is out of this
macOS build and is not required for the tunnel. macOS host traffic does not use these aliases.

```text
api container (myproject)                       db container (otherproject)
  │  Docker's built-in DNS (127.0.0.11):
  │  db.otherproject.wha.internal → 10.250.0.9 (alias on the whalias network)
  └──────────── whalias network (internal) ────► 10.250.0.9:5432
```

No Whalias process is involved in that container-to-container traffic. Docker's own DNS and network
do it all, so it keeps working if the controller stops, until containers are recreated. On macOS the
host path is the tunnel above, not this drawing.

## 5. Discovery and naming

The controller watches the Docker events API through the mounted socket. On each container start,
stop, rename and network change, it updates the name table. On macOS it also updates the sticky
stable address, the DNAT destination, and project-network joins. It does not create relays, and it
does not attach every container for section 9 aliases. That attachment work is the Linux plan and
is out of this macOS build. On Linux it updates the relays and the attachments.

### Names

| Source | Name |
| --- | --- |
| A compose service | `<service>.<project>.wha.internal`, from the `com.docker.compose.service` and `com.docker.compose.project` labels |
| Any container | `<container-name>.wha.internal`. Underscores and other characters not allowed in hostnames become hyphens, so `wizardly_banzai` becomes `wizardly-banzai.wha.internal` |
| A scaled compose service | `<service>-<n>.<project>.wha.internal` reaches replica `n`. On macOS each replica is its own identity and gets its own stable address. The service is not a relay, and per-connection balancing is not part of this build. This document does not define a new way to spread one connection. On Linux, once a service has more than one replica, `<service>.<project>.wha.internal` moves to an address and a relay of its own. That relay resolves the shared alias on every connection and picks one of the replicas' addresses at random |
| Custom names | The `dev.whalias.domains` label, a comma-separated list of names under `wha.internal` |
| OrbStack compatibility | OrbStack's `dev.orbstack.domains` label is honored, with `orb.local` rewritten to `wha.internal` |
| Whalias itself | `wha.internal` and `whalias.wha.internal` are reserved: a container or `dev.whalias.domains` label claiming either is refused. The status page that would serve them is later. It is not this macOS build, and on a Mac it is not `127.87.0.2`. On Linux the page is the second address of `WHALIAS_BLOCK` (section 7) |

### Name collisions

Container names may contain dots, so a container named `web.myproject` claims the same name as service
`web` in project `myproject`, and custom-name labels can overlap. When two containers claim one name,
the claim from the higher source wins:

1. Reserved names (`wha.internal`, `whalias.wha.internal`), which no container gets.
2. `dev.whalias.domains` and `dev.orbstack.domains` labels.
3. Compose service names.
4. Container names.

Between claims from the same source, the container that started first keeps the name. The other loses
only that name, keeps the rest, and gets a warning in the controller log and in the window. When the
winner stops, the name moves to the loser.

### Labels

| Label | Effect |
| --- | --- |
| `dev.whalias.domains` | Extra names for the container |
| `dev.whalias.ports` | Linux: extra TCP ports for the relay to publish, for ports the image does not `EXPOSE`. On macOS the tunnel does not consult this label. DNAT reaches the port the process listens on, without `EXPOSE` and without a publish |
| `dev.whalias.http-port` | The port the HTTPS proxy forwards to, when there is a proxy, overriding the automatic choice |
| `dev.whalias.attach=false` | Linux shared-network plan (section 9): don't attach this container to the `whalias` network. Out of this macOS build. The macOS controller still joins project networks for the tunnel |
| `dev.whalias.role` | Marks Whalias's own objects so the event watcher ignores them. `controller` and `network` apply on macOS. `relay` is the Linux relay. macOS does not create relay containers. The helper is a host script, not a container role. This is not a Windows role set |

### Sticky addresses

An address belongs to a container's identity, not to its ID, so it survives container recreates,
Docker restarts and reboots, and bookmarks and connection strings stay valid. On macOS the identity
is the compose project, service, and container number when those labels exist, and the container
name otherwise. One stable address in `10.254.253.0/24` per identity. It is never the bridge
address. `WHALIAS_BLOCK` is not the Mac pool. On Linux the identity is project, service and replica
number for a compose container, project and service for a scaled service's own address, and the
container name otherwise, allocated from `WHALIAS_BLOCK` (default `127.87.0.0/24`). Windows is not
decided. The table lives in the controller's `whalias-data` volume. Addresses are freed after an
identity has been absent for 30 days (the `WHALIAS_RETENTION_DAYS` setting). When the pool is full,
the controller says so in its log and in the window, rather than reusing an address that is
still remembered. The status page that would also say so is later.

### Which ports

On macOS, DNAT to the current bridge address reaches the port the process is listening on. The
container does not have to `EXPOSE` the port, and it does not have to publish it, for the tunnel to
work. Whalias does not bind that port on `127.87`, and it does not bind 80 or 443 as root. Traffic
enters the `utun`.

On Linux, a container's relay publishes every TCP port in its exposed ports: the image's `EXPOSE`
instructions, compose `expose:` and `ports:`, and the `dev.whalias.ports` label. It cannot see a
port the app listens on without declaring it. See *Caveats*. A port it cannot publish is reported
with what holds it. Windows is not decided; the earlier guess followed this expose limit too.

When something already holds a port on all interfaces, most often the project's own `ports:` mapping
such as `"5432:5432"`, Docker on Linux may refuse to publish the same port on the container's
loopback address. The controller then publishes the relay's other ports without it, and reports the
skipped port, and what holds it. That skip rule is the Linux relay. It is not
a macOS host bind.

The next paragraphs are the loopback experiment on Docker Desktop. They are superseded on macOS by
section 3. They are not an instruction to bind `127.87` on the Mac, and they are not the listen path.

On Docker Desktop 4.99.0, a Docker publish of a specific loopback address failed (spike 04,
20 November 2026). A publish of `127.87.0.120:25432` made a later `docker run -p 25432:25432` and a
`docker compose up` with `ports: ["25432:25432"]` fail with `Bind for 127.87.0.120:25432 failed:
port is already allocated`. The user's container never started.

A Mac process bound only to an alias did not do that (spike 19, same day, port 25460,
`SO_REUSEADDR` off). Both a `docker run -p` and a compose `ports:` mapping started. `127.0.0.1`
reached the project. In that spike the alias reached the Mac process. That bind is not the macOS
product. Traffic on macOS enters the `utun`.

Spike 19 tried the relay-first order, which is the order that broke compose. A native listener on
`0.0.0.0:25432` also coexisted with a Docker publish of the alias in spike 04. Those results stay
as history. Linux still publishes the service port on the loopback address and skips a port it
cannot take. macOS does not.

## 6. Relay containers

This section is the Linux relay plan. It is not the macOS data path. On macOS the privileged
controller terminates WireGuard, DNATs to the current bridge address, and terminates HTTPS itself
(sections 3 and 10). macOS does not start a `whalias-<container>` container, does not bind
`127.87`, and does not bind 80 or 443 as root. Windows is not decided. Where this section mentions
Windows, that is the earlier guess, left as written.

### What a relay is

On Linux, one small container per container, started by the controller beside the container it
serves, and named `whalias-<container name>`. A scaled service's own relay is named
`whalias-<project>-<service>`. macOS has neither.

```bash
# Linux only. What the controller does for myproject-db-1 (illustrative; the controller uses the
# Docker API). macOS does not run this container and does not bind 127.87.0.7:5432.
# Windows is not decided. The earlier guess was that Windows would publish the service port
# directly, the same shape as Linux, until its own spike 4.
docker run -d --name whalias-myproject-db-1 --restart=always \
  --network whalias-edge \
  -p 127.87.0.7:5432:5432 \
  -e WHALIAS_RELAY_TOKEN=<random, per relay> \
  --label dev.whalias.role=relay \
  --label com.docker.compose.project=whalias \
  ghcr.io/whalias/whalias@sha256:<digest> relay --to myproject-db-1.wha.internal
docker network connect whalias whalias-myproject-db-1
```

A database exposes no HTTP port, so this Linux relay publishes no 80 or 443 (section 10).

- **Linux accepts the connection** by publishing the service port on the container's loopback
  address. The old macOS path, a process that bound `127.87.0.7:5432` and forwarded to a backend
  port, is withdrawn. Spike 04 showed that a Docker publish of that address on Docker Desktop broke
  `docker compose up`. That result is history (section 15). It is not a reason to bind `127.87` on
  the Mac. Windows is still described, as a guess, as publishing the service port the way Linux does.
- **Two networks, Linux only.** `whalias-edge` makes the published ports work, because Docker does
  not publish ports for a container attached only to an `--internal` network. The `whalias` network
  is how the relay reaches its target. macOS uses one ordinary bridge named `whalias` for the
  controller's own network (section 3), not this pair.
- **It targets a name, not an IP.** On Linux the `--to` name is an alias on the `whalias` network,
  resolved through Docker's DNS on every new connection, so a target that restarts with a new IP
  needs no new relay. For a scaled service the name has one address per replica, and the relay
  picks one at random per connection. macOS does not use this relay, and per-connection balancing
  is not part of the macOS build.
- **It copies bytes** between each accepted connection and the same port on its target, with no
  protocol parsing except on 443.
- **Grouped in Docker Desktop.** The shared `com.docker.compose.project=whalias` label shows every
  relay under one "whalias" group instead of scattering them through the container list. Whether that
  label also makes `docker compose` treat the relays as a project is a spike item (section 15); if it
  does, the label is dropped.
- **Recreated when the target's ports change,** which drops open connections for that one container.
  Also recreated by the controller if it disappears or stops.

### TLS on 443

On macOS this subsection is not the product. The controller probes whether TCP 443 accepts, and
either passes the handshake through or proxies it (section 10). There is no relay certificate fetch
and no root bind of 80 or 443.

On Linux:

- If the container exposes 443 itself, the relay passes the TLS through untouched.
- Otherwise, if the container has an HTTP port (section 10), the relay terminates TLS with one
  certificate listing all of the container's names, and forwards plain HTTP to that port. It fetches
  the certificate from the controller's internal API (section 7), authenticated by its
  `WHALIAS_RELAY_TOKEN`, and fetches a new one when two thirds of its lifetime has passed. The
  controller generates the token when it creates the relay and forgets it when it removes the relay.
- If the container has neither 443 nor an HTTP port, the relay publishes no 443 and no 80.
- Whenever 443 is published, port 80 is published too. It forwards to the container's port 80 when the container
  exposes one, and otherwise redirects to HTTPS. One rule, not a setting. That port 80 rule is
  Linux. On macOS, port 80 is DNAT to the listened port, and the only redirect is TCP 443 when the
  probe says so.

Spike 20, on 20 November 2026, showed that a root process on the Mac can bind `127.87.0.200:80` and
`127.87.0.201:80` at the same time, each address returning its own marker, with `127.0.0.1:80`
silent. Port 443 did the same. One such bind blocked a later wildcard publish. That spike is
history (section 15). It is not how macOS serves 80 and 443. macOS does not bind either port as
root, on an alias or otherwise. The window does not ask for that bind.

### Security

- The Linux relay has no Docker socket, no added Linux capabilities, and is not privileged.
- On Linux the user-facing socket is the container's loopback address. Spike 01 confirmed a Docker
  publish does that for a high port. Spikes 02 and 18 showed a Docker publish of 80 or 443 listening
  on every interface, so the Linux relay is not asked to publish those two ports through that
  failing Desktop path. Spike 20's root bind is not the macOS product. On macOS the user-facing
  path is the stable tunnel address.
- A Linux relay dials only its one target name, so it cannot be used to reach anything else.

### Image and limits

- On Linux the relay and the controller are one image, `ghcr.io/whalias/whalias`, run as `relay` or
  `controller`: a single static Go binary on `scratch`, a few MB, for `linux/arm64` and `linux/amd64`.
  macOS runs the privileged controller. It does not run a `relay` mode. This build's tag is
  `whalias:local`. The image still includes what section 3 lists for the tunnel.
- Pulled by digest, so every Linux relay runs exactly the image the controller was started from.
- On Linux, containers see the relay's address as the client, not the real client. Visible in
  access logs and IP allow-lists. On macOS a sibling sees Whalias's address on that project network.
  There is no relay container.
- On macOS the path is the Mac, the `utun`, the controller, then DNAT to the container. On Linux it
  is Docker's port forwarder, the relay, then the container. Windows is not decided.
- On Linux each relay is cheap, but a few dozen containers means a few dozen relays and
  published-port sets. To verify (section 15). That limit is not a macOS concern. macOS does not
  start those relays.

## 7. The controller

On macOS, setup creates one ordinary bridge and starts a privileged controller. Nobody types it.
The bridge is not `--internal`, not `10.250.0.0/16`, and not `whalias-edge`. Docker picks its
subnet. `<keys>` is the public-key directory on the Mac. `<hostport>` is the published WireGuard
port, read back from Docker after the container is up.

```bash
docker network create --label dev.whalias.role=network whalias

docker run -d --name whalias --restart=always --privileged \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v whalias-data:/data \
  -v <keys>:/keys \
  --network whalias \
  -p 127.0.0.1:5387:5387/udp -p 127.0.0.1:5387:5387/tcp \
  -p 127.0.0.1:<hostport>:51820/udp \
  --env-file /etc/whalias/config.env \
  --label dev.whalias.role=controller \
  whalias:local controller
```

The socket mounted above is the one the app recorded from the user's current Docker context. It is
not read from `/Applications/Docker.app`, and it is not a path baked in from one machine. The image
in a release is `docker.io/whalias/whalias` at that release's digest. A development setting builds
from a cloned repository instead. The `whalias:local` tag in the command above is that local build.

On Linux, setup still creates both networks and publishes DNS on `127.87.0.1`. This command is not
the macOS one:

```bash
docker network create --label dev.whalias.role=network whalias-edge
docker network create --internal --subnet 10.250.0.0/16 --label dev.whalias.role=network whalias

docker run -d --name whalias --restart=always \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v whalias-data:/data \
  --network whalias-edge \
  -p 127.87.0.1:5387:53/udp -p 127.87.0.1:5387:53/tcp \
  --env-file /etc/whalias/config.env \
  --label dev.whalias.role=controller \
  ghcr.io/whalias/whalias@sha256:<digest> controller
```

On macOS the app runs the first command. On Linux the setup script runs the second. Nobody types
either. Windows is not decided. The earlier
guess, left as written, is that the published DNS port is 53 instead of 5387 (section 8).

- **Responsibilities, macOS:** watches Docker events, keeps the name table and sticky stable
  addresses, answers DNS, runs the local CA, joins project networks, and DNATs to the current
  bridge address. It does not create relays. Attaching every container to `whalias` for aliases is
  section 9, out of this build. The status page is later.
- **Responsibilities, Linux:** the same name table, plus creating and removing relays, attaching
  every container to the internal `whalias` network, and issuing relays' certificates.
- **Its own network.** On macOS the controller is created on the ordinary `whalias` bridge. It does
  not add the status-page aliases in this build. On Linux, on start it connects itself to the
  internal `whalias` network with the aliases `wha.internal` and `whalias.wha.internal`, so relays
  can reach its internal API, and so a container looking up `wha.internal` gets the controller
  rather than a host loopback address that would point back at itself. On Linux it checks that both
  networks exist and recreates any that are missing. Windows is not decided.
- **`whalias-data` volume:** the CA's private key and the address table. Losing it means a new
  root. On macOS the app's next launch trusts the new public certificate. On Linux the setup script
  trusts it again. Settings are not kept here (see *Settings*).
- **DNS.** The port number is 5387 on macOS and on Linux (`WHALIAS_DNS_PORT`). On macOS the
  process listens on 5387 and the publish is `127.0.0.1:5387`, TCP and UDP, which is where
  `/etc/resolver/wha.internal` points. The nameserver is not `127.87.0.1`. On Linux the process
  listens on 53 inside the container and the publish is `127.87.0.1:5387`. Windows is not decided.
  The earlier guess, left as written, is port 53 on the host, because Windows' per-domain DNS rules
  (NRPT) can only send to port 53. 5387 is an arbitrary choice. An unprivileged host port is what
  macOS uses, so no privileged-port helper is needed. macOS does not bind 80 or 443 as root.
- **On macOS it also publishes one WireGuard UDP port** on `127.0.0.1`. On Linux it publishes
  nothing but DNS. It holds the Docker socket, so nothing else about it is reachable from the host
  except those published ports.

### Settings

Settings live in one file on the host, written by setup: `/etc/whalias/config.env` on macOS and Linux,
`C:\ProgramData\Whalias\config.env` on Windows. It is a list of `KEY=value` lines, passed to the
controller with `--env-file`. The host holds them, not the volume, because several of them also shape
host-side setup that only the script can change with admin rights.

| Setting | Default | Also changes on the host |
| --- | --- | --- |
| `WHALIAS_DOMAIN` | `wha.internal` | DNS registration, root certificate (name constraint) |
| `WHALIAS_BLOCK` | `127.87.0.0/24` | Linux loopback addresses and Linux DNS registration. Not the macOS default, and not a macOS `lo0` alias block. macOS stable addresses are `10.254.253.0/24`. Windows is not decided |
| `WHALIAS_DNS_PORT` | `5387` | DNS registration on macOS and Linux. On macOS the nameserver address is `127.0.0.1`, not `127.87.0.1`. Windows port 53 is an undecided guess, left as written in section 8 |
| `WHALIAS_SUBNET` | `10.250.0.0/16` | Linux shared-network recreation (section 9), so every Linux attachment is redone. Not the macOS stable subnet, and not merged with `10.254.253.0/24` or the `/32` tunnel addresses. On macOS, Docker picks the `whalias` bridge subnet |
| `WHALIAS_ATTACH` | `all`, or a comma-separated list of compose projects | Nothing |
| `WHALIAS_RETENTION_DAYS` | `30` | Nothing |

`whalias config get` prints them. `whalias config set KEY=value` validates the value, writes the file,
redoes the host-side setup that the setting affects (asking for admin rights if it needs them), and
recreates the controller.

### Internal API

On Linux the controller serves one HTTP server on port 8087, reachable only from containers on its
two networks. On macOS there is no relay certificate API. The controller signs the proxy certificate
itself (section 10). The status page is later, not this build.

| Path | Used by | Access |
| --- | --- | --- |
| `/` | The status page, later. On Linux, through its relay | Anyone on the Linux networks; read-only. Not this macOS build |
| `/api/v1/names` | The status page; the same JSON as `whalias ls --json` | Anyone on the Linux networks; read-only. On macOS, `whalias ls` is `docker exec` |
| `/api/v1/relay/certificate` | Linux relays, for their certificate and chain | `Authorization: Bearer <WHALIAS_RELAY_TOKEN>`; returns only that relay's certificate. Not the macOS path |

The `whalias` command and the extension reach the controller with `docker exec whalias whalias <cmd>`,
where the same binary has subcommands that print JSON with a `schemaVersion` field:

| Subcommand | Prints |
| --- | --- |
| `ls --json` | Every name with its container, source, and address. On Linux, also published ports, skipped ports, and relay health. On macOS, the stable address and the current DNAT destination, not relay health and not a host publish of the service port |
| `status --json` | Version, image digest, settings in effect, warnings |
| `ca` | The root certificate in PEM, so the script does not depend on `docker cp`. On macOS this build, the file is for `curl`, not for the System keychain |
| `version` | Version. The relay protocol version is the Linux plan, not this macOS build |
- **The status page** is later. It is not this macOS build. Do not give it `127.87.0.2` on a Mac,
  and do not start a relay for it. On Linux, when it is built, `https://wha.internal` is the domain
  itself, as OrbStack's is `orb.local`, published through a relay on the second address of
  `WHALIAS_BLOCK` (`127.87.0.2`), forwarding to the internal API's port 8087. That Linux description
  is not the macOS path. The names `wha.internal` and `whalias.wha.internal` stay reserved either way.
- **Starts with Docker.** `--restart=always` brings it back after Docker Desktop restarts and reboots.
  On macOS it reconciles DNAT, project-network joins, and DNS. It does not recreate relays. On Linux
  it reconciles relays, networks, and attachments that are missing, and removes orphaned ones.
- **The Docker socket** is root-equivalent on the Docker host. That is the same power any tool driving
  Docker has, including Traefik and the host app considered in section 14. What is specific to running
  it in a container is the supply chain, so the image is signed and the app pulls it by digest.

## 8. DNS on the host

The controller answers A records (and AAAA with "no data") for `wha.internal` itself and
`*.wha.internal`, using the name table, and refuses everything else. It only has to answer one zone, so
it is a few hundred lines with `miekg/dns` rather than a general DNS server.

Answers carry a TTL of 5 seconds. Names that don't exist get NXDOMAIN with an SOA record whose minimum
is 1 second. That minimum does not promise a fast recovery on macOS. Spike 13: macOS held a missing
name for at least 75 seconds. On macOS the stable address is published before lookup, and the answer
is never the bridge address. The one-second minimum remains the responder value. It is not a
two-second Mac gate.

The Mac app, or the Linux setup script, registers it for `wha.internal` once:

| OS | Registration |
| --- | --- |
| macOS | `/etc/resolver/wha.internal` containing `nameserver 127.0.0.1` and `port 5387`. Not `nameserver 127.87.0.1`. Answers are stable addresses in `10.254.253.0/24`, never the bridge address |
| Windows | Not decided. Earlier guess, left as written: a Name Resolution Policy Table rule, `Add-DnsClientNrptRule -Namespace ".wha.internal","wha.internal" -NameServers 127.87.0.1`, on port 53, the only port NRPT can use. The leading-dot namespace is a suffix and matches the names under the domain; the second matches the domain itself, for the status page. This guess is not a decision, and it is not the macOS resolver |
| Linux | A systemd-resolved drop-in, `/etc/systemd/resolved.conf.d/whalias.conf`, with `DNS=127.87.0.1:5387` and `Domains=~wha.internal` (the `address:port` form needs systemd 246 or later). Untested: a global `DNS=` server may also receive lookups that no network link claims. If it does, the fallback is a dummy network link carrying the server and the `~wha.internal` routing domain, with `DNSDefaultRoute=false`. NetworkManager's dnsmasq plugin is the alternative where resolved is not used |

### Domain choice: `wha.internal`

Chosen on 19 November 2026.

- **`.internal`** was permanently reserved by ICANN for private use on 29 July 2024 (Board resolution
  2024.07.29.06), so it will never be delegated as a real top-level domain. It is resolved by ordinary
  DNS, so all three operating systems use the same mechanism (the registration above) and `dig` and
  `nslookup` see the names. Chromium's address bar treats `.internal` names as addresses rather than
  searches, as it does `.test` and `.local`. Chrome on this Mac did that for `wha.internal` and for
  `web.myproject.wha.internal` (spike 03). Safari searched for the bare name; the system resolver
  still returned its address. It also sits naturally beside Docker's own
  `host.docker.internal`.
- **`wha`** is the first three letters of the name, as OrbStack's `orb` is of its own. A per-domain
  registration takes over only `wha.internal`, so it can only clash with a network that uses that
  exact label, and an unusual label makes that unlikely. `fin`, `pod` and `ali` were considered:
  `fin` is a plausible finance label on a company's internal DNS, and `pod` would be confused with
  Kubernetes pods.
- The suffix and label are a setting, with `wha.internal` as the default.

Rejected:

- **`.local`**, which OrbStack uses. It belongs to multicast DNS (RFC 6762): OrbStack registers its
  names with macOS's mDNSResponder (Bonjour), which is why `dig web.x.orb.local` finds nothing while
  browsers work. Supporting it means a different mechanism per OS: Bonjour registration on macOS; an
  explicit systemd-resolved routing domain on Linux, relying on nss-mdns's `mdns4_minimal` treating
  names of more than two labels as not its own; and an NRPT rule on Windows. Its one advantage, no
  admin rights for DNS on macOS, is worth nothing here, because setup needs admin rights anyway for the
  tunnel helper, the `utun`, the route, and the resolver file. This macOS build does not add the CA
  to the System keychain. And many companies name Active Directory domains
  `something.local`.
- **`.test`**, reserved by RFC 6761 and technically as good as `.internal`, but it reads as throwaway.
- **`.localhost`.** Chrome and Firefox hard-code every `*.localhost` name to `127.0.0.1` and `::1`,
  ignoring DNS and the hosts file, which would put every name on one address and bring back the port
  conflicts section 3 exists to avoid.
- **A top-level domain of our own, `.whalias`.** Shortest and on-brand, but not reserved, so it could
  be sold in a future round of new top-level domains (as `.dev` was, breaking Laravel Valet and Pow,
  which used it). Browsers search rather than navigate for unknown top-level domains until the user
  types `https://` or a trailing slash, and link detectors and URL validators reject them.
- **`.dev`, `.app` and other unreserved strings** such as `.dock`, `.lan` or `.box`, for the same
  reasons. `.dev` and `.app` are real top-level domains on the browsers' HSTS preload list.
- **`local.whalias.dev`.** Workable with local DNS, but two traps. The whole `.dev` top-level domain is
  HSTS-preloaded, so browsers rewrite `http://name.local.whalias.dev:3000` to `https://` and fail, unless
  relays terminate TLS on every port by sniffing for a TLS handshake. And on any machine where Whalias is
  not running, lookups fall through to public DNS, so whoever holds `whalias.dev` decides where those
  names go: if the domain ever lapsed, a `psql` connection would hand its password to the new owner.
- **Public DNS doing the routing** (a wildcard `*.local.whalias.dev` at Cloudflare, so nothing on the
  host needs configuring). Public DNS cannot know each user's addresses without encoding them in the
  name, running a registration service every controller reports to, or hashing names into a small
  block where they collide. macOS does not need a loopback block. The tunnel is the macOS path.
  Many routers and resolvers
  (dnsmasq, pfSense, NextDNS, corporate DNS) drop public answers pointing at private addresses, as a
  DNS-rebinding defense. And it fails offline and leaks every project name to public resolvers.

### Hosts-file fallback

Where something overrides per-domain DNS (see *Caveats*), the window writes the current names,
`wha.internal` included, into `/etc/hosts` (or `C:\Windows\System32\drivers\etc\hosts`) in a clearly marked block, and
removes that block when the switch is turned off. On macOS the written addresses are the stable tunnel addresses, not
`127.87` addresses. This is not a loopback install step. Nothing on the host watches for new names, so the block is a snapshot:
turn it on again after starting new containers. The window says when the block is stale. A weak
fallback by design, for machines where nothing else works.

## 9. DNS and routing inside containers

This section is the shared-network alias plan. Linux still expects it. It is out of this macOS
build. The tunnel does not require it: host clients resolve the stable address, and the controller
DNATs to the container's current bridge address on the project network it has joined. Do not read
this section as a macOS host path, and do not delete it as if Linux had dropped it.

### Rejected: a Whalias container as every container's DNS server

It can be done, but:

- A container's DNS server is fixed when it is created (`dns:` in compose, `--dns`). Changing it means
  editing every project or setting `"dns"` in the daemon configuration, which on Docker Desktop is a
  settings change and a Docker restart.
- Every other lookup, including Docker Desktop's own names such as `host.docker.internal`, would have to
  be forwarded correctly, and a Whalias container that is down or slow would break or slow DNS in every
  container.
- A DNS answer is useless unless the asking container can route to it, and Docker's isolation rules
  block traffic between different bridge networks. A shared network would still be needed, and once it
  exists Docker's own DNS does the job.

### Chosen: a shared internal network with aliases

The controller creates one network at start-up:

```bash
docker network create --internal --subnet 10.250.0.0/16 \
  --label dev.whalias.role=network whalias
```

When a container starts, the controller attaches it to the network under its names. Docker allows this
on a running container: it adds a network interface without restarting anything.

```bash
docker network connect \
  --alias web.myproject.wha.internal \
  whalias myproject-web-1
```

What that gives:

- **Every container can resolve every name.** Docker's built-in DNS (`127.0.0.11`) answers aliases on
  any network the asking container belongs to, and every container belongs to `whalias`.
- **Each name points at the container itself,** at its own IP on `whalias`, so all ports work and
  nothing conflicts.
- **`--internal` keeps routing unchanged.** An internal network has no gateway, so it never becomes a
  container's default route, and adding it cannot change how the container reaches the internet or its
  own project network.
- **No custom DNS and no daemon configuration change.**

Inside containers, use `http://web.myproject.wha.internal:8080`. From the host, use
`https://web.myproject.wha.internal`. That difference is the accepted trade-off recorded in section 1.

Putting the aliases on the relays instead would make HTTPS inside containers match the host, but a
relay's own network address is not the target's, so every caller would pass through it, and its client
address rewrite would apply inside Docker too. Not worth it while HTTPS inside containers is out of
scope. That paragraph is the Linux leftover. It is not a reason to start relays on macOS.

## 10. HTTPS from the host

### The local CA

- A root CA generated by the controller on first start with Go's `crypto/x509`, kept in the
  `whalias-data` volume.
- **Name-constrained to `wha.internal`**, so even if the private key leaks it cannot sign for any other
  domain. A DNS name constraint permits the domain itself as well as every name under it (RFC 5280), so
  the status page's certificate for `wha.internal` is within it. Modern browsers and OS certificate verifiers enforce name constraints on roots; mkcert's CA
  has none.
- The private key never leaves the volume. Only the root certificate is copied to the host.

### Certificates per container

On macOS the controller terminates TLS itself, or passes the handshake through. It does not issue
a certificate to a relay. On Linux the controller issues each relay one certificate listing exactly
its container's names, signed by the root, valid for 30 days, and renewed after 20. A name gained
or lost means a new certificate, which the relay picks up without restarting.

Wildcard certificates are not used: a wildcard covers only one level, and per-container
certificates avoid the question entirely. The CA stays name-constrained to `wha.internal`.

### Where 443 goes

On macOS the probe connects to the bridge address on TCP 443. It does not ask whether the port is
exposed.

- A successful TCP connect means pass-through. The client sees the container's certificate. No
  redirect is installed.
- Otherwise, if `dev.whalias.http-port` is a port, the probe tries that port. Else it tries 80,
  8080, 3000, 5000, and 8000, in that order. The first successful TCP connect means a proxy: the
  controller redirects TCP 443 to `127.0.0.1:8443` before the DNAT, presents the Whalias
  certificate for that name, and forwards plain HTTP to the bridge address on that port.
- If none of those ports accept, there is no proxy. No listener is invented.
- UDP 443 is not redirected. The proxy does not handle UDP.
- TCP port 80 is not a separate redirect and not a root bind. DNAT delivers it to the port the
  process listens on.
- HTTP/2, WebSockets and gRPC pass through the proxy when a proxy exists.

On Linux the relay still decides from the expose list: if the container exposes 443, the relay
passes TLS through; otherwise it terminates and forwards to `dev.whalias.http-port` or the first of
80, 8080, 3000, 5000, and 8000 that is exposed; if neither is exposed, the relay publishes no 443
and no 80. Whenever Linux publishes 443, it publishes 80 too (section 6). Windows is not decided.

### Trusting the root

The app reads the root from the controller and saves it as `ca.pem` beside the config file. On
macOS, first launch adds that public certificate to the System keychain as a trusted root. On Linux
the setup script installs the file into the stores below.

| Store | How |
| --- | --- |
| macOS | First launch runs `security add-trusted-cert` for the public certificate only, behind the same administrator prompt as the rest of setup. The private key stays in the controller volume. The window can remove that certificate and its trust setting. The development scripts do not touch the keychain |
| Windows | Not decided. Earlier guess, left as written: `Import-Certificate -CertStoreLocation Cert:\LocalMachine\Root` |
| Linux system store | `/usr/local/share/ca-certificates/whalias.crt` plus `update-ca-certificates` (Debian family), or `trust anchor` (Fedora family) |
| Linux browsers | `certutil` (from `libnss3-tools`) into the NSS databases Chrome and Firefox use |
| Firefox on macOS | Firefox keeps its own store. The System keychain trust covers Safari and Chrome. Point Firefox at the CA file, or set its enterprise-roots preference. Windows is not decided |

### Rejected: publicly trusted certificates

Each install could get `*.<id>.local.whalias.dev` from a service we run, as plex.direct does, removing
the root-certificate step. It means running and securing a public service, getting `local.whalias.dev`
onto the Public Suffix List for Let's Encrypt's rate limits, flattening names to one label because a
wildcard covers only one level, and taking on the `.dev` problems in section 8. Possible later; not
needed for the first version.

## 11. How it looks to a user

### Installing

**macOS:** download the DMG from a GitHub release and open Whalias.app, or
`brew install --cask whalias/tap/whalias`. There is no setup script and no `curl | sh`. The first
launch is the setup. The plan is [docs/host-app.md](docs/host-app.md).

**Linux:** `curl -fsSL https://whalias.dev/install.sh | sh` remains the Linux plan. It is not the
Mac install.

**Windows:** the tray app is the install, from a GitHub release or winget, the same shape as the
Mac app. There is no `install.ps1` in that product. The checklist is
[docs/windows-release.md](docs/windows-release.md). The tunnel spikes have not been run, so the
older loopback sentences in this document are still guesses, not that install.

On macOS, the first launch:

1. Checks that Docker is running through the user's `docker` client and the current context. It
   does not look for `/Applications/Docker.app`.
2. Writes the config file with the defaults, or keeps the existing one (section 7, *Settings*).
   The image source defaults to Docker Hub.
3. Installs `/Library/LaunchDaemons/dev.whalias.tunnel.plist`, with
   `AssociatedBundleIdentifiers` set to the app. launchd runs the helper shell script. The helper
   uses a new enough installed `wireguard-go` and `wg`, or the copies the app placed in
   `/usr/local/libexec/whalias/`. It does not write `dev.whalias.loopback.plist` and does not run
   `ifconfig lo0 alias`. A "Background Items Added" notification can still appear, which the window
   explains first. Login Items shows Whalias.
4. The helper owns the `utun`, the Mac's WireGuard peer, and the route for `10.254.253.0/24`. It
   does not bind 80 or 443. There is no throwaway bind on an alias, and spike 20 is not an install
   check.
5. Writes `/etc/resolver/wha.internal` with `nameserver 127.0.0.1` and `port 5387`.
6. Creates the ordinary `whalias` bridge and starts the `whalias` controller `--privileged` on that
   network, with the recorded Docker socket, the public-key directory at `/keys`, the data volume,
   DNS on `127.0.0.1:5387`, and one WireGuard UDP port on `127.0.0.1` (section 7). The image is
   `docker.io/whalias/whalias` at the release digest, unless the image setting says to build from
   a cloned repository.
7. Reads the root certificate from the controller, saves `ca.pem`, and adds the public certificate
   to the System keychain (section 10).
8. Installs `/usr/local/bin/whalias`.
9. Shows the window ready: Docker, the controller, the tunnel, and the names.

It asks for the admin password once. A later launch repairs a missing piece and does not repeat the
setup. It does not install a Docker Desktop extension.

On Linux, the setup script still creates both networks, starts the controller, trusts the CA, and
installs the `whalias` command. Windows is not decided.

### Daily use

Nothing changes in the projects. `docker compose up` as usual, and names appear within a second.

- `https://web.myproject.wha.internal` is the tunnel plus HTTPS inside the controller (sections 3
  and 10). The name resolves to the stable address. If TCP 443 on the bridge accepts, the client
  sees the container's certificate. Otherwise, when a proxy exists, the client sees the Whalias
  certificate. Safari and Chrome trust it after first launch has installed the CA. It is not a root
  bind of the container's address, and spike 20 is not how the Mac serves it. A container does not
  have to avoid publishing 443.
- `psql -h db.myproject.wha.internal -p 5432` reaches that project's database, while
  `db.otherproject.wha.internal:5432` reaches another one. On macOS the name resolves to the stable
  tunnel address, not `127.87`, and DNAT reaches the port the database listens on.
- Existing `ports:` mappings keep working beside Whalias. On macOS the tunnel does not bind the
  service port on the host, which is what spike 04 showed a Docker publish of `127.87` doing to
  `docker compose up`. That spike is history. `127.0.0.1` still reaches the project's own mapping
  when the project published one. The name does not.
- Nothing to keep open for names to resolve. Whalias starts and stops with Docker. When the
  controller is gone, the helper removes the route and the `utun`. Quitting the window leaves the
  helper and the controller running. The app is how Whalias was installed. It does not have to stay
  open afterward.

### The `whalias` command

On macOS the app installs this command. It is not the installer. Commands that need the controller
run `docker exec` into it. `uninstall` acts on the host.

| Command | Does |
| --- | --- |
| `whalias ls` | Lists names and addresses. On Linux, also ports, including skipped ports. On macOS, the stable address and the current DNAT destination. `--json` includes the probe result as `http://…`, `https://…`, or no link |
| `whalias open web.myproject` | Opens the URL in the browser. Opening the status page with no name is later, with that page, and is not this macOS build |
| `whalias uninstall` | Removes Whalias. On macOS this is the same cleanup as the window's Uninstall |

There is no `whalias doctor`. The window shows whether Docker, the controller, the tunnel, the
resolver, and the handshake are up, and whether a DNS override is in the way. It does not turn the
hosts-file fallback on by itself. There is no `whalias install`. Opening the app is the install.
There is no `whalias update` on macOS. A new version is a new DMG, or `brew upgrade --cask whalias`.

The window is where the image source, the hosts-file switch, and the CA install or removal live.
Those are not separate commands.

### Updating

On macOS the app and the controller image update together. A new DMG, or `brew upgrade --cask
whalias`, replaces the app. The new app pulls `docker.io/whalias/whalias` at the digest that
release names and recreates the controller. The local-build setting keeps building from the chosen
repository instead. The controller reconciles DNAT, project-network joins, and DNS. It does not
recreate relays, because macOS has none.

On Linux, when that install exists, an update still starts from the script: `brew upgrade` when
Homebrew installed it, otherwise a new `install.sh` checked against the published SHA-256. The new
script's install pulls the new digest and recreates the controller, and the new controller
recreates every relay. That Linux path is not the Mac install.

On Linux, relays and the controller speak a versioned protocol (the internal API's `/api/v1/` paths
and the relay token), so an old relay keeps working against a new controller for the few seconds
until it is replaced. A change that breaks `v1` gets `v2`, and the controller serves both for at
least one release. That protocol is not this macOS build.

On macOS the app owns install, the health view, and uninstall. Linux, when it is built, keeps its
own install script. There is no `doctor` command on either.

### The status page

Later. Not this macOS build. It is not given `127.87.0.2`, and it is not given a stable address in
this build either. When it exists, `https://wha.internal` is the domain itself, as OrbStack's is
`orb.local`, and so is `whalias.wha.internal`. On Linux the earlier description still serves it
through a relay on `127.87.0.2` (section 7). Chrome on this Mac navigated to the bare name
`wha.internal`. Safari on macOS 27.2.1 searched for it instead (spike 03, 20 November 2026), so
install output and `whalias ls` also print full `https://` URLs for container names. They do not
have to print a status-page URL for this build.

### The Docker Desktop extension

Later. Not this macOS build. The description below is the later product, left in place. Optional,
installed from Docker's extension marketplace or with `docker extension install
whalias/extension`. A Whalias tab in Docker Desktop:

- Names by project, each with **Open** and **Copy**, read from the controller with `docker exec`.
- A health panel for the resolver registration and the root trust. The Mac window is that view
  for this product. Docker Desktop extensions can ship executables that Docker Desktop runs on the
  host for them.
- When Whalias is not set up, the setup command for the user's OS to copy, because the extension
  cannot make admin-level changes itself. It does not install or run the macOS tunnel helper.

### Uninstalling

On macOS, Uninstall in the window and `whalias uninstall` are the same cleanup. A Homebrew cask
uninstall runs it too. It bootouts the `dev.whalias.tunnel` LaunchDaemon, removes that plist and
`/etc/resolver/wha.internal`, and tears down the route and the `utun`. It removes
`/var/lib/whalias/mac.key`, the public-key directory, the config file, `ca.pem`, the public CA and
its trust setting in the System keychain, and `/usr/local/bin/whalias`. It does not quit Docker.
It does not remove `lo0` aliases, because setup does not add them. When Docker answers, it stops
and removes the `whalias` controller, the `whalias` bridge, and the `whalias-data` volume. It does
not remove app containers the person created. When Docker does not answer, those three Whalias
objects stay, and the window says so.

Dragging Whalias.app to the Trash does not run this. The helper checks for the app bundle each
time it starts, including at the next boot. If the bundle is gone, the helper runs this cleanup
and then removes itself. Until that boot, the leftovers stay. If Docker is down at that boot, the
controller, the bridge, and the volume stay behind.

On Linux, `whalias uninstall` removes the DNS registration and hosts-file block, the root
certificate from every store it was added to, every container's attachment to `whalias`, the relays,
the controller, both networks, the volume, the image, the config file and saved root certificate,
and the `whalias` command. A trusted root left behind is a security problem on Linux, where setup
added one. Windows uninstall is not decided.

## 12. Caveats and workarounds

### Names and HTTPS inside containers

- Inside a container, names reach the target directly over plain TCP; use `http://name:port`. HTTPS
  with Whalias's certificates works only from the host. Accepted.
- Containers don't trust Whalias's CA. Use plain HTTP between containers.

### Joining the shared network

This subsection is the Linux shared-network plan (section 9). It is out of this macOS build. The
macOS controller joins project networks for the tunnel and does not attach every container to a
shared alias network.

- **Isolation between projects is gone.** Any container can reach any other container's ports. Normal
  for local development and the same as OrbStack. Every container is attached by default;
  `dev.whalias.attach=false` opts one out, and the `WHALIAS_ATTACH` setting limits attachment to
  chosen compose projects.
- **A second network interface.** Apps that pick "the first IP" can choose the wrong one: clustering
  tools, Kafka's advertised listeners, Java's `getLocalHost()`. Opt them out with the label.
- **A short race at start-up.** The container starts, and a few milliseconds later the controller
  attaches it. An entrypoint that looks up another project's name in its first instant may miss it.
  Most clients retry.
- **Containers that can't take part.** `network_mode: host` and `network_mode: none` containers can't
  join networks. Containers started with plain `docker run` on the default `bridge` network can be
  attached but may keep the host's DNS settings rather than Docker's built-in DNS. Starting them on a
  user-defined network fixes it.
- **The subnet.** `10.250.0.0/16` must not overlap a VPN's or the office LAN's ranges. It is
  configurable, and the window warns on overlap with the host's routes.

### Ports

- **Unexposed ports.** On Linux, Whalias can only publish ports the container declares. Declare the
  port or add `dev.whalias.ports`. That expose limit is the relay model. On macOS, DNAT reaches the
  port the process listens on. A Mac reader does not have to `EXPOSE` or publish for the tunnel to
  work. OrbStack has no expose limit because it routes container IPs. The macOS tunnel routes a
  stable address and DNATs to the bridge, which is the same kind of reach, without routing the
  bridge subnet itself.
- **Changing a container's ports** on Linux recreates its relay, which drops that container's open
  connections. On macOS there is no relay to recreate. DNAT follows the bridge address.
- **A Docker publish of a loopback address** both blocked a `ports:` mapping and was blocked by one
  (spike 04). That is history. It is not the macOS listen path. On macOS no process binds the alias.
  Spike 19 showed a host bind and a user's `ports:` mapping both starting. That bind is superseded.
  The Linux relay publishes the service port on the loopback address and skips a port it cannot
  take (section 5, *Which ports*).
- **A native program on `0.0.0.0`** coexisted with a Docker publish of the alias in spike 04. Each
  address reached its own listener. A native Postgres on `0.0.0.0:5432` is that case for the
  loopback experiment. It is not an instruction to publish `127.87` on macOS.
- **Privileged ports on macOS.** Docker Desktop 4.99.0 has no "Allow privileged port mapping"
  setting, and a Docker publish of 80 or 443 listens on every interface (spikes 02 and 18). Spike 20
  bound two `127.87` addresses as root and left `127.0.0.1` silent. That is not how macOS serves 80
  and 443. The controller handles HTTPS (section 10). There is no root bind. DNS uses port 5387 on
  `127.0.0.1`.
- **Windows' HTTP server (`http.sys`).** Not decided. Earlier guess, left as written: IIS and some
  other apps may hold port 80 or 443 on every address. `netsh http add iplisten 127.0.0.1` frees
  the others.
- **Windows reserved port ranges.** Not decided. Earlier guess, left as written: Hyper-V and WSL
  reserve blocks of ports (`netsh int ipv4 show excludedportrange protocol=tcp`). A container port
  inside one cannot be published, on any address. The window flags it.
- **The address pool.** On Linux, 252 containers by default (one address per container, plus one per
  scaled service) from `WHALIAS_BLOCK`. A full block is reported, and `whalias config set
  WHALIAS_BLOCK=…` configures a larger one. On macOS the stable subnet is `10.254.253.0/24`. It is
  not a loopback block, and nothing re-aliases `lo0`. Windows is not decided.

### DNS that bypasses the OS

- Corporate VPNs (Zscaler, GlobalProtect, AnyConnect) sometimes take over DNS. On Windows a VPN client
  can add its own DNS rules that win over Whalias's.
- Browsers with secure DNS (DNS over HTTPS) may skip the OS resolver. In Firefox, add `wha.internal`
  to `network.trr.excluded-domains`.
- The window detects both and offers the hosts-file fallback. It does not turn that fallback on by itself.

### Apps with their own certificate store

| Runtime | Setting |
| --- | --- |
| Node | `NODE_EXTRA_CA_CERTS=<root path>` |
| Python `requests` | `REQUESTS_CA_BUNDLE=<bundle path>` |
| Java | `keytool -importcert` into the JDK's `cacerts`, or a custom trust store |
| .NET on Linux | Uses the OpenSSL system store, which setup updates |
| Firefox | NSS on Linux. On macOS this build the CA is not in the System keychain, so enterprise-roots is not the trust step; point Firefox at the CA file. Windows is not decided. The earlier guess was enterprise-roots once an OS store held the CA |

`whalias env <runtime>` prints the right one.

### Docker

- Restarting Docker Desktop interrupts connections for a few seconds. The controller comes back with
  Docker. On macOS it reconciles DNAT, project-network joins, and DNS. On Linux it reconciles
  relays, networks, and attachments.
- One Docker context at a time. Remote and cloud contexts are not supported.
- **Docker Business with Enhanced Container Isolation** may refuse to let the controller mount the
  Docker socket unless an administrator allows the image. Without that, Whalias cannot run there.
- **Other runtimes.** The controller needs only the Docker API, but Rancher Desktop, Podman, Colima,
  and rootless Docker each forward published ports their own way, and whether they publish on
  extra loopback addresses such as `127.87.0.7` is untested. Their support for Docker Desktop
  extensions varies too. OrbStack on macOS is decided in section 16: tests 26–29 passed on
  25 November 2026, and the section 3 data path is the same there.
- Some companies' Docker Desktop settings allow image pulls from Docker Hub only. The image is mirrored
  to `docker.io/whalias/whalias`, and setup falls back to it when the GitHub registry is refused.

### Small differences from OrbStack

- On Linux, containers see the relay's address as the client. On macOS, a sibling sees Whalias's
  address on that project network. There is no relay.
- No application UDP in the first version. The macOS tunnel's one WireGuard UDP port is separate
  (section 3).
- On Linux, `ping` and `traceroute` show the loopback address, not the container's. On macOS they
  show the stable tunnel address.
- On Linux, one small relay container per container is visible in `docker ps`, grouped as "whalias"
  in Docker Desktop. macOS does not add those containers. Windows is not decided.

### Locked-down machines

Corporate Windows with AppLocker, WDAC or PowerShell's Constrained Language Mode can block the setup
script outright, and device management can stop a script adding launch daemons, DNS rules or root
certificates. Those users need IT to allow it, or to follow the manual instructions under IT's control.

## 13. Implementation notes

### Language: Go

Chosen on 19 November 2026 over .NET Native AOT. Go has every part ready-made, including the DNS
responder, builds one static binary for `scratch` without a musl toolchain, and is what most Docker
tooling is written in. On Linux, trust-store installation is not Go: the setup script uses that
OS's own tools. On macOS this build the script does not add the CA to the System keychain.

| Part | Go |
| --- | --- |
| Docker API | The official Docker client (`github.com/docker/docker/client`) over the mounted Unix socket |
| Relaying | Linux: `net`. Not the macOS data path |
| HTTPS termination | On macOS, in the controller: `crypto/tls`, plain HTTP to the bridge port. On Linux, the same libraries inside relays |
| Certificates | `crypto/x509`. Name-constrained to `wha.internal` |
| DNS responder | `miekg/dns` |
| Status page and internal API | Later. On Linux, `net/http` and `html/template`. Not this macOS build |
| Image | `CGO_ENABLED=0` static build, `linux/arm64` and `linux/amd64`. This macOS build's image also includes `iproute2`, `iptables`, and `wireguard-tools`, and the container is privileged. Tag `whalias:local` |
| Setup and `whalias` command | POSIX `sh` on macOS and Linux, and PowerShell on Windows (Windows not decided). The macOS helper is a shell script under `scripts/macos/`, not a Go binary this repository signs |

### Repository layout

One repository, `github.com/whalias/whalias`; the Homebrew tap stays separate because Homebrew requires
it.

```text
whalias/
  cmd/whalias/          container binary: controller, ls, ca, version. relay is the Linux subcommand
  internal/             controller, dns, ca, proxy, names, docker. relay and the status API are Linux / later
  scripts/install.sh    Linux setup. Not the macOS product install
  scripts/macos/        development stand-in for the macOS helper. The app replaces this install
  scripts/install.ps1   Windows setup guess. Windows is not decided
  extension/            Docker Desktop extension. Later, not this build
  site/                 whalias.dev. Later, not this build
  test/e2e/             end-to-end tests against a real Docker engine
  Dockerfile
  .github/workflows/    CI and the release workflow
```

Spike scripts and their logs stay in `local-history/` on the machine that ran them. That folder is not published.

`scripts/install.sh` is the Linux setup, when Linux is built. The macOS product install is
Whalias.app. `scripts/macos/` is the development helper on this machine, not the release. `extension/`
and `site/` stay in the tree as later work. This build does not produce them, and it does not produce
a Homebrew cask.

### Testing

- **Unit tests** in Go for naming, collision precedence, address allocation, the DNS responder and
  certificate issuing. No Docker needed.
- **End-to-end tests** in GitHub Actions on Linux: start a real controller with Docker Engine, bring up
  compose fixtures, and check names, published ports, container-to-container lookups and HTTPS with
  `dig` and `curl`.
- **macOS and Windows** have no Docker Desktop on GitHub's hosted runners, so each release is checked
  by hand against a written checklist on real machines, stored as `test/manual-checklist.md`.
  Self-hosted runners can come later.

### Signing and publishing

No app signing. This repository does not produce a signed host binary or a signed Mac app.

- **macOS:** launchd runs the helper script in `scripts/macos/`. That script runs Homebrew's
  `wireguard-go` (`/opt/homebrew/bin/wireguard-go -f utun`). It does not run `/sbin/ifconfig lo0
  alias`. The only `ifconfig` in the helper destroys the `utun` on teardown. The setup script is
  text. Gatekeeper does not inspect it. No Apple Developer ID or notarization. A Background Items
  notification can still appear for the LaunchDaemon. The helper is not the `lo0` job from spike 16.
- **Windows:** `irm … | iex` runs the script's text in memory. PowerShell's execution policy applies to
  script files, not to this, and SmartScreen checks downloaded executables, which this is not. The
  saved copy runs through `whalias.cmd` with `-ExecutionPolicy Bypass` for that process only. No
  code-signing certificate.

What is still signed or protected:

- **The image,** signed in CI (cosign keyless signing from GitHub Actions) and pinned by digest in each
  release of the scripts, so a compromised registry cannot swap the controller.
- **The scripts,** with SHA-256 checksums published beside them for anyone who downloads before running.
  `curl | sh` trusts whalias.dev's TLS and the Cloudflare and GitHub accounts behind it, so both
  accounts use two-factor authentication.
- **The extension image** on Docker Hub, reviewed by Docker if listed in the marketplace.

The helper stays a script. This build still runs Homebrew's `wireguard-go` and `wg` from
`/opt/homebrew/bin`. The host app in section 14 ships its own copies and prefers an installed pair
when that pair is at least as new. The menu-bar app in section 14 is a binary this repository will
produce. Its first distribution is unsigned, or a Homebrew tap install. Developer ID signing and
notarization wait until a downloaded copy should open without a System Settings step. That
membership is $99 USD per year and includes notarization. When the app is notarized, the bundled
`wireguard-go` and `wg` are signed with it. The Mac App Store stays out, because a sandboxed app
cannot see the Docker socket or install the LaunchDaemon. The rejected data-plane app in section
14 stays rejected.

### Release workflow

One workflow in the `whalias` GitHub organization publishes everything from one tagged commit:

- the image to `ghcr.io/whalias/whalias` and `docker.io/whalias/whalias`, signed;
- `install.sh` and `install.ps1`, with the new digest written in, and their checksums to whalias.dev;
- the formula update to `whalias/homebrew-tap`;
- the extension image to `docker.io/whalias/extension`.

### Build order

macOS on Apple Silicon comes first. The macOS gate is the tunnel in section 3. A loopback address,
a relay container, and a root bind of 80 and 443 are not that gate. Linux loopback criteria are not
the Mac gate. Linux and Windows are not started from this revision. Windows is not decided. Steps 6
to 8 stay as later order.

0. **Spike.** Section 15 records what was run. The macOS product is section 3. Loopback items in
   that spike list are superseded on macOS. They are not remaining Mac work.
1. **macOS tunnel controller.** Privileged controller, kernel WireGuard, one UDP publish on
   `127.0.0.1`, the ordinary `whalias` bridge, project-network joins, sticky addresses in
   `10.254.253.0/24`, DNAT, and DNS published before lookup. No relay containers. Setup installs the
   launchd helper, not a loopback block.
   *Done when, on the Mac:* a name resolves to its stable address; an HTTP body comes back through
   the tunnel; a container that accepts TCP 443 shows its own certificate and one that does not
   shows the Whalias certificate; recreate keeps the name and the address; stopping the controller
   makes the helper remove the route and destroy the `utun`. Reaching `127.87.0.x` is not the check.
2. **Container-to-container names.** Out of this macOS build. Not a gate for the Mac product. Left
   in the long-range order for the Linux shared-network plan: opt-out, `WHALIAS_ATTACH`, and the
   controller's own `wha.internal` alias.
   *Done when, on Linux, later:* a container in one project reaches `db.otherproject.wha.internal:5432`;
   a container labeled `dev.whalias.attach=false` is not attached; `docker compose up` run again does
   not recreate attached containers.
3. **Host DNS on macOS.** The responder on port 5387, published on `127.0.0.1`.
   `/etc/resolver/wha.internal` uses `nameserver 127.0.0.1` and `port 5387`, not `127.87.0.1`. The
   stable address is published before lookup (spike 13). A two-second recovery from NXDOMAIN is not
   the Mac gate.
   *Done when:* `dscacheutil -q host -a name db.myproject.wha.internal` returns the stable address,
   and `psql -h db.myproject.wha.internal` reaches the database through the tunnel. A lookup outside
   `wha.internal` is unaffected.
4. **HTTPS inside the controller.** The CA, name-constrained to `wha.internal`. Pass-through when
   TCP 443 accepts. Otherwise the Whalias certificate and plain HTTP to the probed port. No
   privileged-port helper. This build does not add the CA to the System keychain.
   *Done when:* `curl --cacert` with the local CA file succeeds against a proxied name and shows the
   Whalias certificate, a container that accepts TCP 443 shows its own certificate, and a container
   with no HTTP port gets no proxy. Nothing asks for a root bind of 80 and 443.
5. **The `whalias` command the app installs:** `ls`, `open`, and `uninstall`. There is no `doctor`.
   The window is the health view. The status page is later, not part of this step.
   *Done when:* uninstall leaves no `dev.whalias.tunnel` job, no resolver file, no route for
   `10.254.253.0/24`, no helper `utun`, no `whalias` command, and no Whalias CA trust in the System
   keychain. When Docker answers, the controller, the `whalias` bridge, and the volume are gone too.
6. **Linux and Windows, later.** Not this build. Linux stays on loopback. Windows is not decided.
   The earlier text, left as written: correct the best-guess paths of `install.sh` and `install.ps1`
   on real machines, finish the Linux end-to-end tests in CI, and run the manual checklist on
   Windows 11.
7. **Publishing, later:** the Homebrew cask, the GitHub release DMG, the Docker Hub image, and
   image signing. Not this build.
8. **The Docker Desktop extension.** Later. Not this build. It does not install the tunnel helper.
9. **Later:** application UDP, other runtimes, PROXY protocol, and a larger Linux block if 252
   proves tight. The macOS stable subnet stays `10.254.253.0/24`.
10. **The menu-bar app, which is the Mac install.** Later. Not this build. Section 14 and
    [docs/host-app.md](docs/host-app.md). First launch installs the helper, the login item, the
    controller, the CA, and the `whalias` command. The window is the health view. Login Items shows
    Whalias. The Windows tray waits until the tunnel is a Windows service.

## 14. Host management app

Decided on 25 November 2026. The plan is [docs/host-app.md](docs/host-app.md). Implementation has
not started.

The app is the macOS install. A DMG from a GitHub release, or a Homebrew cask, is how it arrives.
There is no setup script. The first launch asks for an administrator password once and installs the
helper, the login item, the controller, the public CA in the System keychain, and
`/usr/local/bin/whalias`. The image defaults to `docker.io/whalias/whalias` at the digest that
release names. A setting builds the controller from a cloned repository instead. The app uses the
user's `docker` client and the current context. It does not look for `/Applications/Docker.app`.

The app is a window with a Dock icon and a menu-bar icon. Closing the window hides it in the menu
bar. It runs as the logged-in user. It shows whether Docker, the controller container, and the
tunnel are up, lists `*.wha.internal` names, and opens a browser only when the controller's HTTP
probe has a target. That window is the health view. There is no `whalias doctor`.

Docker actions use the user's Docker socket: create, start, stop, and recreate the controller, pull
or rebuild the controller image, and start, stop, or restart app containers Whalias already lists.
Compose still defines those app containers. First launch creates the controller, the `whalias`
bridge, and the `whalias-data` volume. It does not create the person's app containers. Tunnel
actions ask for an administrator password and then call the root helper: restart
`dev.whalias.tunnel`, and install or remove the LaunchDaemon, `/etc/resolver/wha.internal`, and the
route. First launch adds the public certificate to the System keychain. The window can install it
again or remove it and its trust setting. The private key stays in the controller volume. Firefox
keeps its own store.

Quitting the window leaves names working. The controller still answers DNS, terminates HTTPS, and
owns DNAT. The launchd helper still owns the `utun` and the route. `KeepAlive` stays false.

Uninstall in the window, `whalias uninstall`, and the Homebrew cask uninstall are the same cleanup.
When Docker answers, that removes the controller, the `whalias` bridge, and the `whalias-data`
volume, and leaves the person's other containers. Dragging the app to the Trash does not run
cleanup. The helper does, at its next start, if the app bundle is gone. [docs/host-app.md](docs/host-app.md)
records the gap until the next boot, and what stays behind when Docker is down.

Linux stays a CLI: `whalias ls` with full URLs, and `whalias open`. There is no `doctor` command.
systemd is the service manager on Ubuntu, Debian, Fedora, RHEL, and Arch. Native Engine does not
need a resident host process. Docker Desktop on Linux gets a system unit for the host WireGuard
helper, the same role as `dev.whalias.tunnel`. A Linux tray icon is out of this plan.

Windows mirrors the Mac install. The tray app is the product, from a GitHub release or winget.
First launch installs a service for the tunnel, the NRPT rule, the controller, the CA, and the
`whalias` command. Neither exists yet. The spike scripts have not been run. The checklist is
[docs/windows-release.md](docs/windows-release.md). The Mac checklist is
[docs/macos-release.md](docs/macos-release.md). Signing is deferred on both.

The first Mac distribution is an unsigned DMG, allowed from System Settings, or the Homebrew cask.
The Apple Developer Program ($99 USD per year, Developer ID and notarization included) waits until
a download should open without that Settings step. The Mac App Store and the Enterprise program
are out.

The window reads one snapshot: Docker, the controller, the tunnel, and the controller's name list
including the probe result as `http://…`, `https://…`, or no link. `whalias ls --json` prints that
name list. The snapshot and the Mac window are the implementation. The Windows tray waits until
the tunnel is a service.

### Login Items name

Decided on 25 November 2026. The plan is [docs/host-app.md](docs/host-app.md). This build does not
do it.

System Settings > General > Login Items & Extensions names a LaunchDaemon from the program it
starts when the job is not tied to an app. This build's plist starts `/bin/sh`, so the row is `sh`.
The label `dev.whalias.tunnel` is not the title. On 25 November 2026 the enabled `sh` row was this
daemon.

The menu-bar app supplies the name. Its display name is Whalias. First launch writes
`AssociatedBundleIdentifiers` with that app's bundle id. Login Items then shows Whalias and the app
icon. The title is Whalias. The row's subtitle already says whether the item is running in the
background. The helper stays the shell script. This is part of the first launch, not a follow-up.

### Host WireGuard binaries

Decided on 25 November 2026. The plan is [docs/host-app.md](docs/host-app.md). This build's helper
still calls `/opt/homebrew/bin/wireguard-go` and `/opt/homebrew/bin/wg`. That install step goes away
for a direct install once the host app ships.

The app carries `wireguard-go` and `wg`. First launch copies them to `/usr/local/libexec/whalias/`,
root-owned, so launchd does not depend on the app bundle staying in place. At start the helper
picks the first candidate pair that is executable and at least as new as the shipped pair:

1. `/opt/homebrew/bin/` (Apple Silicon Homebrew)
2. `/usr/local/bin/` (Intel Homebrew)
3. the copies in `/usr/local/libexec/whalias/`

It does not use `command -v`. Root's PATH is the same class of mistake as a `docker` that points at
a different engine. `wireguard-go --version` prints `wireguard-go v…` and `wg --version` prints
`wireguard-tools v…`. The floor for each tool is the snapshot date of the copy that release ships.
An installed binary is used only when its date is that floor or newer. A missing binary, an older
one, or a version line that does not parse is skipped. The helper log names the path and the version
it chose. The Homebrew cask does not depend on `wireguard-go` or `wireguard-tools`. An already
installed pair is still used when it meets the floor. `brew upgrade` of those formulas can supply
a newer pair before the next Whalias release.

`wireguard-go` is MIT. `wg` comes from wireguard-tools, whose sources offer GPL-2.0 OR MIT. The
bundle ships the upstream license texts and distributes `wg` under the MIT option. The pinned
upstream commits are the ones that release builds.

On Windows, when the service exists, the same rule applies to `wireguard-go.exe`. `wintun.dll` has
to sit next to the `wireguard-go.exe` that runs. An installed copy is used only when its version
meets the floor and that DLL is beside it. On Linux, native Engine does not run host
`wireguard-go`. A Docker Desktop Linux helper prefers a kernel WireGuard device and a distro `wg`
that meets the floor, and uses a bundled `wireguard-go` only when the kernel device is missing.

### Rejected on 19 November 2026: a native data-plane app

Recorded so the reasoning survives. The management app above does not revive this design.

#### What it was

The first version of this design ran a native app on the host:

- a user-level **main service** that watched Docker, answered DNS, listened on `127.0.0.N` for every
  port of every name and terminated HTTPS;
- a small **privileged service** (launchd, a Windows service, systemd) for loopback aliases, the DNS
  registration, the root certificate and the hosts file;
- a single **helper container** that the main service tunneled every connection through, because on
  Docker Desktop the host can reach only published ports;
- a **menu bar or tray app** and a `whalias` CLI;
- signed installers: a notarized `.pkg` and Homebrew cask, an MSI and winget package, `.deb` and
  `.rpm` repositories, Sparkle for updates.

Offering both, with the user choosing, was also considered.

#### Why not

- **Signing costs money, and unsigned costs users.** Apple's developer program is $99 a year, plus a
  Windows code-signing service. Unsigned, a browser-downloaded Mac app needs System Settings, Privacy &
  Security, **Open Anyway** on macOS 15 and later; Windows shows SmartScreen's warning; Windows 11 with
  Smart App Control on blocks unsigned programs outright; and endpoint security tools flag unsigned
  binaries that change DNS and add root certificates. (Installing through a script or Homebrew formula
  avoids macOS's prompt, but not Windows'.)
- **It has to be running.** A login item that crashes takes every name with it, where the controller
  restarts with Docker.
- **More to build and maintain:** per-OS binaries, a privileged service, installers and an updater.
- **Two modes would conflict.** Both use the same DNS address and loopback block, so they would have to
  be mutually exclusive, with the app policing it, for little gain.

#### What it would still have offered

A native menu bar, one health view that sees everything (the Mac window is that view), loopback
addresses added on demand rather than a fixed block, port changes without dropping
connections, and running where Enhanced Container Isolation forbids socket mounts.

### If the data-plane app ever comes back

Reuse rather than rebuild: run the controller's code on the host instead of in a container, listen on
the loopback addresses directly instead of through relays, and reuse the setup script for every
admin-level step, so the app itself needs no privileged service. It would be a second way to run the
same core, mutually exclusive with the containers-only way, and would bring signing back. The
menu-bar app decided on 25 November 2026 is not this return. The container and the helper stay the
data path.

## 15. Spike plan

Everything below was reasoned about, not tested, and each item could change a design choice. The
spike settles them before step 1 of the build order, with no Whalias code: throwaway containers
(`alpine` with `socat` as a stand-in relay, `postgres` and `nginx` as targets) driven by local shell
scripts. Each result goes into the local spike log with the date, the OS and Docker versions, the
commands run, and what the result changes in this document, if anything. Those scripts and logs are
not published with this repository.

The spike is run on macOS on Apple Silicon now. The Linux and Windows items are run later, when
machines are available; until then, those paths are written from the best guesses in this document.

### macOS on Apple Silicon, now

Items that could break the core idea come first. If one fails, stop and revisit the design before
running the rest. The core set was run on this Mac on 20 November 2026 (Docker Desktop 4.99.0,
macOS 27.2.1, OrbStack stopped). Items 1, 5 and 6 passed. Item 3's DNS passed and Safari failed.
Items 2 and 4 failed. Spikes 18 and 19 were run the same day. 18 failed, and 19 passed. The macOS
product is section 3. Loopback text in sections 5, 6, 11, and 12 is the Linux plan or superseded
Mac history. It is not an instruction to alias `127.87`, start a relay, or bind 80 and 443 as root
on a Mac. The spike record is in the local spike log, which is not published with this repository.

**Could break the core idea**

1. **Publishing on an aliased address.** Superseded on macOS. Not remaining work. The test added
   `127.87.0.7` with `ifconfig lo0 alias` and published `-p 127.87.0.7:5432:5432`. That is not the
   macOS product. Do not alias `lo0` to build Whalias on a Mac.
2. **Privileged ports.** Superseded on macOS. Not remaining work. The test published
   `127.87.0.7:443` and `:80`. macOS does not bind 80 or 443 as root. Linux may still meet a
   privileged-port limit later. Windows is not decided.
3. **DNS on an unprivileged port.** Superseded as the macOS nameserver address. The test published
   `127.87.0.1:5387`. The macOS product uses `nameserver 127.0.0.1` and `port 5387` (section 8).
   The port number stays 5387. Checked with `dscacheutil` and a browser, since `dig` bypasses the
   macOS resolver.
4. **Wildcard conflicts, both orders.** Superseded on macOS. Not remaining work. The test was a
   relay publishing `127.87.0.7:5432` against a project's `ports:` mapping. macOS does not start
   that relay and does not bind that address. The Linux relay still skips a port it cannot publish.
5. **A relay on two networks.** Linux relay history, and the shared-network plan. Out of this macOS
   build. Not a tunnel requirement. The test put a container on `whalias-edge` and then on the
   `--internal` `whalias` network. macOS uses one ordinary bridge named `whalias` and does not
   create `whalias-edge`.
6. **Dotted aliases.** Docker's built-in DNS answers an alias such as `web.myproject.wha.internal` from
   another container on the network, and returns every replica's address when several containers
   share one alias (scaled services).

**Follow-ups from that run**

18. **A second privileged publish.** Superseded on macOS. Not remaining work. With 80 and 443 free,
    the test published each on `127.87.0.190` and then on `127.87.0.191`. Run 20 November 2026:
    fail. The first publish listened on `*:80` and `*:443`. macOS does not publish 80 or 443, and
    it does not bind them as root.
19. **A Mac process on the alias, then the user's wildcard publish.** Bind only `127.87.0.195:P` from
    python3, with `SO_REUSEADDR` off, then `docker run -p P:P` and `docker compose up` with
    `ports: ["P:P"]`. Run 20 November 2026, port 25460: pass. Both publishes started. `127.0.0.1`
    reached the project and `127.87.0.195` still reached the Mac process. That host bind is history.
    It is not the macOS listen path. Traffic on macOS enters the `utun`.
20. **Root bind of 80 and 443.** Superseded on macOS. Not remaining work. Run 20 November 2026: the
    binds passed, the user's publishes failed. Root python listened on `127.87.0.200:80` and
    `127.87.0.201:80` only. Each address returned its own marker and `127.0.0.1` did not answer.
    Port 443 did the same. That root bind is not how macOS serves 80 and 443. HTTPS is inside the
    controller (section 10). Nothing on macOS requires that bind.

**Could change a design choice**

7. **Compose drift.** `docker compose up` run again on a project whose containers the controller
   attached to `whalias` leaves them running rather than recreating them.
8. **The grouping label.** Linux relay item. macOS starts no relays. Relays labeled `com.docker.compose.project=whalias` appear in Docker
   Desktop under one group, and whether they also show up in `docker compose ls` or get removed by
   `docker compose -p whalias down`. If compose treats them as a project, drop the label.
9. **Container DNS for names without an alias.** A container's lookup of a `wha.internal` name that
   no container has: whether Docker Desktop forwards it to the host resolver, gets a loopback address
   back and points the container at itself. And that Docker Desktop answers only its own
   `docker.internal` names rather than claiming all of `.internal`.
10. **Default-bridge containers.** Whether a `docker run` container on the default `bridge` network
    starts using Docker's built-in DNS once attached to `whalias`.
11. **Scale.** Linux concern: Docker Desktop's responsiveness with 50 relays. Not a macOS tunnel
    test. macOS does not start those relays.
12. **Boot ordering.** On macOS the helper's `utun` and the route for `10.254.253.0/24` are the boot
    concern, against the controller, not `lo0` aliases against relays. The old check, whether the
    launchd job's aliases existed before relays tried to publish, is superseded.
13. **Negative caching.** Spike 13: macOS held a missing name for at least 75 seconds. An SOA
    minimum of 1 second does not make a name resolve within 2 seconds of start. On macOS the stable
    address is published before lookup. The 2-second recovery is not the Mac gate.
14. **Name constraints.** The CA is name-constrained to `wha.internal` (section 10). Trusting that
    root in the System keychain is a later question, not a step in this macOS build. This build
    does not add the CA. `curl` with the local CA file is enough.
15. **Reading the root.** `docker exec <container> cat` of a PEM file prints it intact, standing in for
    `whalias ca`.
16. **The launchd notification.** A "Background Items Added" notification can still appear. The job
    is `dev.whalias.tunnel`, the helper script, not an `lo0` alias plist. What the notification
    shows is not settled.
17. **A VPN, if one is available.** A VPN that sends `.internal` or all DNS to its own server still
    loses to `/etc/resolver/wha.internal`.

### Linux, later

Linux stays on loopback. These checks are the Linux plan, not a macOS gate, and they are not
started from this revision.

- systemd-resolved: a global `DNS=127.87.0.1:5387` with `Domains=~wha.internal` receives only
  `wha.internal` lookups, or also receives lookups that no link claims. If the latter, switch to the
  dummy-link fallback in section 8.
- Items 1 (without aliasing, since all of `127.0.0.0/8` is loopback), 4, 5, 6, 7, 9 and 14 on native
  Docker Engine, and again with `"userland-proxy": false` in the daemon configuration, where publishing
  goes through iptables alone.
- Docker Desktop on Linux, which runs a VM like macOS, for items 1 to 4.
- The bare domain through `~wha.internal`, and the system and NSS trust stores for item 14.

### Windows 11, later

Windows is not decided. The bullets below are the earlier loopback guesses, left as written. They
are not a decision to use the tunnel, and they are not a decision to drop loopback.

- Docker Desktop publishes UDP and TCP 53 on `127.87.0.1`, and whether Internet Connection Sharing or
  Hyper-V's DNS proxy already holds port 53 on some machines.
- The NRPT rule for names under the domain and for the bare domain, and whether the leading-dot
  namespace alone already matches the bare domain.
- Items 1, 2 (`http.sys`), 4, 5, 7, 9 and 14 (Edge, Chrome, Firefox's enterprise-roots default).
- Port 5432 or similar inside a Hyper-V or WSL reserved port range.
- `irm | iex` under the default execution policy, what Windows Defender makes of a script that adds a
  root certificate and an NRPT rule, and the `whalias.cmd` launcher's `-ExecutionPolicy Bypass` where
  Group Policy sets the execution policy.

### Extension, before build step 8

- Whether a Docker Desktop extension's host-side executables can show the same health view as the
  Mac window, and whether unsigned ones run without a prompt on macOS and Windows.

## 16. Open questions

Resolved on 19 November 2026:

| Question | Answer | Where |
| --- | --- | --- |
| Attach every container by default, or only chosen projects? | Every container; opt out by label or limit with `WHALIAS_ATTACH` | Section 12 |
| Should a health check turn on the hosts-file fallback itself? | No. Resolved again on 25 November 2026: there is no `whalias doctor`. The window offers the switch and leaves it off | Section 11, section 14 |
| Port 80: redirect, or forward to the container's 80? | Linux: forward when the container exposes 80, otherwise redirect. One rule, no setting. On macOS, port 80 is not a host bind. DNAT delivers it. A proxy exists only when section 10's probe finds an HTTP port, and the only redirect is TCP 443 | Sections 6, 10 |
| How long does an absent name keep its address? | 30 days, `WHALIAS_RETENTION_DAYS` | Section 5 |
| Is a 252-address block enough? | Yes for now, with one address per container. Configurable | Section 12 |
| Does the client-address rewrite need PROXY protocol soon? | No. Postponed to *Later* | Section 13 |

Resolved on 20 November 2026, from spikes 18 and 19:

| Question | Answer | Where |
| --- | --- | --- |
| Can two containers each publish 80 and 443 on their own loopback address, with `127.0.0.1` staying silent? | Rejected on macOS. The loopback attempt: not through Docker. A root bind can, and spike 20 showed each alias returned its own marker while `127.0.0.1` stayed silent. One such bind blocks a later wildcard publish. macOS does not use that bind | Sections 3, 6, 12 |
| Does a Mac process bound only to `127.87.0.x:P` block a later Docker `-p P:P` or compose `ports: ["P:P"]`? | No. Both start. `127.0.0.1` reaches the project and the alias still reaches the Mac process. macOS no longer uses that bind | Section 5 |
| On macOS, loopback mappings or the tunnel? | The tunnel. Stable addresses in a subnet only the tunnel routes, NAT to the container's current bridge address, TLS terminated in the Whalias container. `127.87` is not the macOS plan | Section 3 |
| Do Linux and Windows keep the loopback model? | Not decided here. Linux stays on the loopback plan in this document. Windows sentences stay earlier guesses. Neither is converted to the macOS tunnel | Section 3 |

Still open: nothing in this revision decides Windows, and nothing here moves Linux off loopback.

### Developing on OrbStack

Run on 25 November 2026. Tests 26–29 passed (2, 2, 7, and 11 passes, and no failures). The record is
in the local spike log, which is not published with this repository. macOS 27.2.1 on arm64, OrbStack
engine 29.8.2, context `orbstack`, using that machine's OrbStack Docker socket.

The macOS data path in section 3 works on OrbStack. A privileged container created a kernel `wg0`.
TCP and UDP `127.0.0.1:5387` reached the container, and `lsof` showed those listeners on `127.0.0.1`
only. A host directory bind-mounted both ways. The host `utun` handshaked through
`127.0.0.1:51820`, and `10.254.253.10` followed the sibling from `192.168.107.3` to `192.168.107.4`.
The networks already on that engine did not overlap `10.254.253.0/24` or `10.254.254.0/30`.

The app uses the running local engine. It does not require the Docker Desktop binary or the context
name `desktop-linux`, and it does not require the engine string to say OrbStack. One local engine
at a time. `*.orb.local` stays OrbStack's own names. `*.wha.internal` is Whalias. An OrbStack user
can run Whalias. The install is still aimed at people whose engine does not already publish
container domains.

The scripts in `scripts/macos/` still call
`/Applications/Docker.app/Contents/Resources/bin/docker` and `context inspect desktop-linux`. Those
scripts are the development stand-in. The product install is the app, and the app does not use that
path. The `whalias` container runs on one engine at a time: both engines would publish
`127.0.0.1:5387`, and the helper installs one route. Other OrbStack containers stayed up through
this run.

## 17. Decisions for the spike phase

Made on 19 November 2026, after a review of this document.

| Decision | Choice | Where |
| --- | --- | --- |
| Language | Go | Section 13 |
| Relay and address unit | Linux: one relay and one loopback address per container; a scaled service also gets one for the service name. On macOS, one stable address per identity and no relay. Per-connection balancing is not part of this macOS build | Sections 3, 5, 6 |
| Relay target | Linux: an alias name, resolved on every connection; that is also how a Linux scaled service spreads connections. macOS does not use a relay for this | Section 6 |
| Port held on all interfaces | Superseded on macOS. The path is the tunnel in section 3, not a host bind of `127.87.0.x:port`. Spikes 04 and 19 are the loopback record. Linux still skips a port the relay cannot publish | Section 5 |
| DNS port | 5387 on macOS and Linux. On macOS the nameserver is `127.0.0.1`, not `127.87.0.1`. Windows port 53 is an undecided guess | Sections 7, 8 |
| macOS data path | A privileged Whalias container terminates kernel WireGuard. A root launchd helper script runs `wireguard-go -f utun` and owns one route for `10.254.253.0/24`. Names use stable tunnel addresses, not `127.87` and not the bridge address. No relays, no `lo0` aliases, no root bind of 80 and 443. The app's first launch adds the public CA to the System keychain. Spikes 21 and 22 | Section 3 |
| Linux data path | Loopback and relays, unchanged. Not the macOS tunnel | Sections 3, 6 |
| Windows data path | Not decided. Earlier loopback guesses are left in the text. Not converted to the tunnel | Sections 3, 8, 15 |
| macOS privileged ports | Superseded on macOS by the tunnel. The spike record stands: Docker Desktop 4.99.0's publish binds `*:80` and `*:443`, and a root bind of one alias blocks a later wildcard publish. That bind is not the macOS product | Sections 6, 11, 12 |
| Bootstrap | On macOS, one ordinary bridge named `whalias`, not `--internal`, not `10.250.0.0/16`, not `whalias-edge`. On Linux, both networks, and the controller joins `whalias` with the alias `wha.internal` | Section 7 |
| HTTPS port choice | On macOS, pass the handshake through when TCP 443 accepts; otherwise proxy to `dev.whalias.http-port` or the first of 80, 8080, 3000, 5000, 8000 that accepts; if none accept, no proxy. On Linux, the relay uses the expose list: the label, else the first of those ports that is exposed, else no 443 | Section 10 |
| Name collisions | Reserved, then labels, then compose services, then container names; first started wins a tie | Section 5 |
| Settings | A host `config.env` passed with `--env-file`; `whalias config get` and `set` | Section 7 |
| Controller interface | `docker exec` subcommands printing versioned JSON, plus an internal HTTP API on port 8087 | Section 7 |
| DNS caching | TTL 5 seconds, SOA minimum 1 second | Section 8 |
| Updates | Script first, then idempotent install. A versioned relay protocol is the Linux plan, not this macOS build | Section 11 |
| Repository | One repository, `whalias/whalias`, plus the Homebrew tap | Section 13 |
| Testing | Go unit tests, Linux end-to-end tests in CI, a manual checklist on macOS and Windows | Section 13 |
| Platform priority | macOS on Apple Silicon first; Linux and Windows from best guesses, tested later | Sections 13, 15 |
