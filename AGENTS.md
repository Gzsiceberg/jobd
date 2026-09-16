# Agent Instructions

## Git Rules

- Don't commit files unless I ask you to.

## Python Scripts

- Use `uv run` to run all Python scripts, including in documentation, tests, and CI; do not invoke scripts with `python` or `python3` directly.
- Every Python script must include a `#!/usr/bin/env -S uv run --script` shebang and PEP 723 inline metadata declaring `requires-python` and `dependencies` (use `[]` when no dependencies are needed).
