#!/usr/bin/env python3
"""Runs one tmux scenario against Pi (the original under Pi) or PiG (the Go port) and records the screens.

usage: run.py HOST SCENARIO.json OUTDIR

HOST is `pi` or `pig`. The environment is read from STRETCHTEXT_TMUX_*:
  PI, PIG          the executables
  ORIGINAL         the original's index.ts (loaded with -e by pi)
  PORT             the port's extension directory (loaded with -e by pig)
  PIG_ARGS         extra arguments for pig (for example a fused build is --no-extensions and no -e)

A scenario is {"name", "replies": [...], "width", "height", "steps": [...]}. A step is one of
  {"send": "text"}               typed literally
  {"key": "Enter"}               a tmux key name (M-o is alt+o)
  {"raw": "\\u001b[<0;5;5M"}     bytes written as they are
  {"wait": seconds}
  {"until": "regex", "timeout": s}   wait until the visible screen matches
  {"click": "text", "nth": 0}    a left click on the nth screen occurrence of the text (SGR mouse)
  {"snap": "name"}               save the screen as OUTDIR/name.txt (plain) and name.ansi (escapes)
"""
import json, os, re, shutil, subprocess, sys, time, signal

host, scenario_path, out = sys.argv[1], sys.argv[2], sys.argv[3]
scenario = json.load(open(scenario_path))
env = os.environ
os.makedirs(out, exist_ok=True)
home = os.path.join(out, "home")
agent = os.path.join(out, "agent")
for d in (home, agent):
    shutil.rmtree(d, ignore_errors=True)
    os.makedirs(d)
work = os.path.join(out, "work")
shutil.rmtree(work, ignore_errors=True)
os.makedirs(work)

port = 20000 + (os.getpid() % 20000)
replies = os.path.join(out, "replies.json")
json.dump(scenario["replies"], open(replies, "w"))
llm = subprocess.Popen([sys.executable, os.path.join(os.path.dirname(os.path.abspath(__file__)), "mockllm.py"), str(port), replies])
time.sleep(0.5)

models = {"providers": {"mock": {"baseUrl": f"http://127.0.0.1:{port}/v1", "api": "openai-completions", "apiKey": "x",
    "models": [{"id": "mock", "name": "mock", "reasoning": False, "input": ["text"], "contextWindow": 100000, "maxTokens": 4000,
                "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}]}}}
json.dump(models, open(os.path.join(agent, "models.json"), "w"))
settings = {"defaultProvider": "mock", "defaultModel": "mock", "tuiMode": scenario.get("tuiMode", "fullscreen"), "quietStartup": True,
            "collapseChangelog": True, "theme": scenario.get("theme", "dark"), "enableInstallTelemetry": False, "compaction": {"enabled": False}}
settings.update(scenario.get("settings", {}))
json.dump(settings, open(os.path.join(agent, "settings.json"), "w"))

width, height = scenario.get("width", 100), scenario.get("height", 32)
session = f"st{os.getpid()}"
args = []
if host == "pi":
    cmd = [env["STRETCHTEXT_TMUX_PI"], "-e", env["STRETCHTEXT_TMUX_ORIGINAL"]]
else:
    cmd = [env["STRETCHTEXT_TMUX_PIG"]]
    if env.get("STRETCHTEXT_TMUX_PORT"):
        cmd += ["-e", env["STRETCHTEXT_TMUX_PORT"]]
    cmd += env.get("STRETCHTEXT_TMUX_PIG_ARGS", "").split()
cmd += ["--offline", "--model", "mock/mock", "--no-context-files", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-tools"] + scenario.get("args", [])

child_env = {"HOME": home, "PATH": env["PATH"], "TERM": "xterm-256color", "COLORTERM": "truecolor", "LANG": "C.UTF-8",
             "PI_CODING_AGENT_DIR": agent, "PIG_CODING_AGENT_DIR": agent, "PIG_HOME": os.path.join(home, ".pig"), "PI_OFFLINE": "1",
             "PI_SKIP_VERSION_CHECK": "1", "PIG_SKIP_VERSION_CHECK": "1", "GOCACHE": env.get("GOCACHE", "/tmp/gocache"),
             "GOMODCACHE": env.get("GOMODCACHE", "/go/pkg/mod"), "GOFLAGS": env.get("GOFLAGS", ""), "GOWORK": env.get("GOWORK", ""),
             "GOSUMDB": "off", "GOPROXY": "off", "GOTOOLCHAIN": "local"}
child_env.update(scenario.get("env", {}))
shell = "cd %s && exec env %s %s" % (work, " ".join(f"{k}={subprocess.list2cmdline([v])}" for k, v in child_env.items()), " ".join(subprocess.list2cmdline([c]) for c in cmd)) + " 2>>" + subprocess.list2cmdline([os.path.join(out, "stderr.txt")])
tmux = ["tmux", "-L", session]
subprocess.run(tmux + ["new-session", "-d", "-s", session, "-x", str(width), "-y", str(height), "bash", "-c", shell], check=True)


def screen(escapes=False):
    return subprocess.run(tmux + ["capture-pane", "-t", session, "-p"] + (["-e"] if escapes else []), capture_output=True, text=True).stdout


def stop():
    subprocess.run(tmux + ["kill-server"], capture_output=True)
    llm.send_signal(signal.SIGTERM)


def until(pattern, timeout):
    deadline = time.time() + timeout
    while time.time() < deadline:
        if re.search(pattern, screen(), re.M):
            return True
        time.sleep(0.1)
    return False


log = []
try:
    for i, step in enumerate(scenario["steps"]):
        if "send" in step:
            subprocess.run(tmux + ["send-keys", "-t", session, "-l", step["send"]], check=True)
        elif "key" in step:
            subprocess.run(tmux + ["send-keys", "-t", session, step["key"]], check=True)
        elif "raw" in step:
            subprocess.run(tmux + ["send-keys", "-t", session, "-l", step["raw"]], check=True)
        elif "wait" in step:
            time.sleep(step["wait"])
        elif "until" in step:
            ok = until(step["until"], step.get("timeout", 30))
            log.append({"step": i, "until": step["until"], "ok": ok})
            if not ok and not step.get("optional"):
                open(os.path.join(out, "FAILED.txt"), "w").write(f"step {i}: screen never matched {step['until']!r}\n\n" + screen())
                break
        elif "click" in step:
            lines = screen().split("\n")
            hits = [(r, l.index(step["click"])) for r, l in enumerate(lines) if step["click"] in l]
            hit = hits[step.get("nth", 0)] if len(hits) > step.get("nth", 0) else None
            if hit is None:
                open(os.path.join(out, "FAILED.txt"), "w").write(f"step {i}: no {step['click']!r} on screen\n\n" + screen())
                break
            row, col = hit[0] + 1, hit[1] + 1 + step.get("offset", 0)
            for final in ("M", "m"):
                subprocess.run(tmux + ["send-keys", "-t", session, "-l", f"\x1b[<0;{col};{row}{final}"], check=True)
                time.sleep(0.05)
        elif "snap" in step:
            time.sleep(step.get("settle", 0.4))
            open(os.path.join(out, step["snap"] + ".txt"), "w").write(screen())
            open(os.path.join(out, step["snap"] + ".ansi"), "w").write(screen(True))
        time.sleep(step.get("after", 0.15))
finally:
    json.dump(log, open(os.path.join(out, "log.json"), "w"))
    stop()
