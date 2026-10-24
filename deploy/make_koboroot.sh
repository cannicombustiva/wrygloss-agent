#!/bin/sh
# Crea build/KoboRoot.tgz con la sola regola udev (proprietario root, nessun file accessorio di macOS).
set -eu
HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="$HERE/../build/KoboRoot.tgz"
mkdir -p "$HERE/../build"
ROOT="$(mktemp -d)"
trap 'rm -rf "$ROOT"' EXIT
mkdir -p "$ROOT/etc/udev/rules.d"
cp "$HERE/91-wrygloss.rules" "$ROOT/etc/udev/rules.d/"
if tar --version 2>/dev/null | grep -q bsdtar; then
  COPYFILE_DISABLE=1 tar --uid 0 --gid 0 --numeric-owner --no-xattrs -czf "$OUT" -C "$ROOT" etc/udev/rules.d/91-wrygloss.rules
else
  tar --owner=0 --group=0 --numeric-owner -czf "$OUT" -C "$ROOT" etc/udev/rules.d/91-wrygloss.rules
fi
tar tzvf "$OUT"
