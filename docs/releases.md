# Publishing releases

[Overview](../README.md) · [Installation](installation.md) · [Development and testing](development.md)

Commands below run from the repository root.

## Publish a GitHub release

Commit and push the installer/workflow changes before tagging. A version tag triggers [the release workflow](../.github/workflows/release.yml):

```sh
git tag v0.1.0
git push origin v0.1.0
```

The workflow:

1. Tests the Go code and installers.
2. Builds static Linux amd64/arm64 binaries.
3. Creates a draft GitHub Release containing architecture-specific archives (binaries, uninstaller and MIT `LICENSE`), installer/uninstaller scripts, and `SHA256SUMS`.
4. Publishes the completed release.

Release visibility follows repository visibility. For anonymous installation, make the repository public and publish a new release with the license-bearing archive format. The workflow works for either repository visibility; the public curl installer cannot download private assets. Tags with a hyphen are published as prereleases; install those using `--version`. Do not move published version tags. If publishing fails after draft creation, inspect that draft before rerunning.

## Local packaging and installer tests

```sh
sh tooling/package-release.sh v0.1.0 dist
uv run tooling/test-install.py
```

[The packaging script](../tooling/package-release.sh) builds standalone binaries without installing them on the host. [Installer tests](../tooling/test-install.py) build and execute real binaries but replace GitHub downloads with a local anonymous HTTPS-download fixture; they never publish releases or modify your actual installation.
