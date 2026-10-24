#!/bin/sh
# Compila l'agente per il Kobo (Linux ARMv7, musl, statico) in build/wrygloss-agent. Richiede Go e zig (vedi BUILD.md).
# CGO_CFLAGS -g0 e -extldflags -s tolgono i simboli di debug del C (SQLite, libc di zig): da 11,5 a 8,6 MB.
set -eu
cd "$(dirname "$0")"
mkdir -p build
CGO_ENABLED=1 CC="$PWD/zigcc.sh" CGO_CFLAGS="-O2 -g0" GOOS=linux GOARCH=arm GOARM=7 \
  go build -trimpath -tags 'netgo osusergo sqlite_omit_load_extension' \
  -ldflags '-s -w -extldflags "-static -s"' -o build/wrygloss-agent .
# VERSION = versione e impronta del binario: run.sh ricopia il binario in /tmp quando cambia, anche tra due build
# della stessa versione.
VERSION="$(sed -n 's/^const version = "\(.*\)"$/\1/p' main.go)"
SUM="$( (shasum -a 256 build/wrygloss-agent 2>/dev/null || sha256sum build/wrygloss-agent) | cut -c1-12)"
printf '%s %s\n' "$VERSION" "$SUM" > build/VERSION
ls -l build/wrygloss-agent
cat build/VERSION
