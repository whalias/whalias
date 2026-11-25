# macOS release checklist

Follow this from the current tree to a macOS release other people can install. The product shape is [host-app.md](host-app.md) and [WHALIAS-DESIGN.md](../WHALIAS-DESIGN.md) section 14.

Signing is out of this list. No Apple Developer Program, no Developer ID, no notarization, no Mac App Store. Those cost $99 a year and can wait. The release is an unsigned DMG on a GitHub release, and a Homebrew cask. A person who downloads the DMG allows it from System Settings, Privacy & Security.

The scripts in `scripts/macos/` stay until the app replaces them. They are how this machine runs Whalias today. They are not the release.

## Already done

- [x] The controller container answers DNS, holds the CA, terminates HTTPS when the probe says so, and DNATs to the container's current bridge address.
- [x] A root helper can own the `utun` and the route `10.254.253.0/24`, and it tears that down when the controller is gone.
- [x] OrbStack spikes 26–29 passed. The app will use the current Docker context. It will not look for `/Applications/Docker.app`.

## 1. Controller the app can ship

- [ ] Export each name's probe result as `http://…`, `https://…`, or no link, so the window and `whalias ls --json` can show an Open action.
- [ ] Publish `docker.io/whalias/whalias` and pin the digest the app will pull.
- [ ] Add the image setting. Docker Hub is the default. The other value is a cloned repository, and the app builds the controller from that folder. There is no free-form registry URL.

## 2. Helper the app installs

- [ ] Ship `wireguard-go` and `wg` inside the app, with the upstream license texts. First launch copies them to `/usr/local/libexec/whalias/`.
- [ ] At start, use the first pair that runs and is at least as new as the shipped pair: `/opt/homebrew/bin/`, then `/usr/local/bin/`, then the libexec copies. Do not search `PATH`.
- [ ] The app, running as the user, talks to the current `docker` context and records the socket. The helper reads that record. It does not open Docker.app and it does not run `context inspect desktop-linux`.
- [ ] Write `AssociatedBundleIdentifiers` on `dev.whalias.tunnel` so Login Items shows **Whalias**.
- [ ] Keep `KeepAlive` false. A dead controller still tears the tunnel down, and launchd does not restart that exit.
- [ ] On each start, including boot, if the Whalias.app bundle is gone, the helper runs uninstall and then removes itself.

## 3. The app

- [ ] Unsandboxed app: Dock icon, window, and menu-bar icon. Closing the window hides it. Quitting it leaves names working.
- [ ] First launch is the whole setup, with one administrator prompt:
  - [ ] Find Docker. If it is missing or not running, stop and say so.
  - [ ] Write `/etc/whalias/config.env`.
  - [ ] Install the helper, the WireGuard copies, the LaunchDaemon, and `/etc/resolver/wha.internal`.
  - [ ] Pull the Docker Hub digest, or build from the chosen repository.
  - [ ] Create the `whalias` bridge and the `whalias-data` volume, and start the `whalias` controller. Leave the person's other containers alone.
  - [ ] Add the public CA to the System keychain. Leave the private key in the volume.
  - [ ] Start `dev.whalias.tunnel`.
  - [ ] Install `/usr/local/bin/whalias`.
- [ ] The window shows Docker, the controller, the tunnel, and the names. Open appears only when the probe has a target. This replaces `doctor`. There is no `whalias doctor`.
- [ ] The window can restart the helper, start and stop the controller, pull or rebuild its image, and start, stop, or restart app containers Whalias already lists.
- [ ] The window can turn the hosts-file fallback on and off. It stays off until the person turns it on.
- [ ] The window can remove the CA and its trust setting.
- [ ] A later launch repairs a missing piece and does not repeat first-launch setup.
- [ ] `whalias ls`, `whalias open`, and `whalias uninstall` are the only commands. Uninstall deletes the command.

## 4. Uninstall

- [ ] Uninstall in the window, `whalias uninstall`, and the Homebrew cask uninstall run the same cleanup.
- [ ] That cleanup removes the LaunchDaemon, the helper, the resolver file, the route, the `utun`, the CA trust, the config, the key directory, and `/usr/local/bin/whalias`.
- [ ] When Docker answers, it also removes the `whalias` controller, the `whalias` bridge, and the `whalias-data` volume. It does not remove containers the person created.
- [ ] When Docker does not answer, those three objects stay and the window says so.
- [ ] Dragging the app to the Trash does not run cleanup. The helper's next start does, as in section 2. Document that leftovers sit until the next boot, and that a boot with Docker down leaves the controller behind.

## 5. Package it, still unsigned

- [ ] GitHub release with an unsigned DMG. Release notes say the app is unsigned and point at System Settings, Privacy & Security.
- [ ] Homebrew cask `whalias/tap/whalias` that installs the app and whose uninstall runs the cleanup above. The cask does not depend on `wireguard-go` or `wireguard-tools`.
- [ ] The release app's digest matches the Docker Hub image it pulls.

## 6. Call it a release only after this

Run these on a Mac that did not build the app, once from the DMG and once from the cask.

- [ ] Docker Desktop: first launch, then `https://` for a proxied name opens in Safari or Chrome without a certificate warning.
- [ ] OrbStack as the current context: the same path, without a Docker.app install.
- [ ] Quit the app. The name still resolves and the page still loads.
- [ ] Stop the controller. The route and the `utun` go away and stay away.
- [ ] Uninstall from the app. The service, the CA, the command, and the controller are gone. A Compose project that was already running is still there.
- [ ] Install again, drag Whalias.app to the Trash, reboot with Docker running. The service, the command, the CA, and the controller are gone.
- [ ] Firefox is documented as its own certificate store. It is not a release blocker.
