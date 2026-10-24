// devserver is the in-memory reference ingest server, for testing the agent on the device over the local network.
// It validates every request against the spec's schema and prints a summary (counts, never texts).
// Usage: go run ./cmd/devserver -addr :8765 -config-out /tmp/config.json
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
	addr := flag.String("addr", ":8765", "listen address")
	token := flag.String("token", "", "device token (empty: generate one)")
	host := flag.String("host", "", "IP or name of this computer as seen by the device (empty: first private IPv4)")
	out := flag.String("config-out", "", "if set, write the device config.json here")
	minInterval := flag.Int("min-interval", 0, "min_interval_s returned to the agent")
	dump := flag.String("dump", "", "if set, append every accepted request here (one JSON line each), "+
		"to check the real data formats; it contains your texts, keep it on your computer")
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
		fmt.Printf("config.json written to %s\n", *out)
	}
	fmt.Printf("config.json for the device:\n%s\n\nListening on %s, endpoint %s\n", cfg, *addr, contract.SyncPath)

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
		// Counts of live records, for a quick check after a sync.
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
	return "COMPUTER_IP"
}
