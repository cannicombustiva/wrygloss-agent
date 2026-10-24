# Testing v0.2 on a real Kobo

Procedure for the Clara BW (firmware 4.46.23836) with the test server on a Mac, over the local network. **Steps 2
and 9 modify the Kobo filesystem**: run them only once you have decided to. Every other step is done from the Kobo
(wifi, sleep, highlights) or from the Mac.

What it verifies (it opens or closes the "to verify" items of the spec and the README):

| # | Check | Where to read the result |
|---|---|---|
| A | reading real content, date formats, `ChapterIDBookmarked`, `Hidden`, `Type`, chapter titles | `dump.ndjson` on the Mac |
| B | when the route is ready relative to the event | `route_at_event`, `route_ready_ms` |
| C | debounce replacement: double events, quick toggles, wifi on for a few seconds | `SKIP` and `END` lines |
| D | incremental sync, deletions, restore after server data loss | devserver lines |
| E | retries without network and time budget | `retrying` and `FAIL` lines |
| F | duration, memory, bytes sent | `END` line |
| G | uninstalling the v0.1 test build and v0.2 | `UNINSTALL` lines |

Result on October 24, 2026: steps 3-8 passed, see the README performance table and section 10 of the spec.

## 1. On the Mac: build and start the test server

```sh
cd wrygloss-agent
./build.sh
go run ./cmd/devserver -config-out /tmp/wrygloss-config.json -dump /tmp/wrygloss-dump.ndjson
```

The devserver prints the `config.json` with the Mac's IP. If macOS asks to accept incoming connections, allow them.
Kobo and Mac must be on the same wifi network. Keep the terminal open: every sync prints a `run=...` line.
`/tmp/wrygloss-dump.ndjson` contains your texts and stays on the Mac.

## 2. Install (modifies the Kobo)

Connect the Kobo over USB, then:

```sh
sh deploy/install.sh /tmp/wrygloss-config.json
```

It writes `.adds/wrygloss/` and `.kobo/KoboRoot.tgz`, and creates `UNINSTALL` in `.adds/wrygloss-test/` for v0.1.
Eject the Kobo safely: it reboots and installs the rule.

## 3. First sync (boot)

After the reboot, with wifi coming up on its own or turned on by hand: within a few seconds the devserver prints
`run=... upserts=<annotations>/<words>/<books>`. Expected: about 32 annotations (22 highlights, 2 notes, 8 dogears,
or more if you added some), 62 words or more, the cited books.

## 4. Events close together

1. Turn wifi off and on within 30 s of the previous sync: expected `SKIP last successful sync Ns ago`.
2. Wait more than 30 s, turn it off and on: expected a sync with `upserts=0/0/0` (manifest only).
3. Sleep and wake with wifi on: note whether events are doubled (two `START` lines 11-16 s apart) and that the
   second one is a `SKIP`.
4. With wifi off, press the native sync button: check that `END ok` appears even if wifi stays on briefly (v0.1 with
   its 20 s debounce could not make it).

## 5. Changes

1. Highlight a new sentence and save a word with "Add to My Words" (a lookup alone is not saved), then turn wifi on
   again (after 30 s): expected `upserts=1/1/...`.
2. Delete that highlight on the Kobo and turn wifi on again: expected `deleted=1/0/0`.

## 6. Server data loss

Stop the devserver with Ctrl+C and restart it with **the same token** (printed in the config.json from step 1):

```sh
go run ./cmd/devserver -token "$(python3 -c 'import json;print(json.load(open("/tmp/wrygloss-config.json"))["token"])')" -dump /tmp/wrygloss-dump.ndjson
```

Turn wifi on again: expected two `run=` lines with the same id, the first with a non-zero `missing=`, the second with
all the records. `curl http://localhost:8765/status` must show the same counts as in step 3.

## 7. Server unreachable

Stop the devserver and turn wifi on again. Expected in the Kobo log: five `retrying in ...` lines and then
`FAIL sync`, in about 30 s, without the agent staying alive afterwards. Restart the devserver (as in step 6) and turn
wifi on again: syncing resumes.

## 8. Collecting the logs

Connect the Kobo over USB and copy **without modifying anything**:

```sh
cp /Volumes/KOBOeReader/.adds/wrygloss/agent.log /Volumes/KOBOeReader/.adds/wrygloss/agent.stdout.log ~/Desktop/
cp /Volumes/KOBOeReader/.adds/wrygloss-test/agent.log ~/Desktop/agent-test.log   # must end with UNINSTALL
```

Look at: `route_at_event`, `route_ready_ms`, `copy_ms`, `copy_attempts`, `query_ms`, `sync_ms`, `wire_bytes`,
`mem_sys_mb`, `total_ms` and any `warnings`. From `/tmp/wrygloss-dump.ndjson`: format of `created` and `modified`,
values of `hidden` and `type`, `chapter_title` present or `null`, format of `position` in books.

## 9. Uninstall (modifies the Kobo, whenever you want)

Create an empty `UNINSTALL` file in `.adds/wrygloss/`, eject, turn wifi on again: `agent.log` shows
`UNINSTALL: udev rule removed`. Then the `.adds/wrygloss/` folder can be deleted over USB.

`run.sh` from the current repo also removes the v0.1 test rule (`91-wrygloss-test.rules`). A Kobo installed before
that change needs the new `deploy/run.sh` copied into `.adds/wrygloss/` together with `UNINSTALL`.

## Not covered by this test yet

- POST over HTTPS to a real domain: needs the published server (a `*.vercel.app` stub or a registered domain).
- Behaviour after a firmware update.
