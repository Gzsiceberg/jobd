# Publishing releases

[Overview](../README.md) · [Installation](installation.md) · [Development](development.md)

Run commands from the repository root.

## Publish a GitHub release

Commit and push changes first. Tag with a new version:

```sh
git tag vX.Y.Z
git push origin vX.Y.Z
```

The [workflow](../.github/workflows/release.yml):

1. Tests Go code and installers.
2. Builds static Linux amd64/arm64 binaries.
3. Creates a draft with archives, scripts and `SHA256SUMS`.
4. Publishes the release.

Archives include binaries, the uninstaller and MIT `LICENSE`.

Releases inherit repository visibility. Anonymous installs require a public repository and license-bearing archives. The installer cannot fetch private assets.

Tags containing a hyphen become prereleases. Install them with `--version`. Never move published tags. If publishing fails, inspect the draft before retrying.

## Local packaging and installer tests

```sh
sh tooling/package-release.sh v0.1.0 dist
uv run tooling/test-install.py
```

[Packaging](../tooling/package-release.sh) builds binaries without installing them. [Tests](../tooling/test-install.py) run real binaries with local download fixtures. Neither publishes releases nor changes your installation.
