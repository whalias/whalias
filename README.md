# Whalias

![Whalias](docs/logo.svg)

Whalias gives each Docker container a name under `wha.internal`, such as `web.myproject.wha.internal`, on every port that container listens on.

You need macOS, and Docker Desktop or OrbStack running.

## Homebrew

```bash
brew install --cask whalias/tap/whalias
```

Open Whalias. The first launch asks for your password once, then the names work.

## GitHub release

1. Download `Whalias.dmg` from the [latest release](https://github.com/whalias/whalias/releases/latest).
2. Open the disk image and drag Whalias to Applications.
3. Open Whalias. If macOS blocks it, go to System Settings, Privacy & Security, and choose Open Anyway.
4. The first launch asks for your password once, then the names work.

## Uninstall

Choose Uninstall in the Whalias window, or run `whalias uninstall`.

For a Homebrew install you can also run:

```bash
brew uninstall --cask whalias
```

Dragging Whalias to the Trash does not remove the background service. Use Uninstall.
