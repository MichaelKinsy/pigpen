#!/usr/bin/env python3
"""Drive an interactive TUI program in a plain PTY (no Tern) and capture its VT screen.

Uses only Python 3 stdlib (pty, termios, fcntl, select, subprocess) plus a
self-contained VT100/ANSI screen emulator (`VTScreen`):
  - supports CSI cursor movement, erase display/line, scroll regions, insert/delete
    lines, `?7h`/`?7l` autowrap mode, separate main/alt saved cursors (`ESC 7/8`
    vs `CSI s/u`), `ESC c` full reset, alternate screen buffer `?1049h/?1049l`,
    CR/LF/BS/TAB, UTF-8 incremental decode, and scrollback history capture;
  - attaches the child's slave PTY as its controlling terminal (`TIOCSCTTY`) so
    SIGWINCH and `/dev/tty` work;
  - drives steps by polling the loopback LLM server's `/health` turn cursor
    instead of brittle fixed sleeps;
  - wraps the child in `sandbox-exec` (macOS loopback-only) when available,
    propagates `ORACLE_RUN_TAG`, handles SIGTERM/SIGINT/SIGHUP, and sweeps any
    tagged descendant processes (like `__omp_worker_daemon`) on exit.
"""

from __future__ import annotations

import argparse
import codecs
import difflib
import fcntl
import json
import os
import pty
import re
import select
import signal
import struct
import subprocess
import sys
import termios
import time
import unicodedata
import urllib.request
from typing import Any
from pathlib import Path

sys.dont_write_bytecode = True

SCRIPT_DIR = Path(__file__).resolve().parent
if str(SCRIPT_DIR) not in sys.path:
    sys.path.insert(0, str(SCRIPT_DIR))

from allowlist_refs import resolve_allowlist_entries  # noqa: E402
from harness_isolation import (  # noqa: E402
    reap_tagged_processes,
    sandbox_cmd,
)


# Cell SGR tuple: `(fg, bg, attrs)` where:
#   - `fg`, `bg` are `None`, `"rgb:R;G;B"`, `"256:N"`, or `"ansi:N"`
#   - `attrs` is a sorted tuple of active semantic SGR attributes from
#     `{"bold", "faint", "italic", "underline", "inverse", "strike"}`.
CellSGR = tuple[str | None, str | None, tuple[str, ...]]
DEFAULT_CELL_SGR: CellSGR = (None, None, ())


def _apply_sgr_params(cur: CellSGR, params: list[int]) -> CellSGR:
    """Apply a CSI `m` parameter list to `(fg, bg, attrs)`."""
    fg, bg, attrs = cur
    if not params:
        return DEFAULT_CELL_SGR
    aset = set(attrs)
    i = 0
    n = len(params)
    while i < n:
        c = params[i]
        if c == 0:
            fg, bg = None, None
            aset.clear()
        elif c == 1:
            aset.add("bold")
        elif c == 2:
            aset.add("faint")
        elif c == 3:
            aset.add("italic")
        elif c == 4:
            aset.add("underline")
        elif c == 7:
            aset.add("inverse")
        elif c == 9:
            aset.add("strike")
        elif c == 22:
            aset.discard("bold")
            aset.discard("faint")
        elif c == 23:
            aset.discard("italic")
        elif c == 24:
            aset.discard("underline")
        elif c == 27:
            aset.discard("inverse")
        elif c == 29:
            aset.discard("strike")
        elif c == 39:
            fg = None
        elif c == 49:
            bg = None
        elif (30 <= c <= 37) or (90 <= c <= 97):
            fg = f"ansi:{c}"
        elif (40 <= c <= 47) or (100 <= c <= 107):
            bg = f"ansi:{c}"
        elif c in (38, 48) and i + 1 < n:
            mode = params[i + 1]
            if mode == 2 and i + 4 < n:
                val = f"rgb:{params[i + 2]};{params[i + 3]};{params[i + 4]}"
                if c == 38:
                    fg = val
                else:
                    bg = val
                i += 5
                continue
            if mode == 5 and i + 2 < n:
                val = f"256:{params[i + 2]}"
                if c == 38:
                    fg = val
                else:
                    bg = val
                i += 3
                continue
        i += 1
    return (fg, bg, tuple(sorted(aset)))


def _encode_styled_row(chars: list[str], styles: list[CellSGR]) -> list[Any]:
    """Compactly encode one screen/scrollback row as `[text, runs]` where `runs` is `[[col, len, fg, bg, attrs], ...]`."""
    end = len(chars)
    while end > 0 and chars[end - 1] == " " and styles[end - 1][1] is None and not styles[end - 1][2]:
        end -= 1
    text = "".join(chars[:end]).rstrip()
    runs: list[list[Any]] = []
    col = 0
    while col < end:
        sgr = styles[col]
        if sgr == DEFAULT_CELL_SGR:
            col += 1
            continue
        start = col
        while col < end and styles[col] == sgr:
            col += 1
        fg, bg, attrs = sgr
        runs.append([start, col - start, fg, bg, list(attrs)])
    return [text, runs]


def _decode_styled_row(entry: list[Any]) -> list[tuple[str, CellSGR]]:
    """Decode `[text, [[col, len, fg, bg, attrs?], ...]]` back into per-cell `(char, (fg, bg, attrs))`."""
    text = entry[0] if entry else ""
    runs = entry[1] if len(entry) > 1 else []
    max_end = len(text)
    for r in runs:
        max_end = max(max_end, int(r[0]) + int(r[1]))
    cells: list[tuple[str, CellSGR]] = [
        (text[i] if i < len(text) else " ", DEFAULT_CELL_SGR) for i in range(max_end)
    ]
    for r in runs:
        col, length, fg, bg = int(r[0]), int(r[1]), r[2], r[3]
        attrs = tuple(sorted(r[4])) if len(r) > 4 and isinstance(r[4], list) else ()
        sgr: CellSGR = (fg, bg, attrs)
        for i in range(col, min(max_end, col + length)):
            cells[i] = (cells[i][0], sgr)
    return cells
class VTScreen:
    """Deterministic VT100/xterm screen model with scrollback & per-cell SGR capture (rows x cols)."""

    def __init__(self, rows: int = 40, cols: int = 120) -> None:
        self.rows = rows
        self.cols = cols
        self._sgr: tuple[str | None, str | None] = DEFAULT_CELL_SGR
        self._main = self._blank_grid()
        self._main_sgr = self._blank_sgr_grid()
        self._alt = self._blank_grid()
        self._alt_sgr = self._blank_sgr_grid()
        self._in_alt = False
        self.grid = self._main
        self.sgr_grid = self._main_sgr
        self.scrollback: list[str] = []
        self.scrollback_styled: list[list[Any]] = []
        self.full_screen_clears = 0
        self.scrollback_clears = 0
        self.r = 0
        self.c = 0
        self._saved_esc_main = (0, 0)
        self._saved_esc_alt = (0, 0)
        self._saved_csi_main = (0, 0)
        self._saved_csi_alt = (0, 0)
        self.scroll_top = 0
        self.scroll_bottom = rows - 1
        self.autowrap = True
        self._state = "text"
        self._csi_buf = ""
        self._osc_esc = False
        self._wrap_pending = False

    def _blank_grid(self) -> list[list[str]]:
        return [[" "] * self.cols for _ in range(self.rows)]

    def _blank_sgr_grid(self) -> list[list[tuple[str | None, str | None]]]:
        return [[DEFAULT_CELL_SGR] * self.cols for _ in range(self.rows)]

    def resize(self, new_rows: int, new_cols: int) -> None:
        """Resize both main and alt buffers cleanly so ?1049l never restores a stale pre-resize grid."""
        self.rows = new_rows
        self.cols = new_cols
        self.scroll_top = 0
        self.scroll_bottom = new_rows - 1
        self._main = self._blank_grid()
        self._main_sgr = self._blank_sgr_grid()
        self._alt = self._blank_grid()
        self._alt_sgr = self._blank_sgr_grid()
        self.grid = self._alt if self._in_alt else self._main
        self.sgr_grid = self._alt_sgr if self._in_alt else self._main_sgr
        self.r = 0
        self.c = 0
        self._wrap_pending = False

    def _clamp(self) -> None:
        self.r = max(0, min(self.rows - 1, self.r))
        self.c = max(0, min(self.cols - 1, self.c))

    def _scroll_up(self, n: int = 1) -> None:
        top, bot = self.scroll_top, self.scroll_bottom
        n = min(n, bot - top + 1)
        for _ in range(n):
            removed = self.grid.pop(top)
            removed_sgr = self.sgr_grid.pop(top)
            if not self._in_alt and top == 0 and bot == self.rows - 1:
                self.scrollback.append("".join(removed).rstrip())
                self.scrollback_styled.append(_encode_styled_row(removed, removed_sgr))
            self.grid.insert(bot, [" "] * self.cols)
            self.sgr_grid.insert(bot, [DEFAULT_CELL_SGR] * self.cols)

    def _scroll_down(self, n: int = 1) -> None:
        top, bot = self.scroll_top, self.scroll_bottom
        n = min(n, bot - top + 1)
        for _ in range(n):
            del self.grid[bot]
            del self.sgr_grid[bot]
            self.grid.insert(top, [" "] * self.cols)
            self.sgr_grid.insert(top, [DEFAULT_CELL_SGR] * self.cols)

    def _newline(self) -> None:
        self._wrap_pending = False
        if self.r == self.scroll_bottom:
            self._scroll_up(1)
        elif self.r < self.rows - 1:
            self.r += 1

    def _char_width(self, ch: str) -> int:
        if unicodedata.combining(ch) or ch in ("\u200d", "\ufe0e", "\ufe0f"):
            return 0
        if unicodedata.east_asian_width(ch) in ("W", "F"):
            return 2
        return 1

    def _put_char(self, ch: str) -> None:
        w = self._char_width(ch)
        if w == 0:
            return
        if self._wrap_pending or self.c + w > self.cols:
            if self.autowrap:
                self.c = 0
                self._newline()
            else:
                self.c = max(0, self.cols - w)
                self._wrap_pending = False
        self.grid[self.r][self.c] = ch
        self.sgr_grid[self.r][self.c] = self._sgr
        if w == 2 and self.c + 1 < self.cols:
            self.grid[self.r][self.c + 1] = ""
            self.sgr_grid[self.r][self.c + 1] = self._sgr
        self.c += w
        if self.c >= self.cols:
            self.c = self.cols - 1
            if self.autowrap:
                self._wrap_pending = True

    def feed(self, text: str) -> None:
        for ch in text:
            if self._state == "text":
                if ch == "\x1b":
                    self._state = "esc"
                elif ch == "\r":
                    self.c = 0
                    self._wrap_pending = False
                elif ch == "\n":
                    self._newline()
                elif ch == "\b":
                    self._wrap_pending = False
                    if self.c > 0:
                        self.c -= 1
                elif ch == "\t":
                    self._wrap_pending = False
                    self.c = min(self.cols - 1, (self.c // 8 + 1) * 8)
                elif ch == "\x07" or ord(ch) < 0x20 or ord(ch) == 0x7F:
                    pass
                else:
                    self._put_char(ch)
            elif self._state == "esc":
                if ch == "[":
                    self._state = "csi"
                    self._csi_buf = ""
                elif ch == "]":
                    self._state = "osc"
                    self._osc_esc = False
                elif ch in ("P", "_", "^"):
                    self._state = "dcs"
                    self._osc_esc = False
                elif ch in ("(", ")", "*", "+"):
                    self._state = "charset"
                elif ch == "7":
                    if self._in_alt:
                        self._saved_esc_alt = (self.r, self.c)
                    else:
                        self._saved_esc_main = (self.r, self.c)
                    self._state = "text"
                elif ch == "8":
                    self.r, self.c = self._saved_esc_alt if self._in_alt else self._saved_esc_main
                    self._clamp()
                    self._state = "text"
                elif ch == "M":
                    if self.r == self.scroll_top:
                        self._scroll_down(1)
                    elif self.r > 0:
                        self.r -= 1
                    self._state = "text"
                elif ch == "D":
                    self._newline()
                    self._state = "text"
                elif ch == "E":
                    self.c = 0
                    self._newline()
                    self._state = "text"
                elif ch == "c":
                    self._in_alt = False
                    self._main = self._blank_grid()
                    self._main_sgr = self._blank_sgr_grid()
                    self._alt = self._blank_grid()
                    self._alt_sgr = self._blank_sgr_grid()
                    self.grid = self._main
                    self.sgr_grid = self._main_sgr
                    self._sgr = DEFAULT_CELL_SGR
                    self.scroll_top = 0
                    self.scroll_bottom = self.rows - 1
                    self.autowrap = True
                    self.r = 0
                    self.c = 0
                    self._wrap_pending = False
                    self._state = "text"
                else:
                    self._state = "text"
            elif self._state == "charset":
                self._state = "text"
            elif self._state in ("osc", "dcs"):
                if self._osc_esc:
                    self._osc_esc = False
                    if ch == "\\":
                        self._state = "text"
                elif ch == "\x07" and self._state == "osc":
                    self._state = "text"
                elif ch == "\x1b":
                    self._osc_esc = True
            elif self._state == "csi":
                if "\x40" <= ch <= "\x7e":
                    self._handle_csi(self._csi_buf, ch)
                    self._state = "text"
                else:
                    self._csi_buf += ch

    def _parse_params(self, buf: str) -> tuple[str, list[int]]:
        prefix = ""
        if buf and buf[0] in ("?", ">", "<", "=", "!"):
            prefix = buf[0]
            buf = buf[1:]
        if not buf:
            return prefix, []
        out: list[int] = []
        for part in buf.split(";"):
            sub = part.split(":")[0]
            try:
                out.append(int(sub) if sub else 0)
            except ValueError:
                out.append(0)
        return prefix, out

    def _handle_csi(self, buf: str, final: str) -> None:
        prefix, params = self._parse_params(buf)
        p0 = params[0] if params else 0
        p1 = params[1] if len(params) > 1 else 0

        if prefix == "?":
            if final in ("h", "l"):
                enable = final == "h"
                for code in params:
                    if code == 7:
                        self.autowrap = enable
                    elif code in (1049, 1047, 47):
                        if enable and not self._in_alt:
                            self._in_alt = True
                            self._alt = self._blank_grid()
                            self._alt_sgr = self._blank_sgr_grid()
                            self.grid = self._alt
                            self.sgr_grid = self._alt_sgr
                            self.r = 0
                            self.c = 0
                            self._wrap_pending = False
                        elif not enable and self._in_alt:
                            self._in_alt = False
                            self.grid = self._main
                            self.sgr_grid = self._main_sgr
                            self._wrap_pending = False
            return

        if final == "m" and not prefix:
            self._sgr = _apply_sgr_params(self._sgr, params)
            return

        if final in ("m", "n", "c", "t", "q", "u", "s"):
            if final == "s" and not prefix:
                if self._in_alt:
                    self._saved_csi_alt = (self.r, self.c)
                else:
                    self._saved_csi_main = (self.r, self.c)
            elif final == "u" and not prefix:
                self.r, self.c = self._saved_csi_alt if self._in_alt else self._saved_csi_main
                self._clamp()
            return

        self._wrap_pending = False
        if final == "A":
            self.r = max(self.scroll_top, self.r - max(1, p0))
        elif final == "B":
            self.r = min(self.scroll_bottom, self.r + max(1, p0))
        elif final == "C":
            self.c = min(self.cols - 1, self.c + max(1, p0))
        elif final == "D":
            self.c = max(0, self.c - max(1, p0))
        elif final == "E":
            self.r = min(self.rows - 1, self.r + max(1, p0))
            self.c = 0
        elif final == "F":
            self.r = max(0, self.r - max(1, p0))
            self.c = 0
        elif final == "G":
            self.c = max(0, min(self.cols - 1, max(1, p0) - 1))
        elif final == "d":
            self.r = max(0, min(self.rows - 1, max(1, p0) - 1))
        elif final in ("H", "f"):
            self.r = max(0, min(self.rows - 1, max(1, p0) - 1))
            self.c = max(0, min(self.cols - 1, max(1, p1) - 1))
        elif final == "J":
            mode = p0
            if mode == 0:
                for col in range(self.c, self.cols):
                    self.grid[self.r][col] = " "
                    self.sgr_grid[self.r][col] = DEFAULT_CELL_SGR
                for row in range(self.r + 1, self.rows):
                    self.grid[row] = [" "] * self.cols
                    self.sgr_grid[row] = [DEFAULT_CELL_SGR] * self.cols
            elif mode == 1:
                for row in range(0, self.r):
                    self.grid[row] = [" "] * self.cols
                    self.sgr_grid[row] = [DEFAULT_CELL_SGR] * self.cols
                for col in range(0, self.c + 1):
                    self.grid[self.r][col] = " "
                    self.sgr_grid[self.r][col] = DEFAULT_CELL_SGR
            elif mode == 2:
                self.full_screen_clears += 1
                for row in range(self.rows):
                    self.grid[row] = [" "] * self.cols
                    self.sgr_grid[row] = [DEFAULT_CELL_SGR] * self.cols
            elif mode == 3:
                self.scrollback.clear()
                self.scrollback_styled.clear()
                self.scrollback_clears += 1
        elif final == "K":
            mode = p0
            if mode == 0:
                for col in range(self.c, self.cols):
                    self.grid[self.r][col] = " "
                    self.sgr_grid[self.r][col] = DEFAULT_CELL_SGR
            elif mode == 1:
                for col in range(0, self.c + 1):
                    self.grid[self.r][col] = " "
                    self.sgr_grid[self.r][col] = DEFAULT_CELL_SGR
            elif mode == 2:
                self.grid[self.r] = [" "] * self.cols
                self.sgr_grid[self.r] = [DEFAULT_CELL_SGR] * self.cols
        elif final == "L":
            n = max(1, p0)
            if self.scroll_top <= self.r <= self.scroll_bottom:
                for _ in range(min(n, self.scroll_bottom - self.r + 1)):
                    del self.grid[self.scroll_bottom]
                    del self.sgr_grid[self.scroll_bottom]
                    self.grid.insert(self.r, [" "] * self.cols)
                    self.sgr_grid.insert(self.r, [DEFAULT_CELL_SGR] * self.cols)
        elif final == "M":
            n = max(1, p0)
            if self.scroll_top <= self.r <= self.scroll_bottom:
                for _ in range(min(n, self.scroll_bottom - self.r + 1)):
                    del self.grid[self.r]
                    del self.sgr_grid[self.r]
                    self.grid.insert(self.scroll_bottom, [" "] * self.cols)
                    self.sgr_grid.insert(self.scroll_bottom, [DEFAULT_CELL_SGR] * self.cols)
        elif final == "S":
            self._scroll_up(max(1, p0))
        elif final == "T":
            self._scroll_down(max(1, p0))
        elif final == "P":
            n = max(1, p0)
            row = self.grid[self.r]
            srow = self.sgr_grid[self.r]
            self.grid[self.r] = row[:self.c] + row[self.c + n:] + [" "] * min(n, self.cols - self.c)
            self.sgr_grid[self.r] = srow[:self.c] + srow[self.c + n:] + [DEFAULT_CELL_SGR] * min(n, self.cols - self.c)
        elif final == "@":
            n = max(1, p0)
            row = self.grid[self.r]
            srow = self.sgr_grid[self.r]
            self.grid[self.r] = (row[:self.c] + [" "] * n + row[self.c:])[:self.cols]
            self.sgr_grid[self.r] = (srow[:self.c] + [DEFAULT_CELL_SGR] * n + srow[self.c:])[:self.cols]
        elif final == "X":
            n = max(1, p0)
            for col in range(self.c, min(self.cols, self.c + n)):
                self.grid[self.r][col] = " "
                self.sgr_grid[self.r][col] = DEFAULT_CELL_SGR
        elif final == "r":
            top = max(1, p0) - 1
            bot = (p1 if p1 else self.rows) - 1
            if 0 <= top < bot < self.rows:
                self.scroll_top = top
                self.scroll_bottom = bot
            else:
                self.scroll_top = 0
                self.scroll_bottom = self.rows - 1
            self.r = 0
            self.c = 0

    def render(self) -> str:
        lines = ["".join(row).rstrip() for row in self.grid]
        return "\n".join(lines) + "\n"

    def render_with_scrollback(self) -> str:
        lines = list(self.scrollback) + ["".join(row).rstrip() for row in self.grid]
        return "\n".join(lines) + "\n"

    def render_styled_rows(self) -> list[list[Any]]:
        return [_encode_styled_row(row, srow) for row, srow in zip(self.grid, self.sgr_grid)]

    def render_styled_with_scrollback(self) -> list[list[Any]]:
        return list(self.scrollback_styled) + self.render_styled_rows()

def _set_winsize(fd: int, rows: int, cols: int) -> None:
    winsize = struct.pack("HHHH", rows, cols, 0, 0)
    fcntl.ioctl(fd, termios.TIOCSWINSZ, winsize)


def _health_status(llm_origin: str | None) -> tuple[int, int, int] | None:
    """Return (next, completed, in_flight) from llm_server /health."""
    if not llm_origin:
        return None
    try:
        with urllib.request.urlopen(f"{llm_origin}/health", timeout=0.5) as resp:
            data = json.loads(resp.read().decode("utf-8"))
            nxt = int(data.get("next", 0))
            completed = int(data.get("completed", nxt))
            in_flight = int(data.get("in_flight", 0))
            return (nxt, completed, in_flight)
    except Exception:
        return None


def _health_next(llm_origin: str | None) -> int | None:
    st = _health_status(llm_origin)
    return st[0] if st is not None else None


def _drain(
    master_fd: int,
    screen: VTScreen,
    decoder: codecs.IncrementalDecoder,
    raw_chunks: list[bytes],
    query_buf: bytearray,
    duration: float,
) -> int:
    """Drain PTY output for up to `duration` seconds; return bytes read."""
    deadline = time.monotonic() + duration
    total_read = 0
    while True:
        rem = deadline - time.monotonic()
        if rem <= 0:
            break
        rlist, _, _ = select.select([master_fd], [], [], min(0.05, rem))
        if not rlist:
            continue
        try:
            data = os.read(master_fd, 65536)
        except OSError:
            break
        if not data:
            break
        total_read += len(data)
        raw_chunks.append(data)
        query_buf.extend(data)
        if b"\x1b[6n" in query_buf:
            try:
                os.write(master_fd, f"\x1b[{screen.r + 1};{screen.c + 1}R".encode("ascii"))
            except OSError:
                pass
            query_buf.clear()
        elif b"\x1b[c" in query_buf or b"\x1b[0c" in query_buf:
            try:
                os.write(master_fd, b"\x1b[?1;2c")
            except OSError:
                pass
            query_buf.clear()
        elif len(query_buf) > 16:
            del query_buf[:-16]
        screen.feed(decoder.decode(data, final=False))
    return total_read


CAPTURE_INCOMPLETE_EXIT_CODE = 2

_RE_READY_BAND = re.compile(r"^\s*π\s*>\s*.*128K─?\s*$")
_RE_READY_OMP_PROMPT = re.compile(r"^╰─(?:\s.*)?$")
_RE_READY_RULE = re.compile(r"^─{20,}$")


def _screen_has_prompt_ready(screen_text: str, *, side: str | None = None) -> bool:
    """Return True when the active status band AND editor/prompt marker are visibly drawn on screen."""
    lines = screen_text.splitlines()
    band_idx: int | None = None
    for i in range(len(lines) - 1, -1, -1):
        if _RE_READY_BAND.match(lines[i]):
            band_idx = i
            break
    if band_idx is None:
        return False
    tail = [l for l in lines[band_idx + 1 :] if l.strip()]
    if side == "omp":
        return any(_RE_READY_OMP_PROMPT.match(l) for l in tail)
    if side == "pig":
        return sum(1 for l in tail if _RE_READY_RULE.match(l.strip())) >= 2
    # Unknown side: accept either omp's `╰─` prompt or PiG's two-rule stock editor frame
    return any(_RE_READY_OMP_PROMPT.match(l) for l in tail) or (
        sum(1 for l in tail if _RE_READY_RULE.match(l.strip())) >= 2
    )


def _screen_matches_visible_any(screen_text: str, patterns: list[str]) -> bool:
    if not patterns:
        return True
    return any(p in screen_text for p in patterns)


def _screen_matches_resize_width(screen_text: str, target_cols: int) -> bool:
    """Return True once the status band has re-wrapped to width <= target_cols (within 2 cols)."""
    for l in reversed(screen_text.splitlines()):
        if _RE_READY_BAND.match(l):
            w = display_width(l.rstrip())
            return 20 <= w <= target_cols
    return False


def _drain_until_quiet(
    master_fd: int,
    screen: VTScreen,
    decoder: codecs.IncrementalDecoder,
    raw_chunks: list[bytes],
    query_buf: bytearray,
    quiet_s: float = 0.45,
    max_wait: float = 4.0,
) -> None:
    """Drain PTY until no new bytes arrive for `quiet_s` seconds (bounded by `max_wait`)."""
    deadline = time.monotonic() + max_wait
    last_activity = time.monotonic()
    while time.monotonic() < deadline:
        got = _drain(master_fd, screen, decoder, raw_chunks, query_buf, 0.08)
        if got > 0:
            last_activity = time.monotonic()
        elif time.monotonic() - last_activity >= quiet_s:
            return


def _wait_for_screen_condition(
    master_fd: int,
    screen: VTScreen,
    decoder: codecs.IncrementalDecoder,
    raw_chunks: list[bytes],
    query_buf: bytearray,
    predicate: Any,
    max_wait: float,
    quiet_s: float = 0.45,
    settle_max: float = 4.0,
) -> bool:
    """Drain PTY until `predicate(screen.render())` is True, then wait for PTY quiet and re-verify."""
    deadline = time.monotonic() + max_wait
    while time.monotonic() < deadline:
        _drain(master_fd, screen, decoder, raw_chunks, query_buf, 0.1)
        if predicate(screen.render()):
            _drain_until_quiet(master_fd, screen, decoder, raw_chunks, query_buf, quiet_s=quiet_s, max_wait=settle_max)
            if predicate(screen.render()):
                return True
    return bool(predicate(screen.render()))


def _wait_for_cursor(
    master_fd: int,
    screen: VTScreen,
    decoder: codecs.IncrementalDecoder,
    raw_chunks: list[bytes],
    query_buf: bytearray,
    llm_origin: str | None,
    target_next: int | None,
    max_wait: float,
    settle_s: float = 0.5,
    *,
    side: str | None = None,
    expect_visible_any: list[str] | None = None,
    require_prompt_ready: bool = True,
) -> tuple[bool, str]:
    """Wait for EVIDENCE that a turn/step finished:
      1. When `target_next` is set: llm_server `/health` reports `completed >= target_next` and `in_flight == 0`.
      2. PTY output settles (quiet for `settle_s`).
      3. Visible screen shows `expect_visible_any` (if given) AND the program's prompt/editor marker
         (when `require_prompt_ready` is True).
    Returns `(ok, detail)`. Never silently proceeds past an unmet turn deadline.
    """
    deadline = time.monotonic() + max_wait
    if llm_origin is not None and target_next is not None:
        reached = False
        last_st: tuple[int, int, int] | None = None
        while time.monotonic() < deadline:
            _drain(master_fd, screen, decoder, raw_chunks, query_buf, 0.12)
            st = _health_status(llm_origin)
            if st is not None:
                last_st = st
                _nxt, completed, in_flight = st
                if completed >= target_next and in_flight == 0:
                    reached = True
                    break
        if not reached:
            got_c = last_st[1] if last_st else -1
            got_if = last_st[2] if last_st else -1
            return False, f"turn {target_next} not consumed before {max_wait:.1f}s deadline (completed={got_c}, in_flight={got_if})"
    elif expect_visible_any:
        ok_vis = _wait_for_screen_condition(
            master_fd, screen, decoder, raw_chunks, query_buf,
            lambda txt: _screen_matches_visible_any(txt, expect_visible_any),
            max_wait=max_wait,
            quiet_s=settle_s,
            settle_max=min(6.0, max_wait),
        )
        if not ok_vis:
            return False, f"expected visible text {expect_visible_any!r} not rendered within {max_wait:.1f}s"
        if require_prompt_ready:
            ok_p = _wait_for_screen_condition(
                master_fd, screen, decoder, raw_chunks, query_buf,
                lambda txt: _screen_has_prompt_ready(txt, side=side),
                max_wait=max(2.0, deadline - time.monotonic()),
                quiet_s=settle_s,
                settle_max=4.0,
            )
            if not ok_p:
                return False, f"prompt/editor marker not ready after visible text {expect_visible_any!r}"
        return True, "ok"
    else:
        _drain_until_quiet(master_fd, screen, decoder, raw_chunks, query_buf, quiet_s=settle_s, max_wait=max_wait)
        return True, "ok"

    # Turn completed on server: wait for PTY to render the result + quiet + prompt ready + any expected visible marker.
    def _post_turn_ok(txt: str) -> bool:
        if expect_visible_any and not _screen_matches_visible_any(txt, expect_visible_any):
            return False
        if require_prompt_ready and not _screen_has_prompt_ready(txt, side=side):
            return False
        return True

    post_budget = max(8.0, deadline - time.monotonic())
    ok_post = _wait_for_screen_condition(
        master_fd, screen, decoder, raw_chunks, query_buf,
        _post_turn_ok,
        max_wait=post_budget,
        quiet_s=settle_s,
        settle_max=min(6.0, post_budget),
    )
    if not ok_post:
        if expect_visible_any and not _screen_matches_visible_any(screen.render(), expect_visible_any):
            return False, f"turn {target_next} settled on server but expected visible marker {expect_visible_any!r} never appeared"
        return False, f"turn {target_next} settled on server but prompt/editor marker never reappeared"
    return True, "ok"


def validate_terminal_capture(
    side: str,
    scrollback_text: str,
    steps: list[dict[str, Any]],
    *,
    completed_turns: int | None = None,
    in_flight: int | None = None,
    snapshots_present: list[str] | None = None,
    step_errors: list[str] | None = None,
) -> dict[str, Any]:
    """Verify capture completeness and prompt integrity BEFORE any diff comparison.

    Checks:
      1. No step deadline/precondition errors recorded by the PTY driver.
      2. Server-side turn count (`completed_turns >= max expect_next`, `in_flight == 0`) when provided.
      3. Every scripted user prompt (`step["send"]` not starting with `/`) appears at least once
         (and exactly once on its own transcript line), NEVER concatenated with another scripted
         prompt on the same line (the exact signature of typing before the prior turn settled).
      4. Every scripted snapshot name (`step["snapshot"]`) was captured (when `snapshots_present` is given).
    """
    errors: list[str] = []
    if step_errors:
        errors.extend(step_errors)

    expected_max_turn = max(
        (int(s["expect_next"]) for s in steps if s.get("expect_next") is not None),
        default=0,
    )
    if completed_turns is not None and expected_max_turn > 0:
        if completed_turns < expected_max_turn:
            errors.append(
                f"turn {expected_max_turn} not consumed (server completed={completed_turns}, expected>={expected_max_turn})"
            )
    if in_flight is not None and in_flight != 0:
        errors.append(f"server still has {in_flight} in-flight request(s) at capture end")

    prompts = [
        str(s["send"])
        for s in steps
        if isinstance(s.get("send"), str) and str(s["send"]).strip() and not str(s["send"]).strip().startswith("/")
    ]
    lines = scrollback_text.splitlines()
    # Detect concatenated prompts on any single line (e.g. `Show a short markdown example.Compute 20 + 22...`)
    for li, raw_line in enumerate(lines):
        hits = [p for p in prompts if p in raw_line]
        if len(hits) >= 2:
            errors.append(f"prompts concatenated on scrollback line {li + 1}: {hits!r} in {raw_line.strip()[:120]!r}")
        elif len(hits) == 1:
            # Also catch partial concatenation like `Show a short markdown example.Compute...` or
            # editor pile-up `✎ Fast: t 30 numbered lines.a proof subagent...` where a prompt appears
            # with unexpected trailing/leading non-whitespace text on the same line.
            p = hits[0]
            stripped = raw_line.strip()
            if stripped != p:
                # Allow quoted occurrences inside tool output only if the prompt also appears cleanly elsewhere;
                # track separately below.
                pass

    # Also catch prompt-tail fragments piled into a single line (e.g. truncated prefix inside Ask note editor)
    if len(prompts) >= 2:
        prompt_tails = [(p, p[min(10, len(p) // 2) :]) for p in prompts if len(p) >= 16]
        for li, raw_line in enumerate(lines):
            tail_hits = [p for p, tail in prompt_tails if tail in raw_line]
            if len(tail_hits) >= 2:
                msg = f"prompts concatenated on scrollback line {li + 1}: {tail_hits!r} in {raw_line.strip()[:120]!r}"
                if msg not in errors:
                    errors.append(msg)

    # Every scripted prompt must appear cleanly as its own transcript line (`^ <prompt>$` or `^<prompt>$`)
    # exactly once in the scrollback!
    for p in prompts:
        exact_lines = [li for li, l in enumerate(lines) if l.strip() == p]
        if len(exact_lines) == 0:
            errors.append(f"prompt missing as standalone transcript line: {p!r}")
        elif len(exact_lines) > 1:
            errors.append(f"prompt appeared {len(exact_lines)} times as standalone transcript line: {p!r}")

    expected_snaps = [str(s["snapshot"]) for s in steps if isinstance(s.get("snapshot"), str) and s.get("snapshot")]
    if snapshots_present is not None and expected_snaps:
        present_set = set(snapshots_present)
        missing_snaps = [sn for sn in expected_snaps if sn not in present_set]
        if missing_snaps:
            errors.append(f"missing expected snapshot(s): {missing_snaps!r}")

    valid = len(errors) == 0
    summary_msg = (
        "ok"
        if valid
        else f"capture incomplete on {side}: " + "; ".join(errors[:5])
    )
    return {
        "side": side,
        "capture_valid": valid,
        "expected_turns": expected_max_turn,
        "completed_turns": completed_turns,
        "in_flight": in_flight,
        "expected_prompts": len(prompts),
        "expected_snapshots": expected_snaps,
        "snapshots_present": snapshots_present,
        "errors": errors,
        "message": summary_msg,
    }


def run_pty_session(
    launch_script: str,
    steps: list[dict[str, Any]],
    rows: int,
    cols: int,
    startup_wait: float,
    raw_out: str | None,
    screen_out: str,
    scrollback_out: str | None,
    llm_origin: str | None,
    run_tag: str | None,
    side: str | None = None,
    meta_out: str | None = None,
) -> dict[str, Any]:
    screen = VTScreen(rows=rows, cols=cols)
    decoder = codecs.getincrementaldecoder("utf-8")("replace")
    raw_chunks: list[bytes] = []
    query_buf = bytearray()
    master_fd, slave_fd = pty.openpty()
    _set_winsize(slave_fd, rows, cols)

    env = os.environ.copy()
    for k in list(env.keys()):
        if k.startswith(("TERN_", "TERM_PROGRAM", "TMUX", "STY", "ZELLIJ", "KITTY_")):
            env.pop(k, None)
    for k in ("NO_COLOR", "FORCE_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "NODE_DISABLE_COLORS", "CI", "TF_BUILD", "AGENT", "OMPCODE", "TERMINFO"):
        env.pop(k, None)
    env["TERM"] = "xterm-256color"
    env["COLUMNS"] = str(cols)
    env["LINES"] = str(rows)
    env["COLORTERM"] = "truecolor"
    env["PI_TUI_NATIVE"] = "0"
    if run_tag:
        env["ORACLE_RUN_TAG"] = run_tag

    def _setup_ctty() -> None:
        os.setsid()
        try:
            fcntl.ioctl(slave_fd, termios.TIOCSCTTY, 0)
        except OSError:
            pass

    cmd = sandbox_cmd(["/bin/sh", launch_script])
    proc = subprocess.Popen(
        cmd,
        stdin=slave_fd,
        stdout=slave_fd,
        stderr=slave_fd,
        env=env,
        close_fds=True,
        preexec_fn=_setup_ctty,
    )
    os.close(slave_fd)

    def _handle_signal(signum: int, _frame: Any) -> None:
        if run_tag:
            reap_tagged_processes(run_tag)
        raise SystemExit(128 + signum)

    signal.signal(signal.SIGTERM, _handle_signal)
    signal.signal(signal.SIGINT, _handle_signal)
    signal.signal(signal.SIGHUP, _handle_signal)

    final_screen = ""
    full_scrollback = ""
    final_screen_styled: list[list[Any]] = []
    full_scrollback_styled: list[list[Any]] = []
    leaked_pids: list[int] = []
    snapshots: dict[str, str] = {}
    resize_raw_offsets: list[dict[str, int]] = []
    step_errors: list[str] = []
    completed_turns: int | None = None
    in_flight_end: int | None = None
    try:
        # Evidence-based startup wait: NEVER type on a fixed timer.
        # Wait until the program renders its status band + prompt/editor marker and settles quiet,
        # bounded by a generous startup deadline (`max(startup_wait, 45.0)`).
        startup_budget = max(float(startup_wait), 45.0)
        startup_ok = _wait_for_screen_condition(
            master_fd, screen, decoder, raw_chunks, query_buf,
            lambda txt: _screen_has_prompt_ready(txt, side=side),
            max_wait=startup_budget,
            quiet_s=0.6,
            settle_max=6.0,
        )
        if not startup_ok:
            step_errors.append(
                f"startup prompt/editor marker never appeared within {startup_budget:.1f}s"
            )
        else:
            for step_i, step in enumerate(steps):
                if proc.poll() is not None:
                    step_errors.append(f"step {step_i}: process exited early with code {proc.returncode}")
                    break
                # Scale per-step deadline generously so loaded machines never drop a turn prematurely.
                base_wait = float(step.get("wait", 15.0))
                max_wait = max(base_wait * 3.0, 45.0)

                # Precondition wait (e.g., Ask form `Choose a mode:` before pressing Enter)
                pre_vis = step.get("wait_for_visible_any")
                if isinstance(pre_vis, list) and pre_vis:
                    ok_pre = _wait_for_screen_condition(
                        master_fd, screen, decoder, raw_chunks, query_buf,
                        lambda txt, pats=pre_vis: _screen_matches_visible_any(txt, pats),
                        max_wait=max_wait,
                        quiet_s=0.35,
                        settle_max=4.0,
                    )
                    if not ok_pre:
                        step_errors.append(
                            f"step {step_i}: precondition {pre_vis!r} not visible within {max_wait:.1f}s"
                        )
                        break

                if step.get("resize"):
                    new_cols = int(step["resize"].get("cols", cols))
                    new_rows = int(step["resize"].get("rows", rows))
                    start_off = sum(len(c) for c in raw_chunks)
                    _set_winsize(master_fd, new_rows, new_cols)
                    screen.resize(new_rows, new_cols)
                    try:
                        os.killpg(proc.pid, signal.SIGWINCH)
                    except OSError:
                        pass
                    ok_rsz = _wait_for_screen_condition(
                        master_fd, screen, decoder, raw_chunks, query_buf,
                        lambda txt, nc=new_cols: _screen_matches_resize_width(txt, nc) and _screen_has_prompt_ready(txt, side=side),
                        max_wait=max(15.0, base_wait * 2.0),
                        quiet_s=0.6,
                        settle_max=6.0,
                    )
                    end_off = sum(len(c) for c in raw_chunks)
                    resize_raw_offsets.append({"start": start_off, "end": end_off, "rows": new_rows, "cols": new_cols})
                    if not ok_rsz:
                        step_errors.append(f"step {step_i}: resize to {new_cols}x{new_rows} did not settle cleanly")
                        break

                raw_bytes = step.get("raw_keys")
                if isinstance(raw_bytes, str) and raw_bytes:
                    os.write(master_fd, raw_bytes.encode("latin1"))
                    exp_vis = step.get("expect_visible_any")
                    if isinstance(exp_vis, list) and exp_vis:
                        ok_rk = _wait_for_screen_condition(
                            master_fd, screen, decoder, raw_chunks, query_buf,
                            lambda txt, pats=exp_vis: _screen_matches_visible_any(txt, pats),
                            max_wait=max(15.0, base_wait * 2.0),
                            quiet_s=0.45,
                            settle_max=5.0,
                        )
                        if not ok_rk:
                            step_errors.append(
                                f"step {step_i}: raw_keys expected visible marker {exp_vis!r} did not appear"
                            )
                            break
                    else:
                        _drain_until_quiet(master_fd, screen, decoder, raw_chunks, query_buf, quiet_s=0.5, max_wait=4.0)

                text = step.get("send")
                if text:
                    # Before typing a prompt, verify the editor/prompt marker is present (never type into a busy/unready screen)
                    if not _screen_has_prompt_ready(screen.render(), side=side):
                        ok_rdy = _wait_for_screen_condition(
                            master_fd, screen, decoder, raw_chunks, query_buf,
                            lambda txt: _screen_has_prompt_ready(txt, side=side),
                            max_wait=15.0,
                            quiet_s=0.35,
                            settle_max=3.0,
                        )
                        if not ok_rdy:
                            step_errors.append(f"step {step_i}: editor/prompt not ready before sending {text!r}")
                            break
                    os.write(master_fd, text.encode("utf-8"))
                    # Wait for the typed prompt text to echo (or at least PTY quiet) before pressing Enter
                    echo_probe = text[: min(24, len(text))]
                    _wait_for_screen_condition(
                        master_fd, screen, decoder, raw_chunks, query_buf,
                        lambda txt, ep=echo_probe: ep in txt,
                        max_wait=6.0,
                        quiet_s=0.15,
                        settle_max=1.5,
                    )
                    if step.get("enter", True):
                        os.write(master_fd, b"\r")
                elif step.get("enter", False):
                    os.write(master_fd, b"\r")

                target_next = step.get("expect_next")
                exp_vis = step.get("expect_visible_any") if isinstance(step.get("expect_visible_any"), list) else None
                req_prompt = bool(step.get("require_prompt_ready", True))
                if target_next is not None or (not raw_bytes and not step.get("resize")):
                    ok_turn, detail = _wait_for_cursor(
                        master_fd, screen, decoder, raw_chunks, query_buf,
                        llm_origin, target_next, max_wait,
                        settle_s=0.55,
                        side=side,
                        expect_visible_any=exp_vis,
                        require_prompt_ready=req_prompt,
                    )
                    if not ok_turn:
                        step_errors.append(f"step {step_i} ({text or step.get('snapshot') or 'step'}): {detail}")
                        break
                snap_name = step.get("snapshot")
                if isinstance(snap_name, str) and snap_name:
                    snapshots[snap_name] = screen.render()

        final_screen = screen.render()
        full_scrollback = screen.render_with_scrollback()
        final_screen_styled = screen.render_styled_rows()
        full_scrollback_styled = screen.render_styled_with_scrollback()
        st_end = _health_status(llm_origin)
        if st_end is not None:
            completed_turns = st_end[1]
            in_flight_end = st_end[2]

        try:
            os.write(master_fd, b"/quit\r")
            _drain(master_fd, screen, decoder, raw_chunks, query_buf, 1.0)
        except OSError:
            pass
    finally:
        if proc.poll() is None:
            try:
                os.killpg(proc.pid, signal.SIGTERM)
            except OSError:
                try:
                    proc.terminate()
                except OSError:
                    pass
            try:
                proc.wait(timeout=3)
            except subprocess.TimeoutExpired:
                try:
                    os.killpg(proc.pid, signal.SIGKILL)
                except OSError:
                    try:
                        proc.kill()
                    except OSError:
                        pass
                try:
                    proc.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    pass
        if run_tag:
            leaked_pids = reap_tagged_processes(run_tag)
        try:
            os.close(master_fd)
        except OSError:
            pass

    with open(screen_out, "w", encoding="utf-8") as fh:
        fh.write(final_screen)
    with open(screen_out + ".sgr.json", "w", encoding="utf-8") as fh:
        json.dump(
            {"rows": final_screen_styled, "resize_raw_offsets": resize_raw_offsets},
            fh,
            ensure_ascii=False,
        )
    if scrollback_out:
        with open(scrollback_out, "w", encoding="utf-8") as fh:
            fh.write(full_scrollback)
        with open(scrollback_out + ".sgr.json", "w", encoding="utf-8") as fh:
            json.dump(
                {"rows": full_scrollback_styled, "resize_raw_offsets": resize_raw_offsets},
                fh,
                ensure_ascii=False,
            )
    if raw_out:
        with open(raw_out, "wb") as fh:
            fh.write(b"".join(raw_chunks))
    if snapshots:
        base_dir = os.path.dirname(screen_out)
        prefix = os.path.basename(screen_out).split(".")[0]
        for snap_name, snap_text in snapshots.items():
            with open(os.path.join(base_dir, f"{prefix}.snap.{snap_name}.txt"), "w", encoding="utf-8") as fh:
                fh.write(snap_text)

    validity = validate_terminal_capture(
        side or os.path.basename(screen_out).split(".")[0],
        full_scrollback,
        steps,
        completed_turns=completed_turns,
        in_flight=in_flight_end,
        snapshots_present=sorted(snapshots.keys()),
        step_errors=step_errors,
    )
    non_empty = sum(1 for l in final_screen.splitlines() if l.strip())
    meta = {
        "side": side or os.path.basename(screen_out).split(".")[0],
        "rc": proc.returncode,
        "rows": rows,
        "cols": cols,
        "raw_bytes": sum(len(c) for c in raw_chunks),
        "non_empty_lines": non_empty,
        "scrollback_lines": len(screen.scrollback),
        "full_screen_clears": screen.full_screen_clears,
        "scrollback_clears": screen.scrollback_clears,
        "completed_turns": completed_turns,
        "in_flight": in_flight_end,
        "snapshots": sorted(snapshots.keys()),
        "leaked_pids": leaked_pids,
        "screen_path": screen_out,
        "capture_valid": validity["capture_valid"],
        "capture_validation": validity,
    }
    if meta_out:
        with open(meta_out, "w", encoding="utf-8") as fh:
            json.dump(meta, fh, indent=2, ensure_ascii=False)
            fh.write("\n")
    return meta

# Path normalisers (Finding 2): do NOT conflate `/private/tmp/...` and `/tmp/...` into the same token;
# preserve `/private<WORK>` vs `<WORK>` so missing `/private` stripping on macOS shows as a diff.
_RE_ANSI_WORK_PATH_PRIVATE = re.compile(r"/private/tmp/[^/\s\"']+(?:/[^/\s\"']+)*?/(?:omp|pig)/work\b")
_RE_ANSI_WORK_PATH = re.compile(r"(?<!/private)/tmp/[^/\s\"']+(?:/[^/\s\"']+)*?/(?:omp|pig)/work\b")
_RE_STATUS_SIDE_PATH = re.compile(r"(\b[^/\s\"']+)/(?:omp|pig)/work\b")
_RE_ANSI_LOG_PATH = re.compile(r"(?:/private|/var)?/tmp/[^/\s\"']+(?:/[^/\s\"']+)*?/(?:omp|pig)/home/\.omp/logs/http-\d+-requests/[0-9a-zA-Z_-]+\.json")
# Duration masks (Finding 19): preserve the unit (`ms` vs `s`) so `71ms` vs `710s` NEVER masks as equal!
_RE_STOPWATCH_DUR = re.compile(r"(⏱\s*)\d+(?:\.\d+)?(ms|s)\b")
_RE_CARD_PAREN_DUR = re.compile(r"(·\s*\()\d+(?:\.\d+)?(ms|s)(\))")
_RE_CARD_TAIL_DUR = re.compile(r"(·\s*)\d+(?:\.\d+)?(ms|s)(?=(?:\s*(?:─|│|$)))")
_RE_BOX_FILL_TRAIL = re.compile(r"(─{2,})([╮╯])$")
_RE_BOX_SPACE_TRAIL = re.compile(r"([ ]{2,})(│)$")
_RE_WELCOME_VERSION_ROW = re.compile(r"^\s+██\s+v\d+\.\d+\.\d+\s*$")
_RE_PIG_WELCOME_TAIL_ROW = re.compile(r"^\s+▀ ▀\s+████\s*$")
_RE_SGR_SEQ = re.compile(r"\x1b\[([0-9;]*)m")
DEFAULT_TERMINAL_ALLOWLIST_PATH = Path(__file__).resolve().parent / "terminal_allowlist.json"
# Welcome tip validation (Findings 17, 19, 25): canonical omp tips + port `internal/ansi/chrome.go` tips.
WELCOME_TIP_MAX_COLS = 72
_OMP_ANSI_TIPS_CANONICAL: list[str] = [
    'Tired of typing "keep going"? Just send a \'.\'',
    'Type `#` anywhere in your prompt for quick actions — copy the line or the whole prompt, undo, jump the cursor',
    'Type `/` at the start of your prompt to browse every command',
    'Start your prompt with `!` to run it as bash — `!!` runs it without adding the output to context',
    'Start your prompt with `$ ` to run Python — `$$ ` runs it without adding the output to context',
    'You can /btw to ask a side question',
    'Use /tan to fork the current conversation into a background agent',
    'With default keybindings, {key:ctrl+d} deletes the character under the cursor instead of quitting your draft — see /hotkeys for your exit key',
    'Find out which model you emotionally abuse the most with `omp stats`',
    'Try task isolation to create CoW worktrees',
    'Need a cheap nested model call? Use `completion(x...)`. Have a big batch of tasks? Ask clanker to use it!',
    'Spaghetti code? Try complaining with /omfg',
    'Did you know? Each kitty/tmux/cmux/zellij/wezterm split keeps its own session — `omp -c` resumes the right one',
    'Drop the word `ultrathink` in your message for harder multi-step reasoning — watch it glow rainbow as you type',
    'Agent overthinking? Say `chillax` — on models with per-message effort, thinking drops to the lowest level, even mid-turn — watch it glow as you type',
    'Say `orchestrate` in your message to drive a multi-phase task with parallel subagents — watch it glow as you type',
    'Say `workflowz` in your message to drive the task with parallel subagents in eval — watch it glow as you type',
    'Bulk data to classify or triage? Say `jevify` in your message — the eval kernel\'s `judge()` processes the batch and you read only what it flags — watch it glow as you type',
    'Log in to several accounts of the same provider — `/login` again — and omp load-balances across them automatically',
    'Run `omp auth-broker serve` once and every machine pulls live tokens over the wire — refresh keys never leave the host; `omp auth-gateway` fronts it as a drop-in proxy any OpenAI-compatible client can hit',
    'Press {key:alt+p} (or /switch) to switch provider, and {key:ctrl+p} to cycle role models smol -> slow -> etc',
    'Press {key:ctrl+r} to search your prompt history and reuse a past message',
    '`/force read` pins the next turn to one specific tool when the model keeps reaching for the wrong one',
    '`/copy code` grabs the last code block to your clipboard — `/copy cmd` grabs the last shell/python command',
    '`/shake` rips heavy tool results out of context to reclaim tokens without a full /compact — `/shake images` drops just images',
    'Pair up live: `/collab` shares your session through an end-to-end encrypted relay link — a teammate runs `/join <link>` to watch tool calls stream and prompt the agent from their own omp',
    'Press {tap:left} to drill into a running or finished agent and inspect its tool calls and transcript',
    'Hit a Codex rate limit? `/usage reset` spends a saved reset credit to immediately restore your quota',
    'No native tool_calling? Inference provider botches parsing them? `PI_DIALECT=glm|kimi|anthropic…` rolls it locally for them!',
    'Turn on `/advisor` to attach a second model that reviews every turn and quietly injects advice',
    'Try starting your prompt with a ->, and writing a list (1. Do X, 2. Do Y)',
    'Press {key:shift+tab} to cycle through reasoning effort levels',
    'omp can reconfigure and extend itself — ask any questions you have!',
    'Lint/type errors piling up? `omp cleanse` (or /cleanse right here) hunts project diagnostics and fixes them with parallel subagents — {key:escape} cancels',
    'Psst, omp has a natural habitat 🐦 tern (https://scl.so/tern)',
    'Psst, omp has a natural habitat 🐦 tern',
    # Hardcoded 10% nerdfont nag tip in `packages/tui/src/prompt/welcome.ts:233-234`
    'Please use nerdfont 😭.',
]
_ANSI_TIP_KEY_EXPANSIONS: dict[str, tuple[str, ...]] = {
    "{key:ctrl+d}": ("\U000f0634 D", "⌃D", "Ctrl+D", "^D"),
    "{key:alt+p}": ("\U000f0635 P", "⌥P", "Alt+P"),
    "{key:ctrl+p}": ("\U000f0634 P", "⌃P", "Ctrl+P", "^P"),
    "{key:ctrl+r}": ("\U000f0634 R", "⌃R", "Ctrl+R", "^R"),
    "{key:shift+tab}": ("\U000f0636 \U000f0312", "⇧⇥", "Shift+Tab"),
    "{key:escape}": ("\U000f12b7", "⎋", "Esc", "Escape"),
    "{tap:left}": ("← ←", "←←", "Left Left", "double-tap Left"),
}


def _expand_ansi_tip_variants(raw_tips: list[str]) -> set[str]:
    out: set[str] = set()
    for tip in raw_tips:
        variants = [" ".join(tip.strip().split())]
        for ph, repls in _ANSI_TIP_KEY_EXPANSIONS.items():
            if any(ph in v for v in variants):
                nxt: list[str] = []
                for v in variants:
                    if ph in v:
                        for r in repls:
                            nxt.append(v.replace(ph, r))
                    else:
                        nxt.append(v)
                variants = nxt
        for v in variants:
            out.add(v)
            out.add(v.replace("`", ""))
    return out


def _load_pig_ansi_tips() -> set[str]:
    """Read `welcomeTips` from `components/oh-my-pig/extensions/oh-my-pig/internal/ansi/chrome.go`."""
    go_path = SCRIPT_DIR.parent.parent.parent / "components" / "oh-my-pig" / "extensions" / "oh-my-pig" / "internal" / "ansi" / "chrome.go"
    raw: list[str] = []
    if go_path.is_file():
        txt = go_path.read_text(encoding="utf-8")
        # Match to closing `\n}` so `{key:...}` placeholders inside backtick strings do not terminate the match early!
        m = re.search(r"var\s+welcomeTips\s*=\s*\[\]string\{(.*?)\n\}", txt, re.S)
        if m:
            for lit in re.findall(r"`([^`]+)`|\"((?:\\.|[^\"\\])*)\"", m.group(1)):
                val = lit[0] or lit[1]
                if val:
                    raw.append(val)
    return _expand_ansi_tip_variants(raw)

OMP_ANSI_TIP_SET: set[str] = _expand_ansi_tip_variants(_OMP_ANSI_TIPS_CANONICAL)
PIG_ANSI_TIP_SET: set[str] = _load_pig_ansi_tips()

# Exact paired omp-visualizer vs port-fallback bash command rows (Findings 19, 25).
# Each entry is `(omp_inner_exact, pig_inner_exact)` inside `│ <inner> │`.
BASH_SHELL_VIZ_EXACT_PAIRS: list[tuple[str, str]] = [
    ('print("alpha\\\\nbeta\\\\n")', "$ printf 'alpha\\nbeta\\n'"),
    ('reject Proof preview only', '$ reject "Proof preview only"'),
    ('seq 1 30 → awk ‹{printf "fold_line_%02d\\n", $1}›', '$ seq 1 30 | awk \'{printf "fold_line_%02d\\n", $1}\''),
]


def display_width(text: str) -> int:
    """Compute terminal cell width matching VTScreen._char_width (EAW W/F = 2, combining/VS = 0, else 1)."""
    total = 0
    for ch in text:
        if unicodedata.combining(ch) or ch in ("\u200d", "\ufe0e", "\ufe0f"):
            continue
        total += 2 if unicodedata.east_asian_width(ch) in ("W", "F") else 1
    return total


def _normalize_line_with_width_guard(raw_line: str) -> tuple[str, int, bool]:
    """Normalize variable fields on one line and return `(normalized, raw_display_width, had_variable_field)`.

    Box-fill / trailing-space runs are NOT collapsed here unconditionally; they are
    only collapsed pairwise when two compared lines had EQUAL raw display width BEFORE
    normalization and at least one carried a variable-length substitution (so real width
    bugs remain visible diffs). Duration masks preserve the unit (`ms` vs `s`).
    """
    raw_w = display_width(raw_line)
    out = raw_line
    out = _RE_ANSI_LOG_PATH.sub("<HTTP_LOG>", out)
    out = _RE_ANSI_WORK_PATH_PRIVATE.sub("/private<WORK>", out)
    out = _RE_ANSI_WORK_PATH.sub("<WORK>", out)
    out = _RE_STATUS_SIDE_PATH.sub(r"\1/<SIDE>/work", out)
    out = _RE_STOPWATCH_DUR.sub(r"\1<DUR>\2", out)
    out = _RE_CARD_PAREN_DUR.sub(r"\1<DUR>\2\3", out)
    out = _RE_CARD_TAIL_DUR.sub(r"\1<DUR>\2", out)
    changed_len = out != raw_line
    return out, raw_w, changed_len


def _collapse_box_fill_for_comparison(line: str) -> str:
    """Collapse trailing box-rule fill (`─+╮`/`─+╯`) or trailing box padding (` +│`) after variable substitution."""
    out = _RE_BOX_FILL_TRAIL.sub(r"─…\2", line)
    out = _RE_BOX_SPACE_TRAIL.sub(r" …\2", out)
    return out


def normalize_screen_text(text: str) -> str:
    """Normalize run-specific temp paths (preserving /private), status-band `<SIDE>`, and card/stopwatch durations (preserving units)."""
    out_lines: list[str] = []
    for line in text.splitlines():
        norm, _w, _chg = _normalize_line_with_width_guard(line)
        out_lines.append(norm)
    trailing = "\n" if text.endswith("\n") else ""
    return "\n".join(out_lines) + trailing


def width_preserving_normalize_pair(omp_raw_text: str, pig_raw_text: str) -> tuple[str, str]:
    """Normalize both screens while preserving real width mismatches.

    When both raw lines had the EXACT same terminal display width before normalization
    AND masking a variable field (`<WORK>`, `<DUR>ms`, `<DUR>s`, `<HTTP_LOG>`, `<SIDE>`)
    changed length, the trailing box-fill run (`─…╮`, `─…╯`) or box padding (` …│`) is
    collapsed on both lines so equal-width 120/80-col cards do not show fill diffs.
    If the raw display widths differed before normalization, the fill is NOT collapsed,
    so real width bugs stay visible diffs.
    """
    a_raw = omp_raw_text.splitlines()
    b_raw = pig_raw_text.splitlines()
    a_meta = [_normalize_line_with_width_guard(l) for l in a_raw]
    b_meta = [_normalize_line_with_width_guard(l) for l in b_raw]

    a_keys = [_collapse_box_fill_for_comparison(m[0]) if m[2] else m[0] for m in a_meta]
    b_keys = [_collapse_box_fill_for_comparison(m[0]) if m[2] else m[0] for m in b_meta]

    a_out = [m[0] for m in a_meta]
    b_out = [m[0] for m in b_meta]

    sm = difflib.SequenceMatcher(None, a_keys, b_keys, autojunk=False)
    for tag, i1, i2, j1, j2 in sm.get_opcodes():
        if tag == "equal":
            for k in range(i2 - i1):
                ai, bj = i1 + k, j1 + k
                norm_a, raw_wa, chg_a = a_meta[ai]
                norm_b, raw_wb, chg_b = b_meta[bj]
                if chg_a or chg_b:
                    if raw_wa == raw_wb:
                        if norm_a != norm_b:
                            a_out[ai] = _collapse_box_fill_for_comparison(norm_a)
                            b_out[bj] = _collapse_box_fill_for_comparison(norm_b)
                    elif norm_a == norm_b:
                        # Only flag raw_width mismatch on fixed-width framed/box rows
                        # (`╭...╮`, `╰...╯`, `│...│`) where padding keeps both sides equal
                        # width despite variable durations; unframed lines like
                        # `└─ ✔ ⟦task⟧ ProofChild · 49ms` vs `136ms` legitimately differ
                        # in raw width by the digit count of the masked variable field.
                        is_framed = (
                            (a_raw[ai].startswith(("╭", "╰", "│")) and a_raw[ai].endswith(("╮", "╯", "│")))
                            or (b_raw[bj].startswith(("╭", "╰", "│")) and b_raw[bj].endswith(("╮", "╯", "│")))
                        )
                        if is_framed:
                            a_out[ai] = f"{norm_a} [raw_width={raw_wa}]"
                            b_out[bj] = f"{norm_b} [raw_width={raw_wb}]"
    t_a = "\n" if omp_raw_text.endswith("\n") else ""
    t_b = "\n" if pig_raw_text.endswith("\n") else ""
    return "\n".join(a_out) + t_a, "\n".join(b_out) + t_b


def find_welcome_tip_line_indices(lines: list[str], *, side: str = "omp") -> set[int]:
    """Locate 0-based line indices of a VALID wrapped welcome tip block structurally (Findings 19, 25).

    Validates:
      1. Immediately follows the wordmark version row (`  ██  vX.Y.Z`) and blank line(s).
      2. Every non-blank wrapped tip line has trimmed display width <= 72 columns (WELCOME_TIP_MAX_COLS).
      3. The joined tip text (after stripping `Tip:`) is present in omp's `tips.txt` (or the port's
         filtered `welcomeTips` list on the pig side).
    Invalid text or overly wide lines are NOT returned, so they stay unexplained diffs.
    """
    tip_indices: set[int] = set()
    allowed_set = OMP_ANSI_TIP_SET if side == "omp" else (PIG_ANSI_TIP_SET | OMP_ANSI_TIP_SET)
    n = len(lines)
    i = 0
    while i < n:
        is_welcome_end = bool(_RE_WELCOME_VERSION_ROW.match(lines[i])) or (
            side == "pig" and bool(_RE_PIG_WELCOME_TAIL_ROW.match(lines[i]))
        )
        if is_welcome_end:
            j = i + 1
            while j < n and not lines[j].strip():
                j += 1
            if j < n and lines[j].lstrip().startswith("Tip:"):
                block: list[int] = []
                while j < n and lines[j].strip():
                    block.append(j)
                    j += 1
                # Validate width <= 72 cols (trimmed content width, matching welcome.ts maxWidth=72)
                widths_ok = all(display_width(lines[k].strip()) <= WELCOME_TIP_MAX_COLS for k in block)
                joined = " ".join(lines[k].strip() for k in block)
                if joined.startswith("Tip:"):
                    body = joined[len("Tip:"):].strip()
                else:
                    body = joined
                body = " ".join(body.split())
                if widths_ok and body in allowed_set:
                    tip_indices.update(block)
                i = j
                continue
        i += 1
    return tip_indices


_RE_RULE_LINE = re.compile(r"^─{20,}$")
_RE_STATUS_BAND_LINE = re.compile(r"^\s*π\s*>\s*.*128K─?\s*$")
# Finding 25: `find_dock_blocks` includes any `╰─...` prompt row as part of the dock, while
# `find_prompt_editor_frame_line_indices` matches ONLY bare `╰─` (trailing spaces only, NEVER leftover text).
_RE_OMP_PROMPT_ANY_LINE = re.compile(r"^╰─(?:\s.*)?$")
_RE_OMP_PROMPT_BARE_LINE = re.compile(r"^╰─\s*$")
_RE_RAW_HTTP_LOG_FULL = re.compile(
    r"^raw-http-request=(?:<HTTP_LOG>|(?:/private|/var)?/tmp/[^/\s\"']+(?:/[^/\s\"']+)*?/omp/home/\.omp/logs/http-\d+-requests/[0-9a-zA-Z_-]+\.json)$"
)
_RAW_HTTP_PREFIX = "raw-http-request="


def find_omp_raw_http_request_blocks(lines: list[str]) -> list[list[int]]:
    """Locate omp's `raw-http-request=<log>.json` dump lines as logical wrapped blocks.

    omp wraps long `raw-http-request=...json` paths across 1 or more lines at any
    terminal column width (`packages/tui/src/chrome/error-block.ts:45-51`), hanging
    every row at the same leading indent (3 spaces in omp's 1-space margin + 2-space
    `CONTINUATION_INDENT`). Because the wrap point depends on the output directory
    path length and terminal width (and can even split inside `raw-http-request=`
    itself at very narrow widths), this detector:
      - scans starting from any line whose trimmed text is a non-empty prefix of
        `raw-http-request=` (length >= 3) or starts with `raw-http-request=`,
      - joins consecutive continuation lines having the same leading indentation
        (and no internal whitespace, since file paths carry no spaces) until the
        joined logical line matches `_RE_RAW_HTTP_LOG_FULL` (literal
        `raw-http-request=` prefix + omp log path ending in `.json`, or `<HTTP_LOG>`).
    Returns a list of index lists (one per matched logical block). Unmatched or
    truncated/tampered text is NOT returned.
    """
    n = len(lines)
    blocks: list[list[int]] = []
    i = 0
    while i < n:
        raw0 = lines[i]
        s0 = raw0.strip()
        if not s0 or " " in s0:
            i += 1
            continue
        # Must either start with `raw-http-request=` or be a prefix of it (for narrow-column splits inside the prefix)
        if not (s0.startswith(_RAW_HTTP_PREFIX) or (len(s0) >= 3 and _RAW_HTTP_PREFIX.startswith(s0))):
            i += 1
            continue
        indent0 = len(raw0) - len(raw0.lstrip(" "))
        if indent0 < 1:
            i += 1
            continue
        joined = s0
        idxs = [i]
        matched_block: list[int] | None = None
        if _RE_RAW_HTTP_LOG_FULL.match(joined):
            matched_block = list(idxs)
        else:
            j = i + 1
            # Bounded continuation walk (a wrapped path is at most ~12 lines even at 20 cols)
            while j < n and (j - i) < 12:
                rawj = lines[j]
                sj = rawj.strip()
                if not sj or " " in sj:
                    break
                indentj = len(rawj) - len(rawj.lstrip(" "))
                if indentj != indent0:
                    break
                joined += sj
                idxs.append(j)
                if _RE_RAW_HTTP_LOG_FULL.match(joined):
                    matched_block = list(idxs)
                    break
                # Stop early if joined already diverged from the required literal prefix
                if len(joined) >= len(_RAW_HTTP_PREFIX) and not joined.startswith(_RAW_HTTP_PREFIX):
                    break
                j += 1
        if matched_block is not None:
            blocks.append(matched_block)
            i = matched_block[-1] + 1
        else:
            i += 1
    return blocks


def find_omp_raw_http_request_line_indices(lines: list[str], *, side: str = "omp") -> set[int]:
    """Return 0-based line indices of omp's wrapped `raw-http-request=...json` lines (omp side only)."""
    if side != "omp":
        return set()
    out: set[int] = set()
    for blk in find_omp_raw_http_request_blocks(lines):
        out.update(blk)
    return out


def find_dock_blocks(lines: list[str]) -> list[tuple[int, int, int]]:
    """Locate dock blocks in terminal lines as `(start_idx, band_idx, end_idx)` inclusive.

    A dock block starts at the first widget line above the status band (TODO HUD /
    pinned provider error banner / status band itself) and ends at the last prompt /
    stock-editor-frame line (`╰─` on omp, or `rule / input / rule` on PiG).
    Detection is strictly structural (horizontal rule delimiters around the pinned
    error banner, TODO tree glyph prefixes, and status band) and never depends on
    allowlist regexes or where long paths wrap.
    """
    n = len(lines)
    bands = [i for i, l in enumerate(lines) if _RE_STATUS_BAND_LINE.match(l)]
    blocks: list[tuple[int, int, int]] = []
    for b_idx in bands:
        start = b_idx
        i = b_idx - 1
        while i >= 0:
            s = lines[i].strip()
            if not s:
                j = i - 1
                while j >= 0 and not lines[j].strip():
                    j -= 1
                if j >= 0 and (
                    _RE_RULE_LINE.match(lines[j].strip())
                    or lines[j].lstrip().startswith(("└─────", "├─", "│", "TODO", "✘", "Dismissed"))
                ):
                    i = j
                    continue
                break
            if _RE_RULE_LINE.match(s):
                # Structural pinned error banner: a bottom horizontal rule above the status band
                # closes a framed banner bounded above by a matching horizontal rule containing
                # `✘` / `Dismissed` (with any number of wrapped error/path lines inside).
                top_rule: int | None = None
                k = i - 1
                while k >= 0 and (i - k) <= 30:
                    sk = lines[k].strip()
                    if _RE_RULE_LINE.match(sk):
                        inner = [lines[m].strip() for m in range(k + 1, i)]
                        if any(ln.startswith(("✘", "Dismissed")) for ln in inner):
                            top_rule = k
                        break
                    if _RE_STATUS_BAND_LINE.match(lines[k]):
                        break
                    k -= 1
                if top_rule is not None:
                    start = top_rule
                    i = top_rule - 1
                    continue
                start = i
                i -= 1
            elif s.startswith(("TODO", "├─", "│", "└─────", "✘", "Dismissed")):
                start = i
                i -= 1
            else:
                break
        end = b_idx
        if b_idx + 1 < n:
            nxt = lines[b_idx + 1]
            if _RE_OMP_PROMPT_ANY_LINE.match(nxt):
                end = b_idx + 1
            elif _RE_RULE_LINE.match(nxt.strip()):
                j = b_idx + 2
                while j < n and not _RE_RULE_LINE.match(lines[j].strip()):
                    j += 1
                if j < n and _RE_RULE_LINE.match(lines[j].strip()):
                    end = j
                else:
                    end = b_idx + 1
        blocks.append((start, b_idx, end))
    return blocks


def find_prompt_editor_frame_line_indices(lines: list[str], *, side: str = "omp") -> set[int]:
    """Locate the prompt / stock-editor-frame lines structurally at the active dock tail (Findings 19, 25).

    Side-specific:
      - `side == "omp"`: matches ONLY omp's bare `╰─` prompt row immediately after the active status band
        (rejects any `╰─ <leftover text>`).
      - `side == "pig"`: matches ONLY PiG's stock editor top/bottom full-width rule lines immediately
        after the active status band (requires an empty input row between them).
    Never matches arbitrary rule lines elsewhere in the transcript or error banner.
    """
    out: set[int] = set()
    blocks = find_dock_blocks(lines)
    if not blocks:
        return out
    active = blocks[0] if (len(blocks) >= 2 and display_width(lines[blocks[-1][1]]) > display_width(lines[blocks[0][1]])) else blocks[-1]
    _start, b_idx, end = active
    if end > b_idx:
        nxt = lines[b_idx + 1]
        if side == "omp":
            if _RE_OMP_PROMPT_BARE_LINE.match(nxt):
                out.add(b_idx + 1)
        elif side == "pig":
            if _RE_RULE_LINE.match(nxt.strip()):
                out.add(b_idx + 1)
                if end > b_idx + 1 and _RE_RULE_LINE.match(lines[end].strip()):
                    out.add(end)
    return out


def _canonicalize_dock_block_for_rewrap(block_lines: list[str]) -> str:
    """Canonicalize a dock block's content independent of terminal width (Finding 15).

    Re-wrapping a dock from 120 cols to 80 cols changes:
      - full-width rule lengths (`─{20,}` -> `<RULE>`)
      - padding dashes inside the status band (`─+` between tokens -> `─`)
      - hard-wrapped continuation splits (e.g. `raw-http-request=.../http-400-r` + `equests/...json`
        rejoining into `raw-http-request=.../http-400-requests/...json`)
    Every substantive character (TODO items, error message, notice text, status segments, prompt)
    MUST match identically between the stale pre-resize block and the active post-resize dock!
    """
    raw_http_map: dict[int, list[int]] = {}
    for blk in find_omp_raw_http_request_blocks(block_lines):
        raw_http_map[blk[0]] = blk
    parts: list[str] = []
    i = 0
    n = len(block_lines)
    while i < n:
        raw = block_lines[i]
        s = raw.strip()
        if not s:
            i += 1
            continue
        if _RE_RULE_LINE.match(s):
            parts.append("<RULE>")
            i += 1
            continue
        if _RE_STATUS_BAND_LINE.match(raw):
            # Collapse variable dash runs in the status band (the only width-dependent fill in the band)
            band_norm = re.sub(r"─+", "─", s)
            band_norm = _RE_STATUS_SIDE_PATH.sub(r"\1/<SIDE>/work", band_norm)
            parts.append(band_norm)
            i += 1
            continue
        # Join hard-wrapped `raw-http-request=...json` continuations regardless of wrap column
        if i in raw_http_map:
            blk = raw_http_map[i]
            s = "".join(block_lines[k].strip() for k in blk)
            i = blk[-1] + 1
        else:
            i += 1
        s = _RE_ANSI_LOG_PATH.sub("<HTTP_LOG>", s)
        s = _RE_ANSI_WORK_PATH_PRIVATE.sub("/private<WORK>", s)
        s = _RE_ANSI_WORK_PATH.sub("<WORK>", s)
        parts.append(" ".join(s.split()))
    return "\n".join(parts)


def find_stale_pre_resize_frame_indices(
    lines: list[str],
    *,
    side: str = "omp",
) -> tuple[set[int], dict[str, Any] | None]:
    """Detect omp's stale pre-resize dock copy strictly on the omp side (Finding 15).

    Rules:
      1. ONLY runs for `side == "omp"`. On `side == "pig"`, always returns `(set(), None)` so a
         port redraw bug leaving extra dock/garbage lines on screen is NEVER masked.
      2. Requires an active post-resize dock of narrower width `min_w` and a wider block `w > min_w`.
      3. The wider block's canonicalized content MUST EQUAL the active dock's canonicalized content
         re-wrapped at the old width (`_canonicalize_dock_block_for_rewrap`). Any garbage or
         diverging content inside the wider block fails the equality check and stays unexplained.
    """
    if side != "omp":
        return set(), None
    blocks = find_dock_blocks(lines)
    if len(blocks) < 2:
        return set(), None
    widths = [display_width(lines[b[1]]) for b in blocks]
    if len(set(widths)) < 2:
        return set(), None
    min_w = min(widths)
    active_blocks = [blocks[bi] for bi, w in enumerate(widths) if w == min_w]
    if not active_blocks:
        return set(), None
    active_s, _ab, active_e = active_blocks[-1]
    active_canon = _canonicalize_dock_block_for_rewrap(lines[active_s : active_e + 1])

    stale_indices: set[int] = set()
    stale_blocks: list[tuple[int, int, int, int]] = []
    for bi, (s, b, e) in enumerate(blocks):
        w = widths[bi]
        if w <= min_w:
            continue
        cand_canon = _canonicalize_dock_block_for_rewrap(lines[s : e + 1])
        if cand_canon != active_canon:
            continue
        adj_start = s
        if bi > 0:
            prev_end = blocks[bi - 1][2]
            for k in range(prev_end + 1, s):
                if lines[k].strip():
                    # Only include an immediately preceding notice line if that exact line already
                    # appeared above the active dock (e.g. repeated `Plan mode enabled.` line).
                    if lines[k] in lines[:active_s]:
                        adj_start = min(adj_start, k)
        for k in range(adj_start, e + 1):
            stale_indices.add(k)
        stale_blocks.append((adj_start, e, w, min_w))
    if not stale_blocks:
        return set(), None
    return stale_indices, {
        "stale_line_count": sum(1 for k in stale_indices if lines[k].strip()),
        "stale_width": stale_blocks[0][2],
        "active_width": stale_blocks[0][3],
        "ranges": [[s, e] for s, e, _, _ in stale_blocks],
    }


def split_screen_regions(
    lines: list[str],
    *,
    side: str = "omp",
) -> tuple[list[str], list[str], dict[str, Any] | None]:
    """Split a screen capture into `(transcript_lines, dock_lines, stale_info)`.

    Stale pre-resize dock isolation runs ONLY on `side == "omp"` (Finding 15).
    """
    stale_idx, stale_info = find_stale_pre_resize_frame_indices(lines, side=side)
    active_lines = [l for i, l in enumerate(lines) if i not in stale_idx]
    while active_lines and not active_lines[-1].strip():
        active_lines.pop()
    blocks = find_dock_blocks(active_lines)
    if not blocks:
        return active_lines, [], stale_info
    d_start, _b, d_end = blocks[-1]
    transcript = active_lines[:d_start]
    dock = active_lines[d_start : d_end + 1]
    return transcript, dock, stale_info

def replay_raw_bytes(
    raw_bytes: bytes,
    rows: int = 40,
    cols: int = 120,
    *,
    resize_to: tuple[int, int] | None = None,
    stop_before_quit: bool = False,
) -> dict[str, Any]:
    """Replay a saved `.raw.bin` stream through VTScreen (with CSI 3J support) and return screen, scrollback, and clear counts.

    When `resize_to=(new_rows, new_cols)` is given (e.g. `(24, 80)` for the standard `ansi.sh`
    narrow80 resize step), locates the resize point in the stream (omp's `?1049h` reflow or
    PiG's final `CSI 2J` before 80-col output) and invokes `VTScreen.resize(new_rows, new_cols)`.
    When `stop_before_quit=True`, stops before post-capture `/quit` shutdown output (`Closing session…`).
    """
    txt = raw_bytes.decode("utf-8", "replace")
    end_pos = len(txt)
    if stop_before_quit:
        q_idx = txt.find("Closing session")
        if q_idx != -1:
            sync_start = txt.rfind("\x1b[?2026h", 0, q_idx)
            end_pos = sync_start if sync_start != -1 else q_idx
    txt = txt[:end_pos]

    vt = VTScreen(rows=rows, cols=cols)
    if resize_to is not None:
        nr, nc = resize_to
        resize_at = txt.find("\x1b[?1049h")
        if resize_at == -1:
            c2j_positions = [m.start() for m in re.finditer(r"\x1b\[2J", txt)]
            if len(c2j_positions) >= 2:
                resize_at = c2j_positions[-1]
        if resize_at != -1:
            vt.feed(txt[:resize_at])
            vt.resize(nr, nc)
            vt.feed(txt[resize_at:])
        else:
            vt.feed(txt)
    else:
        vt.feed(txt)

    return {
        "screen": vt.render(),
        "scrollback": vt.render_with_scrollback(),
        "screen_styled": vt.render_styled_rows(),
        "scrollback_styled": vt.render_styled_with_scrollback(),
        "scrollback_lines": len(vt.scrollback),
        "full_screen_clears": vt.full_screen_clears,
        "scrollback_clears": vt.scrollback_clears,
    }


# ---------------------------------------------------------------------------
# Validated colour palettes for animated/theme-checked cell SGR comparison
# ---------------------------------------------------------------------------

# Upstream `packages/tui/src/prompt/welcome.ts:550-585`:
#   GRADIENT_STOPS = [[248, 79, 204], [147, 98, 244], [0, 219, 228]]
#   gradientEscape(t, shine): piecewise-linear RGB interpolation across stops plus optional white shine
#   `c' = c + (255 - c) * intensity` with `0 <= intensity <= 1`.
OMP_WELCOME_GRADIENT_STOPS: tuple[tuple[int, int, int], ...] = (
    (248, 79, 204),
    (147, 98, 244),
    (0, 219, 228),
)
OMP_WELCOME_GRADIENT_RAMP_256: frozenset[str] = frozenset(
    f"256:{c}" for c in (206, 170, 134, 99, 69, 74, 44)
)


def _parse_rgb_token(tok: str | None) -> tuple[int, int, int] | None:
    if not isinstance(tok, str) or not tok.startswith("rgb:"):
        return None
    parts = tok[4:].split(";")
    if len(parts) != 3:
        return None
    try:
        r, g, b = int(parts[0]), int(parts[1]), int(parts[2])
    except ValueError:
        return None
    if not (0 <= r <= 255 and 0 <= g <= 255 and 0 <= b <= 255):
        return None
    return (r, g, b)


def is_in_omp_welcome_gradient_stops(sgr_tok: str | None) -> bool:
    """Verify that `sgr_tok` lies strictly on omp's `welcome.ts` 3-stop diagonal gradient (with optional white shine).

    Exact check against `GRADIENT_STOPS = [(248,79,204), (147,98,244), (0,219,228)]` from
    `packages/tui/src/prompt/welcome.ts:550-585` (or `GRADIENT_RAMP_256` for 256-color terminals).
    A colour outside the gradient/shine manifold (e.g. red `rgb:255;0;0` or green `rgb:0;255;0`) returns False.
    """
    if sgr_tok in OMP_WELCOME_GRADIENT_RAMP_256:
        return True
    rgb = _parse_rgb_token(sgr_tok)
    if rgb is None:
        return False
    tr, tg, tb = rgb
    # Check distance to piecewise-linear segment (a -> b) composited with white shine s in [0, 1]:
    #   c(t, s) = (1 - s) * ((1 - f)*a + f*b) + s * 255
    # Solve on dense grid of f in [0, 1] (step 0.002) and exact optimal s for each f, requiring L_inf <= 2
    # (rounding tolerance from `Math.round`).
    stops = OMP_WELCOME_GRADIENT_STOPS
    for seg_i in range(len(stops) - 1):
        a = stops[seg_i]
        b = stops[seg_i + 1]
        for step in range(501):
            f = step / 500.0
            br = a[0] + (b[0] - a[0]) * f
            bg = a[1] + (b[1] - a[1]) * f
            bb = a[2] + (b[2] - a[2]) * f
            # Optimal shine intensity s in [0, 1] projecting target onto ray from base toward (255,255,255)
            dr, dg, db = 255.0 - br, 255.0 - bg, 255.0 - bb
            denom = dr * dr + dg * dg + db * db
            if denom > 0:
                s = ((tr - br) * dr + (tg - bg) * dg + (tb - bb) * db) / denom
                s = max(0.0, min(1.0, s))
            else:
                s = 0.0
            pr = br + dr * s
            pg = bg + dg * s
            pb = bb + db * s
            if abs(pr - tr) <= 2.0 and abs(pg - tg) <= 2.0 and abs(pb - tb) <= 2.0:
                return True
    return False


# Upstream `packages/tui/src/theme/shimmer.ts:71-76` + titanium theme (`defaults.json`):
#   low="dim" (#6b7280), mid="muted" (#9ca3b0), high="accent" (#00b4ff), plus linear blends between them.
OMP_SPINNER_TWEEN_STOPS: tuple[tuple[int, int, int], ...] = (
    (107, 114, 128),  # dim
    (156, 163, 176),  # muted
    (0, 180, 255),    # accent
)


def is_in_omp_spinner_tween_stops(sgr_tok: str | None) -> bool:
    """Verify `sgr_tok` lies on omp's spinner/shimmer tween palette (dim -> muted -> accent)."""
    rgb = _parse_rgb_token(sgr_tok)
    if rgb is None:
        return False
    tr, tg, tb = rgb
    for i in range(len(OMP_SPINNER_TWEEN_STOPS)):
        for j in range(i + 1, len(OMP_SPINNER_TWEEN_STOPS)):
            a, b = OMP_SPINNER_TWEEN_STOPS[i], OMP_SPINNER_TWEEN_STOPS[j]
            dr, dg, db = b[0] - a[0], b[1] - a[1], b[2] - a[2]
            denom = dr * dr + dg * dg + db * db
            f = max(0.0, min(1.0, ((tr - a[0]) * dr + (tg - a[1]) * dg + (tb - a[2]) * db) / denom)) if denom else 0.0
            if abs(a[0] + dr * f - tr) <= 2.0 and abs(a[1] + dg * f - tg) <= 2.0 and abs(a[2] + db * f - tb) <= 2.0:
                return True
    return False


# Exact omp titanium syntax-highlighting foregrounds (`internal/ansi/theme.go:47-55`)
OMP_SYNTAX_HIGHLIGHT_FGS: frozenset[str] = frozenset({
    "rgb:107;114;128",  # syntaxComment
    "rgb:0;180;255",    # syntaxKeyword / syntaxType / syntaxOperator
    "rgb:0;255;136",    # syntaxFunction
    "rgb:232;236;244",  # syntaxVariable
    "rgb:212;192;144",  # syntaxString
    "rgb:255;179;71",   # syntaxNumber
    "rgb:156;163;176",  # syntaxPunctuation
})

# Exact omp titanium host-markdown / thinking / user-bubble colours (`internal/ansi/UPSTREAM.md:90`)
OMP_HOST_MARKDOWN_FGS: frozenset[str] = frozenset({
    "rgb:240;192;64",   # mdHeading / h2
    "rgb:208;111;237",  # mdBold
    "rgb:0;255;136",    # mdCode
    "rgb:0;180;255",    # mdListBullet / mdHeading
    "rgb:42;48;56",     # mdCodeBlockBorder
    "rgb:156;163;176",  # thinkingText / mdCodeBlock
})
PIG_HOST_MARKDOWN_FGS: frozenset[str | None] = frozenset({
    None,
    "256:1", "256:3", "256:4", "256:5", "256:6",
})

NAMED_COLOUR_PALETTES: dict[str, Any] = {
    "omp_welcome_gradient": is_in_omp_welcome_gradient_stops,
    "omp_spinner_tween": is_in_omp_spinner_tween_stops,
    "omp_syntax_fg": lambda tok: tok in OMP_SYNTAX_HIGHLIGHT_FGS,
    "omp_host_markdown_fg": lambda tok: tok in OMP_HOST_MARKDOWN_FGS,
    "pig_host_markdown_fg": lambda tok: tok in PIG_HOST_MARKDOWN_FGS,
}


def _match_colour_spec(spec: Any, actual: str | None) -> bool:
    """Match an actual SGR token (`None`, `'rgb:...'`, `'256:...'`, `'ansi:...'`) against a pinned allowlist spec."""
    if spec == "$any":
        return False  # Wildcards are explicitly forbidden!
    if isinstance(spec, str) and spec.startswith("@"):
        pred = NAMED_COLOUR_PALETTES.get(spec[1:])
        return bool(pred and pred(actual))
    if isinstance(spec, list):
        return any(_match_colour_spec(item, actual) for item in spec)
    return actual == spec


def _terminal_entry_subject(entry: dict[str, Any]) -> str | list[str] | None:
    """Default required_subject extractor for terminal_allowlist entries without explicit `ref_subject`."""
    mk = entry.get("match_kind")
    if mk:
        return mk.replace("_", " ")
    return entry.get("name")


def load_terminal_allowlist(path: Path | None = None) -> list[dict[str, Any]]:
    p = path or DEFAULT_TERMINAL_ALLOWLIST_PATH
    if not p.is_file():
        return []
    entries = json.loads(p.read_text(encoding="utf-8"))
    if not isinstance(entries, list):
        raise ValueError(f"terminal_allowlist must be a JSON array: {p}")
    resolved = resolve_allowlist_entries(entries, source_name=p.name, subject_extractor=_terminal_entry_subject)
    compiled: list[dict[str, Any]] = []
    for i, e in enumerate(resolved):
        for req in ("name", "class", "ref", "why"):
            if not isinstance(e.get(req), str) or not e[req].strip():
                raise ValueError(f"terminal_allowlist[{i}] missing required string field {req!r}")
        kind = e.get("kind", "text")
        if kind == "colour":
            if not isinstance(e.get("regions"), list) or not e["regions"]:
                raise ValueError(f"terminal_allowlist[{i}] colour entry must specify non-empty 'regions'")
            if not any(k in e for k in ("omp_fg", "omp_bg", "omp_attrs", "pig_fg", "pig_bg", "pig_attrs")):
                raise ValueError(f"terminal_allowlist[{i}] colour entry must pin omp/pig fg, bg, or attrs")
            compiled.append({**e, "kind": "colour", "_re": None, "_omp_re": None, "_pig_re": None})
            continue
        if kind == "blank_row":
            if not isinstance(e.get("hunk_names"), list) or not e["hunk_names"]:
                raise ValueError(f"terminal_allowlist[{i}] blank_row entry must pin 'hunk_names'")
            if e.get("side") not in ("omp", "pig", "both"):
                raise ValueError(f"terminal_allowlist[{i}] blank_row entry must pin side ('omp'|'pig'|'both')")
            compiled.append({**e, "kind": "blank_row", "_re": None, "_omp_re": None, "_pig_re": None})
            continue
        if kind == "terminal_mode":
            if e.get("mode") not in ("alt_screen_resize_borrow",):
                raise ValueError(f"terminal_allowlist[{i}] terminal_mode entry has unsupported mode {e.get('mode')!r}")
            compiled.append({**e, "kind": "terminal_mode", "_re": None, "_omp_re": None, "_pig_re": None})
            continue
        mk = e.get("match_kind")
        if mk in ("welcome_tip_block", "bash_shell_visualizer_pair", "prompt_editor_frame", "stale_pre_resize_frame"):
            compiled.append({**e, "kind": "text", "_re": None, "_omp_re": None, "_pig_re": None})
        elif mk == "provider_error_with_raw_http_log":
            omp_rx = e.get("omp_regex")
            pig_rx = e.get("pig_regex")
            if omp_rx is not None and (not isinstance(omp_rx, str) or not omp_rx.strip()):
                raise ValueError(f"terminal_allowlist[{i}] invalid 'omp_regex'")
            if pig_rx is not None and (not isinstance(pig_rx, str) or not pig_rx.strip()):
                raise ValueError(f"terminal_allowlist[{i}] invalid 'pig_regex'")
            compiled.append({
                **e,
                "kind": "text",
                "_re": None,
                "_omp_re": re.compile(omp_rx) if omp_rx else None,
                "_pig_re": re.compile(pig_rx) if pig_rx else None,
            })
        else:
            omp_rx = e.get("omp_regex")
            pig_rx = e.get("pig_regex")
            any_rx = e.get("regex")
            if omp_rx is not None or pig_rx is not None:
                if omp_rx is not None and (not isinstance(omp_rx, str) or not omp_rx.strip()):
                    raise ValueError(f"terminal_allowlist[{i}] invalid 'omp_regex'")
                if pig_rx is not None and (not isinstance(pig_rx, str) or not pig_rx.strip()):
                    raise ValueError(f"terminal_allowlist[{i}] invalid 'pig_regex'")
                compiled.append({
                    **e,
                    "kind": "text",
                    "_re": None,
                    "_omp_re": re.compile(omp_rx) if omp_rx else None,
                    "_pig_re": re.compile(pig_rx) if pig_rx else None,
                })
            else:
                if not isinstance(any_rx, str) or not any_rx.strip():
                    raise ValueError(
                        f"terminal_allowlist[{i}] must specify 'omp_regex'/'pig_regex', 'regex', or a valid 'match_kind'"
                    )
                compiled.append({**e, "kind": "text", "_re": re.compile(any_rx), "_omp_re": None, "_pig_re": None})
    return compiled


def _match_bash_shell_viz_pairs(changed_nonblank: list[tuple[str, int | None, int | None]]) -> set[int]:
    """Match paired omp-visualizer (`-`) and port-fallback (`+`) bash command rows inside one hunk (Findings 19, 25)."""
    omp_by_pair: dict[int, list[int]] = {}
    pig_by_pair: dict[int, list[int]] = {}
    for pos, (cl, _ai, _bi) in enumerate(changed_nonblank):
        body = cl[1:]
        if not (body.startswith("│") and body.endswith("│")):
            continue
        inner = body[1:-1].strip()
        if cl.startswith("-"):
            for pi, (omp_exp, _pig_exp) in enumerate(BASH_SHELL_VIZ_EXACT_PAIRS):
                if inner == omp_exp:
                    omp_by_pair.setdefault(pi, []).append(pos)
                    break
        elif cl.startswith("+"):
            for pi, (_omp_exp, pig_exp) in enumerate(BASH_SHELL_VIZ_EXACT_PAIRS):
                if inner == pig_exp:
                    pig_by_pair.setdefault(pi, []).append(pos)
                    break
    matched_pos: set[int] = set()
    for pi in set(omp_by_pair) & set(pig_by_pair):
        o_list = omp_by_pair[pi]
        p_list = pig_by_pair[pi]
        k = min(len(o_list), len(p_list))
        for idx in range(k):
            matched_pos.add(o_list[idx])
            matched_pos.add(p_list[idx])
    return matched_pos


def _label_terminal_lines_by_region(lines: list[str]) -> list[str]:
    """Assign a semantic region label to every line in a terminal screen/scrollback capture.

    Regions:
      - `welcome`: logo, wordmark, version, and tip rows at startup
      - `card:<tool>`: inside a framed tool card (`card:bash`, `card:edit`, `card:todo`, `card:ask`,
        `card:eval`, `card:task`, `card:web_search`, `card:write`, `card:ast_edit`, `card:unknown`)
      - `inline:<tool>`: inline tool rows (`inline:read`, `inline:grep`, `inline:glob`, `inline:lsp`, `inline:wait`)
      - `dock:todo`: TODO HUD lines above the composer
      - `dock:error_banner`: pinned error banner lines above the composer
      - `status`: powerline status band (`π > ... 128K─`)
      - `editor`: prompt / stock-editor frame below the status band
      - `user_prompt`: user prompt transcript lines (known scripted prompts or 1-space-indented prompt lines)
      - `transcript:markdown`: assistant markdown / thinking / status notice lines
    """
    n = len(lines)
    labels = ["transcript:markdown"] * n

    # 1. Mark dock blocks (status band, editor, todo HUD, error banner)
    dock_blocks = find_dock_blocks(lines)
    for d_start, b_idx, d_end in dock_blocks:
        labels[b_idx] = "status"
        for k in range(b_idx + 1, d_end + 1):
            labels[k] = "editor"
        raw_http_idx = find_omp_raw_http_request_line_indices(lines, side="omp")
        in_err = False
        in_todo = False
        for k in range(d_start, b_idx):
            s = lines[k].strip()
            if s.startswith("TODO"):
                in_todo = True
                in_err = False
            elif _RE_RULE_LINE.match(s):
                in_err = not in_err if (in_err or any("✘" in lines[m] for m in range(k, b_idx))) else False
                labels[k] = "dock:error_banner"
                continue
            if in_err or k in raw_http_idx or s.startswith(("✘", "Dismissed")):
                labels[k] = "dock:error_banner"
                in_err = True
                in_todo = False
            elif in_todo or s.startswith(("├─", "│", "└─────")):
                labels[k] = "dock:todo"
            elif not s:
                labels[k] = "dock:blank"
    # 2. Mark welcome banner lines (logo/wordmark/version/tip at top of transcript)
    for i in range(min(n, 20)):
        s = lines[i].strip()
        if any(ch in s for ch in ("████", "▄▀▀▄", "▀▄▄▀", "▒▒")) or _RE_WELCOME_VERSION_ROW.match(lines[i]):
            labels[i] = "welcome"
    for ti in find_welcome_tip_line_indices(lines, side="omp") | find_welcome_tip_line_indices(lines, side="pig"):
        labels[ti] = "welcome"

    # 3. Mark framed tool cards `╭─── ... ╮` .. `╰─── ... ╯`
    def _card_tool_from_top(line: str) -> str:
        if "Edit:" in line and "AST Edit" not in line:
            return "edit"
        if "AST Edit" in line:
            return "ast_edit"
        if "Write:" in line:
            return "write"
        if "Todo " in line:
            return "todo"
        if "? Ask" in line:
            return "ask"
        if "🐍" in line or "eval" in line.lower():
            return "eval"
        if "Task " in line:
            return "task"
        if "Web Search" in line:
            return "web_search"
        if line.startswith("╭───"):
            return "bash"
        return "unknown"

    i = 0
    while i < n:
        if lines[i].startswith("╭") and lines[i].endswith("╮"):
            tool = _card_tool_from_top(lines[i])
            j = i
            while j < n:
                labels[j] = f"card:{tool}"
                if j > i and lines[j].startswith("╰") and lines[j].endswith("╯"):
                    break
                j += 1
            i = j + 1
            continue
        # Inline tool lines
        if lines[i].startswith((" ✔ Read ", " ✘ Read ", "✔ Read ", "✘ Read ")):
            labels[i] = "inline:read"
        elif lines[i].startswith("🔍 Grep:"):
            labels[i] = "inline:grep"
            j = i + 1
            while j < n and (lines[j].startswith(("└─", "├─", "   ")) and lines[j].strip()):
                labels[j] = "inline:grep"
                j += 1
            i = j
            continue
        elif lines[i].startswith("🔍 Glob:"):
            labels[i] = "inline:glob"
            j = i + 1
            while j < n and lines[j].startswith(("├─", "└─")):
                labels[j] = "inline:glob"
                j += 1
            i = j
            continue
        elif lines[i].startswith("💡 LSP:"):
            labels[i] = "inline:lsp"
            if i + 1 < n and lines[i + 1].startswith("└─"):
                labels[i + 1] = "inline:lsp"
                i += 2
                continue
        elif lines[i].startswith(("✔ 1 job settled", "✔ ")) and "job" in lines[i]:
            labels[i] = "inline:wait"
            j = i + 1
            while j < n and lines[j].startswith(("└─", "├─", "     ")):
                labels[j] = "inline:wait"
                j += 1
            i = j
            continue
        elif lines[i].startswith(" Plan mode enabled.") or lines[i].startswith(" Tool output"):
            labels[i] = "transcript:status"
        i += 1

    return labels


def _classify_blank_row(
    cl: str,
    hunk_matched_names: set[str],
    hunk_matched_classes: set[str],
    hunk_has_unexplained_text: bool,
    entries: list[dict[str, Any]],
    entry_hits: dict[int, int],
) -> dict[str, Any]:
    """Classify a single changed blank row (`-` or `+` with whitespace-only body) via `blank_row` entries."""
    side = "omp" if cl.startswith("-") else "pig"
    # Only allow blank rows that belong to a classified hunk whose non-blank lines are allowlisted
    # and match a `blank_row` entry naming one of the hunk's matched rule names.
    if not hunk_has_unexplained_text and hunk_matched_names:
        for ei, e in enumerate(entries):
            if e.get("kind") != "blank_row":
                continue
            if e["side"] not in (side, "both"):
                continue
            if any(hn in hunk_matched_names for hn in e["hunk_names"]):
                entry_hits[ei] = entry_hits.get(ei, 0) + 1
                return {
                    "side": side,
                    "status": "expected",
                    "name": e["name"],
                    "class": e["class"],
                    "ref": e["ref"],
                    "hunk_names": sorted(hunk_matched_names),
                }
    return {
        "side": side,
        "status": "unexplained",
        "hunk_names": sorted(hunk_matched_names),
    }


# Bounded terminal duration unit-crossing helper (1.0s boundary under machine load):
# Matches a pair of `-`/`+` lines in one hunk ONLY when:
#   - one line carries `<DUR>ms` and the other `<DUR>s` in the SAME duration slot (`⏱`, `· (`, `· `),
#   - replacing `<DUR>ms` and `<DUR>s` with `<DUR>` (and collapsing equal-width box fill) makes the
#     two lines IDENTICAL (nothing else on the line differs), AND
#   - when raw lines are supplied, the `s`-side raw value is strictly bounded near the 1.0s boundary
#     (`1.0s <= sec <= MAX_TERMINAL_TIMING_CROSS_S`, default 5.0s; so `71ms` vs `710s` is NEVER timing!).
MAX_TERMINAL_TIMING_CROSS_S = 5.0
_RE_DUR_TOKEN_MASKED = re.compile(r"<DUR>(ms|s)")
_RE_RAW_DUR_VALUE = re.compile(r"(?:⏱\s*|·\s*\(|·\s*)(\d+(?:\.\d+)?)(ms|s)\b")


def _parse_raw_line_dur_seconds(raw_line: str | None) -> list[tuple[float, str]]:
    if not raw_line:
        return []
    out: list[tuple[float, str]] = []
    for m in _RE_RAW_DUR_VALUE.finditer(raw_line):
        val = float(m.group(1))
        unit = m.group(2)
        sec = val / 1000.0 if unit == "ms" else val
        out.append((sec, unit))
    return out


def _match_timing_unit_crossing_pairs(
    nonblank: list[tuple[str, int | None, int | None]],
    omp_raw_lines: list[str] | None = None,
    pig_raw_lines: list[str] | None = None,
) -> dict[int, dict[str, Any]]:
    """Detect bounded 1.0s duration unit-crossing pairs (`<DUR>ms` vs `<DUR>s`) inside one hunk."""
    omp_cands: list[tuple[int, str, str, int | None]] = []
    pig_cands: list[tuple[int, str, str, int | None]] = []
    for pos, (cl, ai, bi) in enumerate(nonblank):
        body = cl[1:]
        units = _RE_DUR_TOKEN_MASKED.findall(body)
        if not units:
            continue
        skeleton = _collapse_box_fill_for_comparison(_RE_DUR_TOKEN_MASKED.sub("<DUR>", body))
        if cl.startswith("-"):
            omp_cands.append((pos, skeleton, units[0], ai))
        elif cl.startswith("+"):
            pig_cands.append((pos, skeleton, units[0], bi))

    used_pig: set[int] = set()
    matched: dict[int, dict[str, Any]] = {}
    for opos, oskel, ounit, ai in omp_cands:
        for ppos, pskel, punit, bi in pig_cands:
            if ppos in used_pig:
                continue
            if oskel != pskel or ounit == punit:
                continue
            o_raw = omp_raw_lines[ai] if (omp_raw_lines is not None and ai is not None and 0 <= ai < len(omp_raw_lines)) else None
            p_raw = pig_raw_lines[bi] if (pig_raw_lines is not None and bi is not None and 0 <= bi < len(pig_raw_lines)) else None
            if o_raw is not None and p_raw is not None:
                if display_width(o_raw) != display_width(p_raw):
                    continue
                od = _parse_raw_line_dur_seconds(o_raw)
                pd = _parse_raw_line_dur_seconds(p_raw)
                if not od or not pd:
                    continue
                o_sec, _ = od[0]
                p_sec, _ = pd[0]
                lo, hi = min(o_sec, p_sec), max(o_sec, p_sec)
                if not (lo < 1.0 and 1.0 <= hi <= MAX_TERMINAL_TIMING_CROSS_S):
                    continue
            used_pig.add(ppos)
            info = {
                "omp_line": nonblank[opos][0],
                "pig_line": nonblank[ppos][0],
                "omp_raw": o_raw,
                "pig_raw": p_raw,
                "omp_unit": ounit,
                "pig_unit": punit,
            }
            matched[opos] = info
            matched[ppos] = info
            break
    return matched


def classify_terminal_diff(
    omp_text: str,
    pig_text: str,
    allowlist: list[dict[str, Any]] | None = None,
    *,
    omp_raw_text: str | None = None,
    pig_raw_text: str | None = None,
) -> dict[str, Any]:
    """Classify every unified-diff hunk as `expected`, `timing`, or `differs`, classifying BOTH non-blank lines AND blank rows.

    - Every non-blank changed line must match a `text` allowlist rule OR a bounded 1.0s duration
      unit-crossing pair (`timing`, reported separately; exits non-zero unless `--allow-timing`).
    - Every changed blank row must belong to a classified hunk matching a `blank_row` entry in
      `terminal_allowlist.json`; any blank row outside those is counted as `blank_unexplained`.
    """
    entries = allowlist if allowlist is not None else load_terminal_allowlist()
    a_lines = omp_text.splitlines()
    b_lines = pig_text.splitlines()
    a_raw_lines = omp_raw_text.splitlines() if omp_raw_text is not None else None
    b_raw_lines = pig_raw_text.splitlines() if pig_raw_text is not None else None
    a_tip_lines = find_welcome_tip_line_indices(a_lines, side="omp")
    b_tip_lines = find_welcome_tip_line_indices(b_lines, side="pig")
    a_prompt_lines = find_prompt_editor_frame_line_indices(a_lines, side="omp")
    b_prompt_lines = find_prompt_editor_frame_line_indices(b_lines, side="pig")
    a_stale_lines, a_stale_info = find_stale_pre_resize_frame_indices(a_lines, side="omp")
    a_raw_http_lines = find_omp_raw_http_request_line_indices(a_lines, side="omp")
    ud = list(difflib.unified_diff(a_lines, b_lines, fromfile="omp", tofile="oh-my-pig", lineterm=""))
    _re_hunk_hdr = re.compile(r"^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@")
    hunks: list[dict[str, Any]] = []
    cur: dict[str, Any] | None = None
    a_idx = b_idx = 0
    start_i = 2 if (len(ud) >= 2 and ud[0].startswith("--- ") and ud[1].startswith("+++ ")) else 0
    for line in ud[start_i:]:
        if line.startswith("@@"):
            if cur is not None:
                hunks.append(cur)
            m = _re_hunk_hdr.match(line)
            a_idx = int(m.group(1)) - 1 if m else 0
            b_idx = int(m.group(2)) - 1 if m else 0
            cur = {"header": line, "items": []}
        elif cur is not None:
            if line.startswith("-"):
                cur["items"].append((line, a_idx, None))
                a_idx += 1
            elif line.startswith("+"):
                cur["items"].append((line, None, b_idx))
                b_idx += 1
            else:
                cur["items"].append((line, a_idx, b_idx))
                a_idx += 1
                b_idx += 1
    if cur is not None:
        hunks.append(cur)

    entry_hits: dict[int, int] = {i: 0 for i in range(len(entries))}
    classified_hunks: list[dict[str, Any]] = []
    total_blank_removed = 0
    total_blank_added = 0
    blank_expected_count = 0
    blank_unexplained_items: list[dict[str, Any]] = []
    timing_items: list[dict[str, Any]] = []
    blank_only_hunks = 0

    for idx, h in enumerate(hunks):
        changed = [it for it in h["items"] if it[0].startswith(("+", "-"))]
        blank_changed = [it for it in changed if not it[0][1:].strip()]
        blank_rem = sum(1 for cl, _ai, _bi in blank_changed if cl.startswith("-"))
        blank_add = sum(1 for cl, _ai, _bi in blank_changed if cl.startswith("+"))
        total_blank_removed += blank_rem
        total_blank_added += blank_add

        nonblank = [it for it in changed if it[0][1:].strip()]
        bash_viz_matched = _match_bash_shell_viz_pairs(nonblank)
        timing_matched = _match_timing_unit_crossing_pairs(nonblank, a_raw_lines, b_raw_lines)

        matched: list[dict[str, Any]] = []
        hunk_timing: list[dict[str, Any]] = []
        unexplained: list[str] = []
        for pos, (cl, ai, bi) in enumerate(nonblank):
            body = cl[1:]
            is_omp = cl.startswith("-")
            is_pig = cl.startswith("+")
            in_tip_block = (is_omp and ai is not None and ai in a_tip_lines) or (
                is_pig and bi is not None and bi in b_tip_lines
            )
            in_prompt_frame = (is_omp and ai is not None and ai in a_prompt_lines) or (
                is_pig and bi is not None and bi in b_prompt_lines
            )
            in_stale_frame = is_omp and ai is not None and ai in a_stale_lines
            in_omp_raw_http = is_omp and ai is not None and ai in a_raw_http_lines
            hit_idx: int | None = None
            for ei, e in enumerate(entries):
                if e.get("kind", "text") != "text":
                    continue
                mk = e.get("match_kind")
                if mk == "welcome_tip_block":
                    if in_tip_block:
                        hit_idx = ei
                        break
                elif mk == "bash_shell_visualizer_pair":
                    if pos in bash_viz_matched:
                        hit_idx = ei
                        break
                elif mk == "prompt_editor_frame":
                    if in_prompt_frame:
                        hit_idx = ei
                        break
                elif mk == "stale_pre_resize_frame":
                    if in_stale_frame:
                        hit_idx = ei
                        break
                elif mk == "provider_error_with_raw_http_log":
                    if in_omp_raw_http:
                        hit_idx = ei
                        break
                    if is_omp and e.get("_omp_re") is not None and e["_omp_re"].search(body):
                        hit_idx = ei
                        break
                    if is_pig and e.get("_pig_re") is not None and e["_pig_re"].search(body):
                        hit_idx = ei
                        break
                elif e.get("_omp_re") is not None or e.get("_pig_re") is not None:
                    if is_omp and e.get("_omp_re") is not None and e["_omp_re"].search(body):
                        hit_idx = ei
                        break
                    if is_pig and e.get("_pig_re") is not None and e["_pig_re"].search(body):
                        hit_idx = ei
                        break
                elif e.get("_re") is not None and e["_re"].search(body):
                    hit_idx = ei
                    break
            if hit_idx is not None:
                entry_hits[hit_idx] += 1
                e = entries[hit_idx]
                matched.append({
                    "line": cl,
                    "name": e["name"],
                    "class": e["class"],
                    "class_in_doubt": bool(e.get("class_in_doubt")),
                    "ref": e["ref"],
                })
            elif pos in timing_matched:
                trec = {"hunk_index": idx, "header": h["header"], "line": cl, **timing_matched[pos]}
                hunk_timing.append(trec)
                if is_omp:
                    timing_items.append(trec)
            else:
                unexplained.append(cl)

        hunk_names = {m["name"] for m in matched}
        hunk_classes = {m["class"] for m in matched}
        hunk_blank_matched = 0
        hunk_blank_unexplained: list[dict[str, Any]] = []
        for cl, ai, bi in blank_changed:
            bres = _classify_blank_row(cl, hunk_names, hunk_classes, bool(unexplained), entries, entry_hits)
            if bres["status"] == "expected":
                hunk_blank_matched += 1
                blank_expected_count += 1
            else:
                rec = {
                    **bres,
                    "hunk_index": idx,
                    "header": h["header"],
                    "omp_line": ai,
                    "pig_line": bi,
                    "context": [it[0] for it in h["items"][:6]],
                }
                hunk_blank_unexplained.append(rec)
                blank_unexplained_items.append(rec)

        if not nonblank:
            blank_only_hunks += 1
            verdict = "differs" if hunk_blank_unexplained else "expected"
            classes: list[str] = []
            refs: list[str] = []
        elif not unexplained and not hunk_blank_unexplained and hunk_timing:
            verdict = "timing"
            classes = sorted({m["class"] for m in matched} | {"TIMING"})
            refs = sorted({m["ref"] for m in matched})
        elif not unexplained and not hunk_blank_unexplained and matched:
            verdict = "expected"
            classes = sorted({m["class"] for m in matched})
            refs = sorted({m["ref"] for m in matched})
        else:
            verdict = "differs"
            classes = sorted({m["class"] for m in matched})
            refs = sorted({m["ref"] for m in matched})
        classified_hunks.append({
            "index": idx,
            "header": h["header"],
            "verdict": verdict,
            "classes": classes,
            "refs": refs,
            "names": sorted(hunk_names),
            "matched_count": len(matched),
            "timing_count": len(hunk_timing),
            "unexplained_count": len(unexplained),
            "blank_removed": blank_rem,
            "blank_added": blank_add,
            "blank_expected": hunk_blank_matched,
            "blank_unexplained": len(hunk_blank_unexplained),
            "matched": matched,
            "timing": hunk_timing,
            "unexplained": unexplained,
        })

    expected_hunks = sum(1 for h in classified_hunks if h["verdict"] == "expected")
    timing_hunks = sum(1 for h in classified_hunks if h["verdict"] == "timing")
    differs_hunks = sum(1 for h in classified_hunks if h["verdict"] == "differs")
    class_counts: dict[str, int] = {}
    class_in_doubt_lines = 0
    for h in classified_hunks:
        for m in h["matched"]:
            c = m["class"]
            class_counts[c] = class_counts.get(c, 0) + 1
            if m.get("class_in_doubt"):
                class_in_doubt_lines += 1
    overall = (
        "same"
        if not classified_hunks
        else ("differs" if differs_hunks > 0 else ("timing" if timing_hunks > 0 else "expected"))
    )
    stale_report = {"omp": a_stale_info, "pig": None} if a_stale_info else None
    return {
        "verdict": overall,
        "summary": {
            "total_hunks": len(classified_hunks),
            "expected": expected_hunks,
            "timing": timing_hunks,
            "differs": differs_hunks,
            "blank_only": blank_only_hunks,
            "matched_lines": sum(h["matched_count"] for h in classified_hunks),
            "matched_by_class": class_counts,
            "class_in_doubt_lines": class_in_doubt_lines,
            "timing_lines": sum(h["timing_count"] for h in classified_hunks),
            "timing_pairs": len(timing_items),
            "unexplained_lines": sum(h["unexplained_count"] for h in classified_hunks),
            "blank_expected": blank_expected_count,
            "blank_unexplained": len(blank_unexplained_items),
        },
        "timing_diff": {
            "pairs": len(timing_items),
            "items": timing_items,
            "max_seconds": MAX_TERMINAL_TIMING_CROSS_S,
        },
        "blank_row_diff": {
            "omp_blank_lines_removed": total_blank_removed,
            "pig_blank_lines_added": total_blank_added,
            "total_blank_rows_changed": total_blank_removed + total_blank_added,
            "expected": blank_expected_count,
            "unexplained": len(blank_unexplained_items),
            "unexplained_items": blank_unexplained_items,
            "blank_only_hunks": blank_only_hunks,
        },
        "stale_pre_resize_frame": stale_report,
        "hunks": classified_hunks,
        "allowlist_usage": [
            {
                "name": e["name"],
                "kind": e.get("kind", "text"),
                "class": e["class"],
                "class_in_doubt": bool(e.get("class_in_doubt")),
                "optional": bool(e.get("optional")),
                "ref": e["ref"],
                "hits": entry_hits.get(i, 0),
                "stale": (entry_hits.get(i, 0) == 0) and not bool(e.get("optional")),
            }
            for i, e in enumerate(entries)
        ],
    }

def classify_cell_colours(
    omp_styled_rows: list[list[Any]],
    pig_styled_rows: list[list[Any]],
    allowlist: list[dict[str, Any]] | None = None,
    *,
    screen_regions: bool = False,
) -> dict[str, Any]:
    """Compare per-cell `(fg, bg)` SGR on every line whose TEXT matches between omp and oh-my-pig.

    - Lines whose text itself differs follow the text hunk allowlist (not double-counted here).
    - On lines whose normalized text is equal, every contiguous run of cells with identical text
      characters and differing `(fg, bg)` (ignoring foreground colour on plain spaces when
      background is identical) is reported with `(region, omp_line, pig_line, col, text, omp_fg,
      omp_bg, pig_fg, pig_bg)`.
    - Each run is classified through `kind: "colour"` entries in `terminal_allowlist.json` (which
      pin exact colours or validated named palette sets such as `@omp_welcome_gradient`,
      `@omp_spinner_tween`, `@omp_syntax_fg`, `@omp_host_markdown_fg`, `@pig_host_markdown_fg`).
    - Animated frames (welcome gradient sweep, spinner tween) are checked via set membership in the
      exact validated stops derived from omp's source (`welcome.ts`, `shimmer.ts`).
    - Any run not matched by a pinned entry is `unexplained` and fails the overall verdict/exit code.
    """
    entries = allowlist if allowlist is not None else load_terminal_allowlist()

    def _row_text(sr: list[Any]) -> str:
        return sr[0] if sr else ""

    if screen_regions:
        # Align regions identically to classify_screen_regions so final-screen cells align 1:1
        a_raw_lines = [_row_text(r) for r in omp_styled_rows]
        b_raw_lines = [_row_text(r) for r in pig_styled_rows]
        a_stale_idx, _ = find_stale_pre_resize_frame_indices(a_raw_lines, side="omp")
        a_active = [(i, r) for i, r in enumerate(omp_styled_rows) if i not in a_stale_idx]
        b_active = list(enumerate(pig_styled_rows))
        while a_active and not _row_text(a_active[-1][1]).strip():
            a_active.pop()
        while b_active and not _row_text(b_active[-1][1]).strip():
            b_active.pop()
        omp_rows_use = [r for _, r in a_active]
        pig_rows_use = [r for _, r in b_active]
    else:
        omp_rows_use = list(omp_styled_rows)
        pig_rows_use = list(pig_styled_rows)

    a_txts = [_row_text(r) for r in omp_rows_use]
    b_txts = [_row_text(r) for r in pig_rows_use]
    a_regions = _label_terminal_lines_by_region(a_txts)
    b_regions = _label_terminal_lines_by_region(b_txts)

    a_norm_s, b_norm_s = width_preserving_normalize_pair("\n".join(a_txts), "\n".join(b_txts))
    a_norm = a_norm_s.splitlines()
    b_norm = b_norm_s.splitlines()
    # Ensure lengths match even on trailing empty rows
    while len(a_norm) < len(a_txts):
        a_norm.append("")
    while len(b_norm) < len(b_txts):
        b_norm.append("")

    sm = difflib.SequenceMatcher(None, a_norm, b_norm, autojunk=False)
    runs_all: list[dict[str, Any]] = []
    entry_hits: dict[int, int] = {i: 0 for i in range(len(entries))}
    matched_by_class: dict[str, int] = {}
    lines_compared = 0

    for tag, i1, i2, j1, j2 in sm.get_opcodes():
        if tag != "equal":
            continue
        for k in range(i2 - i1):
            ai, bj = i1 + k, j1 + k
            line_txt = a_txts[ai]
            if not line_txt.strip():
                continue
            lines_compared += 1
            ca_cells = _decode_styled_row(omp_rows_use[ai])
            cb_cells = _decode_styled_row(pig_rows_use[bj])
            region = a_regions[ai] if ai < len(a_regions) else "transcript:markdown"
            if region == "transcript:markdown" and line_txt.startswith(" ") and not line_txt.startswith("  "):
                if any(sgr[1] == "rgb:15;18;22" for _, sgr in ca_cells):
                    region = "user_prompt"

            n_cells = min(len(ca_cells), len(cb_cells))
            cur: dict[str, Any] | None = None
            for col in range(n_cells):
                ch_a, (fa, ba, aa) = ca_cells[col]
                ch_b, (fb, bb, ab) = cb_cells[col]
                if ch_a != ch_b:
                    if cur is not None:
                        runs_all.append(cur)
                        cur = None
                    continue
                if ch_a == "":
                    continue
                if (fa, ba, aa) == (fb, bb, ab):
                    if cur is not None:
                        runs_all.append(cur)
                        cur = None
                    continue
                if ch_a == " " and ba == bb:
                    vis_a = tuple(x for x in aa if x in ("underline", "strike", "inverse"))
                    vis_b = tuple(x for x in ab if x in ("underline", "strike", "inverse"))
                    if vis_a == vis_b:
                        if cur is not None:
                            runs_all.append(cur)
                            cur = None
                        continue
                sig = (fa, ba, aa, fb, bb, ab)
                if cur is not None and cur["_sig"] == sig and cur["col"] + cur["len"] == col:
                    cur["text"] += ch_a
                    cur["len"] += 1
                else:
                    if cur is not None:
                        runs_all.append(cur)
                    cur = {
                        "_sig": sig,
                        "region": region,
                        "omp_line": ai,
                        "pig_line": bj,
                        "col": col,
                        "len": 1,
                        "text": ch_a,
                        "line_text": line_txt[:80],
                        "omp_fg": fa,
                        "omp_bg": ba,
                        "omp_attrs": list(aa),
                        "pig_fg": fb,
                        "pig_bg": bb,
                        "pig_attrs": list(ab),
                    }
            if cur is not None:
                runs_all.append(cur)

    def _match_attrs_spec(spec: Any, actual: list[str]) -> bool:
        act_sorted = sorted(actual)
        if isinstance(spec, list) and all(isinstance(x, str) for x in spec):
            return act_sorted == sorted(spec)
        if isinstance(spec, list) and all(isinstance(x, list) for x in spec):
            return any(act_sorted == sorted(opt) for opt in spec)
        return False

    expected_runs: list[dict[str, Any]] = []
    unexplained_runs: list[dict[str, Any]] = []
    for r in runs_all:
        r.pop("_sig", None)
        hit_idx: int | None = None
        for ei, e in enumerate(entries):
            if e.get("kind") != "colour":
                continue
            regs = e["regions"]
            if r["region"] not in regs and not any(reg.endswith(":*") and r["region"].startswith(reg[:-1]) for reg in regs):
                continue
            if "text_regex" in e and not re.search(e["text_regex"], r["text"]):
                continue
            if "line_regex" in e and not re.search(e["line_regex"], r["line_text"]):
                continue
            if "omp_fg" in e and not _match_colour_spec(e["omp_fg"], r["omp_fg"]):
                continue
            if "omp_bg" in e and not _match_colour_spec(e["omp_bg"], r["omp_bg"]):
                continue
            if "pig_fg" in e and not _match_colour_spec(e["pig_fg"], r["pig_fg"]):
                continue
            if "pig_bg" in e and not _match_colour_spec(e["pig_bg"], r["pig_bg"]):
                continue
            if "omp_attrs" in e:
                if not _match_attrs_spec(e["omp_attrs"], r["omp_attrs"]):
                    continue
            if "pig_attrs" in e:
                if not _match_attrs_spec(e["pig_attrs"], r["pig_attrs"]):
                    continue
            if "omp_attrs" not in e and "pig_attrs" not in e and r["omp_attrs"] != r["pig_attrs"]:
                continue
            if e.get("fg_must_match") is True and r["omp_fg"] != r["pig_fg"]:
                continue
            if e.get("bg_must_match") is True and r["omp_bg"] != r["pig_bg"]:
                continue
            hit_idx = ei
            break
        if hit_idx is not None:
            e = entries[hit_idx]
            entry_hits[hit_idx] += 1
            c = e["class"]
            matched_by_class[c] = matched_by_class.get(c, 0) + 1
            expected_runs.append({**r, "status": "expected", "name": e["name"], "class": c, "ref": e["ref"]})
        else:
            unexplained_runs.append({**r, "status": "unexplained"})

    verdict = "same" if not runs_all else ("expected" if not unexplained_runs else "differs")
    return {
        "verdict": verdict,
        "lines_compared": lines_compared,
        "total_runs": len(runs_all),
        "expected": len(expected_runs),
        "unexplained": len(unexplained_runs),
        "matched_by_class": matched_by_class,
        "unexplained_runs": unexplained_runs,
        "expected_sample": expected_runs[:20],
        "entry_hits": entry_hits,
        "animation_policy": (
            "Animated regions (omp welcome diagonal gradient sweep and spinner/shimmer tween) are "
            "checked per cell against the exact colour manifold derived from omp's source stops "
            "(`packages/tui/src/prompt/welcome.ts` GRADIENT_STOPS + shine; `theme/shimmer.ts` dim/muted/accent); "
            "any colour outside those stops fails as unexplained."
        ),
    }


def verify_terminal_modes(
    omp_raw_bytes: bytes,
    pig_raw_bytes: bytes,
    allowlist: list[dict[str, Any]] | None = None,
    *,
    omp_resize_offsets: list[dict[str, int]] | None = None,
) -> dict[str, Any]:
    """Verify terminal mode differences (`alt_screen` / `mouse_modes`) directly from raw PTY byte offsets.

    For omp's resize alt-screen borrow (`CSI ?1049h` ... `CSI ?1049l`):
      - Verifies from raw stream byte offsets that EVERY `?1049h` is paired with a `?1049l`,
      - Verifies that every `(?1049h, ?1049l)` interval lies strictly inside the recorded resize
        window `[start, end]` (when `omp_resize_offsets` is supplied) or immediately precedes
        omp's resize `CSI 2J + CSI 3J` rebuild (within 4 KiB) with zero alt-screen entries
        anywhere else in the session,
      - Verifies PiG never enters alt screen (`?1049h == []`),
      - Classifies via the `terminal_mode` entry in `terminal_allowlist.json`.
    Any alt-screen entry outside resize, any unpaired `?1049h`, or any mouse-mode mismatch is `unexplained`.
    """
    entries = allowlist if allowlist is not None else load_terminal_allowlist()
    omp_txt = omp_raw_bytes.decode("utf-8", "replace")
    pig_txt = pig_raw_bytes.decode("utf-8", "replace")

    def _alt_intervals(raw_b: bytes) -> tuple[list[tuple[int, int | None]], bool]:
        enters = [m.start() for m in re.finditer(rb"\x1b\[\?1049h", raw_b)]
        exits = [m.start() for m in re.finditer(rb"\x1b\[\?1049l", raw_b)]
        intervals: list[tuple[int, int | None]] = []
        ex_i = 0
        unpaired = False
        for ent in enters:
            while ex_i < len(exits) and exits[ex_i] < ent:
                ex_i += 1
            if ex_i < len(exits):
                intervals.append((ent, exits[ex_i]))
                ex_i += 1
            else:
                intervals.append((ent, None))
                unpaired = True
        return intervals, unpaired

    omp_intervals, omp_unpaired = _alt_intervals(omp_raw_bytes)
    pig_intervals, pig_unpaired = _alt_intervals(pig_raw_bytes)
    omp_mouse = sorted(m for m in ("?1000h", "?1002h", "?1003h", "?1006h") if f"\x1b[{m}" in omp_txt)
    pig_mouse = sorted(m for m in ("?1000h", "?1002h", "?1003h", "?1006h") if f"\x1b[{m}" in pig_txt)

    issues: list[dict[str, Any]] = []
    expected_modes: list[dict[str, Any]] = []
    entry_hits: dict[int, int] = {i: 0 for i in range(len(entries))}

    # 1. Mouse modes must match
    if omp_mouse != pig_mouse:
        issues.append({"mode": "mouse_modes", "omp": omp_mouse, "pig": pig_mouse, "reason": "mouse modes differ"})

    # 2. Alt-screen check
    if omp_intervals or pig_intervals:
        if pig_intervals or pig_unpaired:
            issues.append({
                "mode": "alt_screen",
                "side": "pig",
                "intervals": pig_intervals,
                "reason": "port unexpectedly entered alternate screen",
            })
        elif omp_unpaired:
            issues.append({
                "mode": "alt_screen",
                "side": "omp",
                "intervals": omp_intervals,
                "reason": "omp entered alt screen without matching ?1049l exit",
            })
        else:
            # Verify every omp interval occurs strictly during resize (between resize start and settle).
            outside: list[tuple[int, int]] = []
            for ent, ext in omp_intervals:
                assert ext is not None
                if omp_resize_offsets:
                    in_resize = any(r["start"] <= ent <= ext <= r["end"] for r in omp_resize_offsets)
                else:
                    # Fallback when offsets file isn't provided (e.g. unit test / older capture):
                    # require the borrow to be short (<8 KiB) and immediately followed by omp's
                    # resize `CSI 2J CSI 3J` rebuild (within 64 bytes of `?1049l`).
                    after = omp_raw_bytes[ext : ext + 64]
                    in_resize = (ext - ent) < 8192 and (b"\x1b[2J\x1b[3J" in after)
                if not in_resize:
                    outside.append((ent, ext))
            if outside:
                issues.append({
                    "mode": "alt_screen",
                    "side": "omp",
                    "intervals": omp_intervals,
                    "outside_resize": outside,
                    "reason": "omp entered alt screen outside the resize start..settle window",
                })
            else:
                # Look up terminal_mode allowlist entry
                hit_idx = next(
                    (ei for ei, e in enumerate(entries) if e.get("kind") == "terminal_mode" and e.get("mode") == "alt_screen_resize_borrow"),
                    None,
                )
                if hit_idx is not None:
                    e = entries[hit_idx]
                    entry_hits[hit_idx] += 1
                    expected_modes.append({
                        "mode": "alt_screen_resize_borrow",
                        "name": e["name"],
                        "class": e["class"],
                        "ref": e["ref"],
                        "omp_intervals": omp_intervals,
                        "verified_inside_resize_only": True,
                    })
                else:
                    issues.append({
                        "mode": "alt_screen",
                        "side": "omp",
                        "intervals": omp_intervals,
                        "reason": "no terminal_mode allowlist entry for alt_screen_resize_borrow",
                    })

    verdict = "same" if (not expected_modes and not issues) else ("expected" if not issues else "differs")
    return {
        "verdict": verdict,
        "expected": len(expected_modes),
        "unexplained": len(issues),
        "expected_modes": expected_modes,
        "unexplained_modes": issues,
        "omp_alt_intervals": omp_intervals,
        "pig_alt_intervals": pig_intervals,
        "omp_mouse_modes": omp_mouse,
        "pig_mouse_modes": pig_mouse,
        "entry_hits": entry_hits,
    }

def classify_screen_regions(
    omp_text: str,
    pig_text: str,
    allowlist: list[dict[str, Any]] | None = None,
) -> dict[str, Any]:
    """Compare a final screen capture as two independently aligned regions:

    1. `dock`: from the first widget line above the composer (TODO HUD / error banner / band)
       to the bottom of the prompt / editor frame. Reports `dock_height: {omp: N, pig: M}`.
    2. `transcript`: lines above the dock, aligned from the BOTTOM so a taller dock on one
       side only trims top rows that scrolled off-screen (reported as `viewport_offset_lines`).
    Stale pre-resize dock isolation runs ONLY on omp's side (Finding 15).
    Blank rows changed are reported separately under `blank_row_diff`.
    """
    entries = allowlist if allowlist is not None else load_terminal_allowlist()
    a_tr, a_dock, a_stale = split_screen_regions(omp_text.splitlines(), side="omp")
    b_tr, b_dock, b_stale = split_screen_regions(pig_text.splitlines(), side="pig")

    dock_delta = len(b_dock) - len(a_dock)
    a_tr_trimmed = list(a_tr)
    b_tr_trimmed = list(b_tr)
    while a_tr_trimmed and not a_tr_trimmed[0].strip():
        a_tr_trimmed.pop(0)
    while b_tr_trimmed and not b_tr_trimmed[0].strip():
        b_tr_trimmed.pop(0)

    viewport_offset_lines: list[str] = []
    if dock_delta != 0:
        extra_rows = abs(dock_delta)
        if dock_delta > 0 and len(a_tr_trimmed) > len(b_tr_trimmed):
            drop = min(extra_rows, len(a_tr_trimmed) - len(b_tr_trimmed))
            viewport_offset_lines = [l for l in a_tr_trimmed[:drop] if l.strip()]
            a_tr_aligned = a_tr_trimmed[drop:]
            b_tr_aligned = b_tr_trimmed
        elif dock_delta < 0 and len(b_tr_trimmed) > len(a_tr_trimmed):
            drop = min(extra_rows, len(b_tr_trimmed) - len(a_tr_trimmed))
            viewport_offset_lines = [l for l in b_tr_trimmed[:drop] if l.strip()]
            a_tr_aligned = a_tr_trimmed
            b_tr_aligned = b_tr_trimmed[drop:]
        else:
            a_tr_aligned, b_tr_aligned = a_tr_trimmed, b_tr_trimmed
    else:
        a_tr_aligned, b_tr_aligned = a_tr_trimmed, b_tr_trimmed

    tr_raw_a = "\n".join(a_tr_aligned)
    tr_raw_b = "\n".join(b_tr_aligned)
    dk_raw_a = "\n".join(a_dock)
    dk_raw_b = "\n".join(b_dock)
    tr_a_s, tr_b_s = width_preserving_normalize_pair(tr_raw_a, tr_raw_b)
    dk_a_s, dk_b_s = width_preserving_normalize_pair(dk_raw_a, dk_raw_b)

    tr_rep = classify_terminal_diff(tr_a_s, tr_b_s, entries, omp_raw_text=tr_raw_a, pig_raw_text=tr_raw_b)
    dk_rep = classify_terminal_diff(dk_a_s, dk_b_s, entries, omp_raw_text=dk_raw_a, pig_raw_text=dk_raw_b)

    merged_hunks: list[dict[str, Any]] = []
    for reg_name, rep in (("transcript", tr_rep), ("dock", dk_rep)):
        for h in rep["hunks"]:
            merged_hunks.append({**h, "region": reg_name, "index": len(merged_hunks)})

    usage_by_name = {u["name"]: dict(u) for u in tr_rep["allowlist_usage"]}
    for u in dk_rep["allowlist_usage"]:
        usage_by_name[u["name"]]["hits"] += u["hits"]
        usage_by_name[u["name"]]["stale"] = (usage_by_name[u["name"]]["hits"] == 0) and not bool(usage_by_name[u["name"]].get("optional"))
    if a_stale:
        for e in entries:
            if e.get("match_kind") == "stale_pre_resize_frame":
                cnt = a_stale["stale_line_count"]
                usage_by_name[e["name"]]["hits"] += cnt
                usage_by_name[e["name"]]["stale"] = (usage_by_name[e["name"]]["hits"] == 0) and not bool(usage_by_name[e["name"]].get("optional"))
    expected_hunks = sum(1 for h in merged_hunks if h["verdict"] == "expected")
    timing_hunks = sum(1 for h in merged_hunks if h["verdict"] == "timing")
    differs_hunks = sum(1 for h in merged_hunks if h["verdict"] == "differs")
    blank_only_hunks = sum(1 for h in merged_hunks if h["verdict"] == "blank_only")
    unexp_lines = sum(h["unexplained_count"] for h in merged_hunks)
    timing_lines = sum(h.get("timing_count", 0) for h in merged_hunks)
    timing_items = list(tr_rep.get("timing_diff", {}).get("items", [])) + list(
        dk_rep.get("timing_diff", {}).get("items", [])
    )

    class_counts: dict[str, int] = {}
    class_in_doubt_lines = 0
    for h in merged_hunks:
        for m in h["matched"]:
            c = m["class"]
            class_counts[c] = class_counts.get(c, 0) + 1
            if m.get("class_in_doubt"):
                class_in_doubt_lines += 1
    if a_stale:
        for e in entries:
            if e.get("match_kind") == "stale_pre_resize_frame":
                c = e["class"]
                class_counts[c] = class_counts.get(c, 0) + a_stale["stale_line_count"]

    blank_rem = tr_rep["blank_row_diff"]["omp_blank_lines_removed"] + dk_rep["blank_row_diff"]["omp_blank_lines_removed"]
    blank_add = tr_rep["blank_row_diff"]["pig_blank_lines_added"] + dk_rep["blank_row_diff"]["pig_blank_lines_added"]
    blank_exp = tr_rep["blank_row_diff"]["expected"] + dk_rep["blank_row_diff"]["expected"]
    blank_unexp_items = list(tr_rep["blank_row_diff"]["unexplained_items"]) + list(dk_rep["blank_row_diff"]["unexplained_items"])

    overall = (
        "same"
        if (not merged_hunks and dock_delta == 0 and not a_stale and not b_stale and not blank_unexp_items)
        else (
            "differs"
            if (differs_hunks > 0 or blank_unexp_items)
            else ("timing" if timing_hunks > 0 else "expected")
        )
    )
    return {
        "mode": "screen_regions",
        "verdict": overall,
        "dock_height": {"omp": len(a_dock), "pig": len(b_dock), "delta": dock_delta},
        "viewport_offset_lines": viewport_offset_lines,
        "stale_pre_resize_frame": ({"omp": a_stale, "pig": None} if a_stale else None),
        "summary": {
            "total_hunks": len(merged_hunks),
            "expected": expected_hunks,
            "timing": timing_hunks,
            "differs": differs_hunks,
            "blank_only": blank_only_hunks,
            "matched_lines": sum(h["matched_count"] for h in merged_hunks),
            "matched_by_class": class_counts,
            "class_in_doubt_lines": class_in_doubt_lines,
            "timing_lines": timing_lines,
            "timing_pairs": len(timing_items),
            "unexplained_lines": unexp_lines,
            "blank_expected": blank_exp,
            "blank_unexplained": len(blank_unexp_items),
        },
        "timing_diff": {
            "pairs": len(timing_items),
            "items": timing_items,
            "max_seconds": MAX_TERMINAL_TIMING_CROSS_S,
        },
        "blank_row_diff": {
            "omp_blank_lines_removed": blank_rem,
            "pig_blank_lines_added": blank_add,
            "total_blank_rows_changed": blank_rem + blank_add,
            "expected": blank_exp,
            "unexplained": len(blank_unexp_items),
            "unexplained_items": blank_unexp_items,
            "blank_only_hunks": blank_only_hunks,
        },
        "hunks": merged_hunks,
        "allowlist_usage": list(usage_by_name.values()),
    }

def summarize_sgr_palette(raw_bytes: bytes) -> dict[str, Any]:
    """Summarize SGR colour capability/codes emitted in a raw PTY capture."""
    text = raw_bytes.decode("utf-8", "replace")
    truecolor_fg: set[str] = set()
    truecolor_bg: set[str] = set()
    ansi256_fg: set[str] = set()
    ansi256_bg: set[str] = set()
    basic_codes: set[int] = set()
    sgr_seq_count = 0
    alt_screen = "\x1b[?1049h" in text
    mouse_modes = sorted(
        m for m in ("?1000h", "?1002h", "?1003h", "?1006h") if f"\x1b[{m}" in text
    )
    for match in _RE_SGR_SEQ.finditer(text):
        sgr_seq_count += 1
        raw_params = match.group(1)
        if not raw_params:
            continue
        parts = [int(p) if p.isdigit() else 0 for p in raw_params.split(";")]
        i = 0
        while i < len(parts):
            code = parts[i]
            if code in (38, 48) and i + 1 < len(parts):
                mode = parts[i + 1]
                if mode == 2 and i + 4 < len(parts):
                    rgb = f"{parts[i+2]};{parts[i+3]};{parts[i+4]}"
                    (truecolor_fg if code == 38 else truecolor_bg).add(rgb)
                    i += 5
                    continue
                if mode == 5 and i + 2 < len(parts):
                    idx = str(parts[i + 2])
                    (ansi256_fg if code == 38 else ansi256_bg).add(idx)
                    i += 3
                    continue
            if (30 <= code <= 37) or (40 <= code <= 47) or (90 <= code <= 97) or (100 <= code <= 107):
                basic_codes.add(code)
            i += 1
    return {
        "sgr_seq_count": sgr_seq_count,
        "alt_screen": alt_screen,
        "mouse_modes": mouse_modes,
        "truecolor_fg_count": len(truecolor_fg),
        "truecolor_bg_count": len(truecolor_bg),
        "ansi256_fg_count": len(ansi256_fg),
        "ansi256_bg_count": len(ansi256_bg),
        "basic_ansi_count": len(basic_codes),
        "sample_truecolor_fg": sorted(truecolor_fg)[:12],
        "sample_truecolor_bg": sorted(truecolor_bg)[:8],
        "sample_ansi256_fg": sorted(ansi256_fg)[:12],
        "_sets": {
            "truecolor_fg": truecolor_fg,
            "truecolor_bg": truecolor_bg,
            "ansi256_fg": ansi256_fg,
            "ansi256_bg": ansi256_bg,
            "basic_codes": {str(c) for c in basic_codes},
        },
    }


def compare_sgr_palettes(omp_raw_bytes: bytes, pig_raw_bytes: bytes) -> dict[str, Any]:
    """Compute a counted SGR colour/mode diff between two raw PTY captures (reported separately from text diff)."""
    omp_full = summarize_sgr_palette(omp_raw_bytes)
    pig_full = summarize_sgr_palette(pig_raw_bytes)
    osets = omp_full.pop("_sets")
    psets = pig_full.pop("_sets")

    def _set_delta(key: str) -> dict[str, int]:
        o_s, p_s = osets[key], psets[key]
        return {
            "omp": len(o_s),
            "pig": len(p_s),
            "shared": len(o_s & p_s),
            "omp_only": len(o_s - p_s),
            "pig_only": len(p_s - o_s),
        }

    deltas = {
        "truecolor_fg": _set_delta("truecolor_fg"),
        "truecolor_bg": _set_delta("truecolor_bg"),
        "ansi256_fg": _set_delta("ansi256_fg"),
        "ansi256_bg": _set_delta("ansi256_bg"),
        "basic_ansi": _set_delta("basic_codes"),
    }
    total_color_diffs = sum(d["omp_only"] + d["pig_only"] for d in deltas.values())
    mode_diffs = int(omp_full["alt_screen"] != pig_full["alt_screen"]) + int(
        omp_full["mouse_modes"] != pig_full["mouse_modes"]
    )
    verdict = "same" if (total_color_diffs == 0 and mode_diffs == 0) else "differs"
    return {
        "verdict": verdict,
        "total_distinct_color_diffs": total_color_diffs,
        "mode_diffs": mode_diffs,
        "omp": omp_full,
        "pig": pig_full,
        "deltas": deltas,
        "note": "SGR colours and terminal modes are compared separately from text hunks.",
    }


def side_by_side(a_text: str, b_text: str, width: int = 120, label_a: str = "omp", label_b: str = "oh-my-pig") -> str:
    a_lines = a_text.splitlines()
    b_lines = b_text.splitlines()
    n = max(len(a_lines), len(b_lines))
    out: list[str] = []
    hdr = f"{label_a:<{width}} | {label_b}"
    out.append(hdr)
    out.append("-" * width + "-+-" + "-" * width)
    for i in range(n):
        al = a_lines[i] if i < len(a_lines) else ""
        bl = b_lines[i] if i < len(b_lines) else ""
        mark = " " if al == bl else "|"
        al_fit = al[:width].ljust(width)
        bl_fit = bl[:width]
        out.append(f"{al_fit} {mark} {bl_fit}")
    return "\n".join(out) + "\n"

def check_combined_stale_allowlist(
    reports: list[dict[str, Any]],
) -> dict[str, Any]:
    """Aggregate `allowlist_usage` across reports (e.g. screen + scrollback) and flag entries with 0 total hits.

    Entries marked `"optional": true` (such as timing-dependent `OMP_ARTIFACT` resize ghosts)
    are excluded from `stale_entries` while still reporting their combined hit counts.
    """
    combined: dict[str, dict[str, Any]] = {}
    order: list[str] = []
    for rep in reports:
        for u in rep.get("allowlist_usage") or []:
            nm = str(u.get("name") or "")
            if not nm:
                continue
            if nm not in combined:
                order.append(nm)
                combined[nm] = {
                    "name": nm,
                    "kind": u.get("kind", "text"),
                    "class": u.get("class", ""),
                    "optional": bool(u.get("optional")),
                    "ref": u.get("ref", ""),
                    "hits": 0,
                }
            combined[nm]["hits"] += int(u.get("hits", 0))
            if u.get("optional"):
                combined[nm]["optional"] = True
    usage: list[dict[str, Any]] = []
    stale_entries: list[dict[str, Any]] = []
    for nm in order:
        row = combined[nm]
        is_stale = (row["hits"] == 0) and not row["optional"]
        row["stale"] = is_stale
        usage.append(row)
        if is_stale:
            stale_entries.append(row)
    return {
        "stale_count": len(stale_entries),
        "stale_entries": stale_entries,
        "usage": usage,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description="Plain-PTY ANSI screen capture & side-by-side diff")
    sub = parser.add_subparsers(dest="cmd", required=True)

    p_stale = sub.add_parser("check-stale", help="Fail if any non-optional terminal_allowlist entry has 0 hits across diff JSON reports")
    p_stale.add_argument("reports", nargs="+", help="Paths to diff JSON files (e.g. diff.json scrollback-diff.json)")
    p_stale.add_argument("--allow-stale", action="store_true", help="Exit 0 even when stale entries exist")

    p_run = sub.add_parser("run", help="Run a launch script in a PTY and capture the final VT screen")
    p_run.add_argument("--launch", required=True)
    p_run.add_argument("--steps-json", required=True)
    p_run.add_argument("--rows", type=int, default=40)
    p_run.add_argument("--cols", type=int, default=120)
    p_run.add_argument("--startup-wait", type=float, default=2.5)
    p_run.add_argument("--screen-out", required=True)
    p_run.add_argument("--scrollback-out")
    p_run.add_argument("--raw-out")
    p_run.add_argument("--llm-origin")
    p_run.add_argument("--run-tag")
    p_run.add_argument("--side", choices=("omp", "pig"))
    p_run.add_argument("--meta-out")

    p_diff = sub.add_parser("diff", help="Write side-by-side, width-preserving normalized unified, classified hunks, and optional SGR/clear diffs")
    p_diff.add_argument("--omp-screen")
    p_diff.add_argument("--pig-screen")
    p_diff.add_argument("--omp-raw")
    p_diff.add_argument("--pig-raw")
    p_diff.add_argument("--omp-scrollback", help="Optional scrollback text for pre-diff capture validity check")
    p_diff.add_argument("--pig-scrollback", help="Optional scrollback text for pre-diff capture validity check")
    p_diff.add_argument("--omp-meta", help="Optional omp run meta JSON for pre-diff capture validity check")
    p_diff.add_argument("--pig-meta", help="Optional pig run meta JSON for pre-diff capture validity check")
    p_diff.add_argument("--steps-json", help="Optional steps JSON for pre-diff capture validity check")
    p_diff.add_argument("--replay-raw", choices=("screen", "scrollback"), help="Replay --omp-raw and --pig-raw through VTScreen (with CSI 3J) instead of reading pre-rendered text")
    p_diff.add_argument("--screen-regions", action="store_true", help="Compare final screen as separate dock + bottom-aligned transcript regions")
    p_diff.add_argument("--resize-to", help="Optional final WxH resize geometry for --replay-raw (e.g. 80x24)")
    p_diff.add_argument("--stop-before-quit", action="store_true", help="Stop --replay-raw before post-capture /quit shutdown output")
    p_diff.add_argument("--rows", type=int, default=40)
    p_diff.add_argument("--cols", type=int, default=120)
    p_diff.add_argument("--allowlist", default=str(DEFAULT_TERMINAL_ALLOWLIST_PATH))
    p_diff.add_argument("--json-out")
    p_diff.add_argument("--out", required=True)
    p_diff.add_argument("--width", type=int, default=120)
    p_diff.add_argument("--allow-diffs", action="store_true", help="Exit 0 even when unexplained differences exist")
    p_diff.add_argument("--allow-timing", action="store_true", help="Exit 0 when the only non-expected diffs are bounded timing unit crossings")
    args = parser.parse_args()
    if args.cmd == "check-stale":
        loaded = [json.loads(Path(p).read_text(encoding="utf-8")) for p in args.reports]
        res = check_combined_stale_allowlist(loaded)
        if res["stale_count"] > 0:
            names = ", ".join(f"{e['name']} ({e['kind']})" for e in res["stale_entries"])
            print(f"ERROR: {res['stale_count']} stale terminal_allowlist entry(s) with 0 hits across reports: {names}", file=sys.stderr)
            if not args.allow_stale:
                return 1
        return 0
    if args.cmd == "run":
        steps = json.loads(args.steps_json)
        meta = run_pty_session(
            args.launch,
            steps,
            rows=args.rows,
            cols=args.cols,
            startup_wait=args.startup_wait,
            raw_out=args.raw_out,
            screen_out=args.screen_out,
            scrollback_out=args.scrollback_out,
            llm_origin=args.llm_origin,
            run_tag=args.run_tag,
            side=args.side,
            meta_out=args.meta_out,
        )
        print(json.dumps(meta))
        if not meta["capture_valid"]:
            print(meta["capture_validation"]["message"], file=sys.stderr)
            return CAPTURE_INCOMPLETE_EXIT_CODE
        return 1 if meta["leaked_pids"] else 0
    resize_tuple: tuple[int, int] | None = None
    if args.resize_to:
        w_s, h_s = args.resize_to.lower().split("x", 1)
        resize_tuple = (int(h_s), int(w_s))

    omp_clears: dict[str, int] | None = None
    pig_clears: dict[str, int] | None = None
    omp_raw_bytes = open(args.omp_raw, "rb").read() if (args.omp_raw and os.path.isfile(args.omp_raw)) else None
    pig_raw_bytes = open(args.pig_raw, "rb").read() if (args.pig_raw and os.path.isfile(args.pig_raw)) else None

    if omp_raw_bytes is not None:
        omp_rep = replay_raw_bytes(
            omp_raw_bytes,
            rows=args.rows,
            cols=args.cols,
            resize_to=resize_tuple,
            stop_before_quit=args.stop_before_quit,
        )
        omp_clears = {"full_screen_clears": omp_rep["full_screen_clears"], "scrollback_clears": omp_rep["scrollback_clears"]}
    if pig_raw_bytes is not None:
        pig_rep = replay_raw_bytes(
            pig_raw_bytes,
            rows=args.rows,
            cols=args.cols,
            resize_to=resize_tuple,
            stop_before_quit=args.stop_before_quit,
        )
        pig_clears = {"full_screen_clears": pig_rep["full_screen_clears"], "scrollback_clears": pig_rep["scrollback_clears"]}

    if args.replay_raw:
        if omp_raw_bytes is None or pig_raw_bytes is None:
            raise SystemExit("error: --replay-raw requires both --omp-raw and --pig-raw")
        a_raw_text = omp_rep[args.replay_raw]
        b_raw_text = pig_rep[args.replay_raw]
    else:
        if not args.omp_screen or not args.pig_screen:
            raise SystemExit("error: --omp-screen and --pig-screen are required unless --replay-raw is given")
        with open(args.omp_screen, "r", encoding="utf-8") as _fh:
            a_raw_text = _fh.read()
        with open(args.pig_screen, "r", encoding="utf-8") as _fh:
            b_raw_text = _fh.read()

    # Capture validity gate BEFORE any diff comparison!
    # Triggered whenever --steps-json or --omp-meta/--pig-meta is provided.
    use_regions = bool(args.screen_regions or args.replay_raw == "screen")
    capture_validations: dict[str, dict[str, Any]] = {}
    if args.steps_json or args.omp_meta or args.pig_meta:
        steps_check = json.loads(args.steps_json) if args.steps_json else []
        for side_name, meta_path, sb_path, fallback_txt in (
            ("omp", args.omp_meta, args.omp_scrollback, a_raw_text if not use_regions else ""),
            ("pig", args.pig_meta, args.pig_scrollback, b_raw_text if not use_regions else ""),
        ):
            meta_obj: dict[str, Any] = {}
            if meta_path and os.path.isfile(meta_path):
                with open(meta_path, "r", encoding="utf-8") as _mfh:
                    meta_obj = json.load(_mfh)
            sb_txt = fallback_txt
            if sb_path and os.path.isfile(sb_path):
                with open(sb_path, "r", encoding="utf-8") as _sfh:
                    sb_txt = _sfh.read()
            if steps_check and sb_txt:
                val = validate_terminal_capture(
                    side_name,
                    sb_txt,
                    steps_check,
                    completed_turns=meta_obj.get("completed_turns"),
                    in_flight=meta_obj.get("in_flight"),
                    snapshots_present=meta_obj.get("snapshots"),
                    step_errors=(meta_obj.get("capture_validation") or {}).get("errors") if meta_obj.get("capture_valid") is False else None,
                )
            elif isinstance(meta_obj.get("capture_validation"), dict):
                val = meta_obj["capture_validation"]
            else:
                val = {"side": side_name, "capture_valid": True, "errors": [], "message": "ok"}
            capture_validations[side_name] = val

        invalid_sides = [v for v in capture_validations.values() if not v.get("capture_valid", True)]
        if invalid_sides:
            msg = " | ".join(v["message"] for v in invalid_sides)
            invalid_report: dict[str, Any] = {
                "capture_valid": False,
                "verdict": None,
                "error": msg,
                "capture_validation": capture_validations,
                "capture_mode": f"replay-raw:{args.replay_raw}" if args.replay_raw else ("screen_regions" if use_regions else "scrollback"),
            }
            if args.json_out:
                with open(args.json_out, "w", encoding="utf-8") as jfh:
                    json.dump(invalid_report, jfh, indent=2, ensure_ascii=False)
                    jfh.write("\n")
            with open(args.out, "w", encoding="utf-8") as fh:
                fh.write(f"=== CAPTURE INCOMPLETE (capture_valid=false; no diff verdict produced) ===\n")
                for v in invalid_sides:
                    fh.write(f"  {v['message']}\n")
                    for err in v.get("errors", []):
                        fh.write(f"    - {err}\n")
            print(msg, file=sys.stderr)
            return CAPTURE_INCOMPLETE_EXIT_CODE

    a, b = width_preserving_normalize_pair(a_raw_text, b_raw_text)
    allowlist = load_terminal_allowlist(Path(args.allowlist)) if args.allowlist else []
    if use_regions:
        hunk_report = classify_screen_regions(a_raw_text, b_raw_text, allowlist)
    else:
        hunk_report = classify_terminal_diff(a, b, allowlist, omp_raw_text=a_raw_text, pig_raw_text=b_raw_text)
    hunk_report["capture_valid"] = True
    if capture_validations:
        hunk_report["capture_validation"] = capture_validations
    hunk_report["capture_mode"] = f"replay-raw:{args.replay_raw}" if args.replay_raw else ("screen_regions" if use_regions else "scrollback")
    if omp_clears is not None and pig_clears is not None:
        hunk_report["full_screen_clears"] = {"omp": omp_clears, "pig": pig_clears}
    # Load per-cell styled rows (saved alongside capture by `run_pty_session` as `<path>.sgr.json`,
    # or derived by replaying raw bytes if `.sgr.json` is not present).
    omp_styled_rows: list[list[Any]] | None = None
    pig_styled_rows: list[list[Any]] | None = None
    omp_resize_offsets: list[dict[str, int]] | None = None
    if args.replay_raw:
        styled_key = "screen_styled" if args.replay_raw == "screen" else "scrollback_styled"
        omp_styled_rows = omp_rep[styled_key]
        pig_styled_rows = pig_rep[styled_key]
    else:
        omp_sgr_path = (args.omp_screen or "") + ".sgr.json"
        pig_sgr_path = (args.pig_screen or "") + ".sgr.json"
        if os.path.isfile(omp_sgr_path) and os.path.isfile(pig_sgr_path):
            o_meta = json.loads(open(omp_sgr_path, encoding="utf-8").read())
            p_meta = json.loads(open(pig_sgr_path, encoding="utf-8").read())
            omp_styled_rows = o_meta.get("rows")
            pig_styled_rows = p_meta.get("rows")
            omp_resize_offsets = o_meta.get("resize_raw_offsets")
        elif omp_raw_bytes is not None and pig_raw_bytes is not None:
            # Fallback replay with narrow80 resize (24x80) and stop-before-quit so styled rows match capture
            o_r = replay_raw_bytes(omp_raw_bytes, rows=args.rows, cols=args.cols, resize_to=(24, 80), stop_before_quit=True)
            p_r = replay_raw_bytes(pig_raw_bytes, rows=args.rows, cols=args.cols, resize_to=(24, 80), stop_before_quit=True)
            styled_key = "screen_styled" if use_regions else "scrollback_styled"
            omp_styled_rows = o_r[styled_key]
            pig_styled_rows = p_r[styled_key]

    usage_by_name = {u["name"]: u for u in hunk_report["allowlist_usage"]}

    cell_colour_report: dict[str, Any] | None = None
    if omp_styled_rows is not None and pig_styled_rows is not None:
        cell_colour_report = classify_cell_colours(
            omp_styled_rows, pig_styled_rows, allowlist, screen_regions=use_regions
        )
        for ei, cnt in cell_colour_report.pop("entry_hits", {}).items():
            if cnt > 0 and ei < len(allowlist):
                nm = allowlist[ei]["name"]
                if nm in usage_by_name:
                    usage_by_name[nm]["hits"] += cnt
                    usage_by_name[nm]["stale"] = (usage_by_name[nm]["hits"] == 0) and not bool(usage_by_name[nm].get("optional"))
        hunk_report["cell_colour_diff"] = cell_colour_report

    mode_report: dict[str, Any] | None = None
    if omp_raw_bytes is not None and pig_raw_bytes is not None:
        mode_report = verify_terminal_modes(
            omp_raw_bytes, pig_raw_bytes, allowlist, omp_resize_offsets=omp_resize_offsets
        )
        for ei, cnt in mode_report.pop("entry_hits", {}).items():
            if cnt > 0 and ei < len(allowlist):
                nm = allowlist[ei]["name"]
                if nm in usage_by_name:
                    usage_by_name[nm]["hits"] += cnt
                    usage_by_name[nm]["stale"] = (usage_by_name[nm]["hits"] == 0) and not bool(usage_by_name[nm].get("optional"))
        hunk_report["terminal_mode_diff"] = mode_report
        # Keep palette census as a summary only (not a verdict channel)
        hunk_report["sgr_palette_census"] = compare_sgr_palettes(omp_raw_bytes, pig_raw_bytes)

    # Fold unexplained blank rows, cell colour runs, and terminal mode diffs into summary, verdict, and exit code!
    unexp_text = int(hunk_report["summary"].get("unexplained_lines", 0))
    unexp_blank = int(hunk_report["summary"].get("blank_unexplained", 0))
    unexp_colour = int(cell_colour_report["unexplained"]) if cell_colour_report else 0
    unexp_mode = int(mode_report["unexplained"]) if mode_report else 0
    hunk_report["summary"]["colour_expected_runs"] = int(cell_colour_report["expected"]) if cell_colour_report else 0
    hunk_report["summary"]["colour_unexplained_runs"] = unexp_colour
    hunk_report["summary"]["mode_expected"] = int(mode_report["expected"]) if mode_report else 0
    hunk_report["summary"]["mode_unexplained"] = unexp_mode
    hunk_report["summary"]["total_unexplained_all"] = unexp_text + unexp_blank + unexp_colour + unexp_mode

    if hunk_report["summary"]["total_unexplained_all"] > 0:
        hunk_report["verdict"] = "differs"
    elif hunk_report["verdict"] == "same" and (
        hunk_report["summary"]["colour_expected_runs"] > 0
        or hunk_report["summary"]["mode_expected"] > 0
        or hunk_report["summary"].get("blank_expected", 0) > 0
    ):
        hunk_report["verdict"] = "expected"

    sbs = side_by_side(a, b, width=args.width)
    udiff = "".join(
        difflib.unified_diff(
            a.splitlines(keepends=True),
            b.splitlines(keepends=True),
            fromfile="omp.screen.txt",
            tofile="oh-my-pig.screen.txt",
        )
    )
    if args.json_out:
        with open(args.json_out, "w", encoding="utf-8") as jfh:
            json.dump(hunk_report, jfh, indent=2, ensure_ascii=False)
            jfh.write("\n")
    with open(args.out, "w", encoding="utf-8") as fh:
        sm = hunk_report["summary"]
        br = hunk_report.get("blank_row_diff") or {}
        if omp_clears is not None and pig_clears is not None:
            fh.write(
                f"=== FULL-SCREEN CLEARS: omp={omp_clears['full_screen_clears']} (CSI 2J) / "
                f"{omp_clears['scrollback_clears']} (CSI 3J), "
                f"pig={pig_clears['full_screen_clears']} (CSI 2J) / "
                f"{pig_clears['scrollback_clears']} (CSI 3J) ===\n"
            )
        if hunk_report.get("dock_height"):
            dh = hunk_report["dock_height"]
            fh.write(f"=== DOCK HEIGHT: omp {dh['omp']} rows, port {dh['pig']} rows (delta={dh['delta']:+d}) ===\n")
        if hunk_report.get("viewport_offset_lines"):
            fh.write(
                f"=== VIEWPORT OFFSET (scrolled off taller-dock side): {len(hunk_report['viewport_offset_lines'])} line(s): "
                f"{hunk_report['viewport_offset_lines']!r} ===\n"
            )
        if hunk_report.get("stale_pre_resize_frame"):
            sf = hunk_report["stale_pre_resize_frame"]
            fh.write(f"=== STALE FRAME AFTER RESIZE (omp artifact only): {json.dumps(sf, sort_keys=True)} ===\n")
        timing_pairs = int(sm.get("timing_pairs", 0))
        fh.write(
            f"=== OVERALL VERDICT ({hunk_report['verdict']}: total_unexplained={sm['total_unexplained_all']} "
            f"[text={unexp_text}, blank={unexp_blank}, colour={unexp_colour}, mode={unexp_mode}], "
            f"timing_pairs={timing_pairs}) ===\n"
        )
        fh.write(
            f"=== TEXT HUNKS ({sm['expected']} expected, {sm.get('timing', 0)} timing, {sm['differs']} differs of {sm['total_hunks']} hunks; "
            f"{sm['unexplained_lines']} unexplained lines; "
            f"matched={sm.get('matched_lines', 0)} by class {json.dumps(sm.get('matched_by_class', {}), sort_keys=True)}) ===\n"
        )
        if timing_pairs > 0:
            for ti in (hunk_report.get("timing_diff") or {}).get("items", []):
                fh.write(
                    f"  TIMING UNIT CROSSING ({ti['omp_unit']} vs {ti['pig_unit']}): "
                    f"omp={ti.get('omp_raw') or ti['omp_line']!r} vs pig={ti.get('pig_raw') or ti['pig_line']!r}\n"
                )
        fh.write(
            f"=== BLANK ROWS ({br.get('expected', 0)} expected, {br.get('unexplained', 0)} unexplained "
            f"of {br.get('total_blank_rows_changed', 0)} changed: omp_removed={br.get('omp_blank_lines_removed', 0)}, "
            f"pig_added={br.get('pig_blank_lines_added', 0)}) ===\n"
        )
        if cell_colour_report is not None:
            fh.write(
                f"=== CELL COLOUR DIFF ({cell_colour_report['verdict']}: {cell_colour_report['expected']} expected, "
                f"{cell_colour_report['unexplained']} unexplained of {cell_colour_report['total_runs']} differing runs "
                f"across {cell_colour_report['lines_compared']} text-matching lines; "
                f"by class {json.dumps(cell_colour_report['matched_by_class'], sort_keys=True)}) ===\n"
            )
            for cr in cell_colour_report["unexplained_runs"][:15]:
                fh.write(
                    f"  UNEXPLAINED COLOUR [{cr['region']}] L{cr['omp_line']}/{cr['pig_line']}@{cr['col']} "
                    f"{cr['text']!r}: omp=({cr['omp_fg']},{cr['omp_bg']},{cr.get('omp_attrs', [])}) "
                    f"vs pig=({cr['pig_fg']},{cr['pig_bg']},{cr.get('pig_attrs', [])})\n"
                )
        if mode_report is not None:
            fh.write(
                f"=== TERMINAL MODE DIFF ({mode_report['verdict']}: {mode_report['expected']} expected, "
                f"{mode_report['unexplained']} unexplained; omp_alt={mode_report['omp_alt_intervals']}, "
                f"pig_alt={mode_report['pig_alt_intervals']}) ===\n"
            )
            for um in mode_report["unexplained_modes"]:
                fh.write(f"  UNEXPLAINED MODE: {json.dumps(um, sort_keys=True)}\n")
        for h in hunk_report["hunks"]:
            tag = f"{h['verdict'].upper()}"
            if h["classes"]:
                tag += f" [{','.join(h['classes'])}]"
            if h.get("names"):
                tag += f" ({','.join(h['names'])})"
            reg = f"[{h['region']}] " if h.get("region") else ""
            fh.write(
                f"  Hunk #{h['index']} {reg}{h['header']}: {tag} "
                f"(matched={h['matched_count']}, timing={h.get('timing_count', 0)}, unexplained={h['unexplained_count']}, "
                f"blank_exp={h.get('blank_expected', 0)}, blank_unexp={h.get('blank_unexplained', 0)})\n"
            )
            for tl in h.get("timing", [])[:4]:
                fh.write(f"    TIMING: {tl['line']}\n")
            for ul in h["unexplained"][:6]:
                fh.write(f"    UNEXPLAINED: {ul}\n")
        fh.write("\n=== SIDE-BY-SIDE (width-preserving normalized paths, <SIDE>, & durations) ===\n")
        fh.write(sbs)
        fh.write("\n=== UNIFIED DIFF ===\n")
        fh.write(udiff or "(identical)\n")
        census = hunk_report.get("sgr_palette_census")
        if census is not None:
            fh.write("\n=== SGR PALETTE CENSUS (summary only) ===\n")
            fh.write(f"omp: {json.dumps(census['omp'], sort_keys=True)}\n")
            fh.write(f"pig: {json.dumps(census['pig'], sort_keys=True)}\n")
            fh.write(f"deltas: {json.dumps(census['deltas'], sort_keys=True)}\n")
    if hunk_report["summary"]["total_unexplained_all"] > 0 and not args.allow_diffs:
        return 1
    if int(hunk_report["summary"].get("timing_pairs", 0)) > 0 and not args.allow_timing and not args.allow_diffs:
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
