# Come compilare l'agente per il Kobo (Clara BW, armv7l)

L'agente e' un binario Go statico per Linux ARMv7 con SQLite incorporato (mattn/go-sqlite3, richiede CGO).
Serve quindi un compilatore C che produca codice ARM con libc musl: uso `zig cc` come cross-compilatore.

## Verificato (Linux x86_64, Ubuntu 24.04, container senza accesso a proxy.golang.org)
```sh
apt-get install -y golang-go qemu-user git ca-certificates   # Go 1.22.2, qemu-arm per i test
pip install --break-system-packages ziglang                  # fornisce 'python3 -m ziglang cc'

export GOPROXY=direct GONOSUMDB='*' GOFLAGS=-mod=mod GOPRIVATE='github.com/*' GOTOOLCHAIN=local
# (le variabili GOPROXY/GONOSUMDB servono solo se proxy.golang.org non e' raggiungibile)

CGO_ENABLED=1 CC="$PWD/zigcc.sh" GOOS=linux GOARCH=arm GOARM=7 \
  go build -trimpath -tags 'netgo osusergo sqlite_omit_load_extension' \
  -ldflags '-s -w -extldflags "-static"' -o wrygloss-agent .
```
- Tempo di compilazione: circa 3 minuti la prima volta (quasi tutto e' la compilazione di SQLite), 56 s con la cache.
- Ricompilato dal sorgente: stesso SHA-256 del binario provato sul Kobo (compilazione riproducibile):
  dfbf8ac07e988e273cc0cb32b4d127f0006b6e5bbabf9eb222df1181aa2694f7
- Risultato: ELF 32-bit ARM EABI5, linkato staticamente, circa 9,6 MB.
- `file` segnala "not stripped" nonostante `-s -w`: probabilmente simboli C aggiunti dal linker di zig (da indagare).
- `zigcc.sh` e' un wrapper di una riga: `exec python3 -m ziglang cc -target arm-linux-musleabihf "$@"`.

## Non verificato: macOS
Presumibilmente `brew install go zig`, poi lo stesso comando con `CC="zig cc -target arm-linux-musleabihf"`
tramite un wrapper come zigcc.sh. Non l'ho provato. qemu-user non e' disponibile su macOS: i test dell'agente
si fanno sul Kobo oppure in un container Linux.

## Test senza Kobo (emulazione ARM in Linux, kernel di emulazione diverso da quello del Kobo)
```sh
python3 tools/make_test_db.py /tmp/wgtest/KoboReader.sqlite
python3 tools/test_server.py 8765 &
cat > /tmp/wgtest/config.json <<'CFG'
{"endpoint":"http://127.0.0.1:8765/ingest","token":"test-token","db_path":"/tmp/wgtest/KoboReader.sqlite",
 "quiet_seconds":2,"min_interval_seconds":0,"route_wait_seconds":10,"tls_test_url":"https://github.com"}
CFG
qemu-arm ./wrygloss-agent --dir /tmp/wgtest && cat /tmp/wgtest/agent.log
```

## Distribuzione sul Kobo (provata sul Clara BW, firmware 4.46)
- `deploy/make_koboroot.sh` crea `KoboRoot.tgz` con la sola regola udev `91-wrygloss-test.rules`.
- `deploy/install.sh` (macOS) copia agente, `run.sh` e `config.json` in `.adds/wrygloss-test/` e il `KoboRoot.tgz`
  in `.kobo/`. Il Kobo, scollegato in modo sicuro, si riavvia, installa la regola e cancella il `KoboRoot.tgz`.
- `deploy/run.sh` e' lanciato da udev a ogni attivazione di wlan0: copia il binario in /tmp (la partizione utente e' FAT)
  e avvia l'agente in background.
- Disinstallazione: file vuoto `UNINSTALL` in `.adds/wrygloss-test/` e riattivare il wifi.
