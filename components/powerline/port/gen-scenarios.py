#!/usr/bin/env python3
"""Generates render/states.json: scripted host states for drive/drive.mjs, which runs the ORIGINAL extension and
records its bars (golden/render.json). The Go port's layer-1 test replays the same states."""
import json, os
here = os.path.dirname(os.path.abspath(__file__))

MODEL = {"id": "claude-sonnet-4-5", "name": "Claude Sonnet 4.5", "provider": "anthropic", "reasoning": True, "contextWindow": 200000}
PLAIN = {"id": "gpt-5", "name": "GPT-5", "provider": "openai", "reasoning": False, "contextWindow": 400000}

def usage(i=0, o=0, cr=0, cw=0, cost=0.0, total=None):
    u = {"input": i, "output": o, "cacheRead": cr, "cacheWrite": cw, "cost": {"total": cost}}
    if total is not None: u["totalTokens"] = total
    return u

def asst(u, stop="stop"):
    return {"type": "message", "message": {"role": "assistant", "content": [], "usage": u, "stopReason": stop}}

S = []
def sc(name, **kw):
    kw["name"] = name
    kw.setdefault("model", MODEL)
    kw.setdefault("sessionId", "0123456789abcdef")
    kw.setdefault("widths", [140, 70, 30])
    S.append(kw)

BR = [asst(usage(1200, 340, 800, 50, 0.0123, 2390)), asst(usage(15000, 2500, 12000, 0, 0.35))]
GIT = {"gitBranch": "main"}

for p in ["default", "minimal", "compact", "full", "nerd", "ascii"]:
    sc(f"preset-{p}", settings={"powerline": p}, branch=BR, thinkingLevel="medium", contextUsage={"tokens": 18000, "contextWindow": 200000, "percent": 9.0}, elapsedMs=125000, **GIT)
for p in ["default", "full", "nerd"]:
    sc(f"preset-{p}-nerd-fonts", settings={"powerline": p}, env={"POWERLINE_NERD_FONTS": "1"}, branch=BR, thinkingLevel="high", contextUsage={"tokens": 150000, "contextWindow": 200000, "percent": 75.0}, elapsedMs=3700000, **GIT)
sc("unknown-preset", settings={"powerline": "bogus"}, branch=BR)
sc("no-settings", branch=[])
sc("model-qualified", settings={"powerline": {"preset": "default", "segmentOptions": {"model": {"display": "qualified", "showThinkingLevel": True}}}}, thinkingLevel="low")
sc("model-plain-no-reasoning", model=PLAIN, thinkingLevel="high")
sc("no-model", model=None)
for lv in ["off", "minimal", "low", "medium", "high", "xhigh", "max", "weird"]:
    sc(f"thinking-{lv}", settings={"powerline": {"preset": "full"}}, thinkingLevel=lv)
sc("thinking-in-model-ascii", settings={"powerline": {"preset": "ascii"}}, thinkingLevel="xhigh")
sc("thinking-from-session", branch=[{"type": "thinking_level_change", "thinkingLevel": "minimal"}], thinkingLevel="high", settings={"powerline": {"preset": "full"}})
sc("errored-turns-ignored", branch=[asst(usage(100, 10), "error"), asst(usage(7, 3, 0, 0, 0.5), "aborted"), asst(usage(11, 5, 2, 1, 0.02))])
sc("subagent-cost", branch=[asst(usage(10, 5, 0, 0, 0.10)), {"type": "message", "message": {"role": "toolResult", "toolName": "subagent", "details": {"results": [{"usage": {"cost": 0.25}}, {"usage": {"cost": "x"}}, 3]}}}, {"type": "custom_message", "customType": "subagent-slash-result", "details": {"result": {"details": {"results": [{"usage": {"cost": 0.5}}]}}}}], settings={"powerline": {"preset": "full"}})
for n, cu in [("known-low", {"tokens": 1000, "contextWindow": 200000, "percent": 0.5}), ("warn", {"tokens": 150000, "contextWindow": 200000, "percent": 75}), ("error", {"tokens": 190000, "contextWindow": 200000, "percent": 95}), ("unknown", {"tokens": None, "contextWindow": 200000, "percent": None})]:
    sc(f"context-{n}", contextUsage=cu, branch=BR)
    sc(f"context-{n}-percent", contextUsage=cu, branch=BR, settings={"powerline": {"preset": "default", "segmentOptions": {"context": {"format": "percent"}}}})
sc("context-fallback-from-usage", branch=BR, contextUsage=None, settings={"powerline": {"preset": "full"}})
sc("context-no-window", model={"id": "x", "name": "X"}, branch=BR, settings={"powerline": {"preset": "nerd"}})
sc("auto-compact-off", autoCompact=False, contextUsage={"tokens": 5, "contextWindow": 100, "percent": 5})
sc("cost-subscription", usingOAuth=True, branch=BR, settings={"powerline": {"preset": "default"}})
for d in ["subscription", "reported-cost", "both"]:
    sc(f"cost-sub-{d}", usingOAuth=True, branch=BR, settings={"powerline": {"preset": "default", "segmentOptions": {"cost": {"subscriptionDisplay": d}}}})
sc("cost-sub-no-cost", usingOAuth=True, branch=[], settings={"powerline": {"preset": "default", "segmentOptions": {"cost": {"subscriptionDisplay": "both"}}}})
sc("cost-jpy-no-rates", branch=BR, settings={"powerline": {"preset": "default", "segmentOptions": {"cost": {"currency": "JPY"}}}})
sc("cost-bad-currency", branch=BR, settings={"powerline": {"preset": "default", "segmentOptions": {"cost": {"currency": "xyz"}}}})
for f in ["tokens", "percent", "both"]:
    sc(f"cache-read-{f}", branch=BR, settings={"powerline": {"preset": "full", "segmentOptions": {"cache_read": {"format": f}}}})
sc("cache-write", branch=BR, settings={"powerline": {"layout": {"left": ["model", "cache_write", "token_total"]}}})
for m in ["basename", "abbreviated", "full"]:
    sc(f"path-{m}", cwd="/work/team/some/very/deep/project/directory/name", settings={"powerline": {"preset": "default", "segmentOptions": {"path": {"mode": m, "maxLength": 20}}}})
sc("path-home", cwd="{HOME}/code/app", settings={"powerline": {"preset": "full"}})
sc("path-work-prefix", cwd="/work/proj", settings={"powerline": {"preset": "nerd"}})
sc("git-provider-only", gitBranch="feat/x", settings={"powerline": {"preset": "default"}}, env={}, statuses={})
sc("git-hidden", gitBranch="main", settings={"powerline": {"preset": "default", "segmentOptions": {"git": {"showBranch": False, "showStaged": False, "showUnstaged": False, "showUntracked": False}}}})
sc("git-branch-hidden", gitBranch="main", settings={"powerline": {"preset": "default", "segmentOptions": {"git": {"showBranch": False}}}})
sc("time-12h", settings={"powerline": {"layout": {"left": ["model", "time"]}, "segmentOptions": {"time": {"format": "12h", "showSeconds": True}}}}, start="2026-03-04T15:06:07Z")
sc("time-24h", settings={"powerline": {"layout": {"left": ["model", "time", "time_spent"]}}}, start="2026-03-04T00:06:07Z", elapsedMs=65000)
sc("time-spent-short", settings={"powerline": {"layout": {"left": ["model", "time_spent"]}}}, elapsedMs=900)
sc("time-spent-hours", settings={"powerline": {"layout": {"left": ["model", "time_spent"]}}}, elapsedMs=7384000)
sc("session-name", sessionName="  my session ", settings={"powerline": {"layout": {"left": ["session", "model"]}}})
sc("session-blank-name", sessionName="   ", settings={"powerline": {"layout": {"left": ["session"]}}})
sc("session-new", sessionId=None, settings={"powerline": {"layout": {"left": ["session"]}}})
sc("hostname", settings={"powerline": {"layout": {"left": ["hostname", "model"]}}})
for sep in ["powerline", "powerline-thin", "slash", "pipe", "block", "none", "ascii", "dot", "chevron", "star", "nope"]:
    sc(f"separator-{sep}", settings={"powerline": {"preset": "default", "separator": sep}}, branch=BR)
sc("separator-nerd-powerline", settings={"powerline": {"separator": "powerline"}}, env={"POWERLINE_NERD_FONTS": "1"}, branch=BR)
for k, v in [("above", "above"), ("below", "below"), ("bad", "sideways")]:
    sc(f"placement-{k}", settings={"powerline": {"placement": v}})
sc("layout-custom", settings={"powerline": {"layout": {"left": ["model", "nope", "path"], "right": ["session"], "secondary": ["time_spent"]}}}, elapsedMs=5000)
sc("disabled-segments", settings={"powerline": {"preset": "full", "disabledSegments": ["hostname", "time", "bogus", "git"]}}, branch=BR, **GIT)
sc("ext-statuses", settings={"powerline": {"preset": "default"}}, statuses={"lsp": "ok 3", "empty": "", "notif": "[reminder] do the thing", "ansi": "\x1b[31m\x1b[0m", "styled": "\x1b[32mgreen\x1b[0m done"})
sc("ext-statuses-full", settings={"powerline": {"preset": "full"}}, statuses={"lsp": "ok 3", "b": "two\nlines"})
sc("custom-item", settings={"powerline": {"customItems": [{"id": "lsp", "statusKey": "lsp", "position": "left", "prefix": "LSP", "color": "accent"}], "preset": "default"}}, statuses={"lsp": "ready", "other": "x"})
sc("custom-item-missing", settings={"powerline": {"customItems": [{"id": "lsp", "statusKey": "lsp", "position": "right", "hideWhenMissing": False, "prefix": "LSP"}, {"id": "gone", "statusKey": "gone", "position": "secondary"}]}})
sc("custom-item-self-colorize", settings={"powerline": {"customItems": {"fmt": {"statusKey": "fmt", "selfColorize": True, "color": "error", "position": "left"}}}}, statuses={"fmt": "\x1b[33mwarn\x1b[0m!"})
sc("custom-item-hex-color", settings={"powerline": {"customItems": [{"id": "h", "statusKey": "h", "color": "#ff8800", "position": "left"}]}}, statuses={"h": "v"})
sc("custom-in-layout", settings={"powerline": {"customItems": [{"id": "lsp", "statusKey": "lsp"}], "layout": {"left": ["model", "custom:lsp", "custom:missing"]}}}, statuses={"lsp": "up"})
sc("theme-json-colors", themeJson={"colors": {"model": "#112233", "path": "warning", "bogus": "x", "cost": "  ", "tokens": 5}, "icons": {"folder": "D:", "model": "M", "pi": 1}}, branch=BR, settings={"powerline": {"preset": "full"}})
sc("theme-bad-token", themeJson={"colors": {"model": "notAThemeColor"}})
sc("invalid-segments-warn", settings={"powerline": {"disabledSegments": ["nope", "x"], "layout": {"left": ["zzz", "model"]}, "placement": "up"}})
sc("rpc-mode", mode="rpc", settings={"powerline": {"preset": "full"}}, branch=BR)
# command steps
sc("cmd-toggle", steps=[{"command": "powerline", "args": ""}, {"command": "powerline", "args": ""}])
sc("cmd-toggle-off", steps=[{"command": "powerline", "args": ""}])
sc("cmd-preset", steps=[{"command": "powerline", "args": "nerd"}], branch=BR)
sc("cmd-preset-case", steps=[{"command": "powerline", "args": "  MINIMAL "}])
sc("cmd-unknown", steps=[{"command": "powerline", "args": "wat"}])
sc("cmd-placement", steps=[{"command": "powerline", "args": "placement below"}, {"command": "powerline", "args": "placement"}, {"command": "powerline", "args": "placement toggle"}, {"command": "powerline", "args": "placement above"}])
sc("event-model-select", steps=[{"event": "model_select", "data": {"model": PLAIN}}])
sc("event-thinking-level", steps=[{"event": "thinking_level_select", "data": {"level": "xhigh"}}], thinkingLevel="low", settings={"powerline": {"preset": "full"}})
sc("status-update-after-start", steps=[{"status": {"late": "arrived"}}], settings={"powerline": {"preset": "default"}})

# More states, added after the first mutation run showed which code the first set never reached.
def repo(**kw): return kw
sc("git-dirty", repo={"branch": "feat/x", "staged": 1, "unstaged": 2, "untracked": 3}, settings={"powerline": {"preset": "default"}})
sc("git-clean", repo={"branch": "main"}, settings={"powerline": {"preset": "default"}})
for name, remote in [("github", "https://github.com/o/r"), ("gitlab", "git@gitlab.com:o/r.git"), ("bitbucket", "https://u@bitbucket.org/o/r.git"), ("other", "https://git.example.com/o/r")]:
    sc(f"git-host-{name}", repo={"branch": "main", "remote": remote}, env={"POWERLINE_NERD_FONTS": "1"}, settings={"powerline": {"preset": "default", "git": {"hostIcon": True}}})
sc("git-host-no-remote", repo={"branch": "main"}, env={"POWERLINE_NERD_FONTS": "1"}, settings={"powerline": {"preset": "default", "git": {"hostIcon": True}}})
sc("git-detached", repo={"branch": "main", "detached": True}, settings={"powerline": {"preset": "default"}})
sc("git-detached-provider", repo={"branch": "main"}, gitBranch="detached", settings={"powerline": {"preset": "default"}})
sc("git-provider-branch", repo={"branch": "other", "staged": 1}, gitBranch="main", settings={"powerline": {"preset": "default"}})
for mode in ["branch", "off"]:
    sc(f"git-polling-{mode}", repo={"branch": "feat", "staged": 1, "unstaged": 1, "untracked": 1}, gitBranch="provided", settings={"powerline": {"preset": "default", "git": {"polling": mode}}})
sc("git-branch-hidden-dirty", repo={"branch": "main", "staged": 1, "unstaged": 2, "untracked": 1}, env={"POWERLINE_NERD_FONTS": "1"}, settings={"powerline": {"preset": "default", "git": {"showBranch": False}}})
sc("git-only-unstaged", repo={"branch": "main", "staged": 1, "unstaged": 2, "untracked": 1}, settings={"powerline": {"preset": "default", "git": {"showStaged": False, "showUntracked": False}}})
sc("git-only-staged", repo={"branch": "main", "staged": 1, "unstaged": 2, "untracked": 1}, settings={"powerline": {"preset": "default", "git": {"showUnstaged": False}}})
sc("git-all-hidden-dirty", repo={"branch": "main", "staged": 1}, settings={"powerline": {"preset": "default", "git": {"showBranch": False, "showStaged": False, "showUnstaged": False, "showUntracked": False}}})
sc("model-thinking-no-reasoning", model=PLAIN, thinkingLevel="high", settings={"powerline": {"preset": "default", "model": {"showThinkingLevel": True}}})
sc("model-thinking-off", thinkingLevel="off", settings={"powerline": {"preset": "default", "model": {"showThinkingLevel": True}}})
sc("model-thinking-shown", thinkingLevel="high", settings={"powerline": {"preset": "default", "model": {"showThinkingLevel": True}}})
sc("model-thinking-nerd", thinkingLevel="medium", env={"POWERLINE_NERD_FONTS": "1"}, settings={"powerline": {"preset": "ascii"}})
sc("path-abbrev-default", cwd="{HOME}/aaaaaaaaaa/bbbbbbbbbb/cccccccccc/dddddddddd/eeeeeeeeee/ffffffffff", settings={"powerline": {"preset": "default", "path": {"mode": "abbreviated"}}})
sc("path-abbrev-limit", cwd="{HOME}/aaaaaaaaaa/bbbbbbbbbb/cccccccccc", settings={"powerline": {"preset": "default", "path": {"mode": "abbreviated", "maxLength": 20.7}}})
for v in [0, -5]:
    sc(f"path-maxlen-{abs(v)}", cwd="{HOME}/aaaaaaaaaa/bbbbbbbbbb/cccccccccc/dddddddddd/eeeeeeeeee/ffffffffff", settings={"powerline": {"preset": "default", "path": {"mode": "abbreviated", "maxLength": v}}})
sc("tokens-large", branch=[asst(usage(1234567, 25000000, 999999, 9999999, 1.5))], settings={"powerline": {"layout": {"left": ["token_in", "token_out", "token_total", "cache_read", "cache_write"]}}})
sc("tokens-boundaries", branch=[asst(usage(999, 1000, 9999, 10000))], settings={"powerline": {"layout": {"left": ["token_in", "token_out", "cache_read", "cache_write"]}}})
sc("time-spent-1500", elapsedMs=1500, settings={"powerline": {"layout": {"left": ["model", "time_spent"]}}})
sc("time-12h-noon", start="2026-03-04T12:30:00Z", settings={"powerline": {"layout": {"left": ["time"]}, "time": {"format": "12h"}}})
sc("time-12h-midnight", start="2026-03-04T00:15:00Z", settings={"powerline": {"layout": {"left": ["time"]}, "time": {"format": "12h"}}})
sc("time-12h-pm", start="2026-03-04T13:05:09Z", settings={"powerline": {"layout": {"left": ["time"]}, "time": {"format": "12h", "showSeconds": True}}})
sc("custom-hex-upper", settings={"powerline": {"customItems": [{"id": "h", "statusKey": "h", "color": "#FF8800", "position": "left"}]}}, statuses={"h": "v"})
sc("theme-json-padded", themeJson={"colors": {"model": "  #112233  "}})
sc("config-placement-upper", settings={"powerline": {"placement": " BELOW "}})
sc("config-custom-id-hyphen", settings={"powerline": {"customItems": [{"id": "my-item", "statusKey": "k", "position": "left"}]}}, statuses={"k": "v"})
sc("config-custom-duplicate-ids", settings={"powerline": {"customItems": [{"id": "a", "statusKey": "k1", "position": "left", "prefix": "ONE"}, {"id": "a", "statusKey": "k2", "position": "right", "prefix": "TWO"}]}}, statuses={"k1": "x", "k2": "y"})
sc("status-notification-padded", settings={"powerline": {"preset": "default"}}, statuses={"n": "  [lead] note"})
sc("status-notification-wide", settings={"powerline": {"preset": "default"}}, statuses={"n": "[a notification that is much wider than the narrowest terminal]"}, widths=[140, 70, 30])
sc("no-ui", hasUI=False, settings={"powerline": {"disabledSegments": ["nope"]}})
sc("compact-policy-status", settings={"powerline": {"preset": "full"}}, statuses={"compact-policy": "on"}, contextUsage={"tokens": 10, "contextWindow": 100, "percent": 10})
sc("streaming-usage", contextUsage=None, steps=[{"event": "agent_start"}, {"event": "message_update", "data": {"message": {"role": "assistant", "content": [], "usage": usage(500, 100, 0, 0, 0.01, 600), "stopReason": "streaming"}}}], settings={"powerline": {"preset": "full"}}, model={"id": "m", "name": "M", "contextWindow": 1000})
sc("streaming-usage-after-end", contextUsage=None, steps=[{"event": "agent_start"}, {"event": "message_update", "data": {"message": {"role": "assistant", "content": [], "usage": usage(500, 100, 0, 0, 0.01, 600), "stopReason": "streaming"}}}, {"event": "agent_end"}], branch=[asst(usage(40, 10, 0, 0, 0.001))], settings={"powerline": {"preset": "full"}}, model={"id": "m", "name": "M", "contextWindow": 1000})
sc("thinking-from-session-after-tree", branch=[{"type": "thinking_level_change", "thinkingLevel": "minimal"}], thinkingLevel="high", steps=[{"event": "session_tree"}], settings={"powerline": {"preset": "full"}})
sc("thinking-unset-after-tree", thinkingLevel="high", steps=[{"event": "session_tree"}], settings={"powerline": {"preset": "full"}})
sc("subagent-other-tool", branch=[{"type": "message", "message": {"role": "toolResult", "toolName": "bash", "details": {"results": [{"usage": {"cost": 5}}]}}}, asst(usage(10, 5, 0, 0, 0.1))], settings={"powerline": {"preset": "full"}})
sc("project-settings-override", projectSettings={"powerline": {"preset": "minimal"}, "other": 1}, settings={"powerline": {"preset": "full", "placement": "below"}})
sc("project-settings-merge", projectSettings={"powerline": {"separator": "pipe"}}, settings={"powerline": {"preset": "compact", "placement": "below"}})
sc("cmd-writes-project", projectSettings={"powerline": {"preset": "minimal"}}, steps=[{"command": "powerline", "args": "nerd"}])
sc("cmd-writes-project-placement", projectSettings={"powerline": "compact"}, steps=[{"command": "powerline", "args": "placement below"}])
sc("cmd-preset-keeps-object", settings={"powerline": {"preset": "default", "customItems": [{"id": "ci"}], "placement": "below"}}, steps=[{"command": "powerline", "args": "full"}])
sc("cmd-malformed-global", rawSettings="{not json", steps=[{"command": "powerline", "args": "nerd"}, {"command": "powerline", "args": "placement below"}])
sc("cmd-array-settings", rawSettings="[1, 2]", steps=[{"command": "powerline", "args": "nerd"}])
sc("cmd-preset-from-string-settings", settings={"powerline": "compact"}, steps=[{"command": "powerline", "args": "placement below"}])
sc("cmd-placement-spacing", steps=[{"command": "powerline", "args": "placement   BELOW"}, {"command": "powerline", "args": "placement sideways"}, {"command": "powerline", "args": "placementx"}])
sc("cmd-settings-key-order", rawSettings='{"z":1,"10":2,"2":3,"powerline":{"preset":"default"},"a":4}', steps=[{"command": "powerline", "args": "nerd"}])
sc("cost-krw", isolate=True, currencyRates={"krw": 1300, "cny": 7.2}, branch=[asst(usage(1, 1, 0, 0, 1.234))], settings={"powerline": {"preset": "default", "cost": {"currency": "krw"}}})
sc("cost-cny-cached", isolate=True, currencyRates={"cny": 7.2}, branch=[asst(usage(1, 1, 0, 0, 1.0))], settings={"powerline": {"preset": "default", "cost": {"currency": "CNY"}}})
sc("cost-eur-missing-rate", isolate=True, currencyRates={"cny": 7.2}, branch=[asst(usage(1, 1, 0, 0, 1.0))], settings={"powerline": {"preset": "default", "cost": {"currency": "EUR"}}})

# the Go port reads $HOME-relative values; the driver makes a temp HOME, so {HOME} is substituted there
sc("context-fallback-total-tokens", contextUsage=None, branch=[asst(usage(100, 50, 0, 0, 0.01, 5000))], settings={"powerline": {"preset": "full"}}, model={"id": "m", "name": "M", "contextWindow": 10000})

# Bad input: control characters and escape codes in host-supplied text (the session name, the working directory, the model
# name). Neither the original nor the port strips them; the layout must measure them as pi-tui's visibleWidth does (a tab is
# 3 cells, a control or format character none, APC and OSC sequences none), at widths where one cell moves a segment.
BAD_LAYOUT = {"powerline": {"layout": {"left": ["model", "session", "path", "time_spent", "context_pct"]}}}
BAD_WIDTHS = [140, 60, 44, 40, 36, 30]
for k, v in [("tab", "a\tb\tc"), ("osc-title", "x\x1b]0;PWNED\x07y"), ("osc-st", "x\x1b]0;PWNED\x1b\\y"), ("csi-clear", "\x1b[2Jcls"),
             ("csi-up", "up\x1b[2Ax"), ("bell", "bell\x07"), ("apc", "apc\x1b_hidden\x1b\\x"), ("lone-esc", "esc\x1bz"),
             ("cr-lf", "line1\r\nline2"), ("nul-del", "n\x00u\x7fl"), ("c1", "c1\x85\x9bz"), ("combining", "e\u0301e\u0301e\u0301"),
             ("soft-hyphen", "soft\u00adhy"), ("cjk", "\u6f22\u5b57\u540d\u524d"), ("nbsp-only", "\u00a0\u3000")]:
    sc(f"bad-session-{k}", sessionName=v, settings=BAD_LAYOUT, widths=BAD_WIDTHS)
for k, v in [("tab", "pr\toj"), ("osc", "pr\x1b]0;T\x07oj"), ("esc", "pr\x1b[31moj")]:
    sc(f"bad-cwd-{k}", cwd="{HOME}/work/" + v, settings=BAD_LAYOUT, widths=[140, 60, 40, 30])
sc("bad-model-name", model=dict(MODEL, name="Son\tnet\x1b]0;X\x07 4.5"), settings=BAD_LAYOUT, widths=[140, 60, 40, 30])
names = [x["name"] for x in S]
assert len(names) == len(set(names)), "duplicate state names: %s" % sorted({n for n in names if names.count(n) > 1})
# git-status.ts keeps module-level caches and pending fetches; a state that reads a repository gets its own process.
for st in S:
    if st.get("repo"):
        st["isolate"] = True
# Options live at the top level of the powerline object, not under a segmentOptions key (the first set of states had it wrong
# and so tested nothing): flatten them.
for st in S:
    pl = (st.get("settings") or {}).get("powerline")
    if isinstance(pl, dict) and "segmentOptions" in pl:
        pl.update(pl.pop("segmentOptions"))
os.makedirs(os.path.join(here, "..", "extensions", "powerline-footer", "testdata"), exist_ok=True)
json.dump(S, open(os.path.join(here, "..", "extensions", "powerline-footer", "testdata", "render-states.json"), "w"), indent=1, ensure_ascii=False)
open(os.path.join(here, "..", "extensions", "powerline-footer", "testdata", "render-states.json"), "a").write("\n")
print(len(S), "scenarios")
