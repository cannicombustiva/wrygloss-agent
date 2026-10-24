# wrygloss-agent

Agente on-device per [Wrygloss](https://github.com/cannicombustiva/wrygloss): a ogni attivazione del wifi copia il
database del lettore, ne legge una lista fissa di colonne (evidenziazioni, note, dogear, parole cercate nel
dizionario e i libri che citano) e le sincronizza con il server secondo il contratto
[`spec/ingest/v1`](spec/ingest/v1/README.md). Lo studio avviene nell'app, non sul dispositivo.

Non ha nulla a che fare con Rakuten Kobo, che non lo approva ne' lo supporta.

## Cosa legge e cosa no

| Legge | Non legge mai |
|---|---|
| `Bookmark`: id, libro, capitolo, tipo, testo, nota, posizione, colore, `Hidden`, date | tabella `user` (email, token di accesso) |
| `WordList`: parola cosi' come salvata, libro, dizionario, data | `Bookmark.UserID`, `content.___UserID` |
| `content`: titolo, autore, lingua e posizione di lettura, **solo dei libri citati** | cookie, `AnalyticsEvents`, il resto della libreria |
| `.kobo/version`: firmware e modello | numero di serie (primo campo di `.kobo/version`) |

Il database originale non viene mai aperto: l'agente ne copia il file e il WAL in `/tmp`, verifica che non siano
cambiati durante la copia, legge la copia e la cancella. Il file sqlite non viene mai inviato. I test falliscono se un
valore della tabella `user`, degli id dell'account o del numero di serie compare in un payload, nel log o nello stato.

## Compatibilita'

| Dispositivo | Firmware | Stato |
|---|---|---|
| Kobo Clara BW (armv7l, kernel 4.9.77) | 4.46.23836 | v0.2 provata il 24 ottobre 2026 in rete locale (HTTP); HTTPS verso un dominio proprio da provare ([KOBO-TEST.md](KOBO-TEST.md)) |
| Altri modelli o firmware | | non testati |

Un aggiornamento del firmware potrebbe rimuovere la regola udev (non verificato): in quel caso l'agente smette di
sincronizzare in silenzio, l'app lo segnala dall'ultimo contatto e basta ripetere l'installazione.

## Installazione e disinstallazione

L'installazione scrive solo in `.adds/wrygloss/` e `.kobo/KoboRoot.tgz` sulla partizione utente:

```sh
./build.sh                                        # vedi BUILD.md
sh deploy/install.sh /percorso/config.json        # config.json generato dall'app (server_url e token)
```

Poi si espelle il Kobo in modo sicuro: si riavvia, installa la regola `/etc/udev/rules.d/91-wrygloss.rules` e
cancella `KoboRoot.tgz`. `deploy/package.sh` crea lo stesso contenuto come zip da estrarre nella radice del Kobo.

**Disinstallare**: creare un file vuoto `UNINSTALL` in `.adds/wrygloss/` e riattivare il wifi: la regola viene
rimossa (riga `UNINSTALL` nel log). Poi si puo' cancellare la cartella. Anche cancellare solo la cartella rende la
regola inerte. Revocare il token dall'app impedisce comunque ogni invio.

## Flusso di un'esecuzione

1. udev lancia `run.sh` a ogni nuova interfaccia `wlan0` (accensione del wifi, risveglio dallo sleep con wifi acceso,
   pulsante di sincronizzazione nativo). `run.sh` copia il binario in `/tmp` se `VERSION` e' cambiato e lo avvia in
   background.
2. L'agente registra se la rotta predefinita c'e' gia' (`route_at_event`), prende un blocco contro esecuzioni
   sovrapposte e salta se l'ultimo sync riuscito e' di meno di 30 s fa (o di `min_interval_s` indicato dal server).
3. Attende la rotta controllando ogni 250 ms (massimo 45 s) e registra `route_ready_ms`.
4. Copia stabile del database, `quick_check`, tre query sulla lista fissa di colonne.
5. Invia solo i record con impronta cambiata rispetto all'ultima conferma del server, poi il manifest completo per le
   cancellazioni; reinvia cio' che il server segnala come mancante. gzip, una sola connessione, al massimo 6
   tentativi con attese di 1-16 s (circa 30 s; fino a 60 s se il server chiede di attendere con `Retry-After`). Si ferma su token revocato o contratto non piu' accettato finche' non
   cambiano config o versione.
6. Scrive `state.json` (conferme del server) e una riga di log con conteggi e tempi, mai testi.

## Prestazioni: misure e scelte

Misure sul Clara BW reale (v0.1 e v0.2, 12 esecuzioni della v0.2 il 24 ottobre 2026, server in rete locale).

| Voce | v0.1 sul Kobo | v0.2 sul Kobo | Scelta |
|---|---|---|---|
| Durata di un'esecuzione | 21,2-21,7 s, di cui 20 s di debounce | 2,6-3,4 s, di cui 2,3-3,3 s di attesa della rotta | debounce sostituito da blocco, salto dopo un sync recente e attesa della rotta: un sync riesce anche se il wifi resta acceso pochi secondi |
| Rotta pronta all'evento | non misurato | mai (12 su 12): pronta dopo 2,3-3,3 s | attesa della rotta ogni 250 ms, necessaria |
| Eventi doppi al risveglio | uniti dal debounce | secondo evento 12-15 s dopo, saltato (`SKIP`) | salto se l'ultimo sync riuscito e' di meno di 30 s fa |
| Sonde TLS di prova | 1,0-1,4 s | 0 | rimosse: il POST fa gia' l'handshake |
| Handshake TLS | 0,7-1 s | da misurare (serve il server pubblicato) | una sola connessione per tutti i lotti; nessuna ripresa di sessione tra esecuzioni (complessita' sproporzionata) |
| Invio in rete locale | 21-137 ms | 46-175 ms | nessuna |
| Byte inviati | solo conteggi | primo sync (96 record) 6,3 KB; senza cambiamenti 2,6 KB (solo manifest); ripristino dopo perdita dei dati del server 9,1 KB | gzip |
| Copia del database | 4-14 ms | 3-14 ms, sempre al primo tentativo | nessuna |
| Query e `quick_check` | 24-30 ms (solo conteggi) | 46-162 ms (testi, capitoli, libri) | nessuna: meno del 5% della durata, che e' dominata dalla rotta; `quick_check` resta, una copia corrotta produrrebbe cancellazioni false |
| Dimensione del binario | 9,6 MB | 8,6 MB | tolti i simboli di debug del C rimasti nonostante `-s -w`; niente UPX ne' SQLite ridotto: guadagno piccolo, rischio alto |
| Memoria (`mem_sys_mb`) | 5 MB | 5 MB; 9 MB con il ripristino completo in due giri | nessuna |
| Server irraggiungibile | | 6 tentativi, `FAIL` dopo 29 s | nessuna |

Complessita': lettura e confronto in O(n) con mappe, nessun ordinamento; il manifest completo a ogni sync costa circa
6 KB con i dati attuali e circa 350 KB compressi a 10 000 record. Se la misura lo richiedera', la versione 1.1 del
contratto potra' aggiungere un riassunto per tipo.

## File

| Percorso | Contenuto |
|---|---|
| `main.go` | flusso di un'esecuzione, configurazione, log |
| `kobo.go` | lista fissa di colonne, copia stabile del database, lettura dei record |
| `sync.go` | lotti, invio con riprovi, conferme e giri per i record mancanti |
| `state.go` | stato persistente (`state.json`) |
| `internal/contract` | tipi del contratto |
| `internal/ingestref` | server di riferimento in memoria, validazione con lo schema, pulizia delle parole di riferimento |
| `cmd/devserver` | server di prova in rete locale per i test sul dispositivo |
| `spec/ingest/v1` | specifica, JSON Schema, esempi |
| `deploy/` | regola udev, `run.sh`, installazione, pacchetto |
| `testdata/kobo.sql`, `tools/make_test_db.py` | database sintetico con valori canarino |

## Licenza

MIT, vedi [LICENSE](LICENSE).
