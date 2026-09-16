# Installation and uninstallation

[Overview](../README.md) · [CLI](../cli/README.md) · [Worker](../worker/README.md) · [Releases](releases.md)

## Install from public GitHub releases

Requires Linux amd64/arm64, `curl`, GNU `tar`, coreutils and a running systemd user manager. Run from a user login session. No Go, Node.js or GitHub token is needed. Private releases are not supported.

Set worker variables before installing:

- `JOBD_API_KEY`: shared controller key. Without it, only local jobs run.
- `JOBD_CONTROLLER`: your endpoint. Defaults to the project's hosted endpoint.
- `JOBD_QUEUE`: queue name; default `default`.
- `JOBD_STATE_DIR`: optional state directory.
- `JOBD_LOCAL_PERSIST=true`: preserve the local queue across restarts. Default: memory-only.

Self-hosters should use their own endpoint and key.

After a public release exists, download and review the installer:

```sh
curl -fSL https://raw.githubusercontent.com/Gzsiceberg/jobd/main/install.sh -o install.sh
# Review install.sh, then:
sh install.sh --bin-dir "$HOME/.local/bin"
```

Or install directly:

```sh
curl -fsSL https://raw.githubusercontent.com/Gzsiceberg/jobd/main/install.sh | sh
```

| Setting | Default | Override |
| --- | --- | --- |
| Binary directory | `~/.local/bin` | `--bin-dir` or `JOBD_BIN_DIR` |
| Release | Latest non-prerelease | `--version vX.Y.Z` or `JOBD_VERSION` |
| Public repository | `Gzsiceberg/jobd` | `JOBD_REPO` |

The installer verifies SHA-256 checksums. It installs `jobd`, `jobd-worker`, `jobd-uninstall` and `jobd-LICENSE`. Checksums detect corruption, not a compromised publisher.

Archives must include the MIT license. Releases v0.1.1 and earlier need their matching older installer.

## Run the installed binaries

The installer enables and starts `jobd-worker.service`. The unit lives in `${XDG_CONFIG_HOME:-~/.config}/systemd/user` and uses your chosen binary directory.

Settings are imported into the user manager's memory. No key file is written. An unset key clears the old key. Other user services may inherit these values. After a user-manager restart, reimport the key with `jobd --restart`.

No sudo, lingering or shell changes are made. The service runs while the user manager is active. Stop manual workers using the same state directory first.

```sh
export PATH="$HOME/.local/bin:$PATH"
jobd --help
jobd --restart
journalctl --user -u jobd-worker.service
```

See [worker configuration](../worker/README.md) and [CLI commands](../cli/README.md).

## Upgrade or downgrade

Rerun the installer. Use `--version` to pin a release. Keep the key exported. Installation restarts the service and cancels active work.

- `.jobd-install.sha256` tracks managed files and the service.
- Unmanaged or modified files are not silently overwritten. Move conflicting files aside.
- Use systemd drop-ins, not edits to the managed unit.
- Staged replacements roll back on replacement failure. A systemctl failure leaves a tracked install for retry or uninstall.
- `.jobd-install.lock` and `.jobd-service.lock` block concurrent operations.

After a crash, inspect `.jobd-stage.*` recovery files. Remove stale locks only after confirming no installer is running.

## Uninstall

```sh
jobd-uninstall
```

Or, from a source checkout:

```sh
sh uninstall.sh --bin-dir "$HOME/.local/bin"
```

The uninstaller validates files, stops/disables the managed service and removes tracked files. Modified files or symlinks must be moved aside before retrying.

State, logs, service drop-ins, unrelated files and PATH settings remain. Stop manual workers yourself. Older binary-only installs are supported.

No network or GitHub login is needed. Removing a managed service requires access to the user manager.

Sources: [install.sh](../install.sh), [uninstall.sh](../uninstall.sh).
