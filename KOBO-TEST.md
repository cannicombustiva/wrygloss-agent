# Test della v0.2 sul Kobo reale

Procedura per il Clara BW (firmware 4.46.23836) con il server di prova sul Mac, in rete locale. **I passi 2 e 9
modificano il filesystem del Kobo**: eseguirli solo quando si e' deciso di farlo. Tutti gli altri passi si fanno
dal Kobo (wifi, sleep, evidenziazioni) o dal Mac.

Cosa si vuole verificare (apre o chiude i punti "da verificare" della specifica e del README):

| # | Verifica | Dove si legge l'esito |
|---|---|---|
| A | lettura dei contenuti veri, formati di date, `ChapterIDBookmarked`, `Hidden`, `Type`, titoli dei capitoli | `dump.ndjson` sul Mac |
| B | quando la rotta e' pronta rispetto all'evento | `route_at_event`, `route_ready_ms` |
| C | sostituzione del debounce: eventi doppi, toggle ravvicinati, wifi acceso pochi secondi | righe `SKIP` e `END` |
| D | sync incrementale, cancellazioni, ripristino dopo perdita dei dati del server | righe del devserver |
| E | riprovi senza rete e tempo massimo | righe `riprovo` e `FAIL` |
| F | durata, memoria, byte inviati | riga `END` |
| G | disinstallazione della v0.1 di prova e della v0.2 | righe `UNINSTALL` |

## 1. Sul Mac: compilare e avviare il server di prova

```sh
cd wrygloss-agent
./build.sh
go run ./cmd/devserver -config-out /tmp/wrygloss-config.json -dump /tmp/wrygloss-dump.ndjson
```

Il devserver stampa il `config.json` con l'IP del Mac. Se macOS chiede di accettare connessioni in entrata, consentire.
Kobo e Mac devono essere sulla stessa rete wifi. Lasciare il terminale aperto: ogni sync stampa una riga `run=...`.
`/tmp/wrygloss-dump.ndjson` contiene i tuoi testi e resta sul Mac.

## 2. Installazione (modifica il Kobo)

Collegare il Kobo via USB, poi:

```sh
sh deploy/install.sh /tmp/wrygloss-config.json
```

Scrive `.adds/wrygloss/` e `.kobo/KoboRoot.tgz`, e crea `UNINSTALL` in `.adds/wrygloss-test/` per la v0.1.
Espellere il Kobo in modo sicuro: si riavvia e installa la regola.

## 3. Primo sync (avvio)

Dopo il riavvio, con il wifi che si accende da solo o acceso a mano: entro pochi secondi il devserver stampa
`run=... upserts=<annotazioni>/<parole>/<libri>`. Atteso: circa 32 annotazioni (22 evidenziazioni, 2 note, 8 dogear
o piu', se ne hai aggiunte), 62 parole o piu', i libri citati.

## 4. Eventi ravvicinati

1. Spegnere e riaccendere il wifi entro 30 s dal sync precedente: atteso `SKIP ultimo sync riuscito Ns fa`.
2. Attendere piu' di 30 s, spegnere e riaccendere: atteso un sync con `upserts=0/0/0` (solo manifest).
3. Sleep e risveglio con wifi acceso: annotare se gli eventi sono doppi (due `START` a 11-16 s) e che il secondo
   sia `SKIP`.
4. Con il wifi spento, premere il pulsante di sincronizzazione nativo: verificare che compaia `END ok` anche se il
   wifi resta acceso poco (la v0.1 con 20 s di debounce non ce la faceva).

## 5. Modifiche

1. Evidenziare una frase nuova e cercare una parola nel dizionario, poi riattivare il wifi (dopo 30 s):
   atteso `upserts=1/1/...`.
2. Cancellare quell'evidenziazione sul Kobo e riattivare il wifi: atteso `deleted=1/0/0`.

## 6. Perdita dei dati del server

Fermare il devserver con Ctrl+C e riavviarlo con **lo stesso token** (stampato nel config.json del passo 1):

```sh
go run ./cmd/devserver -token wgd_... -dump /tmp/wrygloss-dump.ndjson
```

Riattivare il wifi: attese due righe `run=` con lo stesso id, la prima con `missing=` diverso da zero, la seconda
con tutti i record. `curl http://localhost:8765/status` deve mostrare gli stessi conteggi del passo 3.

## 7. Server irraggiungibile

Fermare il devserver e riattivare il wifi. Atteso nel log del Kobo: cinque righe `riprovo tra ...` e poi `FAIL sync`,
in circa 30 s, senza che l'agente resti attivo oltre. Riavviare il devserver (come al passo 6) e riattivare il wifi:
il sync riprende.

## 8. Raccolta dei log

Collegare il Kobo via USB e copiare **senza modificare nulla**:

```sh
cp /Volumes/KOBOeReader/.adds/wrygloss/agent.log /Volumes/KOBOeReader/.adds/wrygloss/agent.stdout.log ~/Desktop/
cp /Volumes/KOBOeReader/.adds/wrygloss-test/agent.log ~/Desktop/agent-test.log   # deve finire con UNINSTALL
```

Da guardare: `route_at_event`, `route_ready_ms`, `copy_ms`, `copy_attempts`, `query_ms`, `sync_ms`, `wire_bytes`,
`mem_sys_mb`, `totale_ms` e gli eventuali `warnings`. Da `/tmp/wrygloss-dump.ndjson`: formato di `created` e
`modified`, valori di `hidden` e `type`, `chapter_title` presente o `null`, formato di `position` nei libri.

## 9. Disinstallazione (modifica il Kobo, quando si vuole)

Creare un file vuoto `UNINSTALL` in `.adds/wrygloss/`, espellere, riattivare il wifi: in `agent.log` compare
`UNINSTALL: regola udev rimossa`. Poi via USB si puo' cancellare `.adds/wrygloss/`.

## Ancora non coperto da questo test

- POST su HTTPS verso un dominio proprio: richiede il server pubblicato (stub su `*.vercel.app` o dominio registrato).
- Comportamento dopo un aggiornamento del firmware.
