#!/usr/bin/env python3
"""Crea un KoboReader.sqlite sintetico (schema reale, dati finti, valori CANARY_* da non inviare mai)
e il file 'version' accanto, partendo da testdata/kobo.sql e testdata/version.
Uso: python3 tools/make_test_db.py /tmp/wgtest/KoboReader.sqlite"""
import os, shutil, sqlite3, sys
here = os.path.dirname(os.path.abspath(__file__))
path = sys.argv[1] if len(sys.argv) > 1 else "KoboReader.sqlite"
os.makedirs(os.path.dirname(os.path.abspath(path)), exist_ok=True)
if os.path.exists(path):
    os.remove(path)
c = sqlite3.connect(path)
with open(os.path.join(here, "..", "testdata", "kobo.sql"), encoding="utf-8") as f:
    c.executescript(f.read())
c.execute("PRAGMA journal_mode=WAL")
c.commit()
c.close()
shutil.copy(os.path.join(here, "..", "testdata", "version"), os.path.join(os.path.dirname(os.path.abspath(path)), "version"))
print("creato", path)
