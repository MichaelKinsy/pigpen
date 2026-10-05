#!/usr/bin/env python3
"""Drive the REAL pi binary in a PTY with the goal extension.

Focuses a goal from the pool, expands the widget (Ctrl+Shift+T), resizes the
PTY below the widget height, and captures ALL raw bytes to a file plus a
(timestamp, byte-offset) timeline, for post-analysis with @xterm/headless
(see pi-pty-analyze.mjs).

Usage:
  python3 experiments/scroll-repro/pi-pty-driver.py --out /tmp/pi-run.bin --rows 30 --cols 110 --seconds 22
"""
import argparse
import fcntl
import os
import pty
import select
import struct
import sys
import termios
import time

p = argparse.ArgumentParser()
p.add_argument("--rows", type=int, default=30)
p.add_argument("--cols", type=int, default=110)
p.add_argument("--seconds", type=float, default=22)
p.add_argument("--resize-to", type=int, default=14, help="resize rows mid-session (below the widget)")
p.add_argument("--cwd", default="/Users/tom/projects/pi-goal-x")
p.add_argument("--out", default="/tmp/pi-run.bin")
p.add_argument("--focus-index", type=int, default=17, help="index into the /goal-focus list (0-based)")
args = p.parse_args()

pid, fd = pty.fork()
if pid == 0:
    os.chdir(args.cwd)
    os.execvp("pi", ["pi"])
    os._exit(1)


def set_size(fd, rows, cols):
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))


set_size(fd, args.rows, args.cols)

chunks = []  # (t_offset, bytes)
t0 = time.time()
phases = []


def send(data, label):
    os.write(fd, data)
    phases.append((time.time() - t0, label))


focus_sent = select_sent = expand_sent = resize_done = False
while time.time() - t0 < args.seconds:
    r, _, _ = select.select([fd], [], [], 0.05)
    if r:
        try:
            data = os.read(fd, 65536)
        except OSError:
            break
        if not data:
            break
        chunks.append((time.time() - t0, data))
    now = time.time() - t0
    if not focus_sent and now > 6:
        send(b"/goal-focus\r", "focus-cmd")
        focus_sent = True
    elif focus_sent and not select_sent and now > 8.5:
        # navigate down to the chosen goal then select
        send(b"\x1b[B" * args.focus_index + b"\r", "select-goal")
        select_sent = True
    elif select_sent and not expand_sent and now > 11:
        # confirm focus dialog + expand the widget (Ctrl+Shift+T). In a legacy
        # PTY (no kitty protocol response) ctrl+shift+t is ambiguous, so send
        # both the legacy ctrl+t byte and the kitty-style \x1b[116;6u sequence.
        send(b"\r", "confirm-focus")
        send(b"\x14", "expand-widget-ctrl-t")
        send(b"\x1b[116;6u", "expand-widget-kitty")
        expand_sent = True
    elif expand_sent and not resize_done and now > 14:
        set_size(fd, args.resize_to, args.cols)
        os.kill(pid, 0)
        resize_done = True
        chunks.append((time.time() - t0, b"__RESIZE__%d__" % args.resize_to))
        phases.append((time.time() - t0, "resize-to-%d" % args.resize_to))

send(b"\x03", "ctrl-c")
time.sleep(0.4)
try:
    os.close(fd)
    os.waitpid(pid, 0)
except Exception:
    pass

with open(args.out, "wb") as f:
    for _, data in chunks:
        if not data.startswith(b"__RESIZE__"):
            f.write(data)
with open(args.out + ".timeline", "w") as f:
    for t, data in chunks:
        if data.startswith(b"__RESIZE__"):
            f.write(f"RESIZE\t{int(data[10:-2])}\n")
        else:
            f.write(f"{t:.3f}\t{len(data)}\n")

print("phases:", [(f"{t:.1f}s", label) for t, label in phases])
print(f"captured {sum(len(d) for _, d in chunks)} bytes -> {args.out}")
