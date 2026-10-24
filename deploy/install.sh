#!/bin/sh
# Installa l'agente su un Kobo montato via USB (macOS o Linux).
# Uso: sh deploy/install.sh /percorso/config.json [/Volumes/KOBOeReader]
# Scrive solo in .adds/wrygloss/ e .kobo/KoboRoot.tgz. Dopo l'espulsione sicura il Kobo si riavvia, installa la
# regola udev e cancella KoboRoot.tgz. Ripetere l'installazione (es. dopo un aggiornamento del firmware) e' sicuro.
set -eu
CFG="${1:-}"
KOBO="${2:-/Volumes/KOBOeReader}"
HERE="$(cd "$(dirname "$0")" && pwd)"
B="$HERE/../build"
[ -n "$CFG" ] && [ -f "$CFG" ] || { echo "Uso: sh deploy/install.sh /percorso/config.json [punto di montaggio del Kobo]"; exit 1; }
grep -q '"server_url"' "$CFG" && grep -q '"token"' "$CFG" || { echo "$CFG non sembra un config.json di Wrygloss"; exit 1; }
[ -d "$KOBO/.kobo" ] || { echo "Kobo non trovato in $KOBO (controlla con: ls /Volumes)"; exit 1; }
[ -f "$B/wrygloss-agent" ] && [ -f "$B/VERSION" ] || { echo "manca build/wrygloss-agent: esegui ./build.sh"; exit 1; }
sh "$HERE/make_koboroot.sh" >/dev/null

D="$KOBO/.adds/wrygloss"
mkdir -p "$D"
cp "$B/wrygloss-agent" "$B/VERSION" "$HERE/run.sh" "$D/"
cp "$CFG" "$D/config.json"
rm -f "$D/UNINSTALL"
# La versione di prova, se presente, si disinstalla da sola al prossimo evento wifi.
[ -d "$KOBO/.adds/wrygloss-test" ] && touch "$KOBO/.adds/wrygloss-test/UNINSTALL" || true
cp "$B/KoboRoot.tgz" "$KOBO/.kobo/KoboRoot.tgz"
command -v dot_clean >/dev/null 2>&1 && dot_clean -m "$KOBO/.kobo" "$KOBO/.adds" 2>/dev/null || true
echo "Installato $(cat "$B/VERSION") in $D. Ora espelli il Kobo in modo sicuro: si riavviera' e installera' la regola."
