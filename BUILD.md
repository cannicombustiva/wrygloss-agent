# Building and testing the agent

The agent is a static Go binary for Linux ARMv7 with embedded SQLite (mattn/go-sqlite3, needs CGO): it requires a C
compiler that produces ARM code against musl libc. `zig cc` does the job.

## Verified: macOS (Apple Silicon), Go 1.27.3, zig 0.17.0

```sh
brew install go zig
./build.sh            # build/wrygloss-agent and build/VERSION
```

- About 1 minute the first time (mostly SQLite), a few seconds with the cache.
- Result: ELF 32-bit ARM EABI5, static, stripped, about 8.6 MB. The build is reproducible: two successive builds
  produce the same binary.
- `build/VERSION` holds the version and the first 12 hex digits of the binary's SHA-256: `run.sh` copies the binary
  to `/tmp` again when it changes.
- The same command works on Linux; `zigcc.sh` uses `zig` if installed, otherwise `python3 -m ziglang`
  (`pip install ziglang`). `go.mod` requires Go 1.26 or later.

## Tests

```sh
go test -race ./...
```

- Agent logic against the in-memory reference server (`internal/ingestref`), which validates every request against
  the spec's JSON Schema: incremental sync, deletions and restores, server data loss, revoked token, retries on 503,
  batches, 413, missing tables and columns, rows present only in the WAL, double event, no route, no canary value in
  payloads, log or state.
- Valid and invalid spec examples against the schema; word cleaning vectors.
- Error codes of the reference server (401, 410, 413 including a gzip bomb, 415, 422, 429, per-record rejection).

## Running the ARM binary under emulation (Docker Desktop on a Mac)

Alpine `linux/arm/v7` has musl and busybox like the Kobo; the kernel is the emulator's, not the Kobo's 4.9.

```sh
./build.sh && sh deploy/package.sh
W=$(mktemp -d) && (cd "$W" && unzip -q "$OLDPWD/build/wrygloss-agent-0.2.0.zip")
python3 tools/make_test_db.py "$W/.kobo/KoboReader.sqlite"
go run ./cmd/devserver -addr 0.0.0.0:8799 -host host.docker.internal -config-out "$W/.adds/wrygloss/config.json" &
docker run --rm --platform linux/arm/v7 -v "$W:/mnt/onboard" alpine:3.20 sh -c '
  mkdir -p /etc/udev/rules.d && touch /etc/udev/rules.d/91-wrygloss.rules
  sh /mnt/onboard/.adds/wrygloss/run.sh; sleep 8      # wifi event
  sh /mnt/onboard/.adds/wrygloss/run.sh; sleep 3      # double event: SKIP
  touch /mnt/onboard/.adds/wrygloss/UNINSTALL; sh /mnt/onboard/.adds/wrygloss/run.sh; ls /etc/udev/rules.d'
cat "$W/.adds/wrygloss/agent.log"
```

Result on October 24, 2026: full sync of 32 annotations, 62 words and 2 books in 193 ms, double event skipped,
uninstall removing the rule and the binary. With `/etc/ssl` deleted, the TLS handshake to a public site succeeds with
the embedded roots alone.

## Testing on the device

See [KOBO-TEST.md](KOBO-TEST.md).
