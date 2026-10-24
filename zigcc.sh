#!/bin/sh
# Compilatore C per la cross-compilazione verso il Kobo (ARMv7, musl). Usa zig se installato (es. brew install zig),
# altrimenti il pacchetto Python ziglang (pip install ziglang).
if command -v zig >/dev/null 2>&1; then
  exec zig cc -target arm-linux-musleabihf "$@"
fi
exec python3 -m ziglang cc -target arm-linux-musleabihf "$@"
