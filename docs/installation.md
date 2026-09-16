# Installation and uninstallation

[Overview](../README.md) · [CLI](../cli/README.md) · [Worker](../worker/README.md) · [Publishing releases](releases.md)

## Install from public GitHub releases

Supports Linux **amd64** and **arm64**. Go and Node.js are not required on the destination host. A running systemd user manager is required (run from a user login session). Install `curl`, GNU `tar`, and standard coreutils (`sha256sum`). No GitHub account or token is needed once the repository and release are public.

After the first release has been published, install both binaries and the user service. Export `JOBD_API_KEY`, `JOBD_CONTROLLER`, `JOBD_QUEUE` and optionally `JOBD_STATE_DIR` before installation to configure the worker. Without a key, the service runs local jobs only, without the controller idle delay. The CLI automatically uses the local queue and warns on listing to set `JOBD_API_KEY` and run `jobd --restart` to enable controller jobs.

```sh
curl -fsSL https://raw.githubusercontent.com/Gzsiceberg/jobd/main/install.sh | sh
```

Prefer downloading and reviewing the script before execution. The installer downloads release assets anonymously over HTTPS; private repositories are not supported by this installer. The default controller is the project's hosted endpoint; self-hosters should explicitly set `JOBD_CONTROLLER` to their own deployment and use their own key.

To inspect the script before running it, pin a release or change the installation directory:

```sh
curl -fSL https://raw.githubusercontent.com/Gzsiceberg/jobd/main/install.sh -o install.sh
# Review install.sh, then:
sh install.sh --bin-dir "$HOME/.local/bin"
# To pin a version, add --version vX.Y.Z using a published tag.
```

Without `--version`, the latest non-prerelease is used. The installer downloads that exact release's architecture-specific archive and verifies its SHA-256 checksum before installing `jobd`, `jobd-worker`, `jobd-uninstall`, and `jobd-LICENSE`. Archives contain the MIT `LICENSE`; installed license files are checksum-tracked like binaries. This installer requires the new license-bearing archive format; v0.1.1 and earlier archives require their matching older installer.

- Installation directory: `~/.local/bin`, overridden by `--bin-dir` or `JOBD_BIN_DIR`.
- Release: latest, overridden by `--version` or `JOBD_VERSION`.
- Repository: `Gzsiceberg/jobd`, overridden by `JOBD_REPO` for a public fork.

Checksums detect corruption, not a compromised publisher. You still trust GitHub and the repository/release publisher.

## Run the installed binaries

The installer creates `jobd-worker.service` in `${XDG_CONFIG_HOME:-~/.config}/systemd/user`, enables it and starts/restarts it. The unit points to the selected binary directory, including custom paths. Worker settings are imported into the user manager's environment; **no key file is written**. An unset key clears the previous imported key. Environment values remain in memory and may be inherited by other user services. After the user manager restarts, supply the key again with `jobd --restart`.

No sudo, lingering setup or shell configuration changes are performed. The service normally runs while the user's systemd manager is active. Stop manually launched workers using the same state directory before installation. Add the directory to your shell's PATH if necessary:

```sh
export PATH="$HOME/.local/bin:$PATH"
jobd --help
jobd --restart                  # imports current environment and restarts the service
journalctl --user -u jobd-worker.service
```

See the [worker guide](../worker/README.md) for configuration and shutdown behavior, and the [CLI guide](../cli/README.md) for job management.

## Upgrade or downgrade

Rerun the installer to upgrade, or pin an older tag to downgrade. It automatically restarts the user service with the installer's environment, cancelling any active job. Keep `JOBD_API_KEY` set when upgrading. To restart later, use `jobd --restart`.

A `.jobd-install.sha256` manifest records the release, binary checksums, service path and service checksum. Unmanaged or locally modified binaries/units are never silently overwritten. Move an existing manually installed unit aside before installing. Installation stages files on the target filesystems and rolls back replacement failures; concurrent operations are blocked by `.jobd-install.lock` and `.jobd-service.lock`. A systemctl failure after file installation leaves a tracked installation for retry or uninstall, and reports an error. Customize the service using systemd drop-ins rather than editing the managed unit.

After an uncatchable crash, inspect any `.jobd-stage.*` recovery files and remove a stale lock only after confirming no installer is running.

## Uninstall

Run:

```sh
jobd-uninstall
```

The installed uninstaller automatically finds its installation directory. Alternatively, from a source checkout's repository root:

```sh
sh uninstall.sh --bin-dir "$HOME/.local/bin"
```

It validates all managed files, stops and disables the managed user service, removes its unit, reloads the user manager, and removes checksum-matched binaries, the installed license, and their manifest. Worker identity/state, job logs, custom service drop-ins, unrelated files, and shell PATH settings are preserved. Manually launched workers must still be stopped yourself. If a managed file was modified or replaced by a symlink, move it aside before retrying. Older binary-only installations remain supported. The uninstaller needs no GitHub authentication or network access, but needs access to the user manager when removing a managed service.

Sources: [install.sh](../install.sh), [uninstall.sh](../uninstall.sh).
