-- Synthetic database with the real Kobo schema (subset) and the counts of the October 24 test:
-- 22 highlights, 2 notes, 8 dogears, 62 words. CANARY_* values must NEVER appear in a payload or a log.
-- Used by tools/make_test_db.py and the Go tests. The notes are in Italian on purpose, like the user's own notes.
CREATE TABLE Bookmark ( BookmarkID TEXT NOT NULL, VolumeID TEXT NOT NULL, ContentID TEXT NOT NULL, StartContainerPath TEXT NOT NULL, StartContainerChildIndex INTEGER NOT NULL, StartOffset INTEGER NOT NULL, EndContainerPath TEXT NOT NULL, EndContainerChildIndex INTEGER NOT NULL, EndOffset INTEGER NOT NULL, Text TEXT, Annotation TEXT, ExtraAnnotationData BLOB, DateCreated TEXT, ChapterProgress REAL NOT NULL DEFAULT 0, Hidden BOOL NOT NULL DEFAULT 0, Version TEXT, DateModified TEXT, Creator TEXT, UUID TEXT, UserID TEXT, SyncTime TEXT, Published BIT default false, ContextString TEXT, Type TEXT, Color INTEGER DEFAULT 0, PRIMARY KEY (BookmarkID) );
CREATE INDEX bookmark_content ON Bookmark (ContentID);
CREATE INDEX bookmark_volume ON Bookmark (VolumeID);
CREATE TABLE WordList( Text TEXT, VolumeId TEXT, DictSuffix TEXT, DateCreated TEXT, PRIMARY KEY(Text));
CREATE TABLE content ( ContentID TEXT NOT NULL, ContentType TEXT NOT NULL, MimeType TEXT NOT NULL DEFAULT '', BookID TEXT, BookTitle TEXT, Title TEXT, Attribution TEXT, Description TEXT, DateCreated TEXT, ChapterIDBookmarked TEXT, Language TEXT, Publisher TEXT, ISBN TEXT, ___FileSize INTEGER, ___PercentRead INTEGER, ReadStatus INTEGER, ___UserID TEXT NOT NULL, DateLastRead TEXT, TimeSpentReading INTEGER, PRIMARY KEY (ContentID) );
CREATE TABLE user (UserID TEXT, UserEmail TEXT, AuthToken TEXT);
CREATE TABLE AnalyticsEvents (Id TEXT, Type TEXT, Attributes TEXT);
INSERT INTO user VALUES ('CANARY_USER_ID', 'canary-email@example.invalid', 'CANARY_AUTH_TOKEN');
INSERT INTO AnalyticsEvents VALUES ('a1', 'CANARY_ANALYTICS', 'CANARY_ANALYTICS_ATTR');

INSERT INTO content (ContentID, ContentType, Title, Attribution, Description, ChapterIDBookmarked, Language, Publisher, ISBN, ___PercentRead, ___UserID) VALUES
 ('file:///mnt/onboard/Books/Novel.epub', '6', 'A Novel', 'Jane Doe', 'CANARY_DESCRIPTION', 'OEBPS/ch3.xhtml#point(/1/4/14/1:0)', 'en-US', NULL, NULL, 37, 'CANARY_CONTENT_USERID'),
 ('file:///mnt/onboard/Books/Phrasebook.epub', '6', 'Phrasebook', NULL, NULL, NULL, 'en', NULL, NULL, 5, 'CANARY_CONTENT_USERID'),
 ('file:///mnt/onboard/Books/Unreferenced.epub', '6', 'CANARY_UNREFERENCED_TITLE', 'CANARY_UNREFERENCED_AUTHOR', NULL, NULL, 'it', NULL, NULL, 0, 'CANARY_CONTENT_USERID'),
 ('file:///mnt/onboard/Books/Novel.epub#(3)OEBPS/ch3.xhtml', '9', 'Chapter Three', NULL, NULL, NULL, NULL, NULL, NULL, NULL, 'CANARY_CONTENT_USERID'),
 ('file:///mnt/onboard/Books/Phrasebook.epub#(5)OEBPS/Text/c01.xhtml', '9', 'At the market', NULL, NULL, NULL, NULL, NULL, NULL, NULL, 'CANARY_CONTENT_USERID');

WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM n WHERE i < 21)
INSERT INTO Bookmark (BookmarkID, VolumeID, ContentID, StartContainerPath, StartContainerChildIndex, StartOffset, EndContainerPath, EndContainerChildIndex, EndOffset, Text, Annotation, ExtraAnnotationData, DateCreated, ChapterProgress, Hidden, Creator, UserID, Type, Color)
SELECT 'h' || i, 'file:///mnt/onboard/Books/Phrasebook.epub', 'file:///mnt/onboard/Books/Phrasebook.epub#(5)OEBPS/Text/c01.xhtml',
       'OEBPS/Text/c01.xhtml#point(/1/4/2/' || (500 + i) || ':1)', 0, 0, 'OEBPS/Text/c01.xhtml#point(/1/4/2/' || (500 + i) || ':30)', 0, 0,
       'How much does this cost? ' || i || char(10) || '¿Cuánto cuesta esto? ' || i, NULL, CAST('CANARY_EXTRA' AS BLOB),
       '2026-09-19T15:05:59.819', i / 22.0, CASE WHEN i = 21 THEN 'true' ELSE 'false' END, 'CANARY_CREATOR', 'CANARY_BOOKMARK_USERID', 'highlight', i % 4
FROM n;

INSERT INTO Bookmark (BookmarkID, VolumeID, ContentID, StartContainerPath, StartContainerChildIndex, StartOffset, EndContainerPath, EndContainerChildIndex, EndOffset, Text, Annotation, DateCreated, DateModified, ChapterProgress, UserID, Type) VALUES
 ('n0', 'file:///mnt/onboard/Books/Novel.epub', 'file:///mnt/onboard/Books/Novel.epub#(3)OEBPS/ch3.xhtml', 'OEBPS/ch3.xhtml#point(/1/4/12/3:7)', 0, 0, 'OEBPS/ch3.xhtml#point(/1/4/12/3:29)', 0, 0, 'a snazzy little number', 'elegante, vistoso', '2026-09-20T10:00:00.000', '2026-09-20T10:01:12.000', 0.1, 'CANARY_BOOKMARK_USERID', 'note'),
 ('n1', 'file:///mnt/onboard/Books/Novel.epub', 'file:///mnt/onboard/Books/Novel.epub#(9)OEBPS/ch9.xhtml', 'OEBPS/ch9.xhtml#point(/1/4/2/1:0)', 0, 0, 'OEBPS/ch9.xhtml#point(/1/4/2/1:9)', 0, 0, 'kick the bucket', 'morire (idiomatico)', '2026-09-20T11:00:00.000', NULL, 0.9, 'CANARY_BOOKMARK_USERID', 'note');

WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM n WHERE i < 7)
INSERT INTO Bookmark (BookmarkID, VolumeID, ContentID, StartContainerPath, StartContainerChildIndex, StartOffset, EndContainerPath, EndContainerChildIndex, EndOffset, DateCreated, UserID, Type)
SELECT 'd' || i, 'file:///mnt/onboard/Books/Novel.epub', 'file:///mnt/onboard/Books/Novel.epub#(3)OEBPS/ch3.xhtml', 'OEBPS/ch3.xhtml#point(/1/2/' || i || ':0)', 0, 0, 'OEBPS/ch3.xhtml#point(/1/2/' || i || ':0)', 0, 0, '2026-09-21T10:00:00.000', 'CANARY_BOOKMARK_USERID', 'dogear'
FROM n;

-- 62 words: 59 generated, plus two forms of the same word (merged server-side) and one with the es-en dictionary.
WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM n WHERE i < 58)
INSERT INTO WordList SELECT CASE WHEN i % 5 = 0 THEN 'word' || i || ',' ELSE 'word' || i END, 'file:///mnt/onboard/Books/Novel.epub', '-en', '2026-10-01T00:00:00Z' FROM n;
INSERT INTO WordList VALUES ('snazzy,', 'file:///mnt/onboard/Books/Novel.epub', '-en', '2026-10-02T08:00:00Z');
INSERT INTO WordList VALUES ('Snazzy', 'file:///mnt/onboard/Books/Novel.epub', '-en', '2026-10-01T09:00:00Z');
INSERT INTO WordList VALUES ('cuesta', 'file:///mnt/onboard/Books/Phrasebook.epub', '-es-en', '2026-10-03T12:00:00Z');
