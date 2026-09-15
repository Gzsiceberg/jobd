# Installation and uninstallation

[Overview](../README.md) · [CLI](../cli/README.md) · [Worker](../worker/README.md) · [Publishing releases](releases.md)

## Install from private GitHub releases

Supports Linux **amd64** and **arm64**. Go and Node.js are not required on the destination host. Install `gh`, GNU `tar`, and standard coreutils (`sha256sum`), then authenticate with an account that can read this private repository:

```sh
gh auth login
gh auth status
```

After the first release has been published, install both binaries:

```sh
gh api repos/Gzsiceberg/jobd/contents/install.sh \
  -H 'Accept: application/vnd.github.raw+json' | sh
```

This is the authenticated equivalent of `curl ... | sh`; anonymous curl cannot access private release assets. For automated hosts, `gh` also supports a `GH_TOKEN` supplied securely by your environment/secret manager. Do not put credentials in URLs or shell history.

To inspect the script before running it, pin a release or change the installation directory:

```sh
gh api repos/Gzsiceberg/jobd/contents/install.sh \
  -H 'Accept: application/vnd.github.raw+json' > install.sh
# Review install.sh, then:
sh install.sh --version v0.1.0 --bin-dir "$HOME/.local/bin"
```

Without `--version`, the latest non-prerelease is used. The installer downloads that exact release's architecture-specific archive and verifies its SHA-256 checksum before installing `jobd`, `jobd-worker`, and `jobd-uninstall`.

- Installation directory: `~/.local/bin`, overridden by `--bin-dir` or `JOBD_BIN_DIR`.
- Release: latest, overridden by `--version` or `JOBD_VERSION`.
- Repository: `Gzsiceberg/jobd`, overridden by `JOBD_REPO` for a private fork.

Checksums detect corruption; the authenticated repository/release publisher remains trusted.

## Run the installed binaries

**The installer does not start a daemon or install a service.** No sudo or shell configuration changes are performed. Add the directory to your shell's PATH if necessary:

```sh
export PATH="$HOME/.local/bin:$PATH"
jobd --help
jobd-worker --controller http://localhost:8787 --queue default
```

See the [worker guide](../worker/README.md) for configuration and shutdown behavior, and the [CLI guide](../cli/README.md) for job management.

## Upgrade or downgrade

Rerun the installer to upgrade, or pin an older tag to downgrade. Restart existing workers yourself to use the new binary.

A `.jobd-install.sha256` manifest records the release and installed checksums. Unmanaged or locally modified files are never silently overwritten. Installation stages files on the target filesystem and rolls back replacement failures; concurrent install/uninstall operations are blocked by `.jobd-install.lock`.

After an uncatchable crash, inspect any `.jobd-stage.*` recovery files and remove a stale lock only after confirming no installer is running.

## Uninstall

Stop running workers first, then run:

```sh
jobd-uninstall
```

The installed uninstaller automatically finds its installation directory. Alternatively, from a source checkout's repository root:

```sh
sh uninstall.sh --bin-dir "$HOME/.local/bin"
```

It removes only checksum-matched managed binaries and their manifest. Worker identity/state, job logs, unrelated files, and shell PATH settings are preserved. It does not kill processes or stop services. If a managed file was modified or replaced by a symlink, move it aside before retrying. The uninstaller needs no GitHub authentication or network access.

Sources: [install.sh](../install.sh), [uninstall.sh](../uninstall.sh).
