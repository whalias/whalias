# Whalias on macOS

Whalias gives each app container a name under `wha.internal`. The Mac resolves that name to an address in `10.254.253.0/24` and routes that subnet through a `utun`. The app container publishes no ports. You open the app by its name.

The controller container is `whalias`, the image tag is `whalias:local`, and its Docker network is the ordinary bridge `whalias`. App containers use a bridge you create. The steps below are the macOS install. The full tunnel check for this Mac is [docs/macos-testing.md](macos-testing.md).

## What is already installed

Three programs are in place before setup:

- Docker Desktop is installed and running. Its context is `desktop-linux`.
- `/opt/homebrew/bin/wireguard-go` is executable.
- `/opt/homebrew/bin/wg` is executable.

On this Mac those two binaries come from Homebrew:

```bash
brew install wireguard-tools wireguard-go
```

`wireguard-tools` provides `wg`. `wireguard-go` provides `wireguard-go`. Setup looks at those two absolute paths. It exits if either file is missing, and it exits if the Docker Desktop binary below is missing.

## Use the Docker Desktop binary

A `docker` earlier on `PATH` can be OrbStack. Setup always calls the Docker Desktop binary. Leave Docker Desktop running, and leave the Docker context as it is.

```bash
export DOCKER=/Applications/Docker.app/Contents/Resources/bin/docker
$DOCKER context show
```

`context show` prints `desktop-linux`. Stop if it prints any other name.

The rest of this guide uses `$DOCKER` for every Docker command. Run the setup and uninstall commands from the repository root.

## Build the image

```bash
$DOCKER build --progress=plain -t whalias:local .
```

The build can take several minutes. The live-check script plans for about eight.

Setup starts a container named `whalias` from `whalias:local` when that container does not exist yet. When the container already exists, setup leaves it in place and starts it if it is stopped. A new build of the tag `whalias:local` does not replace that container: setup checks the image name, then keeps the container it found.

After a rebuild, when the new image should be the one that runs, remove the existing container with the Desktop binary and then run setup. That is the setup step. Continue at the route check below and skip the setup command in the next section:

```bash
$DOCKER stop whalias
$DOCKER rm whalias
sudo /bin/sh scripts/macos/setup.sh
```

## Run setup

Setup must run as root, with no arguments, from the account that runs Docker Desktop. Skip it when the rebuild commands above already ran it.

```bash
sudo /bin/sh scripts/macos/setup.sh
```

`sudo` in this terminal is required. The script exits if it is not root.

Setup writes `/etc/resolver/wha.internal`:

```text
nameserver 127.0.0.1
port 5387
```

It creates the bridge `whalias` and the volume `whalias-data` when they are absent. When no container named `whalias` exists, setup creates it on that bridge with `whalias-data` mounted at `/data`. When the container already exists, setup starts it if it is stopped. Setup then installs the helper to `/usr/local/libexec/whalias/helper.sh`, installs the LaunchDaemon, and bootstraps `system/dev.whalias.tunnel`. macOS may show a Background Items Added notice for `dev.whalias.tunnel`.

`KeepAlive` is false. When the controller is not running, the helper removes the `10.254.253.0/24` route and the `utun`, then exits, and launchd leaves the job down. The helper log is `/var/log/whalias/tunnel.log`.

Setup prints these lines when it finishes:

```text
controller whalias is running
WireGuard publish:
127.0.0.1:<host-port>
resolver /etc/resolver/wha.internal
LaunchDaemon system/dev.whalias.tunnel
helper log /var/log/whalias/tunnel.log
```

The host port on the WireGuard line is chosen by Docker. The helper reads it.

Confirm the route. Repeat this until the destination, mask, and interface match. The helper waits for the controller's public key and the published UDP port, which can take up to about a minute and a half:

```bash
route -n get 10.254.253.1
```

While the tunnel is up the route contains:

```text
destination: 10.254.253.0
       mask: 255.255.255.0
  interface: utunN
```

`utunN` is the name the kernel assigned. If the route never appears, read `/var/log/whalias/tunnel.log`. A ready helper logs `ready on utunN via 127.0.0.1:<host-port>`.

## Start an app container

The controller joins a normal user bridge. It refuses `host`, `none`, the default bridge named `bridge`, and its own network named `whalias`, and it refuses any network that uses the `host` or `null` driver. It refuses a network with no IPv4 subnet. It refuses a subnet that overlaps `10.254.253.0/24`, `10.254.254.0/30`, or `127.0.0.0/8`, and a subnet that overlaps a network it has already joined. `$DOCKER logs whalias` prints `whalias: skip network <name>:` and the reason for each refusal. Create a bridge and let Docker choose the subnet:

```bash
export DEMO="$HOME/whalias-demo"
mkdir -p "$DEMO"
$DOCKER network inspect whalias-demo-net
```

When inspect reports that the network does not exist, create it:

```bash
$DOCKER network create whalias-demo-net
```

When inspect succeeds, the network is already there. Keep it, including when the controller is already attached. The controller attaches itself to a new network. If you had disconnected the controller from this same network, it remembers that network and stays off until the network is removed and created again.

Repeat the next command until `whalias-demo-net` is in the list. The live checks wait up to 30 seconds:

```bash
$DOCKER container inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{printf "%s\n" $k}}{{end}}' whalias
```

The app image is `nginx:1.27-alpine`. nginx listens on port 80 inside the container. The container publishes no ports. One read-only file is the page:

```bash
printf '%s\n' 'hello from whalias' > "$DEMO/index.html"
chmod 644 "$DEMO/index.html"
$DOCKER image inspect nginx:1.27-alpine
```

When that inspect fails, pull the image:

```bash
$DOCKER pull nginx:1.27-alpine
```

If a container named `whalias-demo` is already there, remove it with `$DOCKER` before the run:

```bash
$DOCKER stop whalias-demo
$DOCKER rm whalias-demo
```

Start the container. There is no `-p`:

```bash
$DOCKER run -d --name whalias-demo --network whalias-demo-net -v "$DEMO/index.html:/usr/share/nginx/html/index.html:ro" nginx:1.27-alpine
```

## Names

The stable address follows the container identity. Identity is the Compose project, service, and container number when `com.docker.compose.project`, `com.docker.compose.service`, and `com.docker.compose.container-number` are all set. Otherwise it is the container name. A container started with `docker run --name whalias-demo` is `whalias-demo.wha.internal`.

When the Compose project and service labels are set, Whalias also publishes `service.project.wha.internal`.

Each name is lowercased. Dots stay as label separators. Inside each label, any character other than a letter, digit, or hyphen becomes a hyphen, repeated hyphens merge into one, and hyphens at either end are trimmed. A label longer than 63 characters is cut to 63. `My_App` becomes `my-app.wha.internal`.

Whalias never publishes `whalias.wha.internal` or `wha.internal` itself. The controller container already uses the name `whalias`. Give the app a different name.

DNS publishes an address in `10.254.253.0/24` (from `.1` through `.254`). It does not publish the container's address on the bridge. The same identity keeps that address after the container is removed and created again.

## Open the name

```bash
dscacheutil -q host -a name whalias-demo.wha.internal
curl -4 --http1.0 http://whalias-demo.wha.internal/
```

`dscacheutil` prints an `ip_address` in `10.254.253.0/24`. `curl` prints `hello from whalias`. If the name has no address yet, wait a few seconds and repeat both commands. The live checks retry for about a minute.

The browser address for this container is `http://whalias-demo.wha.internal/`.

## HTTPS with the Whalias CA

This nginx container listens on port 80. When a container is not listening on 443, the controller tries port 80, then 8080, 3000, 5000, and 8000, or only the port in the label `dev.whalias.http-port` when that label is set. If one of those accepts a connection, HTTPS to the name presents the Whalias certificate and forwards to that port. The certificate authority is `/data/ca.crt` in the controller. Copy it out and pass it to `curl`. Leave the System keychain and the login keychain unchanged.

```bash
$DOCKER cp whalias:/data/ca.crt "$DEMO/ca.crt"
curl -4 --http1.0 -v --cacert "$DEMO/ca.crt" https://whalias-demo.wha.internal/
```

The body is `hello from whalias` again. The verbose output has an `issuer:` line that includes `Whalias`.

A container that listens on 443 is passed through. The Whalias CA is the wrong trust anchor for that server's certificate. [docs/macos-testing.md](macos-testing.md) covers that case.

## Remove the helper

Uninstall must run as root, with no arguments, from the repository root:

```bash
sudo /bin/sh scripts/macos/uninstall.sh
```

Uninstall boots out `system/dev.whalias.tunnel`, removes the stable route `10.254.253.0/24`, and destroys the Whalias `utun` (the one with `10.254.254.1`, or the one whose WireGuard key matches `/var/lib/whalias/mac.key`). A `utun` that is not that tunnel is left alone.

It then deletes these three files:

- `/Library/LaunchDaemons/dev.whalias.tunnel.plist`
- `/etc/resolver/wha.internal`
- `/usr/local/libexec/whalias/helper.sh`

It removes the directory `/usr/local/libexec/whalias` when that directory is empty. The copy of the helper in the repository stays.

Uninstall leaves the rest in place:

- Docker Desktop and the Docker context
- the `whalias` container, the `whalias` network, and the `whalias-data` volume
- `/var/lib/whalias/mac.key` when that file exists, and the rest of `/var/lib/whalias`
- `/var/log/whalias`

When uninstall finishes cleanly it prints that Docker Desktop, the Docker context, and the `whalias` container are unchanged, that the private key remains at `/var/lib/whalias/mac.key` when that file exists, and that it removed `system/dev.whalias.tunnel`, the plist, and `/etc/resolver/wha.internal`.

Running setup again later starts the same controller container and bootstraps the helper again.

## Full check

[docs/macos-testing.md](macos-testing.md) is the numbered runbook for this Mac: the route, the nginx container, HTTPS, a container that listens on 443, recreate, stopping the controller, and uninstall.
