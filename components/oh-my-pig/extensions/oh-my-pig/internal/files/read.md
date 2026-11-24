Use `read` for static web; browser only if needed.

Put line ranges in `path`, never a separate range/offset/limit parameter: `{"path":"notes.txt:450-455"}` reads lines 450-455; `{"path":"notes.txt:-5"}` reads the last 5. Include the selector on every ranged call; repeating a bare path does not advance through the file.

Path suffixes: :50 or :50- starts at line 50; :50-200 inclusive; :50+150 counts lines; :-60 last 60; commas join ranges (:5-16,960-973) or individual lines (:19,59). A single non-raw range also shows 1 line before and 3 after (e.g. :50-80 shows 49-83); non-raw reads (comma lists too) may add the open/close line of a cut block; only :raw stays exact. :raw verbatim without anchors/prefixes; combine :2-4:raw or :raw:2-4 for exactly those lines. Raster images (PNG, JPEG, GIF, WebP) need no selector.

Sources:
- Selected file: `[foo.ts#1A2B]` snapshot+lines. Copy `[FILENAME#TAG]` for anchored edits; NEVER invent tag.
- Directory: complete root; child listings cap at 12 (`… N more`), read child; page via :N-M/:-N.
- Images: decoded inline. URLs: reader text/markdown, :raw original HTML; bare host:port needs trailing slash.
