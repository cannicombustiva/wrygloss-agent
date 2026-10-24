#!/bin/sh
# Crea KoboRoot.tgz con la sola regola udev (nessuna directory nell'archivio, proprietario root).
set -eu
cd "$(dirname "$0")"
mkdir -p /tmp/wg-root/etc/udev/rules.d
cp 91-wrygloss-test.rules /tmp/wg-root/etc/udev/rules.d/
tar --owner=0 --group=0 --numeric-owner -czf KoboRoot.tgz -C /tmp/wg-root etc/udev/rules.d/91-wrygloss-test.rules
tar tzvf KoboRoot.tgz
