#!/bin/sh
# Lanciato da udev a ogni attivazione di wlan0. Avvia l'agente in background e termina subito.
D=/mnt/onboard/.adds/wrygloss
RULE=/etc/udev/rules.d/91-wrygloss.rules
BIN=/tmp/wrygloss-agent

# Disinstallazione: un file vuoto UNINSTALL in .adds/wrygloss/ e una riattivazione del wifi rimuovono la regola.
if [ -f "$D/UNINSTALL" ]; then
  rm -f "$RULE" "$BIN" "$BIN.version"
  echo "$(date '+%F %T') UNINSTALL: regola udev rimossa, ora si puo' cancellare la cartella .adds/wrygloss" >> "$D/agent.log"
  exit 0
fi
[ -f "$D/wrygloss-agent" ] || exit 0

# Copia il binario in /tmp (RAM): la partizione utente e' FAT e non permette l'esecuzione.
# Si ricopia quando cambia VERSION (versione piu' impronta del binario), una volta per avvio del dispositivo.
if [ ! -x "$BIN" ] || [ "$(cat "$D/VERSION" 2>/dev/null)" != "$(cat "$BIN.version" 2>/dev/null)" ]; then
  cp "$D/wrygloss-agent" "$BIN.$$" && chmod 755 "$BIN.$$" && mv "$BIN.$$" "$BIN" && cp "$D/VERSION" "$BIN.version"
fi

( "$BIN" --dir "$D" >> "$D/agent.stdout.log" 2>&1 & ) >/dev/null 2>&1
exit 0
