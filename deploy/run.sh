#!/bin/sh
# Started by udev on every wlan0 activation. Launches the agent in the background and exits immediately.
D=/mnt/onboard/.adds/wrygloss
RULE=/etc/udev/rules.d/91-wrygloss.rules
LEGACY_RULE=/etc/udev/rules.d/91-wrygloss-test.rules # v0.1 test build
BIN=/tmp/wrygloss-agent

# Uninstall: an empty UNINSTALL file in .adds/wrygloss/ plus one wifi activation remove the rule.
if [ -f "$D/UNINSTALL" ]; then
  rm -f "$RULE" "$LEGACY_RULE" "$BIN" "$BIN.version"
  echo "$(date '+%F %T') UNINSTALL: udev rule removed, the .adds/wrygloss folder can now be deleted" >> "$D/agent.log"
  exit 0
fi
[ -f "$D/wrygloss-agent" ] || exit 0

# Copy the binary to /tmp (RAM): the user partition is FAT and does not allow execution.
# It is copied again when VERSION (version plus binary fingerprint) changes, at most once per device boot.
if [ ! -x "$BIN" ] || [ "$(cat "$D/VERSION" 2>/dev/null)" != "$(cat "$BIN.version" 2>/dev/null)" ]; then
  cp "$D/wrygloss-agent" "$BIN.$$" && chmod 755 "$BIN.$$" && mv "$BIN.$$" "$BIN" && cp "$D/VERSION" "$BIN.version"
fi

( "$BIN" --dir "$D" >> "$D/agent.stdout.log" 2>&1 & ) >/dev/null 2>&1
exit 0
