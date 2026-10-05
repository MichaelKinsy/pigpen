#!/usr/bin/env python3
"""Full-stack repro of the bug environment with a MOCK RUNNING goal.

Stack: ghostty <-> zellij <-> pi (goal extension, mock ACTIVE goal with
tasks). Uses `zellij action` for precise control (write keystrokes, scroll,
switch mode, dump-screen) and captures zellij's rendered output + the pane's
internal scrollback state.

Flow:
  1. zellij -s repro (pane shell in the mock cwd)
  2. start pi, /goal-focus the mock goal, expand the widget
  3. resize the PTY below the widget height
  4. scroll up inside zellij
  5. trigger the debug mock audit (continuous goal-state churn)
  6. sample zellij's pane scrollback dump + the on-screen SCROLL: 0/N
     indicator repeatedly during the churn

Usage:
  python3 experiments/scroll-repro/zellij-mock-driver.py --seconds 60
"""
import argparse
import fcntl
import os
import pty
import select
import struct
import subprocess
import sys
import termios
import time

p = argparse.ArgumentParser()
p.add_argument("--rows", type=int, default=24)
p.add_argument("--cols", type=int, default=100)
p.add_argument("--seconds", type=float, default=60)
p.add_argument("--resize-to", type=int, default=12)
p.add_argument("--cwd", default="/tmp/pi-mock-repro")
p.add_argument("--out", default="/tmp/zj-mock.bin")
args = p.parse_args()

SESSION = "mockrepro"


def zcmd(*argv):
    env = dict(os.environ)
    env["ZELLIJ_SESSION_NAME"] = SESSION
    try:
        return subprocess.run(["zellij", "action", *argv],
                              capture_output=True, text=True, timeout=5, env=env).stdout
    except Exception as e:
        return f"(zellij action error: {e})"


pid, fd = pty.fork()
if pid == 0:
    os.environ["PI_GOAL_DEBUG"] = "1"
    os.chdir(args.cwd)
    os.execvp("zellij", ["zellij", "-s", SESSION])
    os._exit(1)


def set_size(fd, rows, cols):
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))


set_size(fd, args.rows, args.cols)
chunks = []
phases = []
marks = []
t0 = time.time()
W = (lambda: (time.time() - t0))


def send_raw(data, label):
    try:
        os.write(fd, data)
    except OSError:
        pass
    phases.append((W(), label))


def mark(label):
    marks.append((W(), label))
    chunks.append((W(), b"__MARK__%s__" % label.encode()))


def pane_write(text):
    zcmd("write-chars", text)
    zcmd("write", "13")


def dump():
    return zcmd("dump-screen", "--full")


def scroll_indicator():
    # The SCROLL: 0/N indicator is in zellij's rendered UI; find it in the
    # PTY stream. Simplest: count lines in zellij's own scrollback dump.
    d = dump()
    return len(d.splitlines()) if d else -1


stage = 0
prev_stage_time = 0
last_dump_len = None
churn_started = False
while time.time() - t0 < args.seconds:
    r, _, _ = select.select([fd], [], [], 0.05)
    if r:
        try:
            data = os.read(fd, 65536)
        except OSError:
            break
        if not data:
            break
        chunks.append((W(), data))
    now = W()
    if stage == 0 and now > 2:
        pane_write("pi")
        mark("pi-start")
        stage = 1
        prev_stage_time = now
    elif stage == 1 and now > 14:
        zcmd("write-chars", "/goal-focus")
        zcmd("write", "13")
        mark("focus-cmd")
        stage = 2
    elif stage == 2 and now > 17:
        zcmd("write", "13")  # select the only goal
        mark("goal-selected")
        stage = 3
    elif stage == 3 and now > 20:
        zcmd("write", "20")   # ctrl+t (expand; ctrl+shift+t in legacy)
        zcmd("write", "28")   # ctrl+shift+t kitty-style ESC [116;6u
        mark("expand-widget")
        stage = 4
    elif stage == 4 and now > 24:
        set_size(fd, args.resize_to, args.cols)
        os.kill(pid, 0)
        mark("resize-%d" % args.resize_to)
        stage = 5
    elif stage == 5 and now > 28:
        # type a message into the editor (like the user's typed message) to
        # make the below-widget content tall (editor wraps to 2-3 lines)
        zcmd("switch-mode", "normal")
        time.sleep(0.3)
        zcmd("write-chars", "STILL it's forcing the window to the bottom when the agent is running text above, and the N keeps changing")
        mark("typed-editor")
        zcmd("switch-mode", "scroll")
        time.sleep(0.3)
        zcmd("scroll-up")
        zcmd("scroll-up")
        zcmd("scroll-up")
        mark("scrolled-up-3")
        stage = 6
    elif stage == 6 and not churn_started and now > 32:
        # debug mode on (ctrl+shift+x), then start the mock audit
        # (ctrl+shift+r) via kitty sequences; the audit animation changes the
        # widget's top content lines continuously
        zcmd("switch-mode", "normal")
        zcmd("write", "\x1b[120;6u")   # ctrl+shift+x -> debug mode on
        time.sleep(0.5)
        zcmd("write", "\x1b[114;6u")   # ctrl+shift+r -> mock audit
        mark("mock-audit-start")
        churn_started = True
        stage = 7
        prev_stage_time = now
    elif stage == 7:
        # keep the audit churning by re-triggering every ~7s; stay scrolled up
        if now - prev_stage_time > 7:
            zcmd("switch-mode", "scroll")
            time.sleep(0.3)
            zcmd("scroll-up")
            zcmd("scroll-up")
            zcmd("scroll-up")
            zcmd("switch-mode", "normal")
            zcmd("write", "\x1b[114;6u")  # restart the mock audit
            mark("mock-audit-restart")
            prev_stage_time = now
        # sample zellij's pane scrollback state every ~1s
        if last_dump_len is None or now - last_dump_len > 1:
            d = dump()
            n = len(d.splitlines()) if d else -1
            phases.append((W(), f"dump-lines={n}"))
            last_dump_len = now

# final scroll dump + the frame indicator
mark("end")
zcmd("switch-mode", "normal")
d = dump()
print(f"final pane dump lines: {len(d.splitlines()) if d else -1}")
print("phases:", [(f"{t:.1f}s", label) for t, label in phases][-25:])

send_raw(b"\x03", "ctrl-c")
time.sleep(0.5)
try:
    os.close(fd)
    os.waitpid(pid, 0)
except Exception:
    pass

print("marks:", [(f"{t:.1f}s", label) for t, label in marks])

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
print(f"captured {sum(len(d) for _, d in chunks)} bytes -> {args.out}")
