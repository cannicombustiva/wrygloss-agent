#!/bin/sh
# Create build/wrygloss-agent-<version>.zip with the same layout as the Kobo root:
#   .adds/wrygloss/{wrygloss-agent,VERSION,run.sh}  and  .kobo/KoboRoot.tgz
# config.json (server and token) is not in the package: the app generates it for each device.
set -eu
HERE="$(cd "$(dirname "$0")" && pwd)"
B="$HERE/../build"
[ -f "$B/wrygloss-agent" ] && [ -f "$B/VERSION" ] || { echo "build/wrygloss-agent is missing: run ./build.sh"; exit 1; }
sh "$HERE/make_koboroot.sh" >/dev/null
V="$(cut -d' ' -f1 "$B/VERSION")"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
mkdir -p "$STAGE/.adds/wrygloss" "$STAGE/.kobo"
cp "$B/wrygloss-agent" "$B/VERSION" "$HERE/run.sh" "$STAGE/.adds/wrygloss/"
cp "$B/KoboRoot.tgz" "$STAGE/.kobo/"
rm -f "$B/wrygloss-agent-$V.zip"
(cd "$STAGE" && COPYFILE_DISABLE=1 zip -qrX "$B/wrygloss-agent-$V.zip" .adds .kobo)
unzip -l "$B/wrygloss-agent-$V.zip"
