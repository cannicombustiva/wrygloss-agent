// devserver e' il server di ingest di riferimento, in memoria, per provare l'agente sul dispositivo in rete locale.
// Valida ogni richiesta contro lo schema della specifica e stampa un riepilogo (conteggi, mai testi).
// Uso: go run ./cmd/devserver -addr :8765 -config-out /tmp/config.json
package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/cannicombustiva/wrygloss-agent/internal/contract"
	"github.com/cannicombustiva/wrygloss-agent/internal/ingestref"
)

func main() {
	addr := flag.String("addr", ":8765", "indirizzo di ascolto")
	token := flag.String("token", "", "token del dispositivo (vuoto: ne genera uno)")
	host := flag.String("host", "", "IP o nome del computer visto dal dispositivo (vuoto: primo IPv4 non locale)")
	out := flag.String("config-out", "", "se impostato, scrive qui il config.json per il dispositivo")
	minInterval := flag.Int("min-interval", 0, "min_interval_s restituito all'agente")
	dump := flag.String("dump", "", "se impostato, aggiunge qui ogni richiesta accettata (una riga JSON ciascuna), "+
		"per controllare i formati reali dei dati; contiene i tuoi testi, resta sul tuo computer")
	flag.Parse()

	if *token == "" {
		b := make([]byte, 32)
		rand.Read(b)
		*token = "wgd_" + base64.RawURLEncoding.EncodeToString(b)
	}
	if *host == "" {
		*host = lanIP()
	}
	_, port, _ := net.SplitHostPort(*addr)
	cfg, _ := json.MarshalIndent(map[string]string{"server_url": "http://" + net.JoinHostPort(*host, port), "token": *token}, "", "  ")

	srv, err := ingestref.New()
	if err != nil {
		log.Fatal(err)
	}
	srv.MinIntervalS = *minInterval
	srv.LatestAgent = "0.2.0"
	srv.Log = func(format string, a ...any) { log.Printf(format, a...) }
	dev := srv.AddDevice(*token)

	if *out != "" {
		if err := os.WriteFile(*out, append(cfg, '\n'), 0600); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("config.json scritto in %s\n", *out)
	}
	fmt.Printf("config.json per il dispositivo:\n%s\n\nIn ascolto su %s, endpoint %s\n", cfg, *addr, contract.SyncPath)

	var handler http.Handler = srv
	if *dump != "" {
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := len(dev.Requests)
			srv.ServeHTTP(w, r)
			f, err := os.OpenFile(*dump, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
			if err != nil {
				log.Printf("dump: %v", err)
				return
			}
			defer f.Close()
			enc := json.NewEncoder(f)
			for _, req := range dev.Requests[n:] {
				enc.Encode(req)
			}
		})
	}
	http.Handle(contract.SyncPath, handler)
	http.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		// Conteggi dei record vivi, per controllare a colpo d'occhio dopo un sync.
		json.NewEncoder(w).Encode(map[string]int{
			"annotations": dev.Live(contract.Annotations), "words": dev.Live(contract.Words),
			"books": dev.Live(contract.Books), "requests": len(dev.Requests),
		})
	})
	log.Fatal(http.ListenAndServe(*addr, nil))
}

func lanIP() string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil && n.IP.IsPrivate() {
			return n.IP.String()
		}
	}
	return "IP_DEL_COMPUTER"
}
