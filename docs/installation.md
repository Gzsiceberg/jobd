# Installation and uninstallation

[Overview](../README.md) · [CLI](../cli/README.md) · [Worker](../worker/README.md) · [Releases](releases.md)

## Install from public GitHub releases

Requires Linux amd64/arm64, `curl`, GNU `tar` and coreutils. Private releases are not supported.

Set worker variables before starting the worker:

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

The installer copies binaries only. No service, sudo or shell changes. Local commands start a detached worker automatically. Remote commands remain client-only.

```sh
export PATH="$HOME/.local/bin:$PATH"
jobd --local echo hello      # starts worker if absent
jobd --restart               # start/restart with current settings
jobd --stop                  # stop worker
tail -f ~/.local/state/jobd-worker/worker.log
```

The worker inherits the CLI's environment and working directory. No key file is written. It survives SSH disconnects. After a crash or reboot, run a local command or `jobd --restart` again. For automatic recovery, use an external supervisor.

See [worker configuration](../worker/README.md) and [CLI commands](../cli/README.md).

## Upgrade or downgrade

Rerun the installer. Use `--version` to pin a release. Then run `jobd --restart` with your settings exported. Restart cancels active work; installation alone leaves it running.

- `.jobd-install.sha256` tracks managed files.
- Unmanaged or modified files are not silently overwritten. Move conflicting files aside.
- Staged replacements roll back on replacement failure.
- `.jobd-install.lock` blocks concurrent installs/uninstalls.

After a crash, inspect `.jobd-stage.*` recovery files. Remove stale locks only after confirming no installer is running.

## Uninstall

```sh
jobd-uninstall
```

Or, from a source checkout:

```sh
sh uninstall.sh --bin-dir "$HOME/.local/bin"
```

The uninstaller validates files, stops the worker selected by `JOBD_STATE_DIR` and removes tracked files. Modified files or symlinks must be moved aside before retrying.

State, logs, unrelated files and PATH settings remain. Stop workers using other state directories separately.

Uninstallation works offline.

Sources: [install.sh](../install.sh), [uninstall.sh](../uninstall.sh).
