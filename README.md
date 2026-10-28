# wrygloss-agent

On-device agent for [Wrygloss](https://github.com/cannicombustiva/wrygloss): on every wifi activation it copies the
e-reader's database, reads a fixed list of columns from it (highlights, notes, dogears, words saved from the
dictionary, and the books they cite) and syncs them with the server according to the contract
[`spec/ingest/v1`](spec/ingest/v1/README.md). Studying happens in the app, not on the device.

It has nothing to do with Rakuten Kobo, which neither endorses nor supports it.

## What it reads and what it does not

| Reads | Never reads |
|---|---|
| `Bookmark`: id, book, chapter, type, text, note, position, color, `Hidden`, dates | the `user` table (email, access tokens) |
| `WordList`: word as saved, book, dictionary, date | `Bookmark.UserID`, `content.___UserID` |
| `content`: title, author, language and reading position, **of cited books only** | cookies, `AnalyticsEvents`, the rest of the library |
| `.kobo/version`: firmware and model | serial number (first field of `.kobo/version`) |

The original database is never opened: the agent copies the file and its WAL to `/tmp`, checks that they did not
change during the copy, reads the copy and deletes it. The sqlite file is never sent. The tests fail if a value from
the `user` table, the account ids or the serial number shows up in a payload, the log or the state.

`WordList` holds only the words added with "Add to My Words" on the Kobo: a dictionary lookup alone is not saved.

## Compatibility

| Device | Firmware | Status |
|---|---|---|
| Kobo Clara BW (armv7l, kernel 4.9.77) | 4.46.23836 | v0.2 tested on October 24, 2026 over the local network (HTTP); HTTPS to a real domain still to test ([KOBO-TEST.md](KOBO-TEST.md)) |
| Other models or firmware | | not tested |

A firmware update might remove the udev rule (not verified): in that case the agent silently stops syncing, the app
flags it from the last contact, and repeating the install is enough.

## Install

You need a Mac or Linux computer with a terminal, the e-reader's USB cable and a `config.json` from the Wrygloss
app. The install writes only to `.adds/wrygloss/` and `.kobo/KoboRoot.tgz` on the e-reader. The commands are for a
Mac with the files in your Downloads folder; on Linux, use your mount point and skip `dot_clean`.

1. **Back up the e-reader.** Connect it over USB and copy everything on it, hidden folders included, for example
   `cp -R /Volumes/KOBOeReader ~/kobo-backup`. Highlights and saved words live in `.kobo/KoboReader.sqlite` (in the
   Finder, Cmd+Shift+. shows hidden folders).
2. **Pair the device in the app.** Wrygloss downloads a `config.json` with the server address and the device's own
   token, shown only once. Keep it: you need it again to reinstall.
3. **Get the agent.** Download `wrygloss-agent-0.2.0.zip` from the [releases](../../releases) into your Downloads
   folder. Or build it yourself: `./build.sh && sh deploy/package.sh` creates `build/wrygloss-agent-0.2.0.zip`
   (needs Go and zig, see [BUILD.md](BUILD.md)).
4. **Connect the e-reader** with the USB cable and tap *Connect* on its screen. It appears as `KOBOeReader`: on a
   Mac at `/Volumes/KOBOeReader` (check with `ls /Volumes`), on Linux usually at `/media/<your user>/KOBOeReader`.
5. **Copy the agent** onto the e-reader:

   ```sh
   unzip -o ~/Downloads/wrygloss-agent-0.2.0.zip -d /Volumes/KOBOeReader
   ```

   If your browser already unzipped it into a folder, run instead
   `cp -R ~/Downloads/wrygloss-agent-0.2.0/. /Volumes/KOBOeReader/`.
6. **Copy your config.json** next to it:

   ```sh
   cp ~/Downloads/config.json /Volumes/KOBOeReader/.adds/wrygloss/config.json
   ```

   If your browser saved it under another name, such as `config (1).json`, use that name in the first path.
7. **Clean up and check.** A Mac leaves hidden `._` files on the e-reader; `dot_clean` removes them. Then list the
   agent's folder:

   ```sh
   dot_clean -m /Volumes/KOBOeReader/.adds /Volumes/KOBOeReader/.kobo
   ls -a /Volumes/KOBOeReader/.adds/wrygloss
   ```

   It must list `config.json`, `run.sh`, `VERSION` and `wrygloss-agent`.
8. **Eject the e-reader** with the Finder's Eject button (or `diskutil eject /Volumes/KOBOeReader`), then unplug
   the cable. It shows an update screen and restarts by itself: it installs the rule
   `/etc/udev/rules.d/91-wrygloss.rules` and deletes `KoboRoot.tgz`. Wait for its home screen.
9. **First sync.** Turn the wifi on (or let it connect on its own): within a few seconds the agent sends your
   highlights and saved words, and the app's tutorial shows the counts. Nothing after a minute? Turn the wifi off and
   on again; still nothing, connect over USB and read the last lines of `.adds/wrygloss/agent.log`.

From then on it syncs every time the wifi turns on. From a clone of this repository,
`sh deploy/install.sh /path/to/config.json [mount point]` does steps 5 to 7 in one go after `./build.sh`.

**Reinstall** (for example after a firmware update removed the rule): repeat steps 4 to 8; it is safe. Without the
device's `config.json`, pair it again in the app for a new one and revoke the old device.

## Uninstall

Create an empty `UNINSTALL` file in `.adds/wrygloss/`, eject the e-reader and turn wifi on again: the rule is removed
(an `UNINSTALL` line in `.adds/wrygloss/agent.log`), along with the rule of the v0.1 test build if present. Then the
folder can be deleted over USB. Deleting just the folder also leaves the rule inert. Revoking the device in the app
stops any upload anyway.

## Flow of a run

1. udev runs `run.sh` on every new `wlan0` interface (wifi turned on, wake from sleep with wifi on, native sync
   button with wifi off). `run.sh` copies the binary to `/tmp` if `VERSION` changed and starts it in the background.
2. The agent records whether the default route is already there (`route_at_event`), takes a lock against overlapping
   runs and skips if the last successful sync was less than 30 s ago (or `min_interval_s` set by the server).
3. It waits for the route, checking every 250 ms (at most 45 s), and records `route_ready_ms`.
4. Stable copy of the database, `quick_check`, three queries on the fixed list of columns.
5. It sends only the records whose fingerprint changed since the server's last ack, then the full manifest for
   deletions; it resends what the server reports as missing. gzip, a single connection, at most 6 attempts with waits
   of 1-16 s (about 30 s; up to 60 s if the server asks to wait with `Retry-After`). It stops on a revoked token or a
   contract no longer accepted until the config or the version changes.
6. It writes `state.json` (server acks) and one log line with counts and timings, never texts.

## Performance: measurements and choices

Measured on the real Clara BW (v0.1 and v0.2; 12 runs of v0.2 on October 24, 2026, server on the local network).

| Item | v0.1 on the Kobo | v0.2 on the Kobo | Choice |
|---|---|---|---|
| Duration of a run | 21.2-21.7 s, of which 20 s debounce | 2.6-3.4 s, of which 2.3-3.3 s waiting for the route | debounce replaced by a lock, a skip after a recent sync and a wait for the route: a sync succeeds even if wifi stays on only a few seconds |
| Route ready at the event | not measured | never (12 out of 12): ready after 2.3-3.3 s | wait for the route every 250 ms, needed |
| Double events on wake | merged by the debounce | second event 12-15 s later, skipped (`SKIP`) | skip if the last successful sync was less than 30 s ago |
| Test TLS probes | 1.0-1.4 s | 0 | removed: the POST already does the handshake |
| TLS handshake | 0.7-1 s | to measure (needs the published server) | one connection for all batches; no session resumption across runs (disproportionate complexity) |
| Upload over the local network | 21-137 ms | 46-175 ms | none |
| Bytes sent | counts only | first sync (96 records) 6.3 KB; no changes 2.6 KB (manifest only); restore after server data loss 9.1 KB | gzip |
| Database copy | 4-14 ms | 3-14 ms, always on the first attempt | none |
| Queries and `quick_check` | 24-30 ms (counts only) | 46-162 ms (texts, chapters, books) | none: under 5% of the run, which is dominated by the route; `quick_check` stays, a corrupt copy would produce false deletions |
| Binary size | 9.6 MB | 8.6 MB | stripped the C debug symbols left despite `-s -w`; no UPX or trimmed SQLite: small gain, high risk |
| Memory (`mem_sys_mb`) | 5 MB | 5 MB; 9 MB for the full restore in two rounds | none |
| Server unreachable | | 6 attempts, `FAIL` after 29 s | none |

Complexity: reading and diffing are O(n) with maps, no sorting. The full manifest on every sync costs 2.6 KB
compressed with the current data and an estimated 350 KB at 10 000 records. If measurements call for it, contract
version 1.1 can add a per-kind summary.

## Files

| Path | Content |
|---|---|
| `main.go` | flow of a run, configuration, log |
| `kobo.go` | fixed list of columns, stable database copy, record reading |
| `sync.go` | batches, upload with retries, acks and rounds for missing records |
| `state.go` | persistent state (`state.json`) |
| `internal/contract` | contract types |
| `internal/ingestref` | in-memory reference server, schema validation, reference word cleaning |
| `cmd/devserver` | local network test server for device tests |
| `spec/ingest/v1` | spec, JSON Schema, examples |
| `deploy/` | udev rule, `run.sh`, install, package |
| `testdata/kobo.sql`, `tools/make_test_db.py` | synthetic database with canary values |

## License

MIT, see [LICENSE](LICENSE).
