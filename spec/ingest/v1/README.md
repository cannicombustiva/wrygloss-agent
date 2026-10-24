# Wrygloss ingest API v1 (`wrygloss.ingest/1`)

Contract between the on-device agent and the Wrygloss server. This folder is the source of truth:

| File | Content |
|---|---|
| `README.md` | normative semantics (this file) |
| `sync-request.schema.json` | JSON Schema 2020-12 of the request body (structure, types, allowed fields) |
| `sync-response.schema.json` | JSON Schema of the 200 response |
| `problem.schema.json` | JSON Schema of error responses |
| `examples/valid/`, `examples/invalid/` | example requests: valid ones must pass the schema, invalid ones must not |
| `examples/word-cleaning.json` | test vectors for word cleaning |

The words MUST, SHOULD and MAY have the meaning given in RFC 2119.

## 1. Principles

1. **The agent sends raw data.** Values copied as they are in the device database, from a fixed list of tables and
   columns (section 4). Cleaning, merging, and interpreting positions and dates happen on the server: fixing the
   server costs a deploy, fixing the agent costs a USB reinstall on every device.
2. **No device account data.** The contract has no fields for the `user` table, `UserID` or `___UserID`, cookies,
   `AnalyticsEvents`, or the device serial number.
3. **Idempotency.** Repeating a request does not change the server state. Every retry is safe.
4. **The server is strict, the agent is tolerant.** The server rejects fields not defined by the declared version;
   the agent ignores response fields it does not know.

## 2. Transport and authentication

```
POST {server_url}/api/ingest/v1/sync
Authorization: Bearer wgd_<43 base64url characters>
Content-Type: application/json
Content-Encoding: gzip            (MAY; if absent the body is uncompressed JSON)
User-Agent: wrygloss-agent/<version>
```

- **Device token.** `wgd_` followed by 32 random bytes in base64url without padding. The server generates the token
  at pairing, shows it only once (inside the `config.json` to copy onto the device) and stores only its SHA-256.
- **Scope.** The token only allows writing the ingest of its own device. It does not allow reading anything.
- **Revocation.** The server marks the device as revoked; every later request gets `401 token_revoked`.
- **New pairing.** A new token is a new device for the server: the agent MUST reset its acked state when the token
  changes and resend everything.
- HTTPS only in production. The agent verifies the certificate with embedded roots (the device has none).

## 3. Request

Full example in `examples/valid/full-sync.json`. Structure:

| Field | Type | Notes |
|---|---|---|
| `schema` | string | contract version used by the agent, `"1.0"` |
| `run` | object | run metadata (3.1) |
| `device` | object | `firmware`, `model_id`, `arch` (3.2) |
| `upserts` | object | `annotations`, `words`, `books`: arrays of new or changed records (section 4) |
| `manifest` | object | only in the last batch (`run.final = true`), required there: full set of ids and `h` (5) |

### 3.1 `run`

| Field | Type | Notes |
|---|---|---|
| `id` | string | run identifier, the same in every batch and retry of the same run; for logs only |
| `agent_version` | string | semver |
| `started_at` | string | instant the agent started, RFC 3339 UTC (`...Z`) |
| `route_at_event` | boolean | default route present when the agent started |
| `route_ready_ms` | integer or null | milliseconds from start to route ready; `null` if the route never came up |
| `attempt` | integer | HTTP attempt number for this batch, from 1 |
| `batch` | integer | batch number within the run, from 1 |
| `final` | boolean | last batch: carries the `manifest` |
| `warnings` | array of string | e.g. `missing_column:Bookmark.Color`, `oversize:annotations`; format `code[:detail]` |

### 3.2 `device`

| Field | Source | Notes |
|---|---|---|
| `firmware` | `.kobo/version`, third field | e.g. `4.46.23836`; `null` if unreadable |
| `model_id` | `.kobo/version`, sixth field | model identifier, the same for every device of that model |
| `arch` | runtime | e.g. `arm` |

The first field of `.kobo/version` is the serial number: the agent MUST NOT send it.

## 4. Records

Every record has `id` (key on the device) and `h` (fingerprint). All other fields are always present and are `null`
if the column is missing or empty in the database.

**`h`**: 16 lowercase hex characters, the first 8 bytes of the SHA-256 of the agent's serialization of the record.
The server stores it as is and never recomputes it: it only tells whether the record changed. An agent MUST produce
the same `h` for the same content from one run to the next.

Dates are raw device strings, with or without `Z` (e.g. `2026-09-19T15:05:59.819` or `2026-09-19T15:05:59Z`): they
are all UTC (verified on the device, section 10) and the server interprets them as such.

### 4.1 `annotations` (table `Bookmark`)

| Field | Column | Notes |
|---|---|---|
| `id` | `BookmarkID` | unique key (UUID and SyncTime are empty on the device) |
| `volume_id` | `VolumeID` | book; for sideloaded books it is the file path |
| `content_id` | `ContentID` | `<file>#(n)<path inside the EPUB>` |
| `chapter_title` | `content.Title` of the row with the same `ContentID` | `null` if not found |
| `type` | `Type` | raw: `highlight`, `note`, `dogear`, other future values |
| `text` | `Text` | highlighted text |
| `annotation` | `Annotation` | user note |
| `start_path`, `end_path` | `StartContainerPath`, `EndContainerPath` | e.g. `OEBPS/c01.xhtml#point(/1/4/2/505:1)` |
| `chapter_progress` | `ChapterProgress` | number |
| `color` | `Color` | integer |
| `hidden` | `Hidden` | boolean (the device may store it as `0/1` or `true/false`: the agent normalizes) |
| `created`, `modified` | `DateCreated`, `DateModified` | raw strings |

`Bookmark` columns never read: `UserID`, `UUID`, `SyncTime`, `Published`, `Creator`, `ExtraAnnotationData`,
`ContextString`, `Version`, container offsets and indexes.

### 4.2 `words` (table `WordList`)

| Field | Column | Notes |
|---|---|---|
| `id` | `Text` | **original string**, punctuation included (e.g. `snazzy,`): it is the key on the device |
| `volume_id` | `VolumeId` | book where the lookup happened; breaks if the file is renamed |
| `dict_suffix` | `DictSuffix` | dictionary selected by the user (`-en`, `-es-en`...): not the language of the word |
| `created` | `DateCreated` | raw string |

### 4.3 `books` (table `content`, `ContentType = 6`)

Only books **cited** by at least one annotation or word sent. No other title from the library.

| Field | Column | Notes |
|---|---|---|
| `id` | `ContentID` | equal to the `volume_id` of the records citing it |
| `title` | `Title` | |
| `author` | `Attribution` | |
| `language` | `Language` | raw (`en`, `en-US`, `it`...) |
| `position` | `ChapterIDBookmarked` | current reading position, raw; the server keeps its history per sync |

`content` columns never read: `___UserID` and every column not listed.

### 4.4 Missing columns

The agent checks which columns exist before reading. If an optional column is missing it sends `null` and adds the
warning `missing_column:<Table>.<Column>`. If an essential column (`Bookmark.BookmarkID`, `Bookmark.VolumeID`,
`WordList.Text`, `content.ContentID`, `content.ContentType`) or the whole table is missing, it does not send that
record kind, adds `missing_table:<Table>` or `missing_column:...` and **omits that kind from the manifest** (5.2), so
the server does not read it as a total deletion.

## 5. Sync semantics

### 5.1 Incremental with acknowledgement

The agent keeps the acked state: for each kind, the `id -> h` map from the last 200 response. On every run it:

1. reads all records and computes their `h`;
2. puts in `upserts` only the records whose `h` differs from the acked state (or that are absent from it);
3. splits the `upserts` into batches (limits in 7); the last batch has `run.final = true` and carries the full
   `manifest`;
4. after every 200 updates the acked state with the batch's records, including those in `rejected`;
5. if the final response contains `missing`, sends those records in a new round (at most 2 extra rounds);
6. at the end the acked state holds only the ids of the manifest.

A batch with `final = false` MUST NOT contain `manifest`; the server applies only the `upserts`.

### 5.2 Manifest, deletions and `missing`

`manifest` has one key per kind; each key is an object `{ id: h }` with **all** the records present on the device,
whether or not they were sent in this run. A kind omitted from the manifest is left untouched (4.4).

After applying the `upserts` of the same batch, for each kind present in the manifest the server applies:

- **deletion**: record live on the server and absent from the manifest -> `deleted_at = now` (soft delete);
- **restore**: record deleted on the server, present in the manifest with the same `h` -> `deleted_at = null`;
- **`missing`**: manifest id unknown to the server, or with an `h` different from the stored one -> listed in
  `missing`.

Rejected records (6.2) count as known with their `h`: they do not end up in `missing`.

Soft delete makes even a wrong manifest reversible (inconsistent database copy, stolen token). What happens to the
study cards of a deleted record is up to the app, not the contract.

### 5.3 Ordering and concurrency

Requests from the same device arrive in sequence (the agent holds a lock). The server SHOULD serialize them per
device anyway.

## 6. Responses

### 6.1 `200 OK`

```json
{
  "schema": "1.0",
  "server_time": "2026-10-24T13:05:03Z",
  "missing":  { "annotations": [], "words": [], "books": [] },
  "rejected": [{ "kind": "annotations", "id": "...", "code": "field_too_long", "pointer": "/text" }],
  "deleted":  { "annotations": 0, "words": 0, "books": 0 },
  "min_interval_s": 0,
  "latest_agent": "0.2.0"
}
```

- `missing` and `deleted` are empty/zero in non-final batches.
- `min_interval_s`: minimum interval in seconds the agent SHOULD keep between two successful syncs. It lets the
  server tune the frequency without updating agents.
- `latest_agent`: latest published version, for a notice in the log and in the app.

### 6.2 Per-record rejections

A value out of limits rejects **only that record**, not the request: otherwise one anomalous record would block every
future sync. The server stores `id`, `h` and the rejection code without the content, so the app can show it.

| `code` | When |
|---|---|
| `id_too_long` | `id` over 512 characters |
| `field_too_long` | a text field over 32 768 characters (Unicode code points) |
| `invalid_value` | value that cannot be interpreted (e.g. non-finite `chapter_progress`) |

### 6.3 Errors

Body `application/problem+json` (RFC 9457) with a stable `code` extension; the agent decides only on `code`.

```json
{ "type": "urn:wrygloss:problem:schema_violation", "title": "Schema violation", "status": 422,
  "code": "schema_violation", "errors": [{ "pointer": "/upserts/words/0/lemma", "message": "field not allowed" }] }
```

| Status | `code` | Meaning | Agent behaviour |
|---|---|---|---|
| 400 | `malformed_json` | body is not JSON | log, no retry |
| 400 | `bad_encoding` | invalid gzip | log, no retry |
| 401 | `invalid_token` | token missing or unknown | blocks until the token in the config changes |
| 401 | `token_revoked` | device revoked | as above |
| 405 | `method_not_allowed` | method other than POST | log |
| 410 | `schema_unsupported` | contract version no longer (or not yet) accepted | blocks until the agent version changes |
| 413 | `payload_too_large` | over the limits in 7 | shrinks the batches and retries once |
| 415 | `unsupported_media_type` | `Content-Type` or `Content-Encoding` not allowed | log |
| 422 | `schema_violation` | structure does not conform to the schema of the declared version | log, no retry |
| 429 | `rate_limited` | over the rate limits; `Retry-After` header in seconds | retries after the wait, if it fits in the time budget |
| 5xx | `internal` (or no body) | server error | retries with growing waits |

Network errors and 5xx: at most 6 attempts, with waits of 1, 2, 4, 8, 16 s (jitter +/-20%), about 30 s in total;
never more than 60 s per run, including `Retry-After` waits. The next wifi event starts over anyway.

## 7. Limits

| Limit | Value | Violation |
|---|---|---|
| Compressed body (on the wire) | 1 MiB | 413 |
| Decompressed body | 8 MiB (streaming decompression with a cap) | 413 |
| Records in `upserts` per request (all kinds) | 1000 | 422 |
| Ids per kind in the `manifest` | 50 000 | 422 |
| `id` | 512 characters | record rejected |
| Text fields | 32 768 characters | record rejected |
| Requests per device | 60 per hour, burst 10 | 429 |

The agent SHOULD keep each batch under 2 MiB uncompressed and not send records over 256 KiB serialized (warning
`oversize:<kind>`).

## 8. Schema evolution

- `schema` in the body is `"1.<minor>"`. A minor version adds **only** optional fields or new allowed values.
- The server accepts every minor version it knows and validates the request against the schema **of that version**:
  a field not defined by the declared version is a 422. An agent MUST NOT send fields from versions later than the
  one it declares.
- A minor version unknown to the server gets 410. To avoid it, the server is always released **before** the agents
  that use the new version.
- Renaming or removing a field, or changing its meaning, requires `/api/ingest/v2/`. The server serves v1 and v2 side
  by side for a long time: agents are only updated through a USB install.
- The response may gain fields in any minor version; the agent ignores them.

## 9. Word cleaning and merging (server side)

Normative for the server; the agent does not apply it. Test vectors in `examples/word-cleaning.json`.

1. `nfc` = Unicode NFC normalization of `id`.
2. `clean` = `nfc` without characters of the Unicode categories `P*`, `S*`, `Z*`, `Cc`, `Cf` **only at the start and
   the end**. Inner marks (apostrophes, hyphens) and capitals stay.
3. If `clean` is empty, the word is `unusable`: it stays visible to the user, it is not silently dropped.
4. `key` = lowercase of `clean` with the default Unicode rules, without a language (in JavaScript `toLowerCase()`).
5. Entries of the same user with the same `(key, dict_suffix)` pair form a single word, with the oldest date
   (`min(created)`) and all original ids as aliases. Displayed form: the `clean` of the oldest entry.
6. A merged word is deleted only when all of its entries are.
7. The server stores `clean` and `key` at ingest together with a `cleaning_version`; if the rules change, it
   recomputes them from the original ids, which it keeps.

In JavaScript steps 1-4 are `const clean = raw.normalize("NFC").replace(/^[\p{P}\p{S}\p{Z}\p{Cc}\p{Cf}]+|[\p{P}\p{S}\p{Z}\p{Cc}\p{Cf}]+$/gu, ""); const key = clean.toLowerCase();`
(verified against the vectors, like the Go reference implementation in `internal/ingestref/words.go`).

Known limits: a leading apostrophe is lost (`'tis` -> `tis`), as is the one of the plural genitive (`students'`).

## 10. Device observations

Observed on the Clara BW, firmware 4.46.23836, first real sync on October 24, 2026 (32 annotations, 62 words,
2 books; every request valid against the schema):

- Dates: `Bookmark.DateCreated` without a timezone and with milliseconds (`2026-10-24T08:00:00.123`),
  `Bookmark.DateModified` and `WordList.DateCreated` with `Z` and without milliseconds (`2026-10-24T08:00:00Z`). For
  all 32 annotations `modified - created = 0`: both forms use the same clock. Confirmed against the real time of a
  highlight: all dates are UTC, including those without `Z`. The server MUST interpret them as UTC.
- `ChapterIDBookmarked`: path inside the EPUB without the `file://` prefix, in the form
  `OEBPS/....html#point(/1/4/2/505:1)`.
- `chapter_title` found for every annotation, dogears included: matching `Bookmark.ContentID` to the chapter rows in
  `content` works.
- `Type`: only `highlight` (22), `note` (2), `dogear` (8). `Hidden`: always false. Dogears have a null `text` and a
  `start_path` equal to a `ContentID` (`file:///...epub#(n)...`), not a `point(...)`.
- `WordList.VolumeId` can cite a book absent from `content` (renamed file: 3 words out of 62): the server must accept
  words without a book.
- `WordList` holds only the words added with "Add to My Words", not every dictionary lookup.
- A newly created highlight is rewritten by Nickel shortly afterwards: `created` loses its milliseconds
  (`.393` -> `.000`), `modified` goes from `null` to the creation date, `chapter_progress` is rounded to 6 decimals.
  The same record therefore arrives twice with a different `h`; the server MUST treat it as a normal update, not as a
  user edit.
- Deleting a highlight on the device removes the `Bookmark` row (it does not set `Hidden`): the id disappears from
  the manifest and the server marks it as deleted.

Still to verify:

- Whether the same string saved with two dictionaries overwrites `DictSuffix` (`Text` is the primary key of
  `WordList`).
- `Hidden` and `Type` values other than those observed (other firmware, hidden highlights).
