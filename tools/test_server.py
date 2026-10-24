#!/usr/bin/env python3
"""Server di prova per Wrygloss: stampa i JSON ricevuti dal Kobo. Solo libreria standard.
Uso: python3 test_server.py [porta]   (default 8765)"""
import json, sys, time
from http.server import BaseHTTPRequestHandler, HTTPServer

class H(BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(n)
        auth = self.headers.get("Authorization", "")
        print(f"\n[{time.strftime('%H:%M:%S')}] POST {self.path} da {self.client_address[0]} ({n} byte) auth={'ok' if auth else 'assente'}")
        try:
            print(json.dumps(json.loads(body), indent=2, ensure_ascii=False))
        except Exception as e:
            print("corpo non JSON:", e)
        self.send_response(200); self.send_header("Content-Type", "application/json"); self.end_headers()
        self.wfile.write(b'{"ok":true}')
    def log_message(self, *a): pass

port = int(sys.argv[1]) if len(sys.argv) > 1 else 8765
print(f"In ascolto su 0.0.0.0:{port} (Ctrl+C per fermare)")
HTTPServer(("0.0.0.0", port), H).serve_forever()
