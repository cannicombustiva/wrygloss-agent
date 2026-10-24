#!/bin/sh
# Crea build/wrygloss-agent-<versione>.zip con la stessa struttura della radice del Kobo:
#   .adds/wrygloss/{wrygloss-agent,VERSION,run.sh}  e  .kobo/KoboRoot.tgz
# Il config.json (server e token) non e' nel pacchetto: lo genera l'app per ogni dispositivo.
set -eu
HERE="$(cd "$(dirname "$0")" && pwd)"
B="$HERE/../build"
[ -f "$B/wrygloss-agent" ] && [ -f "$B/VERSION" ] || { echo "manca build/wrygloss-agent: esegui ./build.sh"; exit 1; }
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
