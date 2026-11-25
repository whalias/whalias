# Whalias tunnel checks on this Mac

Run this in your own terminal, from start to finish in one shell, so `DOCKER` and `DEMO` stay set. `sudo` in this terminal is required for setup and for uninstall. Leave Docker Desktop running. Leave the Docker context as it is.

The app image throughout is `nginx:1.27-alpine`. nginx listens on port 80 inside the HTTP container. Nothing in this runbook publishes that port.

This is a note for you, not a line the scripts print. Setup on the morning of 23 November 2026 already installed the helper that is in the repository, including the script checks added after the passing 20 November 2026 live run. A refusal from setup is one of those checks. When the refusal names the existing `whalias` container, setup leaves that container unchanged.

The five checks follow `test/macos/live-checks.sh`. The names here are the demo names: the HTTP container is `whalias-demo` on the bridge `whalias-demo-net`, and the container that listens on 443 is `whalias-demo-tls`.

## 1. Set the Docker binary and the working directory

```bash
export DOCKER=/Applications/Docker.app/Contents/Resources/bin/docker
export DEMO="$HOME/whalias-demo"
mkdir -p "$DEMO"
```

`$DOCKER` is `/Applications/Docker.app/Contents/Resources/bin/docker`. Use it for every Docker command below. A `docker` earlier on `PATH` can be OrbStack.

Demo files go in `$DEMO` (`$HOME/whalias-demo`), which Docker Desktop can mount. They stay out of `test/macos`.

## 2. Remove a leftover demo container

The network `whalias-demo-net` may already exist, and the controller may already be attached to it. These commands tolerate that.

Remove a leftover HTTP container. If Docker says the container is already gone, continue:

```bash
$DOCKER stop whalias-demo
$DOCKER rm whalias-demo
```

Remove a leftover TLS container from an earlier pass of this runbook the same way:

```bash
$DOCKER stop whalias-demo-tls
$DOCKER rm whalias-demo-tls
```

A clean network is optional. Skip the next two commands to keep `whalias-demo-net` and any current attachment. To drop it and let the controller join a new one, disconnect `whalias` first, then remove the network. If Docker says the container is not connected, or the network is already gone, continue:

```bash
$DOCKER network disconnect whalias-demo-net whalias
$DOCKER network rm whalias-demo-net
```

Disconnecting the controller makes it remember that network id. It joins again after the network is removed and created again.

## 3. Confirm the context

```bash
$DOCKER context show
```

The output is `desktop-linux`. Stop if it prints any other name.

## 4. Build whalias:local

From the repository root:

```bash
$DOCKER build --progress=plain -t whalias:local .
```

Allow several minutes. The live-check script plans for about eight.

The build updates the `whalias:local` tag. It does not replace a container named `whalias` that is already there. Setup, in the next step, keeps that container when its image name is `whalias:local` and starts it only if it is stopped.

Use the image you just built only when the running controller should be that new image. That is the rebuild case. Remove the existing container with the Desktop binary, then run setup from the repository:

```bash
$DOCKER stop whalias
$DOCKER rm whalias
sudo /bin/sh scripts/macos/setup.sh
```

Skip those three commands when the controller that is already running is the one you want. Removing it tears the tunnel down until setup bootstraps the helper again. If you ran setup here, continue at step 6.

## 5. Set up when the route is not already up

```bash
route -n get 10.254.253.1
```

The tunnel is already up when all three of these are present:

```text
destination: 10.254.253.0
       mask: 255.255.255.0
  interface: utunN
```

`utunN` is whichever `utun` the kernel assigned. Note that interface name. When those three lines are present, skip the setup command and continue at step 6.

Otherwise, from the repository root, run setup with no arguments:

```bash
sudo /bin/sh scripts/macos/setup.sh
```

Setup must be root. It exits if `/Applications/Docker.app/Contents/Resources/bin/docker`, `/opt/homebrew/bin/wireguard-go`, or `/opt/homebrew/bin/wg` is missing. It writes `/etc/resolver/wha.internal` with `nameserver 127.0.0.1` and `port 5387`, installs `/usr/local/libexec/whalias/helper.sh`, and bootstraps LaunchDaemon `system/dev.whalias.tunnel`. `KeepAlive` is false. macOS may show a Background Items Added notice for `dev.whalias.tunnel`. The helper log is `/var/log/whalias/tunnel.log`.

A finished setup prints `controller whalias is running`, a `WireGuard publish:` line for `127.0.0.1`, `resolver /etc/resolver/wha.internal`, `LaunchDaemon system/dev.whalias.tunnel`, and `helper log /var/log/whalias/tunnel.log`.

## 6. Wait for the route

Repeat until the destination, mask, and interface match. The helper waits for the controller public key and the published UDP port. That can take up to about a minute and a half:

```bash
route -n get 10.254.253.1
```

```text
destination: 10.254.253.0
       mask: 255.255.255.0
  interface: utunN
```

Record `utunN`. Confirm the tunnel address:

```bash
ifconfig utunN
```

Replace `utunN` with the interface from the route. The output includes `inet 10.254.254.1`.

If the route never appears, read `/var/log/whalias/tunnel.log`. A ready helper logs `ready on utunN via 127.0.0.1:<host-port>`.

## 7. Create the demo network if needed

```bash
$DOCKER network inspect whalias-demo-net
```

When inspect reports that the network does not exist, create it. `network create` with no extra flags makes a bridge and lets Docker choose the subnet:

```bash
$DOCKER network create whalias-demo-net
```

When inspect succeeds, skip `network create`.

The controller joins a normal user bridge. It refuses `host`, `none`, the default bridge named `bridge`, and its own network named `whalias`, and it refuses any network that uses the `host` or `null` driver. It refuses a network with no IPv4 subnet. It refuses a subnet that overlaps `10.254.253.0/24`, `10.254.254.0/30`, or `127.0.0.0/8`, and it refuses a subnet that overlaps a network it has already joined.

Wait until the controller's networks include `whalias-demo-net`. If it was already attached, the name is already in the list. The live checks retry for 30 seconds:

```bash
$DOCKER container inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{printf "%s\n" $k}}{{end}}' whalias
```

If the name never appears, the controller remembered a disconnect of this network, or it refused the network. `$DOCKER logs whalias` prints `whalias: skip network whalias-demo-net:` and the reason when it refused. For a remembered disconnect, run the disconnect and remove commands in step 2, then `network create` again, and repeat the inspect.

## 8. Start nginx with no published ports

```bash
printf '%s\n' http-body-1 > "$DEMO/index.html"
chmod 644 "$DEMO/index.html"
$DOCKER image inspect nginx:1.27-alpine
```

When that inspect fails:

```bash
$DOCKER pull nginx:1.27-alpine
```

The first pull can take a few minutes. The live-check script plans for about three.

Start the container. There is no `-p`:

```bash
$DOCKER run -d --name whalias-demo --network whalias-demo-net -v "$DEMO/index.html:/usr/share/nginx/html/index.html:ro" nginx:1.27-alpine
```

## Check 1. The name, the file, and no published ports

The container has no published ports. The port command prints nothing. `PortBindings` is `null`, `{}`, or `map[]`:

```bash
$DOCKER port whalias-demo
$DOCKER container inspect -f '{{json .HostConfig.PortBindings}}' whalias-demo
```

The name resolves to an address in `10.254.253.0/24`, from `10.254.253.1` through `10.254.253.254`. That address is the stable tunnel address, not the bridge address.

```bash
dscacheutil -q host -a name whalias-demo.wha.internal
/usr/bin/curl -q -4 -fsS --max-time 8 --http1.0 -w '\nremote_ip=%{remote_ip}\n' http://whalias-demo.wha.internal/
```

The body is `http-body-1`. `remote_ip` is the same address `dscacheutil` printed. If the name has no address yet, wait a few seconds and repeat. The live checks retry for about a minute.

Record `remote_ip`. Check 3 compares against it.

## Check 2. Whalias HTTPS, then a container that listens on 443

Copy the public CA out of the controller. The file in the container is `/data/ca.crt`, on the `whalias-data` volume. Pass that file to `curl`. Leave the System keychain and the login keychain unchanged.

```bash
$DOCKER cp whalias:/data/ca.crt "$DEMO/ca.crt"
```

If the copy fails, wait a second and repeat it. The file is the public CA.

The HTTP container is not listening on 443, so HTTPS presents the Whalias certificate and the body is still the file:

```bash
/usr/bin/curl -q -4 -fsSv --http1.0 --max-time 15 --cacert "$DEMO/ca.crt" https://whalias-demo.wha.internal/
```

This exits 0. The body is `http-body-1`. The verbose output contains an `issuer:` line that includes `Whalias`. The live checks retry this for about a minute when the proxy is not ready yet.

The second container listens on 443 with its own certificate. The certificate's CN is the container's `wha.internal` name. Write the OpenSSL config, then the certificate, then the nginx config. These are the commands from `test/macos/live-checks.sh`, with the demo name:

```bash
cat > "$DEMO/tls.cnf" <<'EOF'
[req]
distinguished_name = req_distinguished_name
x509_extensions = v3_req
prompt = no
[req_distinguished_name]
CN = whalias-demo-tls.wha.internal
[v3_req]
subjectAltName = DNS:whalias-demo-tls.wha.internal
basicConstraints = CA:FALSE
keyUsage = digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
EOF
/usr/bin/openssl req -x509 -newkey rsa:2048 -sha256 -days 2 -nodes -keyout "$DEMO/tls.key" -out "$DEMO/tls.crt" -config "$DEMO/tls.cnf"
chmod 644 "$DEMO/tls.crt" "$DEMO/tls.key"
cat > "$DEMO/nginx-tls.conf" <<'EOF'
server {
    listen 443 ssl;
    server_name whalias-demo-tls.wha.internal;
    ssl_certificate /certs/tls.crt;
    ssl_certificate_key /certs/tls.key;
    location / {
        default_type text/plain;
        return 200 "tls-own\n";
    }
}
EOF
$DOCKER run -d --name whalias-demo-tls --network whalias-demo-net -v "$DEMO/tls.crt:/certs/tls.crt:ro" -v "$DEMO/tls.key:/certs/tls.key:ro" -v "$DEMO/nginx-tls.conf:/etc/nginx/conf.d/default.conf:ro" nginx:1.27-alpine
```

There is no `-p`. Repeat the log command until it contains `start worker process`. The live checks wait up to 20 seconds:

```bash
$DOCKER logs whalias-demo-tls
```

The Whalias CA must not verify this certificate. The next command exits non-zero. `-f` is omitted here, matching the live checks, so a certificate failure is the non-zero status:

```bash
/usr/bin/curl -q -4 -sSv --http1.0 --max-time 15 --cacert "$DEMO/ca.crt" https://whalias-demo-tls.wha.internal/
```

Trust the server certificate itself. This exits 0. The body is `tls-own`. The verbose output includes `whalias-demo-tls.wha.internal`, and `remote_ip` is an address in `10.254.253.0/24`:

```bash
/usr/bin/curl -q -4 -fsSv --http1.0 --max-time 15 --cacert "$DEMO/tls.crt" -w '\nremote_ip=%{remote_ip}\n' https://whalias-demo-tls.wha.internal/
```

## Check 3. Recreate the HTTP container

The address stays with the name. Remove the HTTP container, put a new body in the same file, and start the same name again.

```bash
$DOCKER stop whalias-demo
$DOCKER rm whalias-demo
printf '%s\n' http-body-2 > "$DEMO/index.html"
chmod 644 "$DEMO/index.html"
$DOCKER run -d --name whalias-demo --network whalias-demo-net -v "$DEMO/index.html:/usr/share/nginx/html/index.html:ro" nginx:1.27-alpine
```

Repeat until the body is the new file and `remote_ip` equals the address recorded in check 1. The live checks retry for about a minute:

```bash
dscacheutil -q host -a name whalias-demo.wha.internal
/usr/bin/curl -q -4 -fsS --max-time 8 --http1.0 -w '\nremote_ip=%{remote_ip}\n' http://whalias-demo.wha.internal/
```

The body is `http-body-2`. The address is unchanged.

## Check 4. Stop the controller

```bash
$DOCKER stop whalias
```

The helper sees that the controller is not running, deletes the `10.254.253.0/24` route, destroys its `utun`, and exits. `KeepAlive` is false, so launchd leaves it down. `/var/log/whalias/tunnel.log` gets `controller whalias is not running, was replaced, or Docker could not be inspected`, then `teardown utunN` and `utunN is gone`.

Repeat the route command for up to about 40 seconds:

```bash
route -n get 10.254.253.1
```

The stable route is gone when the output no longer shows `destination: 10.254.253.0` with `mask: 255.255.255.0` on a `utun`. That is the only route condition the live checks test. With no stable route, the lookup falls through to the Mac's normal default route. In the 20 November 2026 run, the output after the stop was:

```text
destination: default
       mask: default
    gateway: 10.0.0.1
  interface: en0
```

Your gateway and interface can differ.

The `utun` that had `10.254.254.1` is gone. Use the interface name recorded while the tunnel was up:

```bash
ifconfig utunN
```

`ifconfig` fails for that name.

To use Whalias again after this stop, run setup from the repository. Setup starts the existing `whalias` container and bootstraps the helper:

```bash
sudo /bin/sh scripts/macos/setup.sh
```

`$DOCKER start whalias` leaves the helper down, because the helper has already exited and `KeepAlive` is false. Run setup only if you still want the controller. To remove the helper instead, leave the container stopped and continue to check 5.

## Check 5. Uninstall, then remove the demo

From the repository root, with no arguments:

```bash
sudo /bin/sh scripts/macos/uninstall.sh
```

Uninstall boots out `system/dev.whalias.tunnel`, tears down the stable route and the Whalias `utun`, and deletes:

- `/Library/LaunchDaemons/dev.whalias.tunnel.plist`
- `/etc/resolver/wha.internal`
- `/usr/local/libexec/whalias/helper.sh`

It removes `/usr/local/libexec/whalias` when the directory is empty. The helper script in the repository stays.

It leaves Docker Desktop, the Docker context, and the `whalias` container as they are. `/var/lib/whalias/mac.key` stays when that file exists. The rest of `/var/lib/whalias`, `/var/log/whalias`, the `whalias` network, and the `whalias-data` volume stay as well.

A clean uninstall prints that Docker Desktop, the Docker context, and the `whalias` container are unchanged, that the private key remains at `/var/lib/whalias/mac.key` when that file exists, and that it removed `system/dev.whalias.tunnel`, the plist, and `/etc/resolver/wha.internal`.

The plist, the resolver, and the installed helper are gone when each `test` is silent and the following `echo` prints `0`:

```bash
test ! -e /Library/LaunchDaemons/dev.whalias.tunnel.plist
echo $?
test ! -e /etc/resolver/wha.internal
echo $?
test ! -e /usr/local/libexec/whalias/helper.sh
echo $?
```

The stable route is still gone, as in check 4. The output does not show `destination: 10.254.253.0` with `mask: 255.255.255.0`:

```bash
route -n get 10.254.253.1
```

The `utun` recorded while the tunnel was up is still gone. Replace `utunN` with that name. If you ran setup again after check 4, use the `utun` from the route after that setup. `ifconfig` fails for it:

```bash
ifconfig utunN
```

The private key is still there:

```bash
ls -l /var/lib/whalias/mac.key
```

The container is still there. Its status is whatever it was before uninstall. After check 4, without the optional setup, that status is `exited`:

```bash
$DOCKER container inspect -f '{{.Name}} {{.State.Status}}' whalias
$DOCKER context show
```

The name is `/whalias`. `context show` still prints `desktop-linux`.

Remove the demo containers and the demo network. Stop and remove the containers first, then disconnect the controller, then remove the network. If a container or the network is already gone, continue:

```bash
$DOCKER stop whalias-demo
$DOCKER rm whalias-demo
$DOCKER stop whalias-demo-tls
$DOCKER rm whalias-demo-tls
$DOCKER network disconnect whalias-demo-net whalias
$DOCKER network rm whalias-demo-net
```
