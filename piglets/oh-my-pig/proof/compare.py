#!/usr/bin/env python3
"""Compare headless Tern DOM trees and TSP wire frames between omp and oh-my-pig.

Honesty rule:
  For every compared transcript/chrome part, this comparer walks BOTH subtrees
  recursively and diffs:
    - node kinds (`$kind`) and roles (`role`),
    - child alignment (missing/extra children as `$node`),
    - EVERY non-ephemeral TSP prop on matched nodes (spans, text, tone, status,
      frame, target, targetKind, meta, badges, preview, collapsible, collapsed,
      numbers, wrap, align, gap, lines, hunks, phases, stats, actions, href, ...),
    - Tern DOM verification (`sf-<kind>` present in the live DOM tree).

  Narrow sub-difference allowlist (`drawing_allowlist.json`):
    Each entry names a specific `(part, node_path, prop, side)` sub-difference
    with `class` (`HOST`, `LISTED`, `ON_PURPOSE`) and a verified `ref`.
    The comparer subtracts ONLY matching sub-differences:
      - `same`:     0 differences (both sides present, every node & prop matches).
      - `expected`: >= 1 difference, and EVERY difference matched an allowlist entry
                    (including an allowlisted host-absent part such as `slash menu`).
      - `timing`:   ONLY scheduling-timing noise (e.g. `omp.thinking` head text
                    `Thought` vs `Thought for <d>`, never normalized silently).
      - `differs`:  >= 1 UNEXPLAINED difference remains after allowlist subtraction
                    (even if other differences in the same part were allowlisted or timing).
      - `missing`:  part absent on one/both sides and not allowlisted.
"""

from __future__ import annotations

import argparse
import json
import os
import re
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

SCRIPT_DIR = Path(__file__).resolve().parent
import sys
if str(SCRIPT_DIR) not in sys.path:
    sys.path.insert(0, str(SCRIPT_DIR))
from allowlist_refs import resolve_allowlist_entries  # noqa: E402
DEFAULT_DRAWING_ALLOWLIST_PATH = SCRIPT_DIR / "drawing_allowlist.json"

# Tern DOM classes that correspond directly to TSP node kinds (sf-<kind>).
SF_KIND_RE = re.compile(r"^sf-([a-z0-9-]+)$")
KNOWN_TSP_KINDS = {
    "col", "row", "card", "section", "rule", "spacer", "text", "md", "code",
    "diff", "ansi", "math", "image", "media", "web", "kv", "stats", "table",
    "tree", "json", "badge", "kbd", "icon", "spinner", "shimmer", "elapsed",
    "progress", "rate", "list", "item", "tabs", "editor", "input", "button",
    "status", "seg", "overlay", "toast", "rows", "picker", "prefs", "tool",
    "checklist", "agent", "chart", "meter", "block", "effort", "select",
    "switch", "slider", "field", "otp", "buttons", "avatar", "avatars",
    "breadcrumb", "pager", "empty", "skeleton", "calendar", "date", "menu",
    "menubar", "nav", "split", "scroll", "carousel", "message", "attachment",
    "trace", "stacktrace", "shell", "suggestions", "questions", "doc", "el",
}

# Path normalisers (Finding 2):
# omp strips `/private` on macOS when both spellings resolve alike (`packages/utils/src/dirs.ts:148-158`).
# Do NOT conflate `/private/tmp/...` and `/tmp/...` into the same token: map `/private/tmp/.../(omp|pig)/work`
# to `/private<WORK>` and `/tmp/.../(omp|pig)/work` to `<WORK>`, so a port that forgets to strip `/private`
# shows a real visible diff instead of being masked.
_RE_TEMP_WORK_PATH_PRIVATE = re.compile(r"/private/tmp/[^/\s\"']+(?:/[^/\s\"']+)*?/(?:omp|pig)/work\b")
_RE_TEMP_WORK_PATH = re.compile(r"(?<!/private)/tmp/[^/\s\"']+(?:/[^/\s\"']+)*?/(?:omp|pig)/work\b")
_RE_HTTP_LOG_PATH = re.compile(
    r"(?:/private|/var)?/tmp/[^/\s\"']+(?:/[^/\s\"']+)*?/(?:omp|pig)/home/\.omp/logs/http-\d+-requests/[0-9a-zA-Z_-]+\.json"
)

# Wire-internal reconciler/cross-reference TSP props dropped before comparison (Finding 21):
# - `key`:  internal reconciler key (e.g. `call_4_0` vs Go's `working`/`editor`).
# - `head`: internal cross-reference node ID (`s.k0/head` vs `n.n13.0.head`).
WIRE_INTERNAL_PROP_KEYS = frozenset({"key", "head"})

# Timing props whose numeric VALUE is masked to `<MS>` while PRESENCE and TYPE are preserved (Finding 21):
# - `took`:     run-specific wall-clock duration in milliseconds (number).
# - `age`:      run-specific elapsed-timer age in milliseconds (number).
# - `stopped`:  run-specific elapsed-timer stop timestamp/ms (number).
TIMING_VALUE_PROP_KEYS = frozenset({"took", "age", "stopped"})

# Legacy alias kept for documentation/tests (only truly dropped wire-internal keys).
EPHEMERAL_PROP_KEYS = WIRE_INTERNAL_PROP_KEYS

# Known no-op defaults: NO LONGER silently dropped in diff_subtrees (Finding 21); reported explicitly
# and counted under `noop_default_diffs` in the report unless allowlisted.
NOOP_DEFAULTS_DOC = [
    "md.stream=false",
    "editor/text.text=\"\"",
    "tool.meta=[]",
]


def _is_noop_default(kind: str, prop: str, val: Any) -> bool:
    if kind == "md" and prop == "stream" and val is False:
        return True
    if kind in ("editor", "text") and prop == "text" and val == "":
        return True
    if kind == "tool" and prop == "meta" and val == []:
        return True
    return False


def _normalize_timing_prop(val: Any) -> Any:
    """Mask numeric timing prop values (`took`, `age`, `stopped`) to `<MS>` while preserving non-numeric types."""
    if isinstance(val, (int, float)) and not isinstance(val, bool):
        return "<MS>"
    return val


def normalize_prop_value(val: Any, key: str = "") -> Any:
    """Normalize run-specific temp workspace paths (preserving /private differences), HTTP error log paths, and numeric timing values."""
    if key in TIMING_VALUE_PROP_KEYS:
        return _normalize_timing_prop(val)
    if isinstance(val, str):
        out = _RE_HTTP_LOG_PATH.sub("<HTTP_LOG>", val)
        out = _RE_TEMP_WORK_PATH_PRIVATE.sub("/private<WORK>", out)
        out = _RE_TEMP_WORK_PATH.sub("<WORK>", out)
        return out
    if isinstance(val, list):
        return [normalize_prop_value(x, key) for x in val]
    if isinstance(val, dict):
        out_d: dict[str, Any] = {}
        for dk, dv in val.items():
            if dk in TIMING_VALUE_PROP_KEYS:
                out_d[dk] = _normalize_timing_prop(dv)
            else:
                out_d[dk] = normalize_prop_value(dv, dk)
        return out_d
    return val


# ---------------------------------------------------------------------------
# Welcome tip validation (Findings 17, 19, 24)
# ---------------------------------------------------------------------------

_OMP_WELCOME_TIPS_CANONICAL: list[str] = [
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

_TIP_KEY_EXPANSIONS: dict[str, tuple[str, ...]] = {
    "{key:ctrl+d}": ("⌃D", "Ctrl+D", "^D"),
    "{key:alt+p}": ("⌥P", "Alt+P"),
    "{key:ctrl+p}": ("⌃P", "Ctrl+P", "^P"),
    "{key:ctrl+r}": ("⌃R", "Ctrl+R", "^R"),
    "{key:shift+tab}": ("⇧⇥", "Shift+Tab"),
    "{key:escape}": ("⎋", "Esc", "Escape"),
    "{tap:left}": ("← ←", "←←", "Left Left", "double-tap Left"),
}


def _expand_tip_variants(raw_tips: list[str]) -> set[str]:
    out: set[str] = set()
    for tip in raw_tips:
        variants = [tip.strip()]
        for ph, repls in _TIP_KEY_EXPANSIONS.items():
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
            # Strip inline markdown backticks too (terminal/TSP rendering may strip backticks)
            out.add(v.replace("`", ""))
    return out


_RE_PUA_GLYPH = re.compile(r"[\uE000-\uF8FF\U000F0000-\U000FFFFD\U00100000-\U0010FFFD]")
_OMP_TSP_KEY_GLYPHS: dict[str, tuple[str, ...]] = {
    "{key:ctrl+d}": ("\U000f0634 D", "\U000f0634D", "⌃D", "Ctrl+D", "^D"),
    "{key:alt+p}": ("\U000f0635 P", "\U000f0635P", "⌥P", "Alt+P"),
    "{key:ctrl+p}": ("\U000f0634 P", "\U000f0634P", "⌃P", "Ctrl+P", "^P"),
    "{key:ctrl+r}": ("\U000f0634 R", "\U000f0634R", "⌃R", "Ctrl+R", "^R"),
    "{key:shift+tab}": ("\U000f0636 \U000f0312", "\U000f0636\U000f0312", "⇧⇥", "Shift+Tab"),
    "{key:escape}": ("\U000f12b7", "⎋", "Esc", "Escape"),
    # Nerd preset (`symbols.ts:1190,1205` + `key-hint-format.ts:134-137`): `key.left` is Unicode `←`
    # (non-PUA) joined by `key.joiner` (`" "`), yielding `"← ←"` in plain `text`; unicode yields `"←←"`;
    # ascii yields `"Left Left"`.
    "{tap:left}": ("← ←", "←←", "Left Left", "double-tap Left"),
}


def _expand_tip_variants_tsp(raw_tips: list[str]) -> set[str]:
    out: set[str] = set()
    for tip in raw_tips:
        variants = [tip.strip()]
        for ph, repls in _OMP_TSP_KEY_GLYPHS.items():
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
            for form in (v, v.replace("`", "")):
                out.add(" ".join(form.split()))
                # Also add form with space stripped around PUA icons (matching icons.ts normalizeIconSpans)
                compact = re.sub(r" ?([\uE000-\uF8FF\U000F0000-\U000FFFFD\U00100000-\U0010FFFD]+) ?", r"\1", form)
                out.add(" ".join(compact.split()))
    return out


def _load_pig_tern_tips() -> set[str]:
    """Read `ompWelcomeTips` from `piglets/pig-tern/frontend/omp_transcript.go`."""
    go_path = SCRIPT_DIR.parent.parent / "pig-tern" / "frontend" / "omp_transcript.go"
    raw: list[str] = []
    if go_path.is_file():
        txt = go_path.read_text(encoding="utf-8")
        m = re.search(r"var\s+ompWelcomeTips\s*=\s*\[\]string\{(.*?)\n\}", txt, re.S)
        if m:
            for lit in re.findall(r"`([^`]+)`|\"((?:\\.|[^\"\\])*)\"", m.group(1)):
                val = lit[0] or lit[1]
                if val:
                    raw.append(val)
    return _expand_tip_variants_tsp(raw)


OMP_WELCOME_TIP_SET: set[str] = _expand_tip_variants_tsp(_OMP_WELCOME_TIPS_CANONICAL)
PIG_TERN_WELCOME_TIP_SET: set[str] = _load_pig_tern_tips()


def is_valid_welcome_tip(text: Any, side: str) -> bool:
    if not isinstance(text, str) or not text.strip():
        return False
    s = " ".join(text.strip().split())
    if side == "omp":
        return s in OMP_WELCOME_TIP_SET
    if side == "pig":
        return s in PIG_TERN_WELCOME_TIP_SET or s in OMP_WELCOME_TIP_SET
    return False


def validate_welcome_tip_node(node: TspNode, side: str) -> tuple[bool, str]:
    """Validate that a `text[omp.welcome.tip-text]` node is in the validated tip set AND uses the right wire form.

    Rules (matching `packages/tui/src/native/icons.ts:40-70` & `welcome.ts:261-266`):
      - A tip containing PUA icon glyphs (`{key:...}` expanded) MUST be encoded as `spans` where
        every PUA run is in its own `{t: <pua>, s: "icon"}` span (never raw PUA in plain `text`),
        and no plain `text` prop is set.
      - A plain tip without PUA glyphs (and without link `href` spans) MUST be encoded as plain
        `text` (not `spans`).
      - Its flattened text must belong to `OMP_WELCOME_TIP_SET` (omp) or `PIG_TERN_WELCOME_TIP_SET` (pig).
    """
    has_text = "text" in node.props and isinstance(node.props.get("text"), str)
    has_spans = "spans" in node.props and isinstance(node.props.get("spans"), list)
    if has_text and has_spans:
        return False, f"{side} tip sets both 'text' and 'spans'"
    if has_text:
        t = str(node.props["text"])
        if _RE_PUA_GLYPH.search(t):
            return False, f"{side} tip has raw PUA icon glyph inside plain 'text' instead of icon spans"
        if not is_valid_welcome_tip(t, side):
            return False, f"{side} tip text not in validated tips set: {t!r}"
        return True, "ok"
    if has_spans:
        spans = node.props["spans"]
        if not spans:
            return False, f"{side} tip has empty 'spans'"
        has_icon_or_link = False
        parts: list[str] = []
        for sp in spans:
            if not isinstance(sp, dict) or not isinstance(sp.get("t"), str):
                return False, f"{side} tip span malformed: {sp!r}"
            st = sp["t"]
            style = str(sp.get("s") or "")
            if _RE_PUA_GLYPH.search(st):
                has_icon_or_link = True
                if "icon" not in style.split():
                    return False, f"{side} tip PUA glyph missing 'icon' style: {sp!r}"
                if re.sub(_RE_PUA_GLYPH, "", st):
                    return False, f"{side} tip mixes PUA glyph and non-PUA text in one span: {sp!r}"
            elif sp.get("href"):
                has_icon_or_link = True
            parts.append(st)
        if not has_icon_or_link:
            return False, f"{side} plain tip encoded as 'spans' instead of 'text'"
        joined = "".join(parts)
        if not is_valid_welcome_tip(joined, side):
            return False, f"{side} tip spans text not in validated tips set: {joined!r}"
        return True, "ok"
    return False, f"{side} tip has neither 'text' nor 'spans'"
@dataclass
class TspNode:
    id: str
    kind: str
    props: dict[str, Any] = field(default_factory=dict)
    children: list[str] = field(default_factory=list)

    @property
    def role(self) -> str:
        r = self.props.get("role")
        return str(r) if isinstance(r, str) else ""

    @property
    def hidden(self) -> bool:
        return bool(self.props.get("hidden"))


@dataclass
class TspDocument:
    surface_id: str | None = None
    surface_role: str = ""
    nodes: dict[str, TspNode] = field(default_factory=dict)

    def apply_record(self, rec: dict[str, Any]) -> None:
        if rec.get("dir") != "out":
            return
        verb = rec.get("verb")
        body = rec.get("body")
        if not isinstance(body, dict):
            return

        if verb == "o" and body.get("mode") == "inline":
            sid = str(body.get("id", ""))
            self.surface_id = sid
            self.surface_role = str(body.get("role") or "")
            self.nodes = {
                sid: TspNode(id=sid, kind="surface", props={"role": self.surface_role}, children=[])
            }
            return

        if verb != "f" or body.get("sf") != self.surface_id:
            return

        for op in body.get("ops") or []:
            if not isinstance(op, list) or not op:
                continue
            cmd = op[0]
            if cmd == "add" and len(op) >= 5:
                _, nid, pid, before, node_obj = op[:5]
                if isinstance(node_obj, dict):
                    self._insert_subtree(node_obj)
                    parent = self.nodes.get(str(pid))
                    if parent is not None:
                        nid_s = str(nid)
                        if nid_s in parent.children:
                            parent.children.remove(nid_s)
                        if before is None or str(before) not in parent.children:
                            parent.children.append(nid_s)
                        else:
                            parent.children.insert(parent.children.index(str(before)), nid_s)
            elif cmd == "del" and len(op) >= 2:
                nid_s = str(op[1])
                for p in self.nodes.values():
                    if nid_s in p.children:
                        p.children.remove(nid_s)
                self._delete_subtree(nid_s)
            elif cmd == "set" and len(op) >= 3:
                nid_s = str(op[1])
                patch = op[2]
                node = self.nodes.get(nid_s)
                if node is not None and isinstance(patch, dict):
                    for k, v in patch.items():
                        if v is None:
                            node.props.pop(k, None)
                        else:
                            node.props[k] = v
            elif cmd == "rep" and len(op) >= 3:
                nid_s = str(op[1])
                node_obj = op[2]
                if isinstance(node_obj, dict):
                    self._insert_subtree(node_obj)
            elif cmd == "mov" and len(op) >= 4:
                _, nid, pid, before = op[:4]
                nid_s = str(nid)
                for p in self.nodes.values():
                    if nid_s in p.children:
                        p.children.remove(nid_s)
                parent = self.nodes.get(str(pid))
                if parent is not None:
                    if before is None or str(before) not in parent.children:
                        parent.children.append(nid_s)
                    else:
                        parent.children.insert(parent.children.index(str(before)), nid_s)
            elif cmd == "txt" and len(op) >= 4:
                nid_s, prop_name, delta = str(op[1]), str(op[2]), str(op[3])
                node = self.nodes.get(nid_s)
                if node is not None:
                    cur = node.props.get(prop_name)
                    node.props[prop_name] = (cur if isinstance(cur, str) else "") + delta
            elif cmd == "text" and len(op) >= 4:
                # Tern TSP wire `["text", id, "replace"|"append", content]` op (used by ansi/editor nodes).
                nid_s, mode, content = str(op[1]), str(op[2]), str(op[3])
                node = self.nodes.get(nid_s)
                if node is not None:
                    if mode == "replace":
                        node.props["text"] = content
                    elif mode == "append":
                        cur = node.props.get("text")
                        node.props["text"] = (cur if isinstance(cur, str) else "") + content
            elif cmd == "splice" and len(op) >= 5:
                # Tern TSP wire `["splice", id, start, deleteCount, insertText]` op (used by omp editor).
                nid_s, start_i, del_n, ins = str(op[1]), int(op[2]), int(op[3]), str(op[4])
                node = self.nodes.get(nid_s)
                if node is not None:
                    cur = node.props.get("text")
                    s_cur = cur if isinstance(cur, str) else ""
                    node.props["text"] = s_cur[:start_i] + ins + s_cur[start_i + del_n :]

    def _insert_subtree(self, obj: dict[str, Any]) -> None:
        nid = str(obj.get("id", ""))
        cids: list[str] = []
        for ch in obj.get("c") or []:
            if isinstance(ch, dict):
                self._insert_subtree(ch)
                cids.append(str(ch.get("id", "")))
        self.nodes[nid] = TspNode(
            id=nid,
            kind=str(obj.get("k", "")),
            props=dict(obj.get("p") or {}),
            children=cids,
        )

    def _delete_subtree(self, nid: str) -> None:
        node = self.nodes.pop(nid, None)
        if node is None:
            return
        for cid in list(node.children):
            self._delete_subtree(cid)

    def visible_children(self, nid: str) -> list[TspNode]:
        n = self.nodes.get(nid)
        if n is None:
            return []
        return [self.nodes[cid] for cid in n.children if cid in self.nodes and not self.nodes[cid].hidden]

    def walk_visible(self, nid: str | None = None) -> list[TspNode]:
        root = nid if nid is not None else self.surface_id
        if not root or root not in self.nodes:
            return []
        out: list[TspNode] = []

        def _rec(cur_id: str) -> None:
            n = self.nodes.get(cur_id)
            if n is None or n.hidden:
                return
            out.append(n)
            for cid in n.children:
                _rec(cid)

        _rec(root)
        return out

    def visible_text(self, nid: str) -> str:
        n = self.nodes.get(nid)
        if n is None or n.hidden:
            return ""
        parts: list[str] = []
        p = n.props
        for key in ("text", "md", "title", "name", "cmd", "path", "label", "summary", "sub", "target"):
            val = p.get(key)
            if isinstance(val, str) and val.strip():
                parts.append(val.strip())
        # B14: read `lines` array on `ansi` (and any node carrying `lines: list[str]`), never raw node IDs!
        lines_val = p.get("lines")
        if isinstance(lines_val, list):
            lt = " ".join(str(x).strip() for x in lines_val if isinstance(x, str) and x.strip())
            if lt:
                parts.append(lt)
        spans = p.get("spans")
        if isinstance(spans, list):
            st = "".join(s.get("t", "") for s in spans if isinstance(s, dict) and isinstance(s.get("t"), str))
            if st.strip():
                parts.append(st.strip())
        # NEVER include `head` when it is a string node-ID reference (`s.k0/head`, `2x.0/head`, `n.n90.e.error.head`);
        # only read `head` when it is a list of spans (e.g. card/section inline span headers).
        head = p.get("head")
        if isinstance(head, list):
            ht = "".join(s.get("t", "") for s in head if isinstance(s, dict) and isinstance(s.get("t"), str))
            if ht.strip():
                parts.append(ht.strip())
        for cid in n.children:
            sub = self.visible_text(cid)
            if sub:
                parts.append(sub)
        return " ".join(parts)
    def subtree_signature(self, nid: str, max_depth: int = 3) -> dict[str, Any]:
        n = self.nodes.get(nid)
        if n is None:
            return {}
        sig: dict[str, Any] = {
            "kind": n.kind,
            "role": n.role or None,
        }
        if n.props.get("tone"):
            sig["tone"] = n.props["tone"]
        if n.props.get("status"):
            sig["status"] = n.props["status"]
        if n.props.get("collapsible") is not None:
            sig["collapsible"] = bool(n.props["collapsible"])
        if max_depth > 0:
            kids: list[dict[str, Any]] = []
            for cid in n.children:
                ch = self.nodes.get(cid)
                if ch is None or ch.hidden:
                    continue
                kids.append(self.subtree_signature(cid, max_depth - 1))
            sig["children"] = kids
        return sig


def load_tsp_at_step(lane_dir: str, step: str) -> TspDocument:
    tsp_path = os.path.join(lane_dir, "tsp.jsonl")
    lines_path = os.path.join(lane_dir, f"tsp-lines-{step}.txt")
    doc = TspDocument()
    if not os.path.isfile(tsp_path):
        return doc
    limit: int | None = None
    if os.path.isfile(lines_path):
        try:
            limit = int(open(lines_path, encoding="utf-8").read().strip())
        except ValueError:
            limit = None
    with open(tsp_path, "r", encoding="utf-8") as fh:
        for i, line in enumerate(fh, start=1):
            if limit is not None and i > limit:
                break
            line = line.strip()
            if not line:
                continue
            try:
                rec = json.loads(line)
            except json.JSONDecodeError:
                continue
            doc.apply_record(rec)
    return doc


def extract_dom_regions(tree_path: str) -> dict[str, Any]:
    if not os.path.isfile(tree_path):
        return {"ok": False, "regions": {}, "surface_node_count": 0}
    with open(tree_path, "r", encoding="utf-8") as fh:
        raw = json.load(fh)

    regions: dict[str, list[dict[str, Any]]] = {}

    def dom_kind(cls_str: str) -> str | None:
        tokens = cls_str.split()
        if "sf" not in tokens:
            return None
        for tok in tokens:
            m = SF_KIND_RE.match(tok)
            if m and m.group(1) in KNOWN_TSP_KINDS:
                return m.group(1)
        return None

    def extract_sf_children(dom_node: dict[str, Any]) -> list[dict[str, Any]]:
        out: list[dict[str, Any]] = []
        for ch in dom_node.get("children") or []:
            cls = ch.get("class") or ""
            k = dom_kind(cls)
            if k is not None:
                out.append(
                    {
                        "css_kind": f"sf-{k}",
                        "kind": k,
                        "class": cls,
                        "children": extract_sf_children(ch),
                    }
                )
            else:
                out.extend(extract_sf_children(ch))
        return out

    def find_regions(dom_node: dict[str, Any]) -> None:
        cls = dom_node.get("class") or ""
        tokens = set(cls.split())
        if "sf-region" in tokens:
            for rname in ("sf-main", "sf-dock", "sf-layer"):
                if rname in tokens:
                    regions[rname] = extract_sf_children(dom_node)
                    return
        for ch in dom_node.get("children") or []:
            find_regions(ch)

    for root in raw.get("tree") or []:
        find_regions(root)

    def count_nodes(nodes: list[dict[str, Any]]) -> int:
        return sum(1 + count_nodes(n["children"]) for n in nodes)

    total = sum(count_nodes(v) for v in regions.values())
    return {
        "ok": bool(raw.get("ok")),
        "regions": regions,
        "region_names": sorted(regions.keys()),
        "surface_node_count": total,
    }


def format_nesting(sig: dict[str, Any]) -> str:
    if not sig:
        return "<missing>"
    label = sig["kind"]
    if sig.get("role"):
        label += f"[{sig['role']}]"
    kids = sig.get("children") or []
    if not kids:
        return label
    inner = ", ".join(format_nesting(k) for k in kids)
    return f"{label}({inner})"


def first_dom_match(dom_nodes: list[dict[str, Any]], kind: str) -> dict[str, Any] | None:
    for n in dom_nodes:
        if n["kind"] == kind:
            return n
        sub = first_dom_match(n["children"], kind)
        if sub is not None:
            return sub
    return None


def find_tsp_by_role(doc: TspDocument, role: str, region: str | None = None) -> list[TspNode]:
    start = region if (region and region in doc.nodes) else doc.surface_id
    return [n for n in doc.walk_visible(start) if n.role == role]


def find_tsp_by_predicate(doc: TspDocument, pred: Any, region: str | None = None) -> list[TspNode]:
    start = region if (region and region in doc.nodes) else doc.surface_id
    return [n for n in doc.walk_visible(start) if pred(n)]


def _tool_name_matches_exact(node: TspNode, tool_name: str) -> bool:
    """Exact tool-name match on role/name/tool/title — NEVER substring (`"ask" in "task"` bug)."""
    tl = tool_name.lower()
    if node.role == f"omp.tool.{tl}":
        return True
    p = node.props
    if node.kind == "tool":
        for key in ("name", "tool"):
            v = str(p.get(key) or "").strip().lower()
            if v == tl:
                return True
        title = str(p.get("title") or "").strip().lower().replace(" ", "_")
        if title == tl:
            return True
    return False


def find_tool_nodes(doc: TspDocument, tool_name: str, require_error: bool = False) -> list[TspNode]:
    """Locate visible tool card nodes in `main` for an EXACT tool name (never substring matching)."""
    main_nodes = doc.walk_visible("main")
    matches: list[TspNode] = []

    def is_error_node(n: TspNode) -> bool:
        p = n.props
        if p.get("status") in ("error", "err", "failed") or p.get("tone") == "error":
            return True
        txt = doc.visible_text(n.id).lower()
        return any(tok in txt for tok in ("enoent", "no such file", "not found", "does-not-exist", "error"))

    for n in main_nodes:
        if n.id == "main":
            continue
        if _tool_name_matches_exact(n, tool_name):
            if require_error and not is_error_node(n):
                continue
            matches.append(n)
    return matches


def find_welcome_nodes(doc: TspDocument) -> list[TspNode]:
    """Locate the startup welcome / header node in `main` (omp: `card[omp.welcome]`; port: top-level header node)."""
    by_role = find_tsp_by_role(doc, "omp.welcome", region="main")
    if by_role:
        return by_role
    main = doc.nodes.get("main")
    if main is None:
        return []
    for cid in main.children:
        ch = doc.nodes.get(cid)
        if ch is None or ch.hidden:
            continue
        if ch.kind == "spacer":
            continue
        if ch.role in ("omp.user", "omp.assistant", "omp.thinking", "omp.thinking.live"):
            break
        return [ch]
    return []


def find_thinking_nodes(doc: TspDocument) -> list[TspNode]:
    return find_tsp_by_predicate(
        doc,
        lambda n: n.role in ("omp.thinking", "omp.thinking.live")
        or (n.kind == "section" and "think" in doc.visible_text(n.id).lower()),
        region="main",
    )


def find_working_nodes(doc: TspDocument) -> list[TspNode]:
    return find_tsp_by_predicate(
        doc,
        lambda n: n.role in ("omp.working", "omp.hud", "omp.thinking.live")
        or (n.kind in ("row", "col") and any(
            (doc.nodes.get(c) is not None and doc.nodes[c].kind in ("spinner", "shimmer"))
            for c in n.children
        ))
        or n.kind == "spinner",
        region=None,
    )


def find_composer_nodes(doc: TspDocument) -> list[TspNode]:
    by_role = find_tsp_by_role(doc, "omp.editor", region="dock")
    if by_role:
        return by_role
    return find_tsp_by_predicate(doc, lambda n: n.kind == "editor", region="dock")


def find_status_nodes(doc: TspDocument) -> list[TspNode]:
    """Find an independent status/footer node — NEVER fall back to `omp.composer.bar`."""
    for r in ("omp.status", "omp.footer"):
        found = find_tsp_by_role(doc, r, region="dock")
        if found:
            return found
    composer_ids: set[str] = set()
    for comp in find_composer_nodes(doc):
        for desc in doc.walk_visible(comp.id):
            composer_ids.add(desc.id)
    return find_tsp_by_predicate(
        doc,
        lambda n: n.kind == "status" and n.id not in composer_ids,
        region="dock",
    )


def find_live_ask_nodes(doc: TspDocument) -> list[TspNode]:
    for role in ("omp.ask.card", "omp.ask"):
        found = find_tsp_by_role(doc, role, region=None)
        if found:
            return found
    for reg in ("layer", "dock"):
        pickers = find_tsp_by_predicate(
            doc,
            lambda n: n.kind in ("picker", "overlay", "select") and "choose a mode" in doc.visible_text(n.id).lower(),
            region=reg,
        )
        if pickers:
            return pickers
    return []


def find_error_turn_nodes(doc: TspDocument) -> list[TspNode]:
    by_role = find_tsp_by_role(doc, "omp.error", region="main")
    if by_role:
        return by_role
    return find_tsp_by_predicate(
        doc,
        lambda n: (n.props.get("tone") == "error" and n.kind in ("card", "text", "section"))
        and "simulated provider error" in doc.visible_text(n.id).lower(),
        region="main",
    )


def find_plan_indicator_nodes(doc: TspDocument) -> list[TspNode]:
    return find_tsp_by_predicate(
        doc,
        lambda n: n.kind == "seg" and "plan" in doc.visible_text(n.id).lower(),
        region="dock",
    )


def find_slash_menu_nodes(doc: TspDocument) -> list[TspNode]:
    by_role = find_tsp_by_role(doc, "omp.autocomplete", region="layer")
    if by_role:
        return by_role
    return find_tsp_by_predicate(
        doc,
        lambda n: n.kind in ("overlay", "menu", "picker", "suggestions", "list"),
        region="layer",
    )


# ---------------------------------------------------------------------------
# Recursive subtree + prop differ & narrow allowlist subtraction
# ---------------------------------------------------------------------------


def _child_segment(
    node: TspNode,
    sibling_list: list[TspNode],
    idx: int,
    *,
    kind_differs: bool = False,
) -> str:
    """Stable human-readable path segment for a child node within its parent."""
    if kind_differs:
        return f"[{node.role}]" if node.role else f"[{idx}]"
    if node.role:
        same_role = [s for s in sibling_list if s.role == node.role]
        if len(same_role) > 1:
            return f"{node.kind}[{node.role}]@{idx}"
        return f"{node.kind}[{node.role}]"
    if len(sibling_list) > 1:
        return f"{node.kind}@{idx}"
    return node.kind


def _align_children(
    okids: list[TspNode],
    pkids: list[TspNode],
    odoc: TspDocument,
    pdoc: TspDocument,
) -> list[tuple[int | None, int | None, TspNode | None, TspNode | None]]:
    """Order-preserving LCS alignment of visible children by role, kind, and label text."""
    def match_score(a: TspNode, b: TspNode) -> int:
        if a.role and b.role and a.role == b.role:
            # Prefer matching actions/items with the same visible text when multiple siblings share a role
            # (e.g., `row[omp.error.action]` for Retry / Copy error / Switch model).
            if a.kind == b.kind:
                ta = odoc.visible_text(a.id).strip().lower()
                tb = pdoc.visible_text(b.id).strip().lower()
                if ta and tb and ta == tb:
                    return 6
                return 3
            return 2
        if not a.role and not b.role and a.kind == b.kind:
            return 2
        return 0

    m, n = len(okids), len(pkids)
    dp = [[0] * (n + 1) for _ in range(m + 1)]
    for i in range(m - 1, -1, -1):
        for j in range(n - 1, -1, -1):
            best = max(dp[i + 1][j], dp[i][j + 1])
            sc = match_score(okids[i], pkids[j])
            if sc > 0:
                best = max(best, sc + dp[i + 1][j + 1])
            dp[i][j] = best

    pairs: list[tuple[int | None, int | None, TspNode | None, TspNode | None]] = []
    i = j = 0
    while i < m or j < n:
        if i < m and j < n:
            sc = match_score(okids[i], pkids[j])
            if sc > 0 and dp[i][j] == sc + dp[i + 1][j + 1]:
                pairs.append((i, j, okids[i], pkids[j]))
                i += 1
                j += 1
                continue
        if i < m and (j == n or dp[i + 1][j] >= dp[i][j + 1]):
            pairs.append((i, None, okids[i], None))
            i += 1
        else:
            pairs.append((None, j, None, pkids[j]))
            j += 1
    return pairs


def _compact_repr(val: Any, limit: int = 120) -> str:
    s = json.dumps(val, ensure_ascii=False, sort_keys=True) if isinstance(val, (dict, list)) else repr(val)
    return s if len(s) <= limit else s[: limit - 1] + "…"


def _extract_ask_projection(doc: TspDocument, nid: str) -> tuple[str | None, list[str]]:
    """Extract `(title, option_labels)` projection from an ask form (`col[omp.ask.card]` or `picker`)."""
    n = doc.nodes.get(nid)
    if n is None:
        return None, []
    if n.kind == "picker":
        title = n.props.get("title")
        items = n.props.get("items") or []
        labels: list[str] = []
        for it in items:
            if isinstance(it, dict) and isinstance(it.get("label"), str):
                lbl = it["label"].strip()
                # Normalize "Other (type your own)" / "Other…" to canonical "Other…" only if both are "Other"
                labels.append(lbl)
        return (str(title).strip() if isinstance(title, str) else None), labels
    # omp `col[omp.ask.card]`
    title_val: str | None = None
    labels = []
    for desc in doc.walk_visible(nid):
        if desc.role == "omp.ask.text" and title_val is None:
            t = doc.visible_text(desc.id).strip()
            if t:
                title_val = t
        elif desc.role == "omp.ask.option":
            lbl_prop = desc.props.get("label")
            if isinstance(lbl_prop, list):
                t = "".join(str(sp.get("t", "")) for sp in lbl_prop if isinstance(sp, dict)).strip()
            elif isinstance(lbl_prop, str):
                t = lbl_prop.strip()
            else:
                t = doc.visible_text(desc.id).strip()
            if t:
                labels.append(t)
    return title_val, labels


def _diff_cross_kind_projection(
    odoc: TspDocument,
    on: TspNode,
    pdoc: TspDocument,
    pn: TspNode,
    node_path: str,
    *,
    parent_omp: TspNode | None = None,
    parent_pig: TspNode | None = None,
    tool_omp: TspNode | None = None,
    tool_pig: TspNode | None = None,
) -> list[dict[str, Any]]:
    """Continue comparing shared props, children, and declared semantic projections below a `$kind` diff (Finding 8)."""
    extra: list[dict[str, Any]] = []
    # 1. Compare props that BOTH nodes share (ignoring wire-internal and role, already diffed)
    op = {
        k: normalize_prop_value(v, k)
        for k, v in on.props.items()
        if k not in WIRE_INTERNAL_PROP_KEYS and k != "role"
    }
    pp = {
        k: normalize_prop_value(v, k)
        for k, v in pn.props.items()
        if k not in WIRE_INTERNAL_PROP_KEYS and k != "role"
    }
    for k in sorted(set(op.keys()) & set(pp.keys())):
        if op[k] != pp[k]:
            extra.append({
                "node_path": node_path,
                "prop": k,
                "side": "value",
                "omp": op[k],
                "pig": pp[k],
            })

    # 2. Declared projection: `shell` vs `code` for `omp.tool.bash.command`
    if on.kind == "shell" and pn.kind == "code" and on.role == "omp.tool.bash.command":
        expected_cmd = tool_omp.props.get("command") if tool_omp is not None else None
        if expected_cmd is None and tool_pig is not None:
            expected_cmd = tool_pig.props.get("command")
        pig_text = pn.props.get("text")
        if expected_cmd is not None and normalize_prop_value(pig_text) != normalize_prop_value(expected_cmd):
            extra.append({
                "node_path": node_path,
                "prop": "$projection.command",
                "side": "value",
                "omp": normalize_prop_value(expected_cmd),
                "pig": normalize_prop_value(pig_text),
            })
        if pn.props.get("lang") != "bash":
            extra.append({
                "node_path": node_path,
                "prop": "lang",
                "side": "value",
                "omp": on.props.get("lang", "bash"),
                "pig": pn.props.get("lang"),
            })
        if pn.props.get("wrap") is not True:
            extra.append({
                "node_path": node_path,
                "prop": "wrap",
                "side": "value",
                "omp": True,
                "pig": pn.props.get("wrap"),
            })
        if "preview" not in pn.props:
            extra.append({
                "node_path": node_path,
                "prop": "preview",
                "side": "omp-only",
                "omp": op.get("preview"),
                "pig": None,
            })

    # 3. Declared projection: `col[omp.ask.card]` vs `picker`
    elif (on.kind == "col" and on.role == "omp.ask.card" and pn.kind == "picker") or (
        pn.kind == "col" and pn.role == "omp.ask.card" and on.kind == "picker"
    ):
        o_title, o_opts = _extract_ask_projection(odoc, on.id)
        p_title, p_opts = _extract_ask_projection(pdoc, pn.id)
        if o_title != p_title:
            extra.append({
                "node_path": node_path,
                "prop": "$projection.title",
                "side": "value",
                "omp": o_title,
                "pig": p_title,
            })
        if o_opts != p_opts:
            extra.append({
                "node_path": node_path,
                "prop": "$projection.options",
                "side": "value",
                "omp": o_opts,
                "pig": p_opts,
            })
    else:
        # 4. Generic fallback when two different kinds still have aligned children: compare their children.
        okids = odoc.visible_children(on.id)
        pkids = pdoc.visible_children(pn.id)
        if okids or pkids:
            for oi, pi, ok, pk in _align_children(okids, pkids, odoc, pdoc):
                if ok is not None and pk is not None:
                    seg = _child_segment(ok, okids, oi or 0, kind_differs=(ok.kind != pk.kind))
                    cpath = seg if node_path == "$" else f"{node_path} > {seg}"
                    extra.extend(diff_subtrees(odoc, ok.id, pdoc, pk.id, cpath, tool_omp=tool_omp, tool_pig=tool_pig))
                elif ok is not None:
                    seg = _child_segment(ok, okids, oi or 0)
                    cpath = seg if node_path == "$" else f"{node_path} > {seg}"
                    extra.append({
                        "node_path": cpath,
                        "prop": "$node",
                        "side": "omp-only",
                        "omp": f"{ok.kind}[{ok.role}]" if ok.role else ok.kind,
                        "pig": None,
                    })
                elif pk is not None:
                    seg = _child_segment(pk, pkids, pi or 0)
                    cpath = seg if node_path == "$" else f"{node_path} > {seg}"
                    extra.append({
                        "node_path": cpath,
                        "prop": "$node",
                        "side": "port-only",
                        "omp": None,
                        "pig": f"{pk.kind}[{pk.role}]" if pk.role else pk.kind,
                    })
    return extra


def diff_subtrees(
    odoc: TspDocument,
    onid: str | None,
    pdoc: TspDocument,
    pnid: str | None,
    node_path: str = "$",
    *,
    tool_omp: TspNode | None = None,
    tool_pig: TspNode | None = None,
) -> list[dict[str, Any]]:
    """Compute every semantic difference between two TSP subtrees.

    Each returned diff record has:
      `{node_path, prop, side, omp, pig}` (and optional `noop_default: True` when a prop
      differs only by a known no-op default value on one side).
    where `prop` is:
      - `"$node"` for an absent/extra node
      - `"$kind"` when node kinds differ (`shell` vs `code`, `col` vs `picker`)
      - `"$projection.<field>"` for semantic projections compared below a `$kind` difference
      - `"role"` when `p.role` differs
      - any other TSP prop name (including presence and type of `took`/`age`/`stopped`, whose
        numeric values are masked to `<MS>`).
    and `side` is `"omp-only"`, `"port-only"`, or `"value"`.
    """
    diffs: list[dict[str, Any]] = []
    on = odoc.nodes.get(onid) if onid else None
    pn = pdoc.nodes.get(pnid) if pnid else None

    if on is None and pn is None:
        return diffs
    if on is None:
        diffs.append({
            "node_path": node_path,
            "prop": "$node",
            "side": "port-only",
            "omp": None,
            "pig": f"{pn.kind}[{pn.role}]" if pn.role else pn.kind,
            "node_count": len(pdoc.walk_visible(pn.id)),
        })
        return diffs
    if pn is None:
        diffs.append({
            "node_path": node_path,
            "prop": "$node",
            "side": "omp-only",
            "omp": f"{on.kind}[{on.role}]" if on.role else on.kind,
            "pig": None,
            "node_count": len(odoc.walk_visible(on.id)),
        })
        return diffs

    cur_tool_omp = on if on.kind == "tool" else tool_omp
    cur_tool_pig = pn if pn.kind == "tool" else tool_pig

    if on.kind != pn.kind:
        diffs.append({
            "node_path": node_path,
            "prop": "$kind",
            "side": "value",
            "omp": on.kind,
            "pig": pn.kind,
        })
    if (on.role or None) != (pn.role or None):
        diffs.append({
            "node_path": node_path,
            "prop": "role",
            "side": "value",
            "omp": on.role or None,
            "pig": pn.role or None,
        })

    # Finding 8: do NOT stop at a `$kind` difference — continue comparing shared props,
    # declared projections (`shell` vs `code` command text, `col[omp.ask.card]` vs `picker`
    # title and options), and children.
    if on.kind != pn.kind:
        diffs.extend(
            _diff_cross_kind_projection(
                odoc,
                on,
                pdoc,
                pn,
                node_path,
                tool_omp=cur_tool_omp,
                tool_pig=cur_tool_pig,
            )
        )
        return diffs
    # Welcome tip (`text[omp.welcome.tip-text]`): both sides select a random tip per launch
    # (`welcome.ts:238-245` uses `Math.random()` with no env/seed override). When one side picks
    # a `{key:...}` tip (encoded as `spans` with `s:"icon"`) and the other picks a plain tip
    # (encoded as `text`), validate each side's tip against its source tip set AND wire form
    # (icon spans iff PUA/link present; plain text otherwise) and emit a single `text` diff
    # (only if the two nodes actually differ) rather than spurious `spans` omp-only + `text` port-only.
    if on.kind == "text" and on.role == "omp.welcome.tip-text" and pn.role == "omp.welcome.tip-text":
        o_other = {
            k: normalize_prop_value(v, k)
            for k, v in on.props.items()
            if k not in WIRE_INTERNAL_PROP_KEYS and k not in ("role", "text", "spans")
        }
        p_other = {
            k: normalize_prop_value(v, k)
            for k, v in pn.props.items()
            if k not in WIRE_INTERNAL_PROP_KEYS and k not in ("role", "text", "spans")
        }
        for k in sorted(set(o_other) | set(p_other)):
            if k not in p_other:
                diffs.append({"node_path": node_path, "prop": k, "side": "omp-only", "omp": o_other[k], "pig": None})
            elif k not in o_other:
                diffs.append({"node_path": node_path, "prop": k, "side": "port-only", "omp": None, "pig": p_other[k]})
            elif o_other[k] != p_other[k]:
                diffs.append({"node_path": node_path, "prop": k, "side": "value", "omp": o_other[k], "pig": p_other[k]})
        o_tip_val = on.props.get("spans") if "spans" in on.props else on.props.get("text")
        p_tip_val = pn.props.get("spans") if "spans" in pn.props else pn.props.get("text")
        if o_tip_val != p_tip_val or ("spans" in on.props) != ("spans" in pn.props):
            o_ok, o_reason = validate_welcome_tip_node(on, "omp")
            p_ok, p_reason = validate_welcome_tip_node(pn, "pig")
            diffs.append({
                "node_path": node_path,
                "prop": "text",
                "side": "value",
                "omp": o_tip_val,
                "pig": p_tip_val,
                "tip_valid": o_ok and p_ok,
                "tip_validation": {"omp": o_reason, "pig": p_reason},
            })
        return diffs

    op = {
        k: normalize_prop_value(v, k)
        for k, v in on.props.items()
        if k not in WIRE_INTERNAL_PROP_KEYS and k != "role"
    }
    pp = {
        k: normalize_prop_value(v, k)
        for k, v in pn.props.items()
        if k not in WIRE_INTERNAL_PROP_KEYS and k != "role"
    }

    for k in sorted(set(op.keys()) | set(pp.keys())):
        in_o = k in op
        in_p = k in pp
        if in_o and not in_p:
            rec: dict[str, Any] = {
                "node_path": node_path,
                "prop": k,
                "side": "omp-only",
                "omp": op[k],
                "pig": None,
            }
            if _is_noop_default(on.kind, k, op[k]):
                rec["noop_default"] = True
                rec["node_kind"] = on.kind
            diffs.append(rec)
        elif in_p and not in_o:
            rec = {
                "node_path": node_path,
                "prop": k,
                "side": "port-only",
                "omp": None,
                "pig": pp[k],
            }
            if _is_noop_default(pn.kind, k, pp[k]):
                rec["noop_default"] = True
                rec["node_kind"] = pn.kind
            diffs.append(rec)
        elif op[k] != pp[k] or type(op[k]) is not type(pp[k]):
            diffs.append({
                "node_path": node_path,
                "prop": k,
                "side": "value",
                "omp": op[k],
                "pig": pp[k],
            })

    okids = odoc.visible_children(onid) if onid else []
    pkids = pdoc.visible_children(pnid) if pnid else []
    for oi, pi, ok, pk in _align_children(okids, pkids, odoc, pdoc):
        if ok is not None and pk is not None:
            seg = _child_segment(ok, okids, oi or 0, kind_differs=(ok.kind != pk.kind))
            cpath = seg if node_path == "$" else f"{node_path} > {seg}"
            diffs.extend(diff_subtrees(odoc, ok.id, pdoc, pk.id, cpath, tool_omp=cur_tool_omp, tool_pig=cur_tool_pig))
        elif ok is not None:
            seg = _child_segment(ok, okids, oi or 0)
            cpath = seg if node_path == "$" else f"{node_path} > {seg}"
            diffs.append({
                "node_path": cpath,
                "prop": "$node",
                "side": "omp-only",
                "omp": f"{ok.kind}[{ok.role}]" if ok.role else ok.kind,
                "pig": None,
                "node_count": len(odoc.walk_visible(ok.id)),
            })
        elif pk is not None:
            seg = _child_segment(pk, pkids, pi or 0)
            cpath = seg if node_path == "$" else f"{node_path} > {seg}"
            diffs.append({
                "node_path": cpath,
                "prop": "$node",
                "side": "port-only",
                "omp": None,
                "pig": f"{pk.kind}[{pk.role}]" if pk.role else pk.kind,
                "node_count": len(pdoc.walk_visible(pk.id)),
            })

    return diffs


def _match_pinned_value(expected: Any, actual: Any) -> bool:
    """Match an actual TSP value against a pinned allowlist expectation (literal, regex dict, or span list)."""
    if isinstance(expected, dict):
        if set(expected.keys()) == {"regex"} and isinstance(expected["regex"], str):
            return isinstance(actual, str) and bool(re.search(expected["regex"], actual))
        if not isinstance(actual, dict):
            return False
        # Span / dict pattern: allow `<field>_regex` keys to match string fields by regex while
        # requiring all other keys (including style `s`) to match exactly with no unexpected extra keys.
        exp_keys: set[str] = set()
        for ek, ev in expected.items():
            if ek.endswith("_regex") and isinstance(ev, str):
                base = ek[: -len("_regex")]
                exp_keys.add(base)
                if base not in actual or not isinstance(actual[base], str):
                    return False
                if not re.search(ev, actual[base]):
                    return False
            else:
                exp_keys.add(ek)
                if ek not in actual or not _match_pinned_value(ev, actual[ek]):
                    return False
        return set(actual.keys()) == exp_keys
    if isinstance(expected, list):
        if not isinstance(actual, list) or len(expected) != len(actual):
            return False
        return all(_match_pinned_value(ev, av) for ev, av in zip(expected, actual))
    return actual == expected and type(actual) is type(expected)


def _drawing_entry_subject(entry: dict[str, Any]) -> str | list[str] | None:
    """Default required_subject extractor for drawing_allowlist entries without explicit `ref_subject`."""
    if entry.get("kind") == "frame_child":
        role = entry.get("role")
        if isinstance(role, str) and role:
            return role
        nk = entry.get("node_kind")
        if isinstance(nk, str) and nk:
            return nk
    np_str = str(entry.get("node_path") or "")
    m = re.findall(r"\[([^\]]+)\]", np_str)
    if m:
        return [m[-1], str(entry.get("prop") or "")]
    prop = str(entry.get("prop") or "")
    if prop and not prop.startswith("$"):
        return prop
    part = str(entry.get("part") or "")
    return part or None


def load_drawing_allowlist(path: Path | None = None) -> list[dict[str, Any]]:
    p = path or DEFAULT_DRAWING_ALLOWLIST_PATH
    if not p.is_file():
        return []
    entries = json.loads(p.read_text(encoding="utf-8"))
    if not isinstance(entries, list):
        raise ValueError(f"drawing_allowlist must be a JSON array: {p}")
    resolved = resolve_allowlist_entries(entries, source_name=p.name, subject_extractor=_drawing_entry_subject)
    for i, e in enumerate(resolved):
        entry_kind = e.get("kind", "part_diff")
        if entry_kind == "frame_child":
            for req in ("region", "side", "class", "ref", "why"):
                if not isinstance(e.get(req), str) or not e[req].strip():
                    raise ValueError(f"drawing_allowlist[{i}] frame_child missing required string field {req!r}")
            if e["side"] not in ("omp-only", "port-only"):
                raise ValueError(f"drawing_allowlist[{i}] frame_child invalid side {e['side']!r}")
            if e["region"] not in ("main", "dock", "layer"):
                raise ValueError(f"drawing_allowlist[{i}] frame_child invalid region {e['region']!r}")
            if not e.get("node_kind") and not e.get("role"):
                raise ValueError(f"drawing_allowlist[{i}] frame_child must pin 'node_kind' or 'role'")
            if "text" not in e and "text_regex" not in e and "child_roles" not in e and "empty_container" not in e:
                raise ValueError(
                    f"drawing_allowlist[{i}] frame_child must pin 'text', 'text_regex', 'child_roles', or 'empty_container'"
                )
            continue
        for req in ("part", "node_path", "prop", "side", "class", "ref", "why"):
            if not isinstance(e.get(req), str) or not e[req].strip():
                raise ValueError(f"drawing_allowlist[{i}] missing required string field {req!r}")
        if e["side"] not in ("omp-only", "port-only", "value"):
            raise ValueError(f"drawing_allowlist[{i}] invalid side {e['side']!r}")
        # Finding 8: every `value` entry MUST pin its expected omp and pig values (or use a named match_kind).
        if e["side"] == "value" and not e.get("match_kind"):
            if "omp" not in e and "omp_regex" not in e:
                raise ValueError(f"drawing_allowlist[{i}] side='value' must pin 'omp' or 'omp_regex'")
            if "pig" not in e and "pig_regex" not in e:
                raise ValueError(f"drawing_allowlist[{i}] side='value' must pin 'pig' or 'pig_regex'")
    return resolved


def _match_side_expectation(entry: dict[str, Any], side_key: str, actual: Any) -> bool:
    regex_key = f"{side_key}_regex"
    if regex_key in entry:
        pat = entry[regex_key]
        return isinstance(actual, str) and isinstance(pat, str) and bool(re.search(pat, actual))
    if side_key in entry:
        return _match_pinned_value(entry[side_key], actual)
    return False


def _match_allowlist_entry(
    part_name: str,
    diff: dict[str, Any],
    allowlist: list[dict[str, Any]],
) -> tuple[int, dict[str, Any]] | None:
    """Match a single sub-difference strictly by `(part, node_path, prop, side)` plus pinned value expectations."""
    for idx, entry in enumerate(allowlist):
        if entry.get("kind", "part_diff") != "part_diff":
            continue
        parts_allowed = entry.get("parts")
        part_ok = (part_name in parts_allowed) if isinstance(parts_allowed, list) else (entry["part"] == part_name)
        if not (
            part_ok
            and entry["node_path"] == diff["node_path"]
            and entry["prop"] == diff["prop"]
            and entry["side"] == diff["side"]
        ):
            continue
        if entry["side"] == "value":
            mk = entry.get("match_kind")
            if mk == "welcome_tip":
                if "tip_valid" in diff:
                    if diff["tip_valid"] is True:
                        return (idx, entry)
                    continue
                if is_valid_welcome_tip(diff.get("omp"), "omp") and is_valid_welcome_tip(diff.get("pig"), "pig"):
                    return (idx, entry)
                continue
            if not _match_side_expectation(entry, "omp", diff.get("omp")):
                continue
            if not _match_side_expectation(entry, "pig", diff.get("pig")):
                continue
        return (idx, entry)
    return None

# ---------------------------------------------------------------------------
# Frame-level region child LCS alignment, unmatched-child classification,
# and matched-subtree visible-node-count accounting.
# ---------------------------------------------------------------------------

_RE_ANSI_STRIP = re.compile(r"\x1b\[[0-9;?]*[a-zA-Z]")
_RE_FRAME_NOISE_ID = re.compile(r"\b(?:[sn]\.[A-Za-z0-9_./-]+|\d+[a-z]\.\d+/head)\b")

VISIBLE_TSP_NODE_DEFINITION = (
    "Every TspNode reachable by pre-order DFS from the surface root (`walk_visible`) whose "
    "`props.hidden` is falsy (hidden nodes and their entire subtrees are pruned). "
    "Counts include the `surface` root (1), each visible region container (`main`, `dock`, `layer`), "
    "and every visible child/descendant TSP wire node (e.g. `col`, `row`, `card`, `tool`, `text`, "
    "`md`, `icon`, `kbd`, `spacer`, `ansi`, `checklist`, `elapsed`). Inline `spans` arrays inside a "
    "`text` prop are NOT separate nodes — only wire nodes in `TspDocument.nodes` are counted."
)


def _clean_node_snippet(doc: TspDocument, node: TspNode, limit: int = 80) -> str:
    """Return up to `limit` chars of normalized plain text for a TSP node."""
    raw = doc.visible_text(node.id)
    raw = _RE_ANSI_STRIP.sub("", raw)
    raw = normalize_prop_value(raw)
    s = " ".join(raw.split())
    return s[:limit]


def _alignment_text_key(doc: TspDocument, node: TspNode) -> str:
    """Normalized semantic text snippet used to pair corresponding transcript/dock/layer children."""
    # For user/assistant/tool cards, pick the primary content child so omp's user.tools/turn.usage
    # chrome does not obscure the actual message or tool target during LCS alignment.
    if node.kind == "card" and node.role == "omp.user":
        for c in doc.visible_children(node.id):
            if c.kind == "md":
                return _clean_node_snippet(doc, c, 80).lower()
    if node.kind == "col" and node.role == "omp.assistant":
        for c in doc.visible_children(node.id):
            if c.kind == "md":
                return _clean_node_snippet(doc, c, 80).lower()
            if c.kind == "card" and c.role == "omp.error":
                return "omp.error:" + _clean_node_snippet(doc, c, 60).lower()
    if node.kind == "tool":
        parts = [
            str(node.props.get("name") or ""),
            str(node.props.get("title") or ""),
            str(normalize_prop_value(node.props.get("target") or "")),
            str(normalize_prop_value(node.props.get("command") or "")),
        ]
        return " ".join(p for p in parts if p).strip().lower()[:80]
    return _clean_node_snippet(doc, node, 80).lower()


def _unwrap_single_anonymous_col(doc: TspDocument, node: TspNode) -> TspNode:
    """If `node` is an anonymous wrapper `col` with exactly one visible child, return that child."""
    if node.kind == "col" and not node.role:
        kids = doc.visible_children(node.id)
        if len(kids) == 1:
            return kids[0]
    return node


def _is_anonymous_empty_node(doc: TspDocument, node: TspNode) -> bool:
    """True for anonymous nodes with no role, no visible text, and no visible children (e.g. `spacer`, empty dock slot `col`)."""
    return (
        not node.role
        and not doc.visible_children(node.id)
        and not _clean_node_snippet(doc, node, 80)
    )


def _align_region_children(
    okids: list[TspNode],
    pkids: list[TspNode],
    odoc: TspDocument,
    pdoc: TspDocument,
) -> list[tuple[int | None, int | None, TspNode | None, TspNode | None]]:
    """Order-preserving two-pass LCS alignment of visible children of `main`, `dock`, or `layer`.

    Pass 1 (anchors): LCS-align all non-empty / role-bearing children by `(kind, role, normalized
    text/label snippet)` (never including raw node IDs). Recognises omp's single-child anonymous
    `col` wrappers (e.g. `col(col[omp.editor])`, `col(row[omp.errorBanner])`) and
    `col[omp.assistant](card[omp.error])` on one or both sides.
    Pass 2 (gaps between matched anchors): within each gap between two consecutive matched anchors
    (and before the first / after the last anchor), pair anonymous empty nodes (`spacer`, empty
    anonymous `col` dock slots) of equal `kind` and equal non-internal props (e.g. `size:"sm"`)
    strictly by position inside that gap — so equal runs pair 1:1 while a run of 9 vs 8 leaves
    exact 1 unmatched and a spacer on one side only stays unmatched.
    """
    def _eff(doc: TspDocument, n: TspNode) -> tuple[str, str, str]:
        u = _unwrap_single_anonymous_col(doc, n)
        return u.kind, u.role, _alignment_text_key(doc, u)

    o_eff = [_eff(odoc, n) for n in okids]
    p_eff = [_eff(pdoc, n) for n in pkids]
    o_anon_empty = [_is_anonymous_empty_node(odoc, n) for n in okids]
    p_anon_empty = [_is_anonymous_empty_node(pdoc, n) for n in pkids]

    def match_score(i: int, j: int) -> int:
        # Anonymous empty nodes are paired in Pass 2 strictly within anchor-bounded gaps.
        if o_anon_empty[i] or p_anon_empty[j]:
            return 0
        ok, orole, otxt = o_eff[i]
        pk, prole, ptxt = p_eff[j]
        on, pn = okids[i], pkids[j]
        # Never pair an empty `col[omp.assistant]` (0 children) with a non-empty assistant turn.
        if on.kind == "col" and on.role == "omp.assistant" and not odoc.visible_children(on.id):
            if pn.kind == "col" and pn.role == "omp.assistant" and not pdoc.visible_children(pn.id):
                return 4
            return 0
        if pn.kind == "col" and pn.role == "omp.assistant" and not pdoc.visible_children(pn.id):
            return 0
        # Pair `col[omp.assistant](card[omp.error])` with either wrapped `col[omp.assistant](card[omp.error])`
        # or unwrapped `card[omp.error]`.
        o_is_err = (
            (on.kind == "card" and on.role == "omp.error")
            or (on.kind == "col" and on.role == "omp.assistant" and len(odoc.visible_children(on.id)) == 1 and odoc.visible_children(on.id)[0].role == "omp.error")
        )
        p_is_err = (
            (pn.kind == "card" and pn.role == "omp.error")
            or (pn.kind == "col" and pn.role == "omp.assistant" and len(pdoc.visible_children(pn.id)) == 1 and pdoc.visible_children(pn.id)[0].role == "omp.error")
        )
        if o_is_err and p_is_err:
            return 12 if (otxt and ptxt and otxt == ptxt) else 8
        if ok == pk and orole == prole:
            if orole:
                if otxt and ptxt and otxt == ptxt:
                    return 12
                if ok == "tool" and otxt and ptxt:
                    if otxt[:24] == ptxt[:24]:
                        return 9
                    return 3
                if ok in ("card", "col") and otxt and ptxt:
                    if otxt[:32] == ptxt[:32]:
                        return 10
                    return 0 if orole in ("omp.user", "omp.assistant") else 4
                return 6
            # Non-empty anonymous nodes: require same kind AND matching text or matching child roles
            if otxt and ptxt and otxt == ptxt:
                return 6
            o_sub = [(c.kind, c.role) for c in odoc.visible_children(on.id)]
            p_sub = [(c.kind, c.role) for c in pdoc.visible_children(pn.id)]
            if o_sub and o_sub == p_sub:
                return 5
            return 0
        return 0

    m, n = len(okids), len(pkids)
    dp = [[0] * (n + 1) for _ in range(m + 1)]
    for i in range(m - 1, -1, -1):
        for j in range(n - 1, -1, -1):
            best = max(dp[i + 1][j], dp[i][j + 1])
            sc = match_score(i, j)
            if sc > 0:
                best = max(best, sc + dp[i + 1][j + 1])
            dp[i][j] = best

    anchors: list[tuple[int, int]] = []
    i = j = 0
    while i < m and j < n:
        sc = match_score(i, j)
        if sc > 0 and dp[i][j] == sc + dp[i + 1][j + 1]:
            anchors.append((i, j))
            i += 1
            j += 1
        elif dp[i + 1][j] >= dp[i][j + 1]:
            i += 1
        else:
            j += 1

    def _align_gap(o_start: int, o_end: int, p_start: int, p_end: int) -> list[tuple[int | None, int | None, TspNode | None, TspNode | None]]:
        """Pair anonymous empty nodes of identical kind/props by position inside a single anchor-bounded gap."""
        o_idxs = list(range(o_start, o_end))
        p_idxs = list(range(p_start, p_end))
        gm, gn = len(o_idxs), len(p_idxs)

        def _empty_sig(nodes_list: list[TspNode], idx: int, is_empty: bool) -> tuple[str, str] | None:
            if not is_empty:
                return None
            nd = nodes_list[idx]
            props_clean = {k: normalize_prop_value(v, k) for k, v in nd.props.items() if k not in WIRE_INTERNAL_PROP_KEYS}
            return (nd.kind, json.dumps(props_clean, sort_keys=True))

        o_sigs = [_empty_sig(okids, idx, o_anon_empty[idx]) for idx in o_idxs]
        p_sigs = [_empty_sig(pkids, idx, p_anon_empty[idx]) for idx in p_idxs]
        gdp = [[0] * (gn + 1) for _ in range(gm + 1)]
        for a in range(gm - 1, -1, -1):
            for b in range(gn - 1, -1, -1):
                best = max(gdp[a + 1][b], gdp[a][b + 1])
                if o_sigs[a] is not None and o_sigs[a] == p_sigs[b]:
                    best = max(best, 1 + gdp[a + 1][b + 1])
                gdp[a][b] = best
        out_gap: list[tuple[int | None, int | None, TspNode | None, TspNode | None]] = []
        a = b = 0
        while a < gm or b < gn:
            if a < gm and b < gn and o_sigs[a] is not None and o_sigs[a] == p_sigs[b] and gdp[a][b] == 1 + gdp[a + 1][b + 1]:
                oi, pi = o_idxs[a], p_idxs[b]
                out_gap.append((oi, pi, okids[oi], pkids[pi]))
                a += 1
                b += 1
            elif a < gm and (b == gn or gdp[a + 1][b] >= gdp[a][b + 1]):
                oi = o_idxs[a]
                out_gap.append((oi, None, okids[oi], None))
                a += 1
            else:
                pi = p_idxs[b]
                out_gap.append((None, pi, None, pkids[pi]))
                b += 1
        return out_gap

    pairs: list[tuple[int | None, int | None, TspNode | None, TspNode | None]] = []
    prev_o = 0
    prev_p = 0
    for ao, ap in anchors:
        pairs.extend(_align_gap(prev_o, ao, prev_p, ap))
        pairs.append((ao, ap, okids[ao], pkids[ap]))
        prev_o = ao + 1
        prev_p = ap + 1
    pairs.extend(_align_gap(prev_o, m, prev_p, n))
    return pairs


def _match_frame_child_allowlist(
    item: dict[str, Any],
    allowlist: list[dict[str, Any]],
) -> tuple[int, dict[str, Any]] | None:
    """Classify an unmatched region child via `drawing_allowlist.json` (`kind: "frame_child"`)."""
    for idx, entry in enumerate(allowlist):
        if entry.get("kind") != "frame_child":
            continue
        if entry["region"] != item["region"] or entry["side"] != item["side"]:
            continue
        if "step" in entry and entry["step"] != item["step"]:
            continue
        if "steps" in entry and item["step"] not in entry["steps"]:
            continue
        if "node_kind" in entry and entry["node_kind"] != item["kind"]:
            continue
        if "role" in entry and (entry["role"] or "") != (item["role"] or ""):
            continue
        if entry.get("empty_container") is True:
            if item["visible_node_count"] != 1 or item["text"]:
                continue
        if "next_sibling_kind" in entry:
            if item.get("next_nonempty_sibling_kind") != entry["next_sibling_kind"]:
                continue
        if "child_roles" in entry:
            if item.get("child_roles") != entry["child_roles"]:
                continue
        if "text" in entry and item["text"] != entry["text"]:
            continue
        if "text_regex" in entry and not re.search(entry["text_regex"], item["text"]):
            continue
        return (idx, entry)
    return None


def _canonical_part_name_for_node(node: TspNode, region: str) -> str | None:
    """Map a region child node to its logical compared part name for subtree-diff allowlist classification."""
    r = node.role
    if r == "omp.welcome":
        return "welcome banner"
    if r == "omp.user":
        return "user message"
    if r == "omp.assistant":
        return "assistant text (markdown)"
    if r == "omp.thinking" or r == "omp.thinking.live":
        return "thinking block"
    if r == "omp.working":
        return "working row"
    if r == "omp.editor":
        return "composer"
    if r == "omp.error":
        return "provider error turn"
    if r == "omp.errorBanner":
        return "error banner"
    if r == "omp.ask.card" or node.kind == "picker":
        return "ask form (live)"
    if r == "omp.autocomplete":
        return "slash menu"
    if r.startswith("omp.tool."):
        tool = r[len("omp.tool."):]
        if tool == "read" and (node.props.get("status") == "error" or node.props.get("tone") == "error"):
            return "tool card: failing read"
        return f"tool card: {tool}"
    return None


_ROLE_TO_PART_NAME: dict[str, str] = {
    "omp.welcome": "welcome banner",
    "omp.user": "user message",
    "omp.assistant": "assistant text (markdown)",
    "omp.thinking": "thinking block",
    "omp.thinking.live": "thinking block",
    "omp.working": "working row",
    "omp.editor": "composer",
    "omp.error": "provider error turn",
    "omp.errorBanner": "error banner",
    "omp.ask.card": "ask form (live)",
    "omp.autocomplete": "slash menu",
}


def _match_allowlist_for_nested_diff(
    outer_part: str,
    diff: dict[str, Any],
    allowlist: list[dict[str, Any]],
) -> tuple[int, dict[str, Any]] | None:
    """Match `diff` against `outer_part` first, then against any nested compared-part ancestor in `node_path`.

    General resolution: when a matched container (e.g. `row[omp.hud.activity] > col[omp.hud.status] > row[omp.working]`,
    or `col[omp.assistant] > card[omp.error]`, or `col > col[omp.editor]`) encloses a nested child that is itself a
    compared part (e.g. `working row`, `provider error turn`, `composer`), a diff below that nested part also checks
    that part's allowlist entries with `node_path` relativised to the nested part root!
    """
    hit = _match_allowlist_entry(outer_part, diff, allowlist)
    if hit is not None:
        return hit
    np_str = str(diff.get("node_path") or "")
    if not np_str or np_str == "$":
        return None
    segs = [s.strip() for s in np_str.split(" > ")]
    for i in range(len(segs)):
        m = re.search(r"\[([^\]]+)\]", segs[i])
        if not m:
            continue
        role = m.group(1)
        nested_part = _ROLE_TO_PART_NAME.get(role)
        if nested_part is None and role.startswith("omp.tool."):
            nested_part = f"tool card: {role[len('omp.tool.'):]}"
        if not nested_part:
            continue
        rel_path = " > ".join(segs[i + 1 :]) if (i + 1) < len(segs) else "$"
        rel_diff = {**diff, "node_path": rel_path}
        hit = _match_allowlist_entry(nested_part, rel_diff, allowlist)
        if hit is not None:
            return hit
    return None


def _explain_matched_node_count_delta(
    step: str,
    region: str,
    oi: int,
    pi: int,
    on: TspNode,
    pn: TspNode,
    odoc: TspDocument,
    pdoc: TspDocument,
    allowlist: list[dict[str, Any]],
    allow_hits: dict[int, int] | None,
    part_by_node_id: dict[tuple[str, str], str],
) -> dict[str, Any]:
    """Audit visible-node-count delta inside a matched pair `(on, pn)` of region children.

    Walks the aligned pair (unwrapping a single anonymous wrapper `col` on omp's dock or
    `col[omp.assistant](card[omp.error])` if present on one side only, explicitly counting the wrapper node),
    computes `diff_subtrees` on the inner nodes, resolves allowlist entries on BOTH the outer part AND
    any nested compared-part ancestor in `node_path`, and verifies exact node-count arithmetic.
    """
    o_vis = len(odoc.walk_visible(on.id))
    p_vis = len(pdoc.walk_visible(pn.id))
    delta = o_vis - p_vis

    o_inner = on
    p_inner = pn
    wrapper_note: str | None = None
    wrapper_delta = 0
    # Unwrap when BOTH sides wrap in an identical single-child anonymous `col` (0 wrapper delta)
    if (
        on.kind == "col" and not on.role and len(odoc.visible_children(on.id)) == 1
        and pn.kind == "col" and not pn.role and len(pdoc.visible_children(pn.id)) == 1
    ):
        o_inner = odoc.visible_children(on.id)[0]
        p_inner = pdoc.visible_children(pn.id)[0]
    elif on.kind == "col" and not on.role and len(odoc.visible_children(on.id)) == 1 and not (pn.kind == "col" and not pn.role):
        o_inner = odoc.visible_children(on.id)[0]
        wrapper_delta = 1
        wrapper_note = f"omp wraps {o_inner.kind}[{o_inner.role}] in anonymous col (+1 node)"
    elif (
        on.kind == "col"
        and on.role == "omp.assistant"
        and len(odoc.visible_children(on.id)) == 1
        and odoc.visible_children(on.id)[0].role == "omp.error"
        and pn.kind == "card"
        and pn.role == "omp.error"
    ):
        o_inner = odoc.visible_children(on.id)[0]
        wrapper_delta = 1
        wrapper_note = "omp wraps card[omp.error] in col[omp.assistant] (+1 node)"

    part_name = (
        part_by_node_id.get((step, o_inner.id))
        or part_by_node_id.get((step, p_inner.id))
        or _canonical_part_name_for_node(o_inner, region)
        or _canonical_part_name_for_node(p_inner, region)
        or f"{region}:{o_inner.kind}[{o_inner.role}]"
    )

    sub_diffs = diff_subtrees(odoc, o_inner.id, pdoc, p_inner.id, "$")
    allowlisted_node_diffs: list[dict[str, Any]] = []
    unexplained_node_diffs: list[dict[str, Any]] = []
    allowlisted_prop_diffs = 0
    unexplained_prop_diffs = 0

    for d in sub_diffs:
        hit = _match_allowlist_for_nested_diff(part_name, d, allowlist)
        if hit is not None:
            idx, entry = hit
            if allow_hits is not None:
                allow_hits[idx] = allow_hits.get(idx, 0) + 1
            if d["prop"] == "$node":
                allowlisted_node_diffs.append({**d, "allowlist_class": entry["class"], "allowlist_ref": entry["ref"]})
            else:
                allowlisted_prop_diffs += 1
        elif d.get("noop_default"):
            continue
        else:
            if d["prop"] == "$node":
                unexplained_node_diffs.append(d)
            else:
                unexplained_prop_diffs += 1
    # Compute exact subtree sizes of every `$node` diff by locating the unmatched child in the tree.
    o_inner_vis = len(odoc.walk_visible(o_inner.id))
    p_inner_vis = len(pdoc.walk_visible(p_inner.id))
    inner_delta = o_inner_vis - p_inner_vis

    # Check if wrapper itself is allowlisted via frame_child or explained by a HOST/wrapper note
    wrapper_allow: dict[str, Any] | None = None
    if wrapper_delta:
        w_probe = {
            "step": step,
            "region": region,
            "side": "omp-only",
            "kind": on.kind,
            "role": on.role,
            "visible_node_count": 1,
            "child_roles": [f"{c.kind}[{c.role}]" if c.role else c.kind for c in odoc.visible_children(on.id)],
            "text": wrapper_note or "",
        }
        wh = _match_frame_child_allowlist(w_probe, allowlist)
        if wh is not None:
            widx, wentry = wh
            if allow_hits is not None:
                allow_hits[widx] = allow_hits.get(widx, 0) + 1
            wrapper_allow = wentry

    # Exact visible-node arithmetic check: every single node in `delta = o_vis - p_vis` MUST be
    # accounted for by `wrapper_delta + sum(allowlisted $node deltas) + sum(unexplained $node deltas)`.
    def _signed_cnt(d: dict[str, Any]) -> int:
        c = int(d.get("node_count") or 0)
        return c if d.get("side") == "omp-only" else (-c if d.get("side") == "port-only" else 0)

    allow_node_delta = sum(_signed_cnt(d) for d in allowlisted_node_diffs)
    unexp_node_delta = sum(_signed_cnt(d) for d in unexplained_node_diffs)
    unaccounted_delta = delta - (wrapper_delta + allow_node_delta + unexp_node_delta)

    unexplained_reasons: list[str] = []
    if wrapper_delta and wrapper_allow is None:
        unexplained_reasons.append(wrapper_note or f"unallowlisted wrapper (+{wrapper_delta} node)")
    for ud in unexplained_node_diffs:
        unexplained_reasons.append(f"{format_diff_item(ud)} ({_signed_cnt(ud):+d} nodes)")
    if unaccounted_delta != 0:
        unexplained_reasons.append(
            f"unaccounted visible-node delta of {unaccounted_delta:+d} inside {o_inner.kind}[{o_inner.role}] "
            f"(omp={o_vis}, pig={p_vis}, wrapper={wrapper_delta:+d}, allow_nodes={allow_node_delta:+d}, unexp_nodes={unexp_node_delta:+d})"
        )

    status = "same" if delta == 0 else ("expected" if not unexplained_reasons else "unexplained")
    return {
        "step": step,
        "region": region,
        "omp_index": oi,
        "pig_index": pi,
        "kind": o_inner.kind,
        "role": o_inner.role or None,
        "part": part_name,
        "omp_visible_nodes": o_vis,
        "pig_visible_nodes": p_vis,
        "delta": delta,
        "wrapper_delta": wrapper_delta,
        "allowlisted_node_delta": allow_node_delta,
        "unexplained_node_delta": unexp_node_delta,
        "unaccounted_delta": unaccounted_delta,
        "wrapper_note": wrapper_note,
        "wrapper_allowlisted": wrapper_allow["ref"] if wrapper_allow else None,
        "allowlisted_node_diffs": [
            {
                "node_path": d["node_path"],
                "side": d["side"],
                "omp": d["omp"],
                "pig": d["pig"],
                "node_count": int(d.get("node_count") or 0),
                "class": d["allowlist_class"],
                "ref": d["allowlist_ref"],
            }
            for d in allowlisted_node_diffs
        ],
        "unexplained_node_diffs": [
            {
                "node_path": d["node_path"],
                "side": d["side"],
                "omp": d["omp"],
                "pig": d["pig"],
                "node_count": int(d.get("node_count") or 0),
            }
            for d in unexplained_node_diffs
        ],
        "unexplained_reasons": unexplained_reasons,
        "status": status,
        "text": _clean_node_snippet(odoc, o_inner, 80),
    }

def align_and_classify_step_regions(
    step: str,
    odoc: TspDocument,
    pdoc: TspDocument,
    allowlist: list[dict[str, Any]],
    *,
    allow_hits: dict[int, int] | None = None,
    part_by_node_id: dict[tuple[str, str], str] | None = None,
) -> dict[str, Any]:
    """Align ordered children of `main`, `dock`, and `layer` via LCS and classify every unmatched child and matched-subtree node-count delta."""
    p_map = part_by_node_id or {}
    regions_out: dict[str, Any] = {}
    unmatched_all: list[dict[str, Any]] = []
    count_deltas_all: list[dict[str, Any]] = []

    for reg in ("main", "dock", "layer"):
        okids = odoc.visible_children(reg) if reg in odoc.nodes else []
        pkids = pdoc.visible_children(reg) if reg in pdoc.nodes else []
        pairs = _align_region_children(okids, pkids, odoc, pdoc)
        matched_entries: list[dict[str, Any]] = []
        unmatched_entries: list[dict[str, Any]] = []

        for oi, pi, on, pn in pairs:
            if on is not None and pn is not None:
                cd = _explain_matched_node_count_delta(
                    step, reg, oi or 0, pi or 0, on, pn, odoc, pdoc, allowlist, allow_hits, p_map
                )
                matched_entries.append(cd)
                if cd["delta"] != 0:
                    count_deltas_all.append(cd)
            elif on is not None:
                vis_cnt = len(odoc.walk_visible(on.id))
                child_roles = [f"{c.kind}[{c.role}]" if c.role else c.kind for c in odoc.visible_children(on.id)]
                next_ne_kind: str | None = None
                if oi is not None:
                    for nxt in okids[oi + 1 :]:
                        if not (nxt.kind == "col" and nxt.role == "omp.assistant" and not odoc.visible_children(nxt.id)):
                            next_ne_kind = nxt.kind
                            break
                item: dict[str, Any] = {
                    "step": step,
                    "region": reg,
                    "side": "omp-only",
                    "index": oi,
                    "kind": on.kind,
                    "role": on.role or None,
                    "visible_node_count": vis_cnt,
                    "child_roles": child_roles,
                    "next_nonempty_sibling_kind": next_ne_kind,
                    "text": _clean_node_snippet(odoc, on, 80),
                    "belongs_to_part": p_map.get((step, on.id)),
                }
                hit = _match_frame_child_allowlist(item, allowlist)
                if hit is not None:
                    idx, entry = hit
                    if allow_hits is not None:
                        allow_hits[idx] = allow_hits.get(idx, 0) + 1
                    item["status"] = "expected"
                    item["allowlist"] = {
                        "class": entry["class"],
                        "class_in_doubt": bool(entry.get("class_in_doubt")),
                        "ref": entry["ref"],
                        "why": entry["why"],
                    }
                else:
                    item["status"] = "unexplained"
                unmatched_entries.append(item)
                unmatched_all.append(item)
            elif pn is not None:
                vis_cnt = len(pdoc.walk_visible(pn.id))
                child_roles = [f"{c.kind}[{c.role}]" if c.role else c.kind for c in pdoc.visible_children(pn.id)]
                item = {
                    "step": step,
                    "region": reg,
                    "side": "port-only",
                    "index": pi,
                    "kind": pn.kind,
                    "role": pn.role or None,
                    "visible_node_count": vis_cnt,
                    "child_roles": child_roles,
                    "text": _clean_node_snippet(pdoc, pn, 80),
                    "belongs_to_part": p_map.get((step, pn.id)),
                }
                hit = _match_frame_child_allowlist(item, allowlist)
                if hit is not None:
                    idx, entry = hit
                    if allow_hits is not None:
                        allow_hits[idx] = allow_hits.get(idx, 0) + 1
                    item["status"] = "expected"
                    item["allowlist"] = {
                        "class": entry["class"],
                        "class_in_doubt": bool(entry.get("class_in_doubt")),
                        "ref": entry["ref"],
                        "why": entry["why"],
                    }
                else:
                    item["status"] = "unexplained"
                unmatched_entries.append(item)
                unmatched_all.append(item)

        osig = [f"{c.kind}[{c.role}]" if c.role else c.kind for c in okids]
        psig = [f"{c.kind}[{c.role}]" if c.role else c.kind for c in pkids]
        regions_out[reg] = {
            "same": osig == psig and not unmatched_entries and all(m["delta"] == 0 for m in matched_entries),
            "omp_count": len(okids),
            "pig_count": len(pkids),
            "matched_pairs": len(matched_entries),
            "unmatched_count": len(unmatched_entries),
            "unexplained_unmatched_count": sum(1 for u in unmatched_entries if u["status"] == "unexplained"),
            "unexplained_count_delta_pairs": sum(1 for m in matched_entries if m["status"] == "unexplained"),
            "omp_children": osig,
            "pig_children": psig,
            "unmatched_children": unmatched_entries,
            "matched_count_deltas": [m for m in matched_entries if m["delta"] != 0],
        }

    return {
        "regions": regions_out,
        "unmatched_children": unmatched_all,
        "matched_count_deltas": count_deltas_all,
        "unexplained_unmatched_count": sum(1 for u in unmatched_all if u["status"] == "unexplained"),
        "expected_unmatched_count": sum(1 for u in unmatched_all if u["status"] == "expected"),
        "unexplained_count_delta_count": sum(1 for m in count_deltas_all if m["status"] == "unexplained"),
        "expected_count_delta_count": sum(1 for m in count_deltas_all if m["status"] == "expected"),
    }
def format_diff_item(d: dict[str, Any], allow: dict[str, Any] | None = None) -> str:
    loc = f"{d['node_path']}.{d['prop']}" if d["node_path"] != "$" else d["prop"]
    if d["side"] == "omp-only":
        desc = f"{loc} (omp-only: {_compact_repr(d['omp'])})"
    elif d["side"] == "port-only":
        desc = f"{loc} (port-only: {_compact_repr(d['pig'])})"
    else:
        desc = f"{loc}: omp={_compact_repr(d['omp'])} vs pig={_compact_repr(d['pig'])}"
    if allow:
        doubt = " CLASS_IN_DOUBT" if allow.get("class_in_doubt") else ""
        return f"[{allow['class']}{doubt} {allow['ref']}] {desc}"
    if d.get("noop_default"):
        return f"[NOOP_DEFAULT] {desc}"
    return desc


def summarize_part(
    doc: TspDocument,
    dom: dict[str, Any],
    region_dom_key: str,
    nodes: list[TspNode],
    pick: str = "last",
) -> dict[str, Any] | None:
    if not nodes:
        return None
    chosen = nodes[-1] if pick == "last" else nodes[0]
    sig = doc.subtree_signature(chosen.id, max_depth=2)
    dom_region = dom.get("regions", {}).get(region_dom_key, [])
    dom_hit = first_dom_match(dom_region, chosen.kind)
    if dom_hit is None:
        for rnodes in (dom.get("regions") or {}).values():
            dom_hit = first_dom_match(rnodes, chosen.kind)
            if dom_hit is not None:
                break
    return {
        "id": chosen.id,
        "kind": chosen.kind,
        "css_kind": f"sf-{chosen.kind}",
        "dom_verified": dom_hit is not None,
        "dom_class": dom_hit["class"] if dom_hit is not None else None,
        "role": chosen.role or None,
        "tone": chosen.props.get("tone"),
        "status": chosen.props.get("status"),
        "nesting": sig,
        "nesting_str": format_nesting(sig),
        "count": len(nodes),
    }


# Bounded thinking-duration timing helper (Finding 14):
# Both sides MUST carry a duration (`Thought for ...`) and differ by at most MAX_THINKING_TIMING_DELTA_S;
# `Thought` (no duration) against `Thought for ...` is a real difference, NEVER timing.
MAX_THINKING_TIMING_DELTA_S = 5
_RE_THOUGHT_DUR = re.compile(r"^Thought for (?:(\d+)h ?)?(?:(\d+)m ?)?(?:(\d+)s)?$")


def _parse_thought_duration_seconds(text: str) -> int | None:
    if not isinstance(text, str) or not text.startswith("Thought for "):
        return None
    rest = text[len("Thought for "):].strip()
    if not rest:
        return None
    m = _RE_THOUGHT_DUR.match(text)
    if not m:
        return None
    h_s, m_s, s_s = m.group(1), m.group(2), m.group(3)
    if h_s is None and m_s is None and s_s is None:
        return None
    return int(h_s or 0) * 3600 + int(m_s or 0) * 60 + int(s_s or 0)


def compare_part(
    part_name: str,
    step: str,
    omp_doc: TspDocument,
    pig_doc: TspDocument,
    omp_info: dict[str, Any] | None,
    pig_info: dict[str, Any] | None,
    allowlist: list[dict[str, Any]] | None = None,
    *,
    allow_hits: dict[int, int] | None = None,
    compare_children: bool = True,
    check_count: bool = True,
) -> dict[str, Any]:
    entries = allowlist or []

    if omp_info is None and pig_info is None:
        return {
            "part": part_name,
            "step": step,
            "verdict": "missing",
            "missing_side": "both",
            "allowlisted_diffs": [],
            "class_in_doubt_diffs": [],
            "noop_default_diffs": [],
            "timing_diffs": [],
            "unexpected_diffs": [],
            "details": "not rendered on either side",
            "omp": None,
            "pig": None,
        }

    # Build the full sub-difference list.
    raw_diffs: list[dict[str, Any]] = []
    if omp_info is None and pig_info is not None:
        raw_diffs.append({
            "node_path": "$",
            "prop": "$node",
            "side": "port-only",
            "omp": None,
            "pig": pig_info["nesting_str"],
        })
    elif pig_info is None and omp_info is not None:
        raw_diffs.append({
            "node_path": "$",
            "prop": "$node",
            "side": "omp-only",
            "omp": omp_info["nesting_str"],
            "pig": None,
        })
    else:
        assert omp_info is not None and pig_info is not None
        if check_count and omp_info.get("count") != pig_info.get("count"):
            raw_diffs.append({
                "node_path": "$",
                "prop": "$count",
                "side": "value",
                "omp": omp_info.get("count"),
                "pig": pig_info.get("count"),
            })
        if compare_children:
            raw_diffs.extend(diff_subtrees(omp_doc, omp_info["id"], pig_doc, pig_info["id"], "$"))
        else:
            if omp_info["role"] != pig_info["role"]:
                raw_diffs.append({
                    "node_path": "$",
                    "prop": "role",
                    "side": "value",
                    "omp": omp_info["role"],
                    "pig": pig_info["role"],
                })
        if not omp_info["dom_verified"]:
            raw_diffs.append({
                "node_path": "$",
                "prop": "$dom_verified",
                "side": "omp-only",
                "omp": False,
                "pig": pig_info["dom_verified"],
            })
        if not pig_info["dom_verified"]:
            raw_diffs.append({
                "node_path": "$",
                "prop": "$dom_verified",
                "side": "port-only",
                "omp": omp_info["dom_verified"],
                "pig": False,
            })

    def _is_thinking_head_timing_diff(d: dict[str, Any]) -> bool:
        if part_name != "thinking block" or d.get("node_path") != "text@0" or d.get("prop") != "spans" or d.get("side") != "value":
            return False
        o_spans = d.get("omp")
        p_spans = d.get("pig")
        if not (isinstance(o_spans, list) and isinstance(p_spans, list) and len(o_spans) == 1 and len(p_spans) == 1):
            return False
        os0, ps0 = o_spans[0], p_spans[0]
        if not (isinstance(os0, dict) and isinstance(ps0, dict)):
            return False
        if {k: v for k, v in os0.items() if k != "t"} != {k: v for k, v in ps0.items() if k != "t"}:
            return False
        ot, pt = os0.get("t"), ps0.get("t")
        if not (isinstance(ot, str) and isinstance(pt, str)):
            return False
        o_sec = _parse_thought_duration_seconds(ot)
        p_sec = _parse_thought_duration_seconds(pt)
        if o_sec is None or p_sec is None:
            return False
        return abs(o_sec - p_sec) <= MAX_THINKING_TIMING_DELTA_S

    allowlisted_diffs: list[dict[str, Any]] = []
    class_in_doubt_diffs: list[dict[str, Any]] = []
    noop_default_diffs: list[dict[str, Any]] = []
    timing_diffs: list[dict[str, Any]] = []
    unexpected_diffs: list[dict[str, Any]] = []
    for d in raw_diffs:
        hit = _match_allowlist_entry(part_name, d, entries)
        if hit is not None:
            idx, entry = hit
            if allow_hits is not None:
                allow_hits[idx] = allow_hits.get(idx, 0) + 1
            rec = {**d, "allowlist": entry}
            allowlisted_diffs.append(rec)
            if entry.get("class_in_doubt"):
                class_in_doubt_diffs.append(rec)
        elif _is_thinking_head_timing_diff(d):
            timing_diffs.append(d)
        elif d.get("noop_default"):
            noop_default_diffs.append(d)
        else:
            unexpected_diffs.append(d)

    if not raw_diffs:
        verdict = "same"
        details = f"equal kind (`{omp_info['css_kind']}`), roles, subtree props, and nesting"
    elif not unexpected_diffs and timing_diffs and not allowlisted_diffs and not noop_default_diffs:
        verdict = "timing"
        details = "; ".join(f"[TIMING] {format_diff_item(d)}" for d in timing_diffs)
    elif not unexpected_diffs and not timing_diffs and (allowlisted_diffs or noop_default_diffs):
        verdict = "expected"
        parts_txt = [format_diff_item(d, d["allowlist"]) for d in allowlisted_diffs]
        parts_txt.extend(format_diff_item(d) for d in noop_default_diffs)
        details = "; ".join(parts_txt)
    elif not unexpected_diffs and timing_diffs:
        verdict = "timing"
        parts_txt = [f"[TIMING] {format_diff_item(d)}" for d in timing_diffs]
        parts_txt.extend(format_diff_item(d, d["allowlist"]) for d in allowlisted_diffs)
        parts_txt.extend(format_diff_item(d) for d in noop_default_diffs)
        details = "; ".join(parts_txt)
    elif omp_info is None or pig_info is None:
        verdict = "missing"
        details = "; ".join(format_diff_item(d) for d in unexpected_diffs)
    else:
        verdict = "differs"
        parts_txt = [format_diff_item(d) for d in unexpected_diffs]
        if timing_diffs:
            parts_txt.extend(f"[TIMING] {format_diff_item(d)}" for d in timing_diffs)
        if noop_default_diffs:
            parts_txt.extend(format_diff_item(d) for d in noop_default_diffs)
        if allowlisted_diffs:
            parts_txt.append(f"(+ {len(allowlisted_diffs)} expected)")
        details = "; ".join(parts_txt)

    return {
        "part": part_name,
        "step": step,
        "verdict": verdict,
        "missing_side": ("omp" if omp_info is None else ("oh-my-pig" if pig_info is None else None)),
        "allowlisted_diffs": allowlisted_diffs,
        "class_in_doubt_diffs": class_in_doubt_diffs,
        "noop_default_diffs": noop_default_diffs,
        "timing_diffs": timing_diffs,
        "unexpected_diffs": unexpected_diffs,
        "details": details,
        "omp": omp_info,
        "pig": pig_info,
    }

DEFAULT_STEPS = [
    "00-started",
    "01-markdown",
    "02a-working",
    "02b-thinking",
    "03-tool-read",
    "04-tool-bash",
    "05-tool-edit",
    "06-tool-error",
    "07-tool-todo",
    "08a-ask-form",
    "08b-ask-submitted",
    "09-tool-eval",
    "10-tool-task",
    "11-tool-web-search",
    "12-tool-write-search",
    "13-tool-lsp-ast",
    "14a-long-folded",
    "14b-long-expanded",
    "15-provider-error",
    "16-plan-mode",
    "17a-slash-menu",
    "17b-slash-closed",
    "18-narrow-80",
]


def discover_steps(omp_dir: str, pig_dir: str) -> list[str]:
    """Use DEFAULT_STEPS filtered to steps captured on at least one side (preserving order)."""
    present: set[str] = set()
    for d in (omp_dir, pig_dir):
        if not os.path.isdir(d):
            continue
        for fname in os.listdir(d):
            if fname.startswith("tree-") and fname.endswith(".json"):
                present.add(fname[len("tree-") : -len(".json")])
    if not present:
        return list(DEFAULT_STEPS)
    ordered = [s for s in DEFAULT_STEPS if s in present]
    extras = sorted(s for s in present if s not in ordered)
    return ordered + extras


def compare_directories(
    omp_dir: str,
    pig_dir: str,
    allowlist_path: Path | None = None,
) -> dict[str, Any]:
    """Compare saved headless Tern capture directories offline (re-classifies without re-running binaries)."""
    allowlist = load_drawing_allowlist(allowlist_path)
    steps = discover_steps(omp_dir, pig_dir)
    final_step = steps[-1]

    omp_docs = {s: load_tsp_at_step(omp_dir, s) for s in steps}
    pig_docs = {s: load_tsp_at_step(pig_dir, s) for s in steps}
    omp_doms = {s: extract_dom_regions(os.path.join(omp_dir, f"tree-{s}.json")) for s in steps}
    pig_doms = {s: extract_dom_regions(os.path.join(pig_dir, f"tree-{s}.json")) for s in steps}

    def _collect_sheet_errors(out_dir: str) -> list[dict[str, Any]]:
        errs: list[dict[str, Any]] = []
        tsp_path = os.path.join(out_dir, "tsp.jsonl")
        if not os.path.isfile(tsp_path):
            return errs
        with open(tsp_path, "r", encoding="utf-8", errors="replace") as fh:
            for raw in fh:
                raw = raw.strip()
                if not raw:
                    continue
                try:
                    rec = json.loads(raw)
                except json.JSONDecodeError:
                    continue
                if rec.get("dir") == "in" and rec.get("verb") == "e":
                    body = rec.get("body")
                    if isinstance(body, dict) and body.get("ev") == "error" and body.get("sheet"):
                        errs.append({
                            "sheet": body.get("sheet"),
                            "sf": body.get("sf"),
                            "msg": body.get("msg"),
                        })
        return errs

    omp_sheet_errors = _collect_sheet_errors(omp_dir)
    pig_sheet_errors = _collect_sheet_errors(pig_dir)

    native_proof = {
        "final_step": final_step,
        "omp": {
            "surface_id": omp_docs[final_step].surface_id,
            "surface_role": omp_docs[final_step].surface_role,
            "dom_regions": omp_doms[final_step].get("region_names", []),
            "dom_surface_nodes": omp_doms[final_step].get("surface_node_count", 0),
            "stylesheet_errors": omp_sheet_errors,
        },
        "pig": {
            "surface_id": pig_docs[final_step].surface_id,
            "surface_role": pig_docs[final_step].surface_role,
            "dom_regions": pig_doms[final_step].get("region_names", []),
            "dom_surface_nodes": pig_doms[final_step].get("surface_node_count", 0),
            "stylesheet_errors": pig_sheet_errors,
        },
    }
    rows: list[dict[str, Any]] = []
    allow_hits: dict[int, int] = {i: 0 for i in range(len(allowlist))}

    def has_step(s: str) -> bool:
        return s in omp_docs and s in pig_docs

    if has_step("00-started"):
        rows.append(
            compare_part(
                "surface root",
                "00-started",
                omp_docs["00-started"],
                pig_docs["00-started"],
                {
                    "id": omp_docs["00-started"].surface_id,
                    "kind": "surface",
                    "css_kind": "sf-region",
                    "dom_verified": bool(omp_doms["00-started"].get("region_names")),
                    "dom_class": ",".join(omp_doms["00-started"].get("region_names", [])),
                    "role": omp_docs["00-started"].surface_role or None,
                    "tone": None,
                    "status": None,
                    "nesting": {"kind": "surface", "role": omp_docs["00-started"].surface_role or None, "children": []},
                    "nesting_str": f"surface[{omp_docs['00-started'].surface_role}]",
                    "count": 1,
                }
                if omp_docs["00-started"].surface_id
                else None,
                {
                    "id": pig_docs["00-started"].surface_id,
                    "kind": "surface",
                    "css_kind": "sf-region",
                    "dom_verified": bool(pig_doms["00-started"].get("region_names")),
                    "dom_class": ",".join(pig_doms["00-started"].get("region_names", [])),
                    "role": pig_docs["00-started"].surface_role or None,
                    "tone": None,
                    "status": None,
                    "nesting": {"kind": "surface", "role": pig_docs["00-started"].surface_role or None, "children": []},
                    "nesting_str": f"surface[{pig_docs['00-started'].surface_role}]",
                    "count": 1,
                }
                if pig_docs["00-started"].surface_id
                else None,
                allowlist,
                allow_hits=allow_hits,
                compare_children=False,
            )
        )
        rows.append(
            compare_part(
                "welcome banner",
                "00-started",
                omp_docs["00-started"],
                pig_docs["00-started"],
                summarize_part(omp_docs["00-started"], omp_doms["00-started"], "sf-main", find_welcome_nodes(omp_docs["00-started"]), pick="first"),
                summarize_part(pig_docs["00-started"], pig_doms["00-started"], "sf-main", find_welcome_nodes(pig_docs["00-started"]), pick="first"),
                allowlist,
                allow_hits=allow_hits,
            )
        )

    if has_step("01-markdown"):
        rows.append(
            compare_part(
                "user message",
                "01-markdown",
                omp_docs["01-markdown"],
                pig_docs["01-markdown"],
                summarize_part(omp_docs["01-markdown"], omp_doms["01-markdown"], "sf-main", find_tsp_by_role(omp_docs["01-markdown"], "omp.user", "main"), pick="first"),
                summarize_part(pig_docs["01-markdown"], pig_doms["01-markdown"], "sf-main", find_tsp_by_role(pig_docs["01-markdown"], "omp.user", "main"), pick="first"),
                allowlist,
                allow_hits=allow_hits,
            )
        )
        rows.append(
            compare_part(
                "assistant text (markdown)",
                "01-markdown",
                omp_docs["01-markdown"],
                pig_docs["01-markdown"],
                summarize_part(omp_docs["01-markdown"], omp_doms["01-markdown"], "sf-main", find_tsp_by_role(omp_docs["01-markdown"], "omp.assistant", "main"), pick="first"),
                summarize_part(pig_docs["01-markdown"], pig_doms["01-markdown"], "sf-main", find_tsp_by_role(pig_docs["01-markdown"], "omp.assistant", "main"), pick="first"),
                allowlist,
                allow_hits=allow_hits,
            )
        )

    if has_step("02a-working"):
        rows.append(
            compare_part(
                "working row",
                "02a-working",
                omp_docs["02a-working"],
                pig_docs["02a-working"],
                summarize_part(omp_docs["02a-working"], omp_doms["02a-working"], "sf-dock", find_working_nodes(omp_docs["02a-working"]), pick="first"),
                summarize_part(pig_docs["02a-working"], pig_doms["02a-working"], "sf-main", find_working_nodes(pig_docs["02a-working"]), pick="first"),
                allowlist,
                allow_hits=allow_hits,
            )
        )

    if has_step("02b-thinking"):
        rows.append(
            compare_part(
                "thinking block",
                "02b-thinking",
                omp_docs["02b-thinking"],
                pig_docs["02b-thinking"],
                summarize_part(omp_docs["02b-thinking"], omp_doms["02b-thinking"], "sf-main", find_thinking_nodes(omp_docs["02b-thinking"]), pick="last"),
                summarize_part(pig_docs["02b-thinking"], pig_doms["02b-thinking"], "sf-main", find_thinking_nodes(pig_docs["02b-thinking"]), pick="last"),
                allowlist,
                allow_hits=allow_hits,
            )
        )

    tool_specs: list[tuple[str, str, str, bool]] = [
        ("tool card: read", "03-tool-read", "read", False),
        ("tool card: bash", "04-tool-bash", "bash", False),
        ("tool card: edit", "05-tool-edit", "edit", False),
        ("tool card: failing read", "06-tool-error", "read", True),
        ("tool card: todo", "07-tool-todo", "todo", False),
        ("tool card: ask", "08b-ask-submitted", "ask", False),
        ("tool card: eval", "09-tool-eval", "eval", False),
        ("tool card: task", "10-tool-task", "task", False),
        ("tool card: wait", "10-tool-task", "wait", False),
        ("tool card: web_search", "11-tool-web-search", "web_search", False),
        ("tool card: write", "12-tool-write-search", "write", False),
        ("tool card: grep", "12-tool-write-search", "grep", False),
        ("tool card: glob", "12-tool-write-search", "glob", False),
        ("tool card: lsp", "13-tool-lsp-ast", "lsp", False),
        ("tool card: ast_edit", "13-tool-lsp-ast", "ast_edit", False),
    ]
    for label, step_name, tool_name, req_err in tool_specs:
        if not has_step(step_name):
            continue
        rows.append(
            compare_part(
                label,
                step_name,
                omp_docs[step_name],
                pig_docs[step_name],
                summarize_part(omp_docs[step_name], omp_doms[step_name], "sf-main", find_tool_nodes(omp_docs[step_name], tool_name, require_error=req_err), pick="last"),
                summarize_part(pig_docs[step_name], pig_doms[step_name], "sf-main", find_tool_nodes(pig_docs[step_name], tool_name, require_error=req_err), pick="last"),
                allowlist,
                allow_hits=allow_hits,
            )
        )

    if has_step("08a-ask-form"):
        rows.append(
            compare_part(
                "ask form (live)",
                "08a-ask-form",
                omp_docs["08a-ask-form"],
                pig_docs["08a-ask-form"],
                summarize_part(omp_docs["08a-ask-form"], omp_doms["08a-ask-form"], "sf-dock", find_live_ask_nodes(omp_docs["08a-ask-form"]), pick="first"),
                summarize_part(pig_docs["08a-ask-form"], pig_doms["08a-ask-form"], "sf-layer", find_live_ask_nodes(pig_docs["08a-ask-form"]), pick="first"),
                allowlist,
                allow_hits=allow_hits,
            )
        )

    if has_step("14a-long-folded"):
        rows.append(
            compare_part(
                "long bash (folded)",
                "14a-long-folded",
                omp_docs["14a-long-folded"],
                pig_docs["14a-long-folded"],
                summarize_part(omp_docs["14a-long-folded"], omp_doms["14a-long-folded"], "sf-main", find_tool_nodes(omp_docs["14a-long-folded"], "bash"), pick="last"),
                summarize_part(pig_docs["14a-long-folded"], pig_doms["14a-long-folded"], "sf-main", find_tool_nodes(pig_docs["14a-long-folded"], "bash"), pick="last"),
                allowlist,
                allow_hits=allow_hits,
            )
        )

    if has_step("14b-long-expanded"):
        rows.append(
            compare_part(
                "long bash (expanded)",
                "14b-long-expanded",
                omp_docs["14b-long-expanded"],
                pig_docs["14b-long-expanded"],
                summarize_part(omp_docs["14b-long-expanded"], omp_doms["14b-long-expanded"], "sf-main", find_tool_nodes(omp_docs["14b-long-expanded"], "bash"), pick="last"),
                summarize_part(pig_docs["14b-long-expanded"], pig_doms["14b-long-expanded"], "sf-main", find_tool_nodes(pig_docs["14b-long-expanded"], "bash"), pick="last"),
                allowlist,
                allow_hits=allow_hits,
            )
        )

    if has_step("15-provider-error"):
        rows.append(
            compare_part(
                "provider error turn",
                "15-provider-error",
                omp_docs["15-provider-error"],
                pig_docs["15-provider-error"],
                summarize_part(omp_docs["15-provider-error"], omp_doms["15-provider-error"], "sf-main", find_error_turn_nodes(omp_docs["15-provider-error"]), pick="last"),
                summarize_part(pig_docs["15-provider-error"], pig_doms["15-provider-error"], "sf-main", find_error_turn_nodes(pig_docs["15-provider-error"]), pick="last"),
                allowlist,
                allow_hits=allow_hits,
            )
        )

    if has_step("16-plan-mode"):
        rows.append(
            compare_part(
                "plan mode indicator",
                "16-plan-mode",
                omp_docs["16-plan-mode"],
                pig_docs["16-plan-mode"],
                summarize_part(omp_docs["16-plan-mode"], omp_doms["16-plan-mode"], "sf-dock", find_plan_indicator_nodes(omp_docs["16-plan-mode"]), pick="first"),
                summarize_part(pig_docs["16-plan-mode"], pig_doms["16-plan-mode"], "sf-dock", find_plan_indicator_nodes(pig_docs["16-plan-mode"]), pick="first"),
                allowlist,
                allow_hits=allow_hits,
            )
        )

    if has_step("17a-slash-menu"):
        rows.append(
            compare_part(
                "slash menu",
                "17a-slash-menu",
                omp_docs["17a-slash-menu"],
                pig_docs["17a-slash-menu"],
                summarize_part(omp_docs["17a-slash-menu"], omp_doms["17a-slash-menu"], "sf-layer", find_slash_menu_nodes(omp_docs["17a-slash-menu"]), pick="first"),
                summarize_part(pig_docs["17a-slash-menu"], pig_doms["17a-slash-menu"], "sf-layer", find_slash_menu_nodes(pig_docs["17a-slash-menu"]), pick="first"),
                allowlist,
                allow_hits=allow_hits,
            )
        )

    if has_step("17b-slash-closed"):
        # Compare the composer after Escape+Backspace (verifies slash menu closed & composer returned to clean state).
        rows.append(
            compare_part(
                "slash closed (composer)",
                "17b-slash-closed",
                omp_docs["17b-slash-closed"],
                pig_docs["17b-slash-closed"],
                summarize_part(omp_docs["17b-slash-closed"], omp_doms["17b-slash-closed"], "sf-dock", find_composer_nodes(omp_docs["17b-slash-closed"]), pick="first"),
                summarize_part(pig_docs["17b-slash-closed"], pig_doms["17b-slash-closed"], "sf-dock", find_composer_nodes(pig_docs["17b-slash-closed"]), pick="first"),
                allowlist,
                allow_hits=allow_hits,
            )
        )

    if has_step("18-narrow-80"):
        # Compare the composer at the 800x600 narrow viewport resize step (verifies dock/composer integrity after resize).
        rows.append(
            compare_part(
                "narrow 80 (composer)",
                "18-narrow-80",
                omp_docs["18-narrow-80"],
                pig_docs["18-narrow-80"],
                summarize_part(omp_docs["18-narrow-80"], omp_doms["18-narrow-80"], "sf-dock", find_composer_nodes(omp_docs["18-narrow-80"]), pick="first"),
                summarize_part(pig_docs["18-narrow-80"], pig_doms["18-narrow-80"], "sf-dock", find_composer_nodes(pig_docs["18-narrow-80"]), pick="first"),
                allowlist,
                allow_hits=allow_hits,
            )
        )

    if has_step("01-markdown"):
        rows.append(
            compare_part(
                "composer",
                "01-markdown",
                omp_docs["01-markdown"],
                pig_docs["01-markdown"],
                summarize_part(omp_docs["01-markdown"], omp_doms["01-markdown"], "sf-dock", find_composer_nodes(omp_docs["01-markdown"]), pick="first"),
                summarize_part(pig_docs["01-markdown"], pig_doms["01-markdown"], "sf-dock", find_composer_nodes(pig_docs["01-markdown"]), pick="first"),
                allowlist,
                allow_hits=allow_hits,
            )
        )
        omp_st = find_status_nodes(omp_docs["01-markdown"])
        pig_st = find_status_nodes(omp_docs["01-markdown"])
        if omp_st or pig_st:
            rows.append(
                compare_part(
                    "status / footer",
                    "01-markdown",
                    omp_docs["01-markdown"],
                    pig_docs["01-markdown"],
                    summarize_part(omp_docs["01-markdown"], omp_doms["01-markdown"], "sf-dock", omp_st, pick="first"),
                    summarize_part(pig_docs["01-markdown"], pig_doms["01-markdown"], "sf-dock", pig_st, pick="first"),
                    allowlist,
                    allow_hits=allow_hits,
                )
            )

    # Frame-level region child LCS alignment + matched-subtree node-count delta accounting for EVERY step.
    compared_steps: set[str] = {r["step"] for r in rows}
    compared_node_ids_by_step: dict[str, tuple[set[str], set[str]]] = {}
    part_by_node_id: dict[tuple[str, str], str] = {}
    for r in rows:
        st = r["step"]
        oset, pset = compared_node_ids_by_step.setdefault(st, (set(), set()))
        if r.get("omp") and r["omp"].get("id"):
            oid = r["omp"]["id"]
            part_by_node_id[(st, oid)] = r["part"]
            if r["part"] == "surface root":
                oset.add(oid)
            else:
                for n in omp_docs[st].walk_visible(oid):
                    oset.add(n.id)
        if r.get("pig") and r["pig"].get("id"):
            pid = r["pig"]["id"]
            part_by_node_id[(st, pid)] = r["part"]
            if r["part"] == "surface root":
                pset.add(pid)
            else:
                for n in pig_docs[st].walk_visible(pid):
                    pset.add(n.id)

    def _role_counts(doc: TspDocument) -> dict[str, int]:
        counts: dict[str, int] = {}
        for n in doc.walk_visible():
            key = n.role if n.role else f"<{n.kind}>"
            counts[key] = counts.get(key, 0) + 1
        return counts

    frame_comparison: dict[str, Any] = {}
    not_compared_steps: list[str] = []
    steps_with_region_diffs = 0
    all_omp_only_roles: set[str] = set()
    all_pig_only_roles: set[str] = set()
    total_not_compared_nodes_omp = 0
    total_not_compared_nodes_pig = 0
    total_unmatched_children = 0
    total_expected_unmatched_children = 0
    total_unexplained_unmatched_children = 0
    total_count_delta_pairs = 0
    total_expected_count_delta_pairs = 0
    total_unexplained_count_delta_pairs = 0
    unexplained_unmatched_samples: list[dict[str, Any]] = []
    unexplained_count_delta_samples: list[dict[str, Any]] = []

    for s in steps:
        od = omp_docs[s]
        pd = pig_docs[s]
        o_vis = od.walk_visible()
        p_vis = pd.walk_visible()
        o_roles = _role_counts(od)
        p_roles = _role_counts(pd)
        omp_only_roles = sorted(r for r in o_roles if r not in p_roles and not r.startswith("<"))
        pig_only_roles = sorted(r for r in p_roles if r not in o_roles and not r.startswith("<"))
        role_count_diffs = {
            r: {"omp": o_roles.get(r, 0), "pig": p_roles.get(r, 0)}
            for r in sorted(set(o_roles) | set(p_roles))
            if o_roles.get(r, 0) != p_roles.get(r, 0)
        }
        all_omp_only_roles.update(omp_only_roles)
        all_pig_only_roles.update(pig_only_roles)

        aligned = align_and_classify_step_regions(
            s, od, pd, allowlist, allow_hits=allow_hits, part_by_node_id=part_by_node_id
        )
        regions_cmp = aligned["regions"]
        any_reg_diff = any(not regions_cmp[r]["same"] for r in ("main", "dock", "layer"))
        if any_reg_diff or len(o_vis) != len(p_vis):
            steps_with_region_diffs += 1

        total_unmatched_children += len(aligned["unmatched_children"])
        total_expected_unmatched_children += aligned["expected_unmatched_count"]
        total_unexplained_unmatched_children += aligned["unexplained_unmatched_count"]
        total_count_delta_pairs += len(aligned["matched_count_deltas"])
        total_expected_count_delta_pairs += aligned["expected_count_delta_count"]
        total_unexplained_count_delta_pairs += aligned["unexplained_count_delta_count"]

        for u in aligned["unmatched_children"]:
            if u["status"] == "unexplained":
                unexplained_unmatched_samples.append(u)
        for m in aligned["matched_count_deltas"]:
            if m["status"] == "unexplained":
                unexplained_count_delta_samples.append(m)

        in_part_o, in_part_p = compared_node_ids_by_step.get(s, (set(), set()))
        uncovered_o = [n for n in o_vis if n.id not in in_part_o and n.id not in (od.surface_id, "main", "dock", "layer")]
        uncovered_p = [n for n in p_vis if n.id not in in_part_p and n.id not in (pd.surface_id, "main", "dock", "layer")]
        total_not_compared_nodes_omp += len(uncovered_o)
        total_not_compared_nodes_pig += len(uncovered_p)
        if s not in compared_steps:
            not_compared_steps.append(s)

        frame_comparison[s] = {
            "part_compared": s in compared_steps,
            "visible_nodes": {
                "omp": len(o_vis),
                "pig": len(p_vis),
                "omp_hidden_nodes": sum(1 for n in od.nodes.values() if n.hidden),
                "pig_hidden_nodes": sum(1 for n in pd.nodes.values() if n.hidden),
            },
            "regions": regions_cmp,
            "unmatched_children": aligned["unmatched_children"],
            "matched_count_deltas": aligned["matched_count_deltas"],
            "unexplained_unmatched_count": aligned["unexplained_unmatched_count"],
            "expected_unmatched_count": aligned["expected_unmatched_count"],
            "unexplained_count_delta_count": aligned["unexplained_count_delta_count"],
            "expected_count_delta_count": aligned["expected_count_delta_count"],
            "omp_only_roles": omp_only_roles,
            "pig_only_roles": pig_only_roles,
            "role_count_diffs": role_count_diffs,
            "not_compared_visible_nodes": {"omp": len(uncovered_o), "pig": len(uncovered_p)},
            "not_compared_roles": {
                "omp": sorted({n.role for n in uncovered_o if n.role}),
                "pig": sorted({n.role for n in uncovered_p if n.role}),
            },
        }

    # Compute roles that appeared on either side in any step but were NEVER inside ANY compared part.
    compared_roles_omp: set[str] = set()
    compared_roles_pig: set[str] = set()
    for st, (oset, pset) in compared_node_ids_by_step.items():
        od = omp_docs[st]
        pd = pig_docs[st]
        for nid in oset:
            n = od.nodes.get(nid)
            if n and n.role:
                compared_roles_omp.add(n.role)
        for nid in pset:
            n = pd.nodes.get(nid)
            if n and n.role:
                compared_roles_pig.add(n.role)
    all_seen_roles_omp = {n.role for s in steps for n in omp_docs[s].walk_visible() if n.role}
    all_seen_roles_pig = {n.role for s in steps for n in pig_docs[s].walk_visible() if n.role}
    never_compared_roles_omp = sorted(all_seen_roles_omp - compared_roles_omp)
    never_compared_roles_pig = sorted(all_seen_roles_pig - compared_roles_pig)

    per_step_summary = {
        s: {
            "omp_dom_regions": omp_doms[s].get("region_names", []),
            "omp_dom_surface_nodes": omp_doms[s].get("surface_node_count", 0),
            "pig_dom_regions": pig_doms[s].get("region_names", []),
            "pig_dom_surface_nodes": pig_doms[s].get("surface_node_count", 0),
            "omp_visible_tsp_nodes": frame_comparison[s]["visible_nodes"]["omp"],
            "pig_visible_tsp_nodes": frame_comparison[s]["visible_nodes"]["pig"],
            "main_children": {
                "omp": frame_comparison[s]["regions"]["main"]["omp_count"],
                "pig": frame_comparison[s]["regions"]["main"]["pig_count"],
            },
            "dock_children": {
                "omp": frame_comparison[s]["regions"]["dock"]["omp_count"],
                "pig": frame_comparison[s]["regions"]["dock"]["pig_count"],
            },
            "layer_children": {
                "omp": frame_comparison[s]["regions"]["layer"]["omp_count"],
                "pig": frame_comparison[s]["regions"]["layer"]["pig_count"],
            },
            "unexplained_unmatched": frame_comparison[s]["unexplained_unmatched_count"],
            "expected_unmatched": frame_comparison[s]["expected_unmatched_count"],
            "unexplained_count_deltas": frame_comparison[s]["unexplained_count_delta_count"],
            "expected_count_deltas": frame_comparison[s]["expected_count_delta_count"],
            "part_compared": frame_comparison[s]["part_compared"],
        }
        for s in steps
    }

    final_frame = frame_comparison.get(final_step, {})
    final_not_compared_roles = final_frame.get("not_compared_roles", {"omp": [], "pig": []})
    class_counts: dict[str, int] = {}
    for r in rows:
        for ad in r["allowlisted_diffs"]:
            c = ad["allowlist"]["class"]
            class_counts[c] = class_counts.get(c, 0) + 1
    frame_unmatched_by_class: dict[str, int] = {}
    for s in steps:
        for u in frame_comparison[s]["unmatched_children"]:
            if u["status"] == "expected":
                c = u["allowlist"]["class"]
                frame_unmatched_by_class[c] = frame_unmatched_by_class.get(c, 0) + 1

    return {
        "capture_valid": True,
        "native_proof": native_proof,
        "visible_tsp_node_definition": VISIBLE_TSP_NODE_DEFINITION,
        "normalization_policy": {
            "wire_internal_props_ignored": sorted(WIRE_INTERNAL_PROP_KEYS),
            "timing_value_props_masked_presence_and_type_checked": sorted(TIMING_VALUE_PROP_KEYS),
            "noop_defaults_counted_not_dropped": list(NOOP_DEFAULTS_DOC),
            "value_normalizations": [
                "<WORK> (/tmp workspace path) vs /private<WORK> (/private/tmp path preserved as diff)",
                "<HTTP_LOG> (omp http-400 log path)",
                "<MS> (numeric took/age/stopped values only; presence & type checked)",
            ],
        },
        "steps": per_step_summary,
        "frame_comparison": frame_comparison,
        "parts": rows,
        "allowlist_usage": [
            {
                "kind": e.get("kind", "part_diff"),
                "part": e.get("part") or f"frame:{e.get('region')}",
                "node_path": e.get("node_path") or f"{e.get('node_kind', '*')}[{e.get('role', '')}]",
                "prop": e.get("prop") or "$frame_child",
                "side": e["side"],
                "class": e["class"],
                "class_in_doubt": bool(e.get("class_in_doubt")),
                "ref": e["ref"],
                "hits": allow_hits.get(i, 0),
                "stale": allow_hits.get(i, 0) == 0,
            }
            for i, e in enumerate(allowlist)
        ],
        "summary": {
            "same": sum(1 for r in rows if r["verdict"] == "same"),
            "expected": sum(1 for r in rows if r["verdict"] == "expected"),
            "timing": sum(1 for r in rows if r["verdict"] == "timing"),
            "differs": sum(1 for r in rows if r["verdict"] == "differs"),
            "missing": sum(1 for r in rows if r["verdict"] == "missing"),
            "total": len(rows),
            "unexpected_diff_count": sum(len(r["unexpected_diffs"]) for r in rows),
            "timing_diff_count": sum(len(r.get("timing_diffs") or []) for r in rows),
            "allowlisted_diff_count": sum(len(r["allowlisted_diffs"]) for r in rows),
            "allowlisted_by_class": class_counts,
            "class_in_doubt_diff_count": sum(len(r.get("class_in_doubt_diffs") or []) for r in rows),
            "noop_default_diff_count": sum(len(r.get("noop_default_diffs") or []) for r in rows),
            "steps_total": len(steps),
            "steps_with_region_diffs": steps_with_region_diffs,
            "not_compared_steps": not_compared_steps,
            "not_compared_step_count": len(not_compared_steps),
            "frame_unmatched_children_total": total_unmatched_children,
            "frame_unmatched_children_expected": total_expected_unmatched_children,
            "frame_unmatched_children_expected_by_class": frame_unmatched_by_class,
            "frame_unmatched_children_unexplained": total_unexplained_unmatched_children,
            "frame_count_delta_pairs_total": total_count_delta_pairs,
            "frame_count_delta_pairs_expected": total_expected_count_delta_pairs,
            "frame_count_delta_pairs_unexplained": total_unexplained_count_delta_pairs,
            "final_step_unexplained_unmatched": final_frame.get("unexplained_unmatched_count", 0),
            "final_step_unexplained_count_deltas": final_frame.get("unexplained_count_delta_count", 0),
            "pig_stylesheet_error_count": len(pig_sheet_errors),
            "omp_stylesheet_error_count": len(omp_sheet_errors),
            "total_unexplained_all": (
                sum(len(r["unexpected_diffs"]) for r in rows)
                + total_unexplained_unmatched_children
                + total_unexplained_count_delta_pairs
                + len(pig_sheet_errors)
            ),
            "never_compared_roles": {
                "omp": never_compared_roles_omp,
                "pig": never_compared_roles_pig,
            },
            "never_compared_role_count": {
                "omp": len(never_compared_roles_omp),
                "pig": len(never_compared_roles_pig),
            },
            "omp_only_roles_across_steps": sorted(all_omp_only_roles),
            "pig_only_roles_across_steps": sorted(all_pig_only_roles),
            "not_compared_roles_at_final_step": final_not_compared_roles,
            "not_compared_role_count_at_final_step": {
                "omp": len(final_not_compared_roles.get("omp", [])),
                "pig": len(final_not_compared_roles.get("pig", [])),
            },
            "not_compared_visible_nodes_total": {
                "omp": total_not_compared_nodes_omp,
                "pig": total_not_compared_nodes_pig,
            },
        },
    }


def render_markdown(report: dict[str, Any]) -> str:
    def esc(val: str) -> str:
        return val.replace("|", "\\|").replace("\n", " ")

    np = report["native_proof"]
    sm = report["summary"]
    lines: list[str] = [
        "# oh-my-pig vs omp — Headless Tern Drawing Parity Report",
        "",
        "## Native Rendering Proof",
        "",
        f"- **omp**: surface `{np['omp']['surface_id']}` (role `{np['omp']['surface_role']}`), "
        f"Tern DOM regions: `{', '.join(np['omp']['dom_regions'])}` "
        f"({np['omp']['dom_surface_nodes']} surface nodes at final step `{np['final_step']}`)",
        f"- **oh-my-pig**: surface `{np['pig']['surface_id']}` (role `{np['pig']['surface_role']}`), "
        f"Tern DOM regions: `{', '.join(np['pig']['dom_regions'])}` "
        f"({np['pig']['dom_surface_nodes']} surface nodes at final step `{np['final_step']}`)",
        f"- **Visible TSP node definition**: {report.get('visible_tsp_node_definition', VISIBLE_TSP_NODE_DEFINITION)}",
        "",
        f"Parts summary: **{sm['same']} same**, **{sm['expected']} expected**, **{sm.get('timing', 0)} timing**, "
        f"**{sm['differs']} differs**, **{sm['missing']} missing** "
        f"(out of {sm['total']} compared parts; {sm['unexpected_diff_count']} unexpected sub-diffs, "
        f"{sm.get('timing_diff_count', 0)} timing sub-diffs, {sm['allowlisted_diff_count']} allowlisted sub-diffs "
        f"[{sm.get('class_in_doubt_diff_count', 0)} class_in_doubt], {sm.get('noop_default_diff_count', 0)} noop-default sub-diffs).",
        f"Frame alignment summary (all {sm.get('steps_total', 0)} steps): "
        f"**{sm.get('frame_unmatched_children_unexplained', 0)} unexplained** / "
        f"{sm.get('frame_unmatched_children_expected', 0)} expected unmatched region children; "
        f"**{sm.get('frame_count_delta_pairs_unexplained', 0)} unexplained** / "
        f"{sm.get('frame_count_delta_pairs_expected', 0)} expected matched-subtree node-count deltas; "
        f"**{sm.get('not_compared_step_count', 0)} step(s) not compared by any part** "
        f"({', '.join(sm.get('not_compared_steps', [])) or 'none'}).",
        "",
        "## Transcript & Chrome Comparison",
        "",
        "| Part | Step | Verdict | Unexpected | Expected | Noop | Difference |",
        "|---|---|---|---:|---:|---:|---|",
    ]
    for r in report["parts"]:
        lines.append(
            f"| {esc(r['part'])} | `{r['step']}` | **{esc(r['verdict'])}** | "
            f"{len(r['unexpected_diffs'])} | {len(r['allowlisted_diffs'])} | "
            f"{len(r.get('noop_default_diffs') or [])} | {esc(r['details'])} |"
        )

    lines.extend(
        [
            "",
            "## Per-Step Frame & Region Alignment",
            "",
            "| Step | Part compared? | Visible TSP (omp / pig) | main (omp / pig) | dock (omp / pig) | layer (omp / pig) | Unmatched (unexp / exp) | Count deltas (unexp / exp) |",
            "|---|---|---|---|---|---|---|---|",
        ]
    )
    fc = report.get("frame_comparison") or {}
    for s, st in report["steps"].items():
        lines.append(
            f"| `{s}` | {'yes' if st.get('part_compared') else 'not compared'} | "
            f"{st.get('omp_visible_tsp_nodes', 0)} / {st.get('pig_visible_tsp_nodes', 0)} | "
            f"{st['main_children']['omp']} / {st['main_children']['pig']} | "
            f"{st['dock_children']['omp']} / {st['dock_children']['pig']} | "
            f"{st['layer_children']['omp']} / {st['layer_children']['pig']} | "
            f"{st.get('unexplained_unmatched', 0)} / {st.get('expected_unmatched', 0)} | "
            f"{st.get('unexplained_count_deltas', 0)} / {st.get('expected_count_deltas', 0)} |"
        )

    # Print the final step's detailed unmatched children and matched-subtree count deltas so a reader
    # can see EVERY single extra child and node-count delta without opening report.json.
    final_step = np["final_step"]
    ff = fc.get(final_step) or {}
    lines.extend(
        [
            "",
            f"## Final Step (`{final_step}`) Unmatched Region Children",
            "",
            "| Region | Side | Index | Kind | Role | Vis nodes | Status | Text (≤80 chars) |",
            "|---|---|---:|---|---|---:|---|---|",
        ]
    )
    for u in ff.get("unmatched_children", []):
        st_str = u["status"]
        if u["status"] == "expected":
            st_str = f"expected [{u['allowlist']['class']} {u['allowlist']['ref']}]"
        lines.append(
            f"| `{u['region']}` | `{u['side']}` | {u['index']} | `{u['kind']}` | `{u['role'] or '—'}` | "
            f"{u['visible_node_count']} | {esc(st_str)} | {esc(u['text']) or '—'} |"
        )

    lines.extend(
        [
            "",
            f"## Final Step (`{final_step}`) Matched-Subtree Visible-Node Count Deltas",
            "",
            "| Region | Pair (omp→pig) | Kind[Role] | Vis (omp / pig, Δ) | Status | Explanation |",
            "|---|---|---|---|---|---|",
        ]
    )
    for m in ff.get("matched_count_deltas", []):
        expl_parts: list[str] = []
        if m.get("wrapper_note"):
            expl_parts.append(m["wrapper_note"])
        for ad in m.get("allowlisted_node_diffs", []):
            expl_parts.append(f"[{ad['class']}] {ad['node_path']} ({ad['side']})")
        for ur in m.get("unexplained_reasons", []):
            expl_parts.append(f"UNEXPLAINED: {ur}")
        lines.append(
            f"| `{m['region']}` | `{m['omp_index']}→{m['pig_index']}` | "
            f"`{m['kind']}[{m['role'] or ''}]` | {m['omp_visible_nodes']} / {m['pig_visible_nodes']} ({m['delta']:+d}) | "
            f"**{m['status']}** | {esc('; '.join(expl_parts)) or '—'} |"
        )
    lines.append("")
    return "\n".join(lines)


CAPTURE_INCOMPLETE_EXIT_CODE = 2


def validate_tern_capture_dir(side: str, out_dir: str) -> dict[str, Any]:
    """Validate a Tern capture directory before diffing (checks capture-meta.json if present and required step files)."""
    errors: list[str] = []
    meta_path = os.path.join(out_dir, "capture-meta.json")
    completed_turns: int | None = None
    in_flight: int | None = None
    expected_turns = 33
    if os.path.isfile(meta_path):
        try:
            with open(meta_path, encoding="utf-8") as _mfh:
                meta = json.load(_mfh)
            completed_turns = meta.get("completed_turns")
            in_flight = meta.get("in_flight")
            expected_turns = int(meta.get("expected_turns", 33))
            if completed_turns is not None and int(completed_turns) < expected_turns:
                errors.append(
                    f"turn {expected_turns} not consumed (completed={completed_turns}, expected>={expected_turns})"
                )
            if in_flight is not None and int(in_flight) != 0:
                errors.append(f"in_flight={in_flight} at capture end")
        except Exception as exc:
            errors.append(f"unreadable capture-meta.json: {exc}")
    tsp_path = os.path.join(out_dir, "tsp.jsonl")
    if not os.path.isfile(tsp_path) or os.path.getsize(tsp_path) == 0:
        errors.append("missing or empty tsp.jsonl")
    missing_steps = [
        s for s in DEFAULT_STEPS if not os.path.isfile(os.path.join(out_dir, f"tree-{s}.json"))
    ]
    if missing_steps:
        errors.append(f"missing step snapshot(s): {missing_steps!r}")
    valid = len(errors) == 0
    return {
        "side": side,
        "capture_valid": valid,
        "completed_turns": completed_turns,
        "in_flight": in_flight,
        "expected_turns": expected_turns,
        "errors": errors,
        "message": "ok" if valid else f"capture incomplete on {side}: " + "; ".join(errors),
    }


def main() -> int:
    parser = argparse.ArgumentParser(description="Compare omp and oh-my-pig headless Tern captures")
    parser.add_argument("--omp-dir", required=True)
    parser.add_argument("--pig-dir", required=True)
    parser.add_argument("--md-out", required=True)
    parser.add_argument("--json-out", required=True)
    parser.add_argument("--allowlist", default=str(DEFAULT_DRAWING_ALLOWLIST_PATH))
    parser.add_argument("--allow-diffs", action="store_true", help="Exit 0 even when unexplained differences exist")
    args = parser.parse_args()

    val_omp = validate_tern_capture_dir("omp", args.omp_dir)
    val_pig = validate_tern_capture_dir("pig", args.pig_dir)
    if not val_omp["capture_valid"] or not val_pig["capture_valid"]:
        invalid = [v for v in (val_omp, val_pig) if not v["capture_valid"]]
        msg = " | ".join(v["message"] for v in invalid)
        inv_report = {
            "capture_valid": False,
            "verdict": None,
            "error": msg,
            "capture_validation": {"omp": val_omp, "pig": val_pig},
        }
        with open(args.json_out, "w", encoding="utf-8") as fh:
            json.dump(inv_report, fh, indent=2, ensure_ascii=False)
            fh.write("\n")
        with open(args.md_out, "w", encoding="utf-8") as fh:
            fh.write("# Capture Incomplete\n\n")
            fh.write(f"`capture_valid: false` — no diff verdict produced.\n\n- {msg}\n")
        print(msg, file=sys.stderr)
        return CAPTURE_INCOMPLETE_EXIT_CODE

    report = compare_directories(args.omp_dir, args.pig_dir, Path(args.allowlist))
    report["capture_validation"] = {"omp": val_omp, "pig": val_pig}
    with open(args.json_out, "w", encoding="utf-8") as fh:
        json.dump(report, fh, indent=2, ensure_ascii=False)
        fh.write("\n")
    with open(args.md_out, "w", encoding="utf-8") as fh:
        fh.write(render_markdown(report))
    sm = report["summary"]
    has_unexplained = (
        (sm.get("differs", 0) > 0)
        or (sm.get("missing", 0) > 0)
        or (sm.get("unexpected_diff_count", 0) > 0)
        or (sm.get("frame_unmatched_children_unexplained", 0) > 0)
        or (sm.get("frame_count_delta_pairs_unexplained", 0) > 0)
        or (sm.get("pig_stylesheet_error_count", 0) > 0)
    )
    if has_unexplained and not args.allow_diffs:
        return 1
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
