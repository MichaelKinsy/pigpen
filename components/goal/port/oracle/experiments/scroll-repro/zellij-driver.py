#!/usr/bin/env python3
"""Drive the FULL user stack: zellij running pi in a pane, inside a controlled
PTY. Captures zellij's rendered output (what ghostty would show) with a
timeline for @xterm/headless post-analysis.

Flow:
  1. zellij -s repro starts (pane runs the default shell)
  2. type `pi` to start pi inside the pane
  3. /goal-focus, navigate to the current goal, Enter, expand the widget
  4. resize the outer PTY below the widget height
  5. enter zellij scroll mode, scroll up, keep watching while pi sits idle

Usage:
  python3 experiments/scroll-repro/zellij-driver.py --out /tmp/zj.bin --seconds 40
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
p.add_argument("--rows", type=int, default=24)
p.add_argument("--cols", type=int, default=100)
p.add_argument("--seconds", type=float, default=40)
p.add_argument("--resize-to", type=int, default=12)
p.add_argument("--out", default="/tmp/zj.bin")
p.add_argument("--focus-index", type=int, default=17)
args = p.parse_args()

pid, fd = pty.fork()
if pid == 0:
    os.chdir("/Users/tom/projects/pi-goal-x")
    os.execvp("zellij", ["zellij", "-s", "repro"])
    os._exit(1)


def set_size(fd, rows, cols):
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))


set_size(fd, args.rows, args.cols)

chunks = []
phases = []
t0 = time.time()


def send(data, label):
    try:
        os.write(fd, data)
    except OSError:
        pass
    phases.append((time.time() - t0, label))


def mark(label):
    phases.append((time.time() - t0, label))
    chunks.append((time.time() - t0, b"__MARK__%s__" % label.encode()))


stage = 0
resized = scrolled = False
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
    if stage == 0 and now > 2:
        send(b"pi\r", "start-pi")
        stage = 1
    elif stage == 1 and now > 14:
        send(b"/goal-focus\r", "focus-cmd")
        stage = 2
    elif stage == 2 and now > 17:
        send(b"\x1b[B" * args.focus_index + b"\r", "select-goal")
        stage = 3
    elif stage == 3 and now > 20:
        send(b"\r", "confirm-focus")
        send(b"\x14", "expand-ctrl-t")
        send(b"\x1b[116;6u", "expand-kitty")
        stage = 4
    elif stage == 4 and not resized and now > 24:
        set_size(fd, args.resize_to, args.cols)
        os.kill(pid, 0)
        resized = True
        mark("resize-%d" % args.resize_to)
    elif stage == 4 and resized and not scrolled and now > 28:
        # enter zellij scroll mode (Ctrl+s) and scroll up 12 lines
        send(b"\x13", "scroll-mode")  # Ctrl+S
        time.sleep(0.6)
        send(b"k" * 12, "scroll-up-12")
        scrolled = True
        mark("scrolled-up-12")

send(b"\x03", "ctrl-c")
time.sleep(0.5)
try:
    os.close(fd)
    os.waitpid(pid, 0)
except Exception:
    pass

with open(args.out, "wb") as f:
    for _, data in chunks:
        if not data.startswith(b"__MARK__"):
            f.write(data)
with open(args.out + ".timeline", "w") as f:
    for t, data in chunks:
        if data.startswith(b"__MARK__"):
            f.write(f"MARK\t{data[8:-2].decode()}\n")
        else:
            f.write(f"{t:.3f}\t{len(data)}\n")

print("phases:", [(f"{t:.1f}s", label) for t, label in phases])
print(f"captured {sum(len(d) for _, d in chunks)} bytes -> {args.out}")
