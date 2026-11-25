# Whalias host app

Plan, written 25 November 2026, revised the same day. Nothing in this file is implemented. [WHALIAS-DESIGN.md](../WHALIAS-DESIGN.md) section 14 is the decision. This file is the shape of the app.

On macOS the app is the install. A person gets it from a DMG on a GitHub release, or from a Homebrew cask. There is no setup script in the product. The first launch does the host setup. The scripts in `scripts/macos/` are the current development stand-in on this machine. They are not what a release ships.

The app is a window the user can see. It lists names, reports whether Docker, the controller, and the tunnel are up, and runs the management actions below. The controller container still answers DNS, terminates HTTPS, and owns DNAT. On macOS the launchd helper still owns the `utun` and the route for `10.254.253.0/24`. Quitting the window leaves names working.

The window is a control panel. A native data path stays rejected, as section 14 records from 19 November 2026: a host process that watched Docker, answered DNS, and terminated HTTPS.

The Docker images are the part that stays outside the app. The default is Docker Hub. A development setting can build the controller from a cloned repository instead.

## Processes

| Piece | Runs as | Names keep working after the window quits |
| --- | --- | --- |
| Window and menu-bar icon | The logged-in user | Yes |
| Privileged helper | root on macOS and Linux, SYSTEM on Windows | Yes |
| `whalias` container | Docker, `--restart=always` | Yes |
| `whalias` command | The logged-in user, when they run it | Yes |

The window talks to Docker as the user who runs the engine. It uses that user's `docker` client and the current context. It does not look for `/Applications/Docker.app`. An action that installs a route, a resolver file, a launchd job, a System keychain trust, or the `whalias` command asks for an administrator password once. The window process does not stay root.

## How it gets onto a Mac

Two installs, and both of them are the app:

- A DMG attached to a GitHub release. The person drags Whalias.app to Applications and opens it.
- A Homebrew cask, `brew install --cask whalias/tap/whalias`. The cask installs the app. It does not install a setup script, and it does not depend on `wireguard-go` or `wireguard-tools`.

Opening the app the first time is the setup. There is no `whalias install` command and no `curl | sh` on macOS.

## First launch

The first window is the setup. One administrator prompt covers the host changes. After it finishes, names work without another install step.

1. Find Docker with the user's `docker` client and the current context. If that client is missing, or the engine does not answer, the window says so and waits. It does not look for Docker.app.
2. Write `/etc/whalias/config.env`. The image source defaults to Docker Hub.
3. Install the helper, the bundled `wireguard-go` and `wg`, `/Library/LaunchDaemons/dev.whalias.tunnel.plist`, and `/etc/resolver/wha.internal`. The plist sets `AssociatedBundleIdentifiers` to the app's bundle id, so Login Items shows Whalias.
4. Pull `docker.io/whalias/whalias` at the digest this release names, or build from the chosen repository when that setting is on. Create the `whalias` bridge and the `whalias-data` volume, and start the `whalias` controller. The user's other containers are left alone.
5. Wait until the controller has written its CA. Copy the public certificate out and add it to the System keychain as a trusted root. The private key stays in the volume. Firefox keeps its own store, so this step covers Safari and Chrome.
6. Bootstrap `dev.whalias.tunnel` so the helper starts.
7. Install the `whalias` command at `/usr/local/bin/whalias`.

The window then shows the engine, the controller, the tunnel, and the names. A later launch does not run this again. It repairs a missing piece when one is gone.

## Image source

A setting in the window, stored in the config file:

- **Docker Hub**, the default. The app pulls `docker.io/whalias/whalias` at the digest the running app was released with. A newer app brings a newer digest. The person does not type a registry URL.
- **Local build**, for development and testing. The person chooses a cloned repository. The app runs `docker build` in that directory and starts the controller from that image. This is not a second registry.

## Docker, without a fixed app path

The app and the `whalias` command call the user's `docker`. The current context is the engine, Docker Desktop or OrbStack. The app records that socket for the root helper. The helper reads the record. It does not search `PATH`, and it does not open `/Applications/Docker.app` or `context inspect desktop-linux`.

One local engine at a time. If both are installed, the current context is the one Whalias uses. If the context changes later, the window is where that gets pointed at the new socket.

## What the window shows

This is the health view. There is no `whalias doctor` and no separate diagnostics command.

- Docker: the engine answers, and which context the helper is aimed at.
- Controller: the `whalias` container is missing, stopped, or running.
- Tunnel: the privileged helper is up, and `10.254.253.0/24` is routed at its interface. On macOS that is `dev.whalias.tunnel`, the `utun`, and the route.
- Names: each `*.wha.internal` name, the container it belongs to, whether that container is running, and an Open button only when the controller's HTTP probe has a target.
- Anything wrong with the resolver file, the LaunchDaemon, the WireGuard handshake, or a DNS override. A DNS override offers the hosts-file switch. The switch stays off until the person turns it on.

The probe already decides the link inside the controller. It connects to the bridge address on 443, then to `dev.whalias.http-port` or 80, 8080, 3000, 5000, and 8000. The window needs that decision exported. A container with no accepting HTTP port, such as Postgres, shows its name and address and has no Open button.

A container that accepts TCP 443 shows its own certificate. A proxied name shows the Whalias certificate. The row says which.

## What the window does

Docker actions run as the user. No password.

- Create the `whalias` controller when it is missing, start it, stop it, and recreate it after a new image.
- Pull the Docker Hub digest, or rebuild from the chosen repository.
- Start, stop, and restart app containers Whalias already lists.

Compose and `docker run` still define those app containers. The window does not edit projects, build images except the controller in local-build mode, or create networks for them.

Tunnel actions ask for an administrator password, then use the root helper.

- Restart `dev.whalias.tunnel` when it has exited.
- Install and remove the LaunchDaemon, `/etc/resolver/wha.internal`, and the route.

CA actions ask for an administrator password. First launch already trusts the public certificate. The window can install it again or remove it and its trust setting. The private key stays in the controller volume.

Spike 14, on 20 November 2026, saw Safari and Chrome open `wha.internal` names with no warning after that trust. The root is name-constrained to `wha.internal`. Firefox keeps its own store.

The window can copy a URL, open a URL, and turn the hosts-file fallback on or off.

## The `whalias` command

The app installs this on first launch, at `/usr/local/bin/whalias`. Uninstall deletes it. It is a way to list names and open one from a terminal. It is not a second installer.

| Command | Does |
| --- | --- |
| `whalias ls` | Lists names and addresses. `--json` includes the probe result as `http://…`, `https://…`, or no link |
| `whalias open <name>` | Opens that name in the browser |
| `whalias uninstall` | The same cleanup as the window's Uninstall |

There is no `whalias doctor`, no `whalias install`, and no `whalias update`. Health is the window. Installing is opening the app. A new version is a new DMG or `brew upgrade --cask whalias`.

## Uninstall

The supported path is Uninstall in the window, or `whalias uninstall`. Homebrew's cask uninstall runs that same cleanup. One administrator prompt. It does not quit Docker.

When Docker answers, uninstall stops and removes the `whalias` controller, the `whalias` bridge, and the `whalias-data` volume. That volume holds the CA private key. It does not remove app containers the person created with Compose or `docker run`. When Docker does not answer, those Whalias objects are left, and the window says so.

It also removes the LaunchDaemon, the helper, the resolver file, the route, the `utun`, the public CA and its trust setting in the System keychain, the config file, the key directory, and `/usr/local/bin/whalias`.

Dragging Whalias.app to the Trash does not run any of that. macOS does not start an app as it is deleted. The helper covers that case because it is still installed. On each start, including `RunAtLoad` at the next boot, the helper looks for the app bundle it was given. If the bundle is gone, the helper runs this same cleanup and then removes itself. Until that boot, the service, the CLI, the CA, and the controller are still there. If Docker is not running at that boot, the controller, the bridge, and the volume stay behind. Nothing is left to try them again.

## macOS

A normal app: a Dock icon, a window, and a menu-bar icon. The icon is [logo.svg](logo.svg), the navy whale with three teal arrows. Closing the window hides it in the menu bar. The tunnel stays with launchd. `KeepAlive` stays false, so a dead controller still tears the tunnel down.

The app is unsandboxed. A sandboxed Mac App Store app cannot see the Docker socket or install a LaunchDaemon. Open at login applies to the window only. The LaunchDaemon starts at boot.

Privileged work uses the root helper and the system password dialog. A helper embedded in the app bundle, which installs itself without that script, waits until the app is Developer ID signed.

## Login Items name

System Settings > General > Login Items & Extensions names a LaunchDaemon from the program it starts, unless the job is tied to an app. The development helper starts `/bin/sh`, so that row is `sh`. The launchd label `dev.whalias.tunnel` is not that title. On 25 November 2026 the enabled `sh` row on this Mac was `dev.whalias.tunnel`, executable `/bin/sh`.

First launch writes `AssociatedBundleIdentifiers` with the app's bundle id. The app's `CFBundleDisplayName` is Whalias. Login Items then shows Whalias and the app icon. The line under the row already says whether the item is running in the background.

The helper stays a shell script. The title comes from the app, not from replacing `/bin/sh`.

## Host WireGuard binaries

The Mac app ships `wireguard-go` and `wg`. First launch copies them to `/usr/local/libexec/whalias/`, root-owned, so launchd does not depend on the app bundle staying in place. A person does not install them first.

The helper checks three places, in order, and does not search `PATH`:

1. `/opt/homebrew/bin/` for an Apple Silicon Homebrew install.
2. `/usr/local/bin/` for an Intel Homebrew install.
3. `/usr/local/libexec/whalias/` for the copies shipped with Whalias.

A pair is new enough when `wireguard-go --version` and `wg --version` both parse, and each snapshot date is at least the date of the copy that release ships. The helper log records the path and the version. An app update replaces the libexec copies when the bundle is newer. It leaves a newer Homebrew pair in place.

`wireguard-go` is MIT. wireguard-tools offers GPL-2.0 or MIT; the bundled `wg` is distributed under MIT, with the upstream license texts beside it. The first unsigned app includes unsigned copies of both. Notarization, when it happens, signs them with the app.

Windows uses the same rule once the tunnel is a service: `wireguard-go.exe` plus `wintun.dll` in the same directory. Linux native Engine does not need a host `wireguard-go`. A Docker Desktop Linux helper prefers the kernel device and a distro `wg`, and falls back to a bundled `wireguard-go` only when the kernel device is missing.

## Windows

The product path mirrors macOS. The tray app is the install. A GitHub release or winget puts it on the machine. First launch installs the service, the NRPT rule, the controller, the CA, and the `whalias` command. The service is the Mac helper: Wintun, the route, and `wireguard-go`. The tray is this window. Signing is deferred, the same way the Mac release skips notarization. The checklist is [windows-release.md](windows-release.md).

The data path is not proven. The Windows spike scripts have not been run. The loopback sentences in the design are earlier guesses, not this path. The checklist starts with those spikes. The window has not been started, and the service has not been started.

## Linux

systemd is the service manager on Ubuntu, Debian, Fedora, RHEL, and Arch. Other init systems are out of scope.

Native Docker Engine does not need a resident Whalias process on the host. The controller container is the service, and Docker restarts it. DNS is a drop-in for systemd-resolved, which the distro already runs. The CLI lists names and full URLs, and `whalias open` uses `xdg-open`. There is no `whalias doctor` on Linux either. A Linux health view is not part of this Mac decision.

Docker Desktop on Linux needs a host WireGuard process, because the containers are in a VM. That process is a system unit, root, `WantedBy=multi-user.target`. It is the same role as `dev.whalias.tunnel`. A tray icon is not part of this plan. GNOME and KDE do not share one tray API, and a Flatpak build would be sandboxed away from the Docker socket.

## Distribution

Building and running the Mac app on the developer's machine costs nothing. Xcode is free. A free Apple Account can compile and launch it. No notarization is required for that.

The steps to that Mac release are [macos-release.md](macos-release.md). Signing stays off that list.

The copies other people run are the DMG on a GitHub release, or the Homebrew cask. A browser download is what marks the app quarantined. An install that never comes from a browser often skips that prompt. An unsigned DMG is allowed from System Settings, Privacy & Security. That is the accepted first distribution.

The [Apple Developer Program](https://developer.apple.com/programs/) is $99 USD per year and is deferred until a downloaded app should open without that Settings step. Membership includes the Developer ID certificate and notarization. There is no per-upload fee. Already-signed copies keep running if the membership lapses. Signing the next version requires renewing. Only the Account Holder can create the Developer ID certificate. Prices are shown in local currency at enrollment. Nonprofits, schools, and government bodies can ask Apple to waive the fee.

The Mac App Store is out. The sandbox cannot see the Docker socket or install the LaunchDaemon, and this app is not sold through the store. The Apple Developer Enterprise Program is the wrong program. It is for private distribution through device management.

Windows Authenticode is a separate certificate from a different vendor, and only when a Windows build is distributed. It is not part of the Apple membership.
