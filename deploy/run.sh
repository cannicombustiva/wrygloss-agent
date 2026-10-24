#!/bin/sh
# Lanciato da udev a ogni attivazione di wlan0. Avvia l'agente in background e termina subito.
D=/mnt/onboard/.adds/wrygloss-test
RULE=/etc/udev/rules.d/91-wrygloss-test.rules
BIN=/tmp/wrygloss-agent

# Disinstallazione: crea un file vuoto UNINSTALL in .adds/wrygloss-test/ e riattiva il wifi.
if [ -f "$D/UNINSTALL" ]; then
  rm -f "$RULE"
  echo "$(date '+%F %T') UNINSTALL: regola udev rimossa" >> "$D/agent.log"
  exit 0
fi

# Copia il binario in /tmp (RAM) per non dipendere dai permessi della partizione FAT.
if [ "$(wc -c < "$BIN" 2>/dev/null)" != "$(wc -c < "$D/wrygloss-agent" 2>/dev/null)" ]; then
  cp "$D/wrygloss-agent" "$BIN.$$" && chmod 755 "$BIN.$$" && mv "$BIN.$$" "$BIN"
fi

( "$BIN" --dir "$D" >> "$D/agent.stdout.log" 2>&1 & ) >/dev/null 2>&1
exit 0
