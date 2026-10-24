#!/bin/sh
# Installa il test Wrygloss sul Kobo montato. Uso: sh install.sh [/Volumes/KOBOeReader]
set -eu
KOBO="${1:-/Volumes/KOBOeReader}"
HERE="$(cd "$(dirname "$0")" && pwd)"
[ -d "$KOBO/.kobo" ] || { echo "Kobo non trovato in $KOBO (controlla con: ls /Volumes)"; exit 1; }

IP="$(ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1 2>/dev/null || true)"
if [ -z "$IP" ]; then
  echo "Non riesco a leggere l'IP del Mac: dopo l'installazione modifica 'endpoint' in .adds/wrygloss-test/config.json"
  IP="IP_DEL_MAC"
fi

D="$KOBO/.adds/wrygloss-test"
mkdir -p "$D"
cp "$HERE/wrygloss-agent" "$HERE/run.sh" "$D/"
if [ ! -f "$D/config.json" ]; then
  cat > "$D/config.json" <<CFG
{
  "endpoint": "http://$IP:8765/ingest",
  "token": "test-token",
  "quiet_seconds": 20,
  "min_interval_seconds": 0,
  "route_wait_seconds": 60,
  "tls_test_url": "https://example.com"
}
CFG
fi
rm -f "$D/UNINSTALL"

# Disattiva la vecchia sonda (si rimuove da sola al prossimo evento di rete).
[ -d "$KOBO/.adds/kobosync-probe" ] && touch "$KOBO/.adds/kobosync-probe/UNINSTALL" || true

cp "$HERE/KoboRoot.tgz" "$KOBO/.kobo/KoboRoot.tgz"
command -v dot_clean >/dev/null 2>&1 && dot_clean -m "$KOBO/.kobo" "$KOBO/.adds" 2>/dev/null || true
echo "Copiato (endpoint http://$IP:8765/ingest). Ora espelli il Kobo in modo sicuro: si riavviera' e installera' la regola."
