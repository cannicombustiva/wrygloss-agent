#!/bin/sh
# Install the agent on a Kobo mounted over USB (macOS or Linux).
# Usage: sh deploy/install.sh /path/to/config.json [/Volumes/KOBOeReader]
# Writes only to .adds/wrygloss/ and .kobo/KoboRoot.tgz. After a safe eject the Kobo reboots, installs the udev rule
# and deletes KoboRoot.tgz. Repeating the install (e.g. after a firmware update) is safe.
set -eu
CFG="${1:-}"
KOBO="${2:-/Volumes/KOBOeReader}"
HERE="$(cd "$(dirname "$0")" && pwd)"
B="$HERE/../build"
[ -n "$CFG" ] && [ -f "$CFG" ] || { echo "Usage: sh deploy/install.sh /path/to/config.json [Kobo mount point]"; exit 1; }
grep -q '"server_url"' "$CFG" && grep -q '"token"' "$CFG" || { echo "$CFG does not look like a Wrygloss config.json"; exit 1; }
[ -d "$KOBO/.kobo" ] || { echo "Kobo not found at $KOBO (check with: ls /Volumes)"; exit 1; }
[ -f "$B/wrygloss-agent" ] && [ -f "$B/VERSION" ] || { echo "build/wrygloss-agent is missing: run ./build.sh"; exit 1; }
sh "$HERE/make_koboroot.sh" >/dev/null

D="$KOBO/.adds/wrygloss"
mkdir -p "$D"
cp "$B/wrygloss-agent" "$B/VERSION" "$HERE/run.sh" "$D/"
cp "$CFG" "$D/config.json"
rm -f "$D/UNINSTALL"
# The test build, if present, uninstalls itself at the next wifi event.
[ -d "$KOBO/.adds/wrygloss-test" ] && touch "$KOBO/.adds/wrygloss-test/UNINSTALL" || true
cp "$B/KoboRoot.tgz" "$KOBO/.kobo/KoboRoot.tgz"
command -v dot_clean >/dev/null 2>&1 && dot_clean -m "$KOBO/.kobo" "$KOBO/.adds" 2>/dev/null || true
echo "Installed $(cat "$B/VERSION") in $D. Now eject the Kobo safely: it will reboot and install the rule."
