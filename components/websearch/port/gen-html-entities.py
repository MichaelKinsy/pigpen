#!/usr/bin/env python3
"""Generates the tables extensions/websearch/tables/ld-entities.gz and ld-legacy-entities.gz: the HTML named character
references (the table of the HTML standard, as in Python's html.entities.html5) that the port of htmlparser2's entity
decoding needs. The format is that of port/gen-tables-util.mjs: records separated by U+0001, fields by U+0000, gzipped.

    python3 port/gen-html-entities.py extensions/websearch/tables
"""
import gzip
import os
import sys
from html.entities import html5

names = sorted(k for k in html5 if k.endswith(";"))
legacy = sorted(k for k in html5 if not k.endswith(";"))
for k in legacy:
    assert k + ";" in html5 and html5[k] == html5[k + ";"]


def write(directory, name, rows):
    records = []
    for row in rows:
        for field in row:
            assert "\x00" not in field and "\x01" not in field
        records.append("\x00".join(row))
    data = "\x01".join(records).encode("utf-8")
    # mtime 0 and no file name: the same bytes on every run
    with open(os.path.join(directory, name + ".gz"), "wb") as f:
        f.write(gzip.compress(data, compresslevel=9, mtime=0))


directory = sys.argv[1]
os.makedirs(directory, exist_ok=True)
# every named reference with its semicolon removed, and its characters
write(directory, "ld-entities", [[k[:-1], html5[k]] for k in names])
# the names that also decode without a semicolon
write(directory, "ld-legacy-entities", [[k] for k in legacy])
