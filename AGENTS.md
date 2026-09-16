# Agent Instructions

## Git Rules

- Don't commit files unless I ask you to.

## Python Scripts

- Run Python scripts with `uv run`, including in docs, tests and CI. Never use `python` or `python3` directly.
- Every script needs the `#!/usr/bin/env -S uv run --script` shebang.
- Include PEP 723 metadata: `requires-python` and `dependencies`. Use `[]` for no dependencies.
