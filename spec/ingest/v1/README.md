# Wrygloss ingest API v1 (`wrygloss.ingest/1`)

Contratto tra l'agente sul dispositivo e il server di Wrygloss. Questa cartella e' la fonte di verita':

| File | Contenuto |
|---|---|
| `README.md` | semantica normativa (questo file) |
| `sync-request.schema.json` | JSON Schema 2020-12 del corpo della richiesta (struttura, tipi, campi ammessi) |
| `sync-response.schema.json` | JSON Schema della risposta 200 |
| `problem.schema.json` | JSON Schema delle risposte di errore |
| `examples/valid/`, `examples/invalid/` | richieste di esempio: le valide devono passare lo schema, le non valide no |
| `examples/word-cleaning.json` | vettori di prova per la pulizia delle parole |

Le parole MUST, SHOULD e MAY hanno il significato della RFC 2119.

## 1. Principi

1. **L'agente invia dati grezzi.** Valori copiati cosi' come sono nel database del dispositivo, da una lista fissa di
   tabelle e colonne (sezione 4). Pulizia, unione, interpretazione di posizioni e date si fanno sul server: correggere
   il server costa un deploy, correggere l'agente costa una reinstallazione via USB su ogni dispositivo.
2. **Nessun dato dell'account del dispositivo.** Il contratto non ha campi per la tabella `user`, per `UserID` o
   `___UserID`, per i cookie, per `AnalyticsEvents` ne' per il numero di serie del dispositivo.
3. **Idempotenza.** Ripetere una richiesta non cambia lo stato del server. Ogni riprovo e' sicuro.
4. **Il server e' severo, l'agente e' tollerante.** Il server rifiuta i campi non previsti dalla versione dichiarata;
   l'agente ignora i campi della risposta che non conosce.

## 2. Trasporto e autenticazione

```
POST {server_url}/api/ingest/v1/sync
Authorization: Bearer wgd_<43 caratteri base64url>
Content-Type: application/json
Content-Encoding: gzip            (MAY; se assente il corpo e' JSON non compresso)
User-Agent: wrygloss-agent/<versione>
```

- **Token di dispositivo.** `wgd_` seguito da 32 byte casuali in base64url senza padding. Il server genera il token al
  pairing, lo mostra una sola volta (dentro il `config.json` da copiare sul dispositivo) e ne conserva solo lo SHA-256.
- **Ambito.** Il token permette solo di scrivere l'ingest del proprio dispositivo. Non permette di leggere nulla.
- **Revoca.** Il server marca il dispositivo come revocato; ogni richiesta successiva riceve `401 token_revoked`.
- **Nuovo pairing.** Un nuovo token e' un nuovo dispositivo per il server: l'agente MUST azzerare il proprio stato
  confermato quando il token cambia e reinviare tutto.
- Solo HTTPS in produzione. L'agente verifica il certificato con radici incorporate (il dispositivo non ne ha).

## 3. Richiesta

Esempio completo in `examples/valid/full-sync.json`. Struttura:

| Campo | Tipo | Note |
|---|---|---|
| `schema` | string | versione del contratto usata dall'agente, `"1.0"` |
| `run` | object | metadati dell'esecuzione (3.1) |
| `device` | object | `firmware`, `model_id`, `arch` (3.2) |
| `upserts` | object | `annotations`, `words`, `books`: array di record nuovi o cambiati (sezione 4) |
| `manifest` | object | solo nell'ultimo lotto (`run.final = true`), obbligatorio li': insieme completo di id e `h` (5) |

### 3.1 `run`

| Campo | Tipo | Note |
|---|---|---|
| `id` | string | identificativo dell'esecuzione, uguale in tutti i lotti e riprovi della stessa esecuzione; solo per i log |
| `agent_version` | string | semver |
| `started_at` | string | istante di avvio dell'agente, RFC 3339 UTC (`...Z`) |
| `route_at_event` | boolean | rotta predefinita presente all'avvio dell'agente |
| `route_ready_ms` | integer o null | millisecondi dall'avvio alla rotta pronta; `null` se la rotta non e' arrivata |
| `attempt` | integer | numero del tentativo HTTP per questo lotto, da 1 |
| `batch` | integer | numero del lotto nell'esecuzione, da 1 |
| `final` | boolean | ultimo lotto: porta il `manifest` |
| `warnings` | array di string | es. `missing_column:Bookmark.Color`, `oversize:annotations`; formato `codice[:dettaglio]` |

### 3.2 `device`

| Campo | Fonte | Note |
|---|---|---|
| `firmware` | `.kobo/version`, terzo campo | es. `4.46.23836`; `null` se illeggibile |
| `model_id` | `.kobo/version`, sesto campo | identificativo del modello, uguale per tutti i dispositivi dello stesso modello |
| `arch` | runtime | es. `arm` |

Il primo campo di `.kobo/version` e' il numero di serie: l'agente MUST NOT inviarlo.

## 4. Record

Ogni record ha `id` (chiave sul dispositivo) e `h` (impronta). Tutti gli altri campi sono sempre presenti e valgono
`null` se la colonna e' assente o vuota nel database.

**`h`**: 16 caratteri esadecimali minuscoli, i primi 8 byte dello SHA-256 della serializzazione del record fatta
dall'agente. Il server lo conserva cosi' com'e' e non lo ricalcola: serve solo a sapere se il record e' cambiato.
Un agente MUST produrre lo stesso `h` per lo stesso contenuto tra un'esecuzione e l'altra.

Le date sono stringhe grezze del dispositivo (es. `2026-09-19T15:05:59.819`, senza fuso orario dichiarato): il server
le interpreta.

### 4.1 `annotations` (tabella `Bookmark`)

| Campo | Colonna | Note |
|---|---|---|
| `id` | `BookmarkID` | chiave unica (UUID e SyncTime sono vuoti sul dispositivo) |
| `volume_id` | `VolumeID` | libro; per i libri caricati dall'utente e' il percorso del file |
| `content_id` | `ContentID` | `<file>#(n)<percorso nell'EPUB>` |
| `chapter_title` | `content.Title` della riga con `ContentID` uguale | `null` se non trovata |
| `type` | `Type` | grezzo: `highlight`, `note`, `dogear`, altri valori futuri |
| `text` | `Text` | testo evidenziato |
| `annotation` | `Annotation` | nota dell'utente |
| `start_path`, `end_path` | `StartContainerPath`, `EndContainerPath` | es. `OEBPS/c01.xhtml#point(/1/4/2/505:1)` |
| `chapter_progress` | `ChapterProgress` | numero |
| `color` | `Color` | intero |
| `hidden` | `Hidden` | booleano (il dispositivo puo' salvarlo come `0/1` o `true/false`: l'agente normalizza) |
| `created`, `modified` | `DateCreated`, `DateModified` | stringhe grezze |

Colonne di `Bookmark` mai lette: `UserID`, `UUID`, `SyncTime`, `Published`, `Creator`, `ExtraAnnotationData`,
`ContextString`, `Version`, offset e indici dei contenitori.

### 4.2 `words` (tabella `WordList`)

| Campo | Colonna | Note |
|---|---|---|
| `id` | `Text` | **stringa originale**, punteggiatura compresa (es. `snazzy,`): e' la chiave sul dispositivo |
| `volume_id` | `VolumeId` | libro in cui e' avvenuta la ricerca; si rompe se il file viene rinominato |
| `dict_suffix` | `DictSuffix` | dizionario selezionato dall'utente (`-en`, `-es-en`...): non e' la lingua della parola |
| `created` | `DateCreated` | stringa grezza |

### 4.3 `books` (tabella `content`, `ContentType = 6`)

Solo i libri **citati** da almeno un'annotazione o una parola inviata. Nessun altro titolo della libreria.

| Campo | Colonna | Note |
|---|---|---|
| `id` | `ContentID` | uguale al `volume_id` dei record che lo citano |
| `title` | `Title` | |
| `author` | `Attribution` | |
| `language` | `Language` | grezzo (`en`, `en-US`, `it`...) |
| `position` | `ChapterIDBookmarked` | posizione di lettura corrente, grezza; il server ne conserva la storia per sync |

Colonne di `content` mai lette: `___UserID` e tutte quelle non elencate.

### 4.4 Colonne mancanti

L'agente controlla le colonne presenti prima di leggere. Se manca una colonna facoltativa invia `null` e aggiunge
l'avviso `missing_column:<Tabella>.<Colonna>`. Se manca una colonna essenziale (`Bookmark.BookmarkID`,
`Bookmark.VolumeID`, `WordList.Text`, `content.ContentID`, `content.ContentType`) o la tabella intera, non invia quel
tipo di record, aggiunge `missing_table:<Tabella>` o `missing_column:...` e **omette quel tipo dal manifest** (5.2),
cosi' il server non lo interpreta come una cancellazione totale.

## 5. Semantica del sync

### 5.1 Incrementale con conferma

L'agente conserva lo stato confermato: per ogni tipo, la mappa `id -> h` dell'ultima risposta 200. A ogni esecuzione:

1. legge tutti i record e ne calcola `h`;
2. mette in `upserts` solo i record con `h` diverso dallo stato confermato (o assenti da esso);
3. divide gli `upserts` in lotti (limiti in 7); l'ultimo lotto ha `run.final = true` e porta il `manifest` completo;
4. dopo ogni 200 aggiorna lo stato confermato con i record del lotto, compresi quelli in `rejected`;
5. se la risposta finale contiene `missing`, invia quei record in un nuovo giro (al massimo 2 giri in piu');
6. alla fine lo stato confermato contiene solo gli id del manifest.

Un lotto con `final = false` MUST NOT contenere `manifest`; il server applica solo gli `upserts`.

### 5.2 Manifest, cancellazioni e `missing`

`manifest` ha una chiave per tipo; ogni chiave e' un oggetto `{ id: h }` con **tutti** i record presenti sul
dispositivo, inviati o no in questa esecuzione. Un tipo omesso dal manifest non viene toccato (4.4).

Il server, dopo aver applicato gli `upserts` dello stesso lotto, per ogni tipo presente nel manifest:

- **cancellazione**: record vivo sul server e assente dal manifest -> `deleted_at = now` (soft delete);
- **ripristino**: record cancellato sul server, presente nel manifest con lo stesso `h` -> `deleted_at = null`;
- **`missing`**: id del manifest sconosciuto al server, o con `h` diverso da quello salvato -> elencato in `missing`.

I record rifiutati (6.2) contano come noti con il loro `h`: non finiscono in `missing`.

Il soft delete rende reversibile anche un manifest sbagliato (copia del database incoerente, token rubato). Cosa
succede alle carte di studio di un record cancellato lo decide l'app, non il contratto.

### 5.3 Ordine e concorrenza

Le richieste di uno stesso dispositivo arrivano in sequenza (l'agente ha un blocco). Il server SHOULD comunque
serializzarle per dispositivo.

## 6. Risposte

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

- `missing` e `deleted` sono vuoti/zero nei lotti non finali.
- `min_interval_s`: intervallo minimo in secondi che l'agente SHOULD rispettare tra due sync riusciti. Permette di
  regolare la frequenza senza aggiornare gli agenti.
- `latest_agent`: ultima versione pubblicata, per un avviso nel log e nell'app.

### 6.2 Rifiuti per record

Un valore fuori limite rifiuta **solo quel record**, non la richiesta: altrimenti un record anomalo bloccherebbe ogni
sync futuro. Il server salva `id`, `h` e il codice del rifiuto senza il contenuto, cosi' l'app puo' mostrarlo.

| `code` | Quando |
|---|---|
| `id_too_long` | `id` oltre 512 caratteri |
| `field_too_long` | un campo di testo oltre 32 768 caratteri (code point Unicode) |
| `invalid_value` | valore non interpretabile (es. `chapter_progress` non finito) |

### 6.3 Errori

Corpo `application/problem+json` (RFC 9457) con estensione `code` stabile; l'agente decide solo in base a `code`.

```json
{ "type": "urn:wrygloss:problem:schema_violation", "title": "Schema violation", "status": 422,
  "code": "schema_violation", "errors": [{ "pointer": "/upserts/words/0/lemma", "message": "campo non ammesso" }] }
```

| Status | `code` | Significato | Comportamento dell'agente |
|---|---|---|---|
| 400 | `malformed_json` | corpo non e' JSON | log, nessun riprovo |
| 400 | `bad_encoding` | gzip non valido | log, nessun riprovo |
| 401 | `invalid_token` | token assente o sconosciuto | si blocca finche' il token nel config non cambia |
| 401 | `token_revoked` | dispositivo revocato | come sopra |
| 405 | `method_not_allowed` | metodo diverso da POST | log |
| 410 | `schema_unsupported` | versione del contratto non piu' (o non ancora) accettata | si blocca finche' la versione dell'agente non cambia |
| 413 | `payload_too_large` | oltre i limiti di 7 | riduce i lotti e riprova una volta |
| 415 | `unsupported_media_type` | `Content-Type` o `Content-Encoding` non ammessi | log |
| 422 | `schema_violation` | struttura non conforme allo schema della versione dichiarata | log, nessun riprovo |
| 429 | `rate_limited` | oltre i limiti di frequenza; intestazione `Retry-After` in secondi | riprova dopo l'attesa, se rientra nel tempo massimo |
| 5xx | `internal` (o nessun corpo) | errore del server | riprova con attese crescenti |

Errori di rete e 5xx: riprovi con attese di 1, 2, 4, 8, 16 s (jitter +/-20%), al massimo circa 60 s per esecuzione.
Il prossimo evento wifi riparte comunque da capo.

## 7. Limiti

| Limite | Valore | Violazione |
|---|---|---|
| Corpo compresso (sul filo) | 1 MiB | 413 |
| Corpo decompresso | 8 MiB (decompressione a flusso con tetto) | 413 |
| Record in `upserts` per richiesta (somma dei tipi) | 1000 | 422 |
| Id per tipo nel `manifest` | 50 000 | 422 |
| `id` | 512 caratteri | rifiuto del record |
| Campi di testo | 32 768 caratteri | rifiuto del record |
| Richieste per dispositivo | 60 all'ora, picco 10 | 429 |

L'agente SHOULD tenere ogni lotto sotto 2 MiB non compressi e non inviare record oltre 256 KiB serializzati (avviso
`oversize:<tipo>`).

## 8. Evoluzione dello schema

- `schema` nel corpo e' `"1.<minore>"`. Una versione minore aggiunge **solo** campi facoltativi o nuovi valori ammessi.
- Il server accetta ogni versione minore che conosce e valida la richiesta contro lo schema **di quella versione**:
  un campo non previsto dalla versione dichiarata e' un 422. Un agente MUST NOT inviare campi di versioni successive
  a quella che dichiara.
- Una versione minore sconosciuta al server riceve 410. Per evitarlo il server viene sempre rilasciato **prima**
  degli agenti che usano la nuova versione.
- Rinominare o rimuovere un campo, o cambiarne il significato, richiede `/api/ingest/v2/`. Il server serve v1 e v2 in
  parallelo a lungo: gli agenti si aggiornano solo con un'installazione via USB.
- La risposta puo' guadagnare campi in qualsiasi versione minore; l'agente li ignora.

## 9. Pulizia e unione delle parole (lato server)

Normativa per il server; l'agente non la applica. Vettori di prova in `examples/word-cleaning.json`.

1. `nfc` = normalizzazione Unicode NFC di `id`.
2. `clean` = `nfc` senza i caratteri delle categorie Unicode `P*`, `S*`, `Z*`, `Cc`, `Cf` **solo all'inizio e alla
   fine**. Segni interni (apostrofi, trattini) e maiuscole restano.
3. Se `clean` e' vuoto, la parola e' `unusable`: resta visibile all'utente, non viene scartata in silenzio.
4. `key` = minuscolo di `clean` con le regole Unicode predefinite, senza lingua (in JavaScript `toLowerCase()`).
5. Le voci con la stessa coppia `(key, dict_suffix)` dello stesso utente formano una sola parola, con data piu'
   vecchia (`min(created)`) e tutti gli id originali come alias. Forma mostrata: `clean` della voce piu' vecchia.
6. Una parola unita e' cancellata solo quando lo sono tutte le sue voci.
7. Il server salva `clean` e `key` all'ingest insieme a un `cleaning_version`; se le regole cambiano, ricalcola
   partendo dagli id originali, che conserva.

In JavaScript i passi 1-4 sono `const clean = raw.normalize("NFC").replace(/^[\p{P}\p{S}\p{Z}\p{Cc}\p{Cf}]+|[\p{P}\p{S}\p{Z}\p{Cc}\p{Cf}]+$/gu, ""); const key = clean.toLowerCase();`
(verificato contro i vettori, come l'implementazione Go di riferimento in `internal/ingestref/words.go`).

Limiti noti: un apostrofo iniziale si perde (`'tis` -> `tis`), come quello del genitivo plurale (`students'`).

## 10. Note da verificare sul dispositivo

- Fuso orario delle date di `Bookmark` e `WordList`.
- Formato di `ChapterIDBookmarked` e corrispondenza tra `Bookmark.ContentID` e le righe dei capitoli in `content`.
- Valori reali di `Hidden` e di `Type` oltre a `highlight`, `note`, `dogear`.
- Se la stessa stringa cercata con due dizionari sovrascrive `DictSuffix` (`Text` e' chiave primaria di `WordList`).
