# Windows release checklist

This mirrors the macOS release in [macos-release.md](macos-release.md). The tray app is the install. First launch does the setup. There is no `install.ps1` in the product, and there is no `doctor`. Quitting the tray leaves names working.

Signing is out of this list. No Authenticode certificate. A downloaded unsigned app shows SmartScreen's "More info", then "Run anyway". Windows 11 with Smart App Control turned on can refuse an unsigned app outright. That limit is accepted until signing is worth paying for. The release notes say so.

The Windows tunnel is not proven yet. The Windows spike scripts are local research and are not in this repository. They have not been run. The loopback sentences in [WHALIAS-DESIGN.md](../WHALIAS-DESIGN.md) are old guesses. They are not this checklist. Section 1 below is the gate. Do not build the service or the tray on a path those spikes reject.

The shape, once the spikes pass, is the Mac one: a privileged controller container, a host WireGuard tunnel to `10.254.253.0/24`, DNS aimed at that controller, and the app as the only installer.

## 1. Prove the path

Run on Windows 11 with Docker Desktop, from an elevated PowerShell. Keep the logs in local research notes. Do not commit them.

- [ ] Run the local Windows spike scripts.
- [ ] The controller can publish DNS on `127.0.0.1` and an NRPT rule can send `wha.internal` there. The spike decides the port. macOS uses 5387. NRPT may only be able to send to port 53. Use the port the spike shows, and write it into the design. Do not keep both.
- [ ] A privileged container can terminate kernel WireGuard, the same job the Mac controller does.
- [ ] `wireguard-go.exe` with `wintun.dll` beside it can own the host tunnel and the route `10.254.253.0/24`, and a name on that route reaches a container.
- [ ] HTTPS through the controller's CA works for a proxied name, and a container that accepts TCP 443 shows its own certificate.
- [ ] Stopping the controller tears the route and the adapter down, and Windows does not bring them back by itself.
- [ ] If a spike fails, stop this list and change the design before writing the service.

## 2. Service, the Mac helper

- [ ] A Windows service, running as SYSTEM, owns Wintun, the route `10.254.253.0/24`, and `wireguard-go`.
- [ ] Ship `wireguard-go.exe`, `wg.exe`, and `wintun.dll` in the app, with the upstream license texts. The service runs the copies it installed next to each other.
- [ ] Prefer an already installed pair only when it is at least as new as the shipped pair and `wintun.dll` is beside that `wireguard-go.exe`. Otherwise use the shipped copies.
- [ ] The tray app, as the user, talks to the current `docker` context and records the socket. The service reads that record. It does not look for a fixed Docker Desktop path.
- [ ] When the controller is gone, the service removes the route and the adapter and stays down. It does not restart that exit.
- [ ] On each start, including boot, if the app is no longer installed, the service runs uninstall and then removes itself.

## 3. The tray app

- [ ] A tray app in the user session. Closing the window leaves the tray. Quitting the tray leaves names working.
- [ ] First launch is the whole setup, with one administrator prompt:
  - [ ] Find Docker. If it is missing or not running, stop and say so.
  - [ ] Write `C:\ProgramData\Whalias\config.env`.
  - [ ] Install and start the service.
  - [ ] Add the NRPT rule for `wha.internal` from section 1.
  - [ ] Pull `docker.io/whalias/whalias` at the digest this release names, or build from a chosen repository when that setting is on.
  - [ ] Create the `whalias` bridge and the `whalias-data` volume, and start the `whalias` controller. Leave the person's other containers alone.
  - [ ] Add the public CA to the Local Machine root store. Leave the private key in the volume.
  - [ ] Install a `whalias` command on the user's `PATH`.
- [ ] The window shows Docker, the controller, the tunnel, and the names. Open appears only when the probe has a target. There is no `doctor`.
- [ ] The window can restart the service, start and stop the controller, pull or rebuild its image, and start, stop, or restart app containers Whalias already lists.
- [ ] The window can turn the hosts-file fallback on and off. It stays off until the person turns it on.
- [ ] The window can remove the CA and its trust setting.
- [ ] A later launch repairs a missing piece and does not repeat first-launch setup.
- [ ] `whalias ls`, `whalias open`, and `whalias uninstall` are the only commands. Uninstall deletes the command.
- [ ] The image setting matches macOS: Docker Hub by default, or a local build from a chosen folder.

## 4. Uninstall

- [ ] Uninstall in the window, `whalias uninstall`, and the winget uninstall run the same cleanup.
- [ ] That cleanup removes the service, the route, the adapter, the NRPT rule, the CA trust, the config, the key directory, and the `whalias` command.
- [ ] When Docker answers, it also removes the `whalias` controller, the `whalias` bridge, and the `whalias-data` volume. It does not remove containers the person created.
- [ ] When Docker does not answer, those three objects stay and the window says so.
- [ ] Deleting the app without Uninstall does not run cleanup. The service's next start does, as in section 2. Document that leftovers sit until the next boot, and that a boot with Docker down leaves the controller behind.

## 5. Package it, still unsigned

- [ ] GitHub release with an unsigned installer or zip. Release notes include the SmartScreen "More info", then "Run anyway" steps, and they say Smart App Control can block the app.
- [ ] A winget manifest that installs the app and whose uninstall runs the cleanup above. It does not require a separate WireGuard install.
- [ ] The release app's digest matches the Docker Hub image it pulls.

## 6. Call it a release only after this

Run these on a Windows 11 machine that did not build the app, once from the GitHub download and once from winget.

- [ ] Docker Desktop: first launch, then `https://` for a proxied name opens in Edge or Chrome without a certificate warning.
- [ ] Quit the tray. The name still resolves and the page still loads.
- [ ] Stop the controller. The route and the adapter go away and stay away.
- [ ] Uninstall from the app. The service, the NRPT rule, the CA, the command, and the controller are gone. A Compose project that was already running is still there.
- [ ] Install again, delete the app without Uninstall, reboot with Docker running. The service, the command, the CA, the NRPT rule, and the controller are gone.
- [ ] Firefox is documented as its own certificate store. It is not a release blocker.
