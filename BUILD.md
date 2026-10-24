# Compilare e provare l'agente

L'agente e' un binario Go statico per Linux ARMv7 con SQLite incorporato (mattn/go-sqlite3, richiede CGO): serve un
compilatore C che produca codice ARM con libc musl. Si usa `zig cc`.

## Verificato: macOS (Apple Silicon), Go 1.27.3, zig 0.17.0

```sh
brew install go zig
./build.sh            # build/wrygloss-agent e build/VERSION
```

- Circa 1 minuto la prima volta (quasi tutto e' SQLite), pochi secondi con la cache.
- Risultato: ELF 32-bit ARM EABI5, statico, stripped, circa 8,6 MB. Compilazione riproducibile: due build
  successive producono lo stesso binario.
- `build/VERSION` contiene versione e prime 12 cifre dello SHA-256 del binario: `run.sh` ricopia il binario in `/tmp`
  quando cambia.
- Su Linux vale lo stesso comando; `zigcc.sh` usa `zig` se installato, altrimenti `python3 -m ziglang`
  (`pip install ziglang`). Il `go.mod` richiede Go 1.26 o successivo.

## Test

```sh
go test -race ./...
```

- Logica dell'agente contro il server di riferimento in memoria (`internal/ingestref`), che valida ogni richiesta con
  il JSON Schema della specifica: sync incrementale, cancellazioni e ripristini, perdita dei dati sul server, token
  revocato, riprovi su 503, lotti, 413, tabelle e colonne mancanti, righe presenti solo nel WAL, evento doppio,
  assenza di rotta, nessun valore canarino in payload, log e stato.
- Esempi validi e non validi della specifica contro lo schema; vettori di pulizia delle parole.
- Codici di errore del server di riferimento (401, 410, 413 compresa una gzip bomb, 415, 422, 429, rifiuto per record).

## Prova del binario ARM in emulazione (Docker Desktop su Mac)

Alpine `linux/arm/v7` ha musl e busybox come il Kobo; il kernel e' quello dell'emulazione, non il 4.9 del Kobo.

```sh
./build.sh && sh deploy/package.sh
W=$(mktemp -d) && (cd "$W" && unzip -q "$OLDPWD/build/wrygloss-agent-0.2.0.zip")
python3 tools/make_test_db.py "$W/.kobo/KoboReader.sqlite"
go run ./cmd/devserver -addr 0.0.0.0:8799 -host host.docker.internal -config-out "$W/.adds/wrygloss/config.json" &
docker run --rm --platform linux/arm/v7 -v "$W:/mnt/onboard" alpine:3.20 sh -c '
  mkdir -p /etc/udev/rules.d && touch /etc/udev/rules.d/91-wrygloss.rules
  sh /mnt/onboard/.adds/wrygloss/run.sh; sleep 8      # evento wifi
  sh /mnt/onboard/.adds/wrygloss/run.sh; sleep 3      # evento doppio: SKIP
  touch /mnt/onboard/.adds/wrygloss/UNINSTALL; sh /mnt/onboard/.adds/wrygloss/run.sh; ls /etc/udev/rules.d'
cat "$W/.adds/wrygloss/agent.log"
```

Esito del 24 ottobre 2026: sync completo di 32 annotazioni, 62 parole e 2 libri in 193 ms, evento doppio saltato,
disinstallazione che rimuove regola e binario. Con `/etc/ssl` cancellata, l'handshake TLS verso un sito pubblico
riesce con le sole radici incorporate.

## Prova sul dispositivo

Vedi [KOBO-TEST.md](KOBO-TEST.md).
