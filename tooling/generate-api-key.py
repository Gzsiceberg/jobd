#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Print a cryptographically secure, 256-bit JOBD_API_KEY to stdout."""

import secrets


if __name__ == "__main__":
    print(secrets.token_hex(32))
