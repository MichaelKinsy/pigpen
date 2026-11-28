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
import select
import signal
import struct
import subprocess
import sys
import termios
import time
import unicodedata
import urllib.request
from pathlib import Path
from typing import Any

sys.dont_write_bytecode = True

SCRIPT_DIR = Path(__file__).resolve().parent
if str(SCRIPT_DIR) not in sys.path:
    sys.path.insert(0, str(SCRIPT_DIR))

from harness_isolation import (  # noqa: E402
    reap_tagged_processes,
    sandbox_cmd,
)


class VTScreen:
    """Deterministic VT100/xterm screen model with scrollback capture (rows x cols)."""

    def __init__(self, rows: int = 40, cols: int = 120) -> None:
        self.rows = rows
        self.cols = cols
        self._main = self._blank_grid()
        self._alt = self._blank_grid()
        self._in_alt = False
        self.grid = self._main
        self.scrollback: list[str] = []
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

    def _clamp(self) -> None:
        self.r = max(0, min(self.rows - 1, self.r))
        self.c = max(0, min(self.cols - 1, self.c))

    def _scroll_up(self, n: int = 1) -> None:
        top, bot = self.scroll_top, self.scroll_bottom
        n = min(n, bot - top + 1)
        for _ in range(n):
            removed = self.grid.pop(top)
            if not self._in_alt and top == 0 and bot == self.rows - 1:
                self.scrollback.append("".join(removed).rstrip())
            self.grid.insert(bot, [" "] * self.cols)

    def _scroll_down(self, n: int = 1) -> None:
        top, bot = self.scroll_top, self.scroll_bottom
        n = min(n, bot - top + 1)
        for _ in range(n):
            del self.grid[bot]
            self.grid.insert(top, [" "] * self.cols)

    def _newline(self) -> None:
        self._wrap_pending = False
        if self.r == self.scroll_bottom:
            self._scroll_up(1)
        elif self.r < self.rows - 1:
            self.r += 1

    def _char_width(self, ch: str) -> int:
        cp = ord(ch)
        if unicodedata.combining(ch) or ch in ("\u200d", "\ufe0e", "\ufe0f"):
            return 0
        if unicodedata.east_asian_width(ch) in ("W", "F"):
            return 2
        # Common emoji blocks rendered width 2 in terminals
        if 0x1F300 <= cp <= 0x1FAFF:
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
        if w == 2 and self.c + 1 < self.cols:
            self.grid[self.r][self.c + 1] = ""
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
                    # RIS: full reset including alternate buffer, scroll region, autowrap
                    self._in_alt = False
                    self._main = self._blank_grid()
                    self._alt = self._blank_grid()
                    self.grid = self._main
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
                            self.grid = self._alt
                            self.r = 0
                            self.c = 0
                            self._wrap_pending = False
                        elif not enable and self._in_alt:
                            self._in_alt = False
                            self.grid = self._main
                            self._wrap_pending = False
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
                for row in range(self.r + 1, self.rows):
                    self.grid[row] = [" "] * self.cols
            elif mode == 1:
                for row in range(0, self.r):
                    self.grid[row] = [" "] * self.cols
                for col in range(0, self.c + 1):
                    self.grid[self.r][col] = " "
            elif mode in (2, 3):
                for row in range(self.rows):
                    self.grid[row] = [" "] * self.cols
        elif final == "K":
            mode = p0
            if mode == 0:
                for col in range(self.c, self.cols):
                    self.grid[self.r][col] = " "
            elif mode == 1:
                for col in range(0, self.c + 1):
                    self.grid[self.r][col] = " "
            elif mode == 2:
                self.grid[self.r] = [" "] * self.cols
        elif final == "L":
            n = max(1, p0)
            if self.scroll_top <= self.r <= self.scroll_bottom:
                for _ in range(min(n, self.scroll_bottom - self.r + 1)):
                    del self.grid[self.scroll_bottom]
                    self.grid.insert(self.r, [" "] * self.cols)
        elif final == "M":
            n = max(1, p0)
            if self.scroll_top <= self.r <= self.scroll_bottom:
                for _ in range(min(n, self.scroll_bottom - self.r + 1)):
                    del self.grid[self.r]
                    self.grid.insert(self.scroll_bottom, [" "] * self.cols)
        elif final == "S":
            self._scroll_up(max(1, p0))
        elif final == "T":
            self._scroll_down(max(1, p0))
        elif final == "P":
            n = max(1, p0)
            row = self.grid[self.r]
            self.grid[self.r] = row[:self.c] + row[self.c + n:] + [" "] * min(n, self.cols - self.c)
        elif final == "@":
            n = max(1, p0)
            row = self.grid[self.r]
            self.grid[self.r] = (row[:self.c] + [" "] * n + row[self.c:])[:self.cols]
        elif final == "X":
            n = max(1, p0)
            for col in range(self.c, min(self.cols, self.c + n)):
                self.grid[self.r][col] = " "
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


def _set_winsize(fd: int, rows: int, cols: int) -> None:
    winsize = struct.pack("HHHH", rows, cols, 0, 0)
    fcntl.ioctl(fd, termios.TIOCSWINSZ, winsize)


def _health_next(llm_origin: str | None) -> int | None:
    if not llm_origin:
        return None
    try:
        with urllib.request.urlopen(f"{llm_origin}/health", timeout=0.5) as resp:
            data = json.loads(resp.read().decode("utf-8"))
            return int(data.get("next", 0))
    except Exception:
        return None


def _drain(
    master_fd: int,
    screen: VTScreen,
    decoder: codecs.IncrementalDecoder,
    raw_chunks: list[bytes],
    query_buf: bytearray,
    duration: float,
) -> None:
    deadline = time.monotonic() + duration
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
) -> None:
    if llm_origin is None or target_next is None:
        _drain(master_fd, screen, decoder, raw_chunks, query_buf, max_wait)
        return
    deadline = time.monotonic() + max_wait
    while time.monotonic() < deadline:
        _drain(master_fd, screen, decoder, raw_chunks, query_buf, 0.12)
        cur = _health_next(llm_origin)
        if cur is not None and cur >= target_next:
            # Let final repaint flush after the server finished serving the turn
            _drain(master_fd, screen, decoder, raw_chunks, query_buf, settle_s)
            return
    _drain(master_fd, screen, decoder, raw_chunks, query_buf, settle_s)


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
) -> dict[str, Any]:
    screen = VTScreen(rows=rows, cols=cols)
    decoder = codecs.getincrementaldecoder("utf-8")("replace")
    raw_chunks: list[bytes] = []
    query_buf = bytearray()
    master_fd, slave_fd = pty.openpty()
    _set_winsize(slave_fd, rows, cols)

    env = os.environ.copy()
    for k in list(env.keys()):
        if k.startswith(("TERN_", "TERM_PROGRAM", "TMUX", "STY", "ZELLIJ")):
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
    leaked_pids: list[int] = []
    try:
        _drain(master_fd, screen, decoder, raw_chunks, query_buf, startup_wait)
        for step in steps:
            text = step.get("send")
            if text:
                os.write(master_fd, text.encode("utf-8"))
                _drain(master_fd, screen, decoder, raw_chunks, query_buf, 0.15)
            if step.get("enter", True):
                os.write(master_fd, b"\r")
            max_wait = float(step.get("wait", 10.0))
            target_next = step.get("expect_next")
            _wait_for_cursor(
                master_fd, screen, decoder, raw_chunks, query_buf,
                llm_origin, target_next, max_wait,
            )

        final_screen = screen.render()
        full_scrollback = screen.render_with_scrollback()

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
    if scrollback_out:
        with open(scrollback_out, "w", encoding="utf-8") as fh:
            fh.write(full_scrollback)
    if raw_out:
        with open(raw_out, "wb") as fh:
            fh.write(b"".join(raw_chunks))

    non_empty = sum(1 for l in final_screen.splitlines() if l.strip())
    return {
        "rc": proc.returncode,
        "rows": rows,
        "cols": cols,
        "raw_bytes": sum(len(c) for c in raw_chunks),
        "non_empty_lines": non_empty,
        "scrollback_lines": len(screen.scrollback),
        "leaked_pids": leaked_pids,
        "screen_path": screen_out,
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


def main() -> int:
    parser = argparse.ArgumentParser(description="Plain-PTY ANSI screen capture & side-by-side diff")
    sub = parser.add_subparsers(dest="cmd", required=True)

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

    p_diff = sub.add_parser("diff", help="Write side-by-side and unified diffs of two captured screens")
    p_diff.add_argument("--omp-screen", required=True)
    p_diff.add_argument("--pig-screen", required=True)
    p_diff.add_argument("--out", required=True)
    p_diff.add_argument("--width", type=int, default=120)

    args = parser.parse_args()
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
        )
        print(json.dumps(meta))
        return 1 if meta["leaked_pids"] else 0

    a = open(args.omp_screen, "r", encoding="utf-8").read()
    b = open(args.pig_screen, "r", encoding="utf-8").read()
    sbs = side_by_side(a, b, width=args.width)
    udiff = "".join(
        difflib.unified_diff(
            a.splitlines(keepends=True),
            b.splitlines(keepends=True),
            fromfile="omp.screen.txt",
            tofile="oh-my-pig.screen.txt",
        )
    )
    with open(args.out, "w", encoding="utf-8") as fh:
        fh.write("=== SIDE-BY-SIDE ===\n")
        fh.write(sbs)
        fh.write("\n=== UNIFIED DIFF ===\n")
        fh.write(udiff or "(identical)\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
