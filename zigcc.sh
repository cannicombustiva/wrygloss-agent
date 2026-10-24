#!/bin/sh
# C compiler for cross-compiling to the Kobo (ARMv7, musl). Uses zig if installed (e.g. brew install zig),
# otherwise the ziglang Python package (pip install ziglang).
if command -v zig >/dev/null 2>&1; then
  exec zig cc -target arm-linux-musleabihf "$@"
fi
exec python3 -m ziglang cc -target arm-linux-musleabihf "$@"
