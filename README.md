# Wrygloss agent: sorgente della versione di prova (v0.1.0-test)

Agente on-device per Kobo Clara BW (firmware 4.46.23836, kernel 4.9.77, armv7l). Questa e' la versione di prova:
legge solo i conteggi di `Bookmark` e `WordList` e li invia a un endpoint. Non legge testi, titoli o la tabella `user`.
Da evolvere nella v0.2 (vedi sotto). Vedi BUILD.md per compilare e provare.

## File
- `main.go`: tutto l'agente (circa 350 righe). Debounce, blocco, attesa della rotta, test DNS/TLS, copia del database,
  conteggi, POST, log.
- `cacert.pem`: radici Mozilla (124 certificati, copiate da Ubuntu) incorporate nel binario con go:embed.
- `go.mod`, `go.sum`: unica dipendenza esterna, github.com/mattn/go-sqlite3 v1.14.24.
- `zigcc.sh`: wrapper del compilatore C per la cross-compilazione.
- `deploy/`: regola udev, `run.sh`, `install.sh`, `make_koboroot.sh`.
- `tools/`: `make_test_db.py` (database sintetico, con tabella `user` finta per verificare che non venga letta),
  `test_server.py` (server di prova che stampa i JSON ricevuti).

## Flusso attuale
1. udev lancia `run.sh` a ogni nuova interfaccia `wlan0` (accensione wifi, risveglio dallo sleep, pulsante di sincronizzazione).
2. L'agente scrive il suo orario in `/tmp/wrygloss-last-event`, dorme `quiet_seconds` (20) e prosegue solo se nessun evento
   piu' recente ha riscritto il file (vince l'ultimo evento di una raffica).
3. Blocco con `flock` su `/tmp/wrygloss.lock` contro esecuzioni sovrapposte; intervallo minimo opzionale tra sync riusciti.
4. Attende la rotta predefinita (`/proc/net/route`), poi esegue DNS e due GET HTTPS di prova (certificati incorporati
   e di sistema; questi ultimi falliscono sul Kobo perche' `/etc/ssl/certs` e' vuota).
5. Copia `KoboReader.sqlite` (piu' `-wal` e `-shm`, se esistono) in `/tmp`, la apre in sola lettura, esegue `quick_check`
   e conta le righe, poi cancella la copia.
6. POST JSON con token `Bearer` all'endpoint configurato, log in `agent.log` (rotazione a 200 KB).

## Misure reali sul Clara BW (5 esecuzioni complete, 24 ottobre 2026)
| Voce | Valore |
|---|---|
| Durata totale per esecuzione | circa 21,2-21,7 s, di cui 20 s sono l'attesa del debounce |
| Memoria (`mem_sys_mb`, Go runtime) | 5 MB |
| Copia del database (circa 1 MB piu' WAL) | 4-14 ms |
| Query dei conteggi | 24-30 ms (comprende `quick_check`) |
| DNS | 46-77 ms |
| Handshake e GET HTTPS con radici incorporate | 690-1039 ms |
| GET HTTPS con radici di sistema (solo test, fallisce) | 310-353 ms sprecati |
| POST in rete locale | 21-137 ms |
| Dimensione del binario | 9,6 MB |
| Rotta predefinita | presente alla prima verifica, ma la verifica avviene dopo 20 s: non si sa quando fosse pronta |

Il database usa il WAL (file `-wal` e `-shm` presenti). `quick_check` ha dato `ok` in tutte le esecuzioni.

## Da fare nella v0.2 (compiti per chi riprende il lavoro)
1. Contratto JSON versionato e API di ingest (autenticazione con token per dispositivo, risposte, errori).
2. Leggere e inviare i dati veri: evidenziazioni, note, dogear, parole (pulizia ai bordi con chiave in minuscolo,
   unione dei duplicati, id originali), metadati dei libri e stato di lettura. Mai la tabella `user`: lista fissa di
   tabelle e colonne.
3. Sync incrementale con conferma del server (cursore o insieme di id), non l'intero contenuto a ogni evento.
4. Registrare lo stato della rotta all'istante dell'evento; riprovi con attese crescenti.
5. Rimuovere le sonde di sola prova (`tls_system`, URL di test) dalla versione di produzione.
6. Revisione di prestazioni e complessita' (vedi sotto).

## Revisione di prestazioni e complessita' (richiesta esplicita)
Misura prima di ottimizzare: sul dispositivo CPU e memoria sono trascurabili, il tempo e' dominato dall'attesa del
debounce e dall'handshake TLS. Ottimizza solo dove la misura lo giustifica e tieni il codice semplice.
Punti da valutare:
- Complessita' algoritmica: lettura e confronto delle righe in O(n) con n righe di `Bookmark` e `WordList`; delta con
  hash per riga (`BookmarkID` piu' `DateModified`); evitare ordinamenti inutili; `Bookmark` ha indici solo su
  `ContentID` e `VolumeID`.
- Efficienza di rete: un'unica connessione riusata, compressione gzip del payload, dimensione dei lotti, HTTP keep-alive.
- Tempo: debounce di 20 s fisso (si potrebbe abbreviare se la rotta e' gia' pronta all'evento); `quick_check` scorre
  tutto il database a ogni sync (eseguirlo solo se una query fallisce?); coerenza tra copia del file e del WAL
  (alternative: API di backup di SQLite, `VACUUM INTO`, apertura diretta in sola lettura).
- Dimensione del binario (9,6 MB, copiato in RAM a ogni avvio se la dimensione cambia): flag di compilazione di
  SQLite per togliere funzioni inutili, simboli di debug residui, `-Os`.
- Memoria e batteria: GOGC/GOMEMLIMIT, numero di processi lanciati per evento (shell piu' agente).
- Complessita' del codice: un solo binario, poche dipendenze, test automatici su database sintetici.
