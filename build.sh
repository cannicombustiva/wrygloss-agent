#!/bin/sh
# Build the agent for the Kobo (Linux ARMv7, musl, static) into build/wrygloss-agent. Needs Go and zig (see BUILD.md).
# CGO_CFLAGS -g0 and -extldflags -s strip the C debug symbols (SQLite, zig's libc): from 11.5 to 8.6 MB.
set -eu
cd "$(dirname "$0")"
mkdir -p build
CGO_ENABLED=1 CC="$PWD/zigcc.sh" CGO_CFLAGS="-O2 -g0" GOOS=linux GOARCH=arm GOARM=7 \
  go build -trimpath -tags 'netgo osusergo sqlite_omit_load_extension' \
  -ldflags '-s -w -extldflags "-static -s"' -o build/wrygloss-agent .
# VERSION = version and binary fingerprint: run.sh copies the binary to /tmp again when it changes, even between two
# builds of the same version.
VERSION="$(sed -n 's/^const version = "\(.*\)"$/\1/p' main.go)"
SUM="$( (shasum -a 256 build/wrygloss-agent 2>/dev/null || sha256sum build/wrygloss-agent) | cut -c1-12)"
printf '%s %s\n' "$VERSION" "$SUM" > build/VERSION
ls -l build/wrygloss-agent
cat build/VERSION
