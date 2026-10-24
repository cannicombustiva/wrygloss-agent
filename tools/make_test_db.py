#!/usr/bin/env python3
"""Crea un KoboReader.sqlite sintetico con lo schema reale (Bookmark, WordList) e una tabella user
con dati finti, per provare l'agente senza toccare dati personali.
Uso: python3 make_test_db.py /tmp/wgtest/KoboReader.sqlite"""
import sqlite3, sys
path = sys.argv[1] if len(sys.argv) > 1 else "KoboReader.sqlite"
c = sqlite3.connect(path)
c.executescript("""
CREATE TABLE Bookmark ( BookmarkID TEXT NOT NULL, VolumeID TEXT NOT NULL, ContentID TEXT NOT NULL, StartContainerPath TEXT NOT NULL, StartContainerChildIndex INTEGER NOT NULL, StartOffset INTEGER NOT NULL, EndContainerPath TEXT NOT NULL, EndContainerChildIndex INTEGER NOT NULL, EndOffset INTEGER NOT NULL, Text TEXT, Annotation TEXT, ExtraAnnotationData BLOB, DateCreated TEXT, ChapterProgress REAL NOT NULL DEFAULT 0, Hidden BOOL NOT NULL DEFAULT 0, Version TEXT, DateModified TEXT, Creator TEXT, UUID TEXT, UserID TEXT, SyncTime TEXT, Published BIT default false, ContextString TEXT, Type TEXT, Color INTEGER DEFAULT 0, PRIMARY KEY (BookmarkID) );
CREATE INDEX bookmark_content ON Bookmark (ContentID);
CREATE INDEX bookmark_volume ON Bookmark (VolumeID);
CREATE TABLE WordList( Text TEXT, VolumeId TEXT, DictSuffix TEXT, DateCreated TEXT, PRIMARY KEY(Text));
CREATE TABLE user (UserID TEXT, UserEmail TEXT, AuthToken TEXT);
INSERT INTO user VALUES ('u1','secret@example.org','SECRETTOKEN');
""")
ins = "INSERT INTO Bookmark (BookmarkID,VolumeID,ContentID,StartContainerPath,StartContainerChildIndex,StartOffset,EndContainerPath,EndContainerChildIndex,EndOffset,Text,Annotation,Type,DateCreated) VALUES (?,?,?,?,0,0,?,0,0,?,?,?,?)"
for i in range(22): c.execute(ins, (f"h{i}","file:///mnt/onboard/book.epub","file:///mnt/onboard/book.epub#(5)OEBPS/Text/c01.xml","OEBPS/Text/c01.xml#point(/1/4/2/%d:1)"%i,"p",f"frase evidenziata {i}",None,"highlight","2026-09-19T15:05:59.819"))
for i in range(2): c.execute(ins, (f"n{i}","file:///mnt/onboard/book.epub","c","p","p",f"testo {i}",f"nota {i}","note","2026-09-20T10:00:00.000"))
for i in range(7): c.execute(ins, (f"d{i}","file:///mnt/onboard/book.epub","c","p","p",None,None,"dogear","2026-09-21T10:00:00.000"))
for i in range(61): c.execute("INSERT INTO WordList VALUES (?,?,?,?)", (f"word{i}," if i % 5 == 0 else f"word{i}","file:///mnt/onboard/book.epub","-en","2026-10-01T00:00:00Z"))
c.commit(); print("creato", path)
