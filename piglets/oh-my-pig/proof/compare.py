#!/usr/bin/env python3
"""Compare headless Tern DOM trees and TSP wire frames between omp and oh-my-pig.

Honesty rule:
  A transcript/chrome part is marked `same` ONLY when both programs rendered it
  natively, its data-role matches, its TSP node kind & Tern CSS kind class match,
  and its normalized child nesting structure matches. Any difference in role,
  kind, or nesting is reported as `differs` with the concrete delta; absence on
  either side is reported as `missing on <side>`.

Inputs:
  --omp-dir  Directory containing tree-<step>.json, tsp.jsonl, tsp-lines-<step>.txt
  --pig-dir  Same for oh-my-pig
  --md-out   Output Markdown report path
  --json-out Output JSON report path
"""

from __future__ import annotations

import argparse
import json
import os
import re
from dataclasses import dataclass, field
from typing import Any


# Tern DOM classes that correspond directly to TSP node kinds (sf-<kind>).
# Structural internal DOM wrappers (sf-card-body, sf-ed-scroll, sf-mt-track,
# sf-st-row, sf-t-*, sf-act, sf-deco, sf-term-clip, etc.) are filtered out when
# summarizing surface nodes so we compare the TSP-level tree Tern actually built.
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
                # Text/markdown append op: ["txt", id, prop, delta]
                nid_s, prop_name, delta = str(op[1]), str(op[2]), str(op[3])
                node = self.nodes.get(nid_s)
                if node is not None:
                    cur = node.props.get(prop_name)
                    node.props[prop_name] = (cur if isinstance(cur, str) else "") + delta

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
        """Collect human-readable text strings stored inside a TSP subtree."""
        n = self.nodes.get(nid)
        if n is None or n.hidden:
            return ""
        parts: list[str] = []
        p = n.props
        for key in ("text", "md", "title", "name", "cmd", "path", "label", "summary", "sub"):
            val = p.get(key)
            if isinstance(val, str) and val.strip():
                parts.append(val.strip())
        spans = p.get("spans")
        if isinstance(spans, list):
            st = "".join(s.get("t", "") for s in spans if isinstance(s, dict) and isinstance(s.get("t"), str))
            if st.strip():
                parts.append(st.strip())
        head = p.get("head")
        if isinstance(head, str) and head.strip():
            parts.append(head.strip())
        elif isinstance(head, list):
            ht = "".join(s.get("t", "") for s in head if isinstance(s, dict) and isinstance(s.get("t"), str))
            if ht.strip():
                parts.append(ht.strip())
        for cid in n.children:
            sub = self.visible_text(cid)
            if sub:
                parts.append(sub)
        return " ".join(parts)

    def subtree_signature(self, nid: str, max_depth: int = 3) -> dict[str, Any]:
        """Extract kind, role, and visible child nesting for comparison."""
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
    """Replay TSP JSONL up to the line count recorded when `step` was captured."""
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
    """Extract Tern surface regions (`sf-main`, `sf-dock`, `sf-layer`) and their
    TSP-kind DOM nodes (`class` containing `sf` + `sf-<kind>`) from `tern ctl tree`."""
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
    """Compact one-line representation of kind[role](child1, child2, ...)."""
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
    """Find first DOM surface node of the given kind in a region."""
    for n in dom_nodes:
        if n["kind"] == kind:
            return n
        sub = first_dom_match(n["children"], kind)
        if sub is not None:
            return sub
    return None


def find_all_dom_kind(dom_nodes: list[dict[str, Any]], kind: str) -> list[dict[str, Any]]:
    found: list[dict[str, Any]] = []
    for n in dom_nodes:
        if n["kind"] == kind:
            found.append(n)
        found.extend(find_all_dom_kind(n["children"], kind))
    return found


def find_tsp_by_role(doc: TspDocument, role: str, region: str | None = None) -> list[TspNode]:
    start = region if (region and region in doc.nodes) else doc.surface_id
    return [n for n in doc.walk_visible(start) if n.role == role]


def find_tsp_by_predicate(doc: TspDocument, pred: Any, region: str | None = None) -> list[TspNode]:
    start = region if (region and region in doc.nodes) else doc.surface_id
    return [n for n in doc.walk_visible(start) if pred(n)]


def find_tool_nodes(doc: TspDocument, tool_name: str, require_error: bool = False) -> list[TspNode]:
    """Locate visible tool card/section nodes in `main` for a given tool name.

    Works for both omp (`role == "omp.tool.<name>"` or `kind == "tool"` / `section`
    / `card` whose text/props reference the tool) and oh-my-pig (`kind == "tool"`
    or `card`/`section` inside `main` matching the tool name).
    """
    main_nodes = doc.walk_visible("main")
    matches: list[TspNode] = []

    def is_error_node(n: TspNode) -> bool:
        p = n.props
        if p.get("status") in ("error", "err", "failed") or p.get("tone") == "error":
            return True
        txt = doc.visible_text(n.id).lower()
        return any(tok in txt for tok in ("enoent", "no such file", "not found", "does-not-exist", "error"))

    # Pass 1: explicit role match (`omp.tool.<name>`) or `kind == "tool"` matching name
    for n in main_nodes:
        if n.id == "main":
            continue
        p = n.props
        role_match = n.role == f"omp.tool.{tool_name}"
        kind_tool_match = n.kind == "tool" and (
            str(p.get("name") or "").lower() == tool_name
            or str(p.get("tool") or "").lower() == tool_name
            or tool_name in doc.visible_text(n.id).lower()
        )
        if role_match or kind_tool_match:
            if require_error and not is_error_node(n):
                continue
            matches.append(n)

    if matches:
        return matches

    # Pass 2: top-level main children (excluding user/assistant/welcome/spacer)
    main = doc.nodes.get("main")
    if main is None:
        return []
    for cid in main.children:
        n = doc.nodes.get(cid)
        if n is None or n.hidden:
            continue
        if n.role in ("omp.user", "omp.assistant", "omp.welcome", "omp.thinking", "omp.thinking.live", "omp.toast.status"):
            continue
        if n.kind in ("spacer", "ansi", "text"):
            continue
        txt = doc.visible_text(n.id).lower()
        head = str(n.props.get("head") or "").lower()
        if tool_name in txt or tool_name in head:
            if require_error and not is_error_node(n):
                continue
            matches.append(n)
    return matches


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
    for r in ("omp.status", "omp.footer", "omp.composer.bar"):
        found = find_tsp_by_role(doc, r, region="dock")
        if found:
            return found
    return find_tsp_by_predicate(doc, lambda n: n.kind == "status", region="dock")


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


def compare_part(
    part_name: str,
    step: str,
    omp_info: dict[str, Any] | None,
    pig_info: dict[str, Any] | None,
) -> dict[str, Any]:
    if omp_info is None and pig_info is None:
        return {
            "part": part_name,
            "step": step,
            "verdict": "missing on both",
            "outer_role_and_kind_equal": False,
            "details": "not rendered on either side",
            "omp": None,
            "pig": None,
        }
    if omp_info is None:
        return {
            "part": part_name,
            "step": step,
            "verdict": "missing on omp",
            "outer_role_and_kind_equal": False,
            "details": f"oh-my-pig rendered `{pig_info['nesting_str']}`; absent on omp",
            "omp": None,
            "pig": pig_info,
        }
    if pig_info is None:
        return {
            "part": part_name,
            "step": step,
            "verdict": "missing on oh-my-pig",
            "outer_role_and_kind_equal": False,
            "details": f"omp rendered `{omp_info['nesting_str']}`; absent on oh-my-pig",
            "omp": omp_info,
            "pig": None,
        }

    diffs: list[str] = []
    role_eq = omp_info["role"] == pig_info["role"]
    kind_eq = omp_info["kind"] == pig_info["kind"]
    tone_eq = omp_info["tone"] == pig_info["tone"]
    status_eq = omp_info["status"] == pig_info["status"]
    nesting_eq = omp_info["nesting_str"] == pig_info["nesting_str"]

    if not role_eq:
        diffs.append(f"role: omp={omp_info['role'] or '<none>'} vs pig={pig_info['role'] or '<none>'}")
    if not kind_eq:
        diffs.append(f"kind: omp={omp_info['css_kind']} vs pig={pig_info['css_kind']}")
    if not tone_eq:
        diffs.append(f"tone: omp={omp_info['tone'] or '<none>'} vs pig={pig_info['tone'] or '<none>'}")
    if not status_eq:
        diffs.append(f"status: omp={omp_info['status'] or '<none>'} vs pig={pig_info['status'] or '<none>'}")
    if not nesting_eq:
        diffs.append("nesting differs")
    if not omp_info["dom_verified"]:
        diffs.append("omp kind not verified in Tern DOM tree")
    if not pig_info["dom_verified"]:
        diffs.append("oh-my-pig kind not verified in Tern DOM tree")

    if not diffs:
        verdict = "same"
        details = f"equal role, kind (`{omp_info['css_kind']}`), and nesting"
    else:
        verdict = "differs"
        if role_eq and kind_eq and tone_eq and status_eq and omp_info["dom_verified"] and pig_info["dom_verified"]:
            details = f"same outer role & kind (`{omp_info['css_kind']}[{omp_info['role']}]`), child nesting differs"
        else:
            details = "; ".join(diffs)

    return {
        "part": part_name,
        "step": step,
        "verdict": verdict,
        "outer_role_and_kind_equal": bool(role_eq and kind_eq),
        "details": details,
        "omp": omp_info,
        "pig": pig_info,
    }

def main() -> int:
    parser = argparse.ArgumentParser(description="Compare omp and oh-my-pig headless Tern captures")
    parser.add_argument("--omp-dir", required=True)
    parser.add_argument("--pig-dir", required=True)
    parser.add_argument("--md-out", required=True)
    parser.add_argument("--json-out", required=True)
    args = parser.parse_args()

    steps = [
        "00-started",
        "01-markdown",
        "02a-working",
        "02b-thinking",
        "03-tool-read",
        "04-tool-bash",
        "05-tool-edit",
        "06-tool-error",
    ]

    omp_docs = {s: load_tsp_at_step(args.omp_dir, s) for s in steps}
    pig_docs = {s: load_tsp_at_step(args.pig_dir, s) for s in steps}
    omp_doms = {s: extract_dom_regions(os.path.join(args.omp_dir, f"tree-{s}.json")) for s in steps}
    pig_doms = {s: extract_dom_regions(os.path.join(args.pig_dir, f"tree-{s}.json")) for s in steps}

    # Proof that both programs drew natively in headless Tern.
    native_proof = {
        "omp": {
            "surface_id": omp_docs["06-tool-error"].surface_id,
            "surface_role": omp_docs["06-tool-error"].surface_role,
            "dom_regions": omp_doms["06-tool-error"].get("region_names", []),
            "dom_surface_nodes": omp_doms["06-tool-error"].get("surface_node_count", 0),
        },
        "pig": {
            "surface_id": pig_docs["06-tool-error"].surface_id,
            "surface_role": pig_docs["06-tool-error"].surface_role,
            "dom_regions": pig_doms["06-tool-error"].get("region_names", []),
            "dom_surface_nodes": pig_doms["06-tool-error"].get("surface_node_count", 0),
        },
    }

    rows: list[dict[str, Any]] = []

    # 1. Surface root
    rows.append(
        compare_part(
            "surface root",
            "00-started",
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
        )
    )

    # 2. User message
    rows.append(
        compare_part(
            "user message",
            "01-markdown",
            summarize_part(omp_docs["01-markdown"], omp_doms["01-markdown"], "sf-main", find_tsp_by_role(omp_docs["01-markdown"], "omp.user", "main"), pick="first"),
            summarize_part(pig_docs["01-markdown"], pig_doms["01-markdown"], "sf-main", find_tsp_by_role(pig_docs["01-markdown"], "omp.user", "main"), pick="first"),
        )
    )

    # 3. Assistant Markdown text
    rows.append(
        compare_part(
            "assistant text (markdown)",
            "01-markdown",
            summarize_part(omp_docs["01-markdown"], omp_doms["01-markdown"], "sf-main", find_tsp_by_role(omp_docs["01-markdown"], "omp.assistant", "main"), pick="first"),
            summarize_part(pig_docs["01-markdown"], pig_doms["01-markdown"], "sf-main", find_tsp_by_role(pig_docs["01-markdown"], "omp.assistant", "main"), pick="first"),
        )
    )

    # 4. Working row (captured mid-turn during delayed turn 2)
    rows.append(
        compare_part(
            "working row",
            "02a-working",
            summarize_part(omp_docs["02a-working"], omp_doms["02a-working"], "sf-dock", find_working_nodes(omp_docs["02a-working"]), pick="first"),
            summarize_part(pig_docs["02a-working"], pig_doms["02a-working"], "sf-main", find_working_nodes(pig_docs["02a-working"]), pick="first"),
        )
    )

    # 5. Thinking block
    rows.append(
        compare_part(
            "thinking block",
            "02b-thinking",
            summarize_part(omp_docs["02b-thinking"], omp_doms["02b-thinking"], "sf-main", find_thinking_nodes(omp_docs["02b-thinking"]), pick="last"),
            summarize_part(pig_docs["02b-thinking"], pig_doms["02b-thinking"], "sf-main", find_thinking_nodes(pig_docs["02b-thinking"]), pick="last"),
        )
    )

    # 6. Tool card: read
    rows.append(
        compare_part(
            "tool card: read",
            "03-tool-read",
            summarize_part(omp_docs["03-tool-read"], omp_doms["03-tool-read"], "sf-main", find_tool_nodes(omp_docs["03-tool-read"], "read"), pick="last"),
            summarize_part(pig_docs["03-tool-read"], pig_doms["03-tool-read"], "sf-main", find_tool_nodes(pig_docs["03-tool-read"], "read"), pick="last"),
        )
    )

    # 7. Tool card: bash
    rows.append(
        compare_part(
            "tool card: bash",
            "04-tool-bash",
            summarize_part(omp_docs["04-tool-bash"], omp_doms["04-tool-bash"], "sf-main", find_tool_nodes(omp_docs["04-tool-bash"], "bash"), pick="last"),
            summarize_part(pig_docs["04-tool-bash"], pig_doms["04-tool-bash"], "sf-main", find_tool_nodes(pig_docs["04-tool-bash"], "bash"), pick="last"),
        )
    )

    # 8. Tool card: edit
    rows.append(
        compare_part(
            "tool card: edit",
            "05-tool-edit",
            summarize_part(omp_docs["05-tool-edit"], omp_doms["05-tool-edit"], "sf-main", find_tool_nodes(omp_docs["05-tool-edit"], "edit"), pick="last"),
            summarize_part(pig_docs["05-tool-edit"], pig_doms["05-tool-edit"], "sf-main", find_tool_nodes(pig_docs["05-tool-edit"], "edit"), pick="last"),
        )
    )

    # 9. Tool card: failing tool call (read missing file)
    rows.append(
        compare_part(
            "tool card: failing read",
            "06-tool-error",
            summarize_part(omp_docs["06-tool-error"], omp_doms["06-tool-error"], "sf-main", find_tool_nodes(omp_docs["06-tool-error"], "read", require_error=True), pick="last"),
            summarize_part(pig_docs["06-tool-error"], pig_doms["06-tool-error"], "sf-main", find_tool_nodes(pig_docs["06-tool-error"], "read", require_error=True), pick="last"),
        )
    )

    # 10. Composer
    rows.append(
        compare_part(
            "composer",
            "01-markdown",
            summarize_part(omp_docs["01-markdown"], omp_doms["01-markdown"], "sf-dock", find_composer_nodes(omp_docs["01-markdown"]), pick="first"),
            summarize_part(pig_docs["01-markdown"], pig_doms["01-markdown"], "sf-dock", find_composer_nodes(pig_docs["01-markdown"]), pick="first"),
        )
    )

    # 11. Status / footer
    rows.append(
        compare_part(
            "status / footer",
            "01-markdown",
            summarize_part(omp_docs["01-markdown"], omp_doms["01-markdown"], "sf-dock", find_status_nodes(omp_docs["01-markdown"]), pick="first"),
            summarize_part(pig_docs["01-markdown"], pig_doms["01-markdown"], "sf-dock", find_status_nodes(pig_docs["01-markdown"]), pick="first"),
        )
    )

    # Build JSON report
    per_step_summary = {}
    for s in steps:
        per_step_summary[s] = {
            "omp_dom_regions": omp_doms[s].get("region_names", []),
            "omp_dom_surface_nodes": omp_doms[s].get("surface_node_count", 0),
            "pig_dom_regions": pig_doms[s].get("region_names", []),
            "pig_dom_surface_nodes": pig_doms[s].get("surface_node_count", 0),
        }

    report = {
        "native_proof": native_proof,
        "steps": per_step_summary,
        "parts": rows,
        "summary": {
            "same": sum(1 for r in rows if r["verdict"] == "same"),
            "differs": sum(1 for r in rows if r["verdict"] == "differs"),
            "missing": sum(1 for r in rows if r["verdict"].startswith("missing")),
            "total": len(rows),
        },
    }

    with open(args.json_out, "w", encoding="utf-8") as fh:
        json.dump(report, fh, indent=2, ensure_ascii=False)
        fh.write("\n")

    # Build Markdown report
    def esc(val: str) -> str:
        return val.replace("|", "\\|").replace("\n", " ")

    lines: list[str] = [
        "# oh-my-pig vs omp — Headless Tern Drawing Parity Report",
        "",
        "## Native Rendering Proof",
        "",
        f"- **omp**: surface `{native_proof['omp']['surface_id']}` (role `{native_proof['omp']['surface_role']}`), "
        f"Tern DOM regions: `{', '.join(native_proof['omp']['dom_regions'])}` "
        f"({native_proof['omp']['dom_surface_nodes']} surface nodes at final step)",
        f"- **oh-my-pig**: surface `{native_proof['pig']['surface_id']}` (role `{native_proof['pig']['surface_role']}`), "
        f"Tern DOM regions: `{', '.join(native_proof['pig']['dom_regions'])}` "
        f"({native_proof['pig']['dom_surface_nodes']} surface nodes at final step)",
        "",
        f"Summary: **{report['summary']['same']} same**, **{report['summary']['differs']} differs**, "
        f"**{report['summary']['missing']} missing** (out of {report['summary']['total']} compared parts).",
        "",
        "## Transcript & Chrome Comparison",
        "",
        "| Part | Step | Verdict | omp (kind / role / nesting) | oh-my-pig (kind / role / nesting) | Difference |",
        "|---|---|---|---|---|---|",
    ]

    for r in rows:
        omp_cell = f"`{r['omp']['nesting_str']}`" if r["omp"] else "_missing_"
        pig_cell = f"`{r['pig']['nesting_str']}`" if r["pig"] else "_missing_"
        lines.append(
            f"| {esc(r['part'])} | `{r['step']}` | **{esc(r['verdict'])}** | "
            f"{esc(omp_cell)} | {esc(pig_cell)} | {esc(r['details'])} |"
        )

    lines.extend(
        [
            "",
            "## Per-Step DOM Surface Node Counts",
            "",
            "| Step | omp regions | omp surface nodes | oh-my-pig regions | oh-my-pig surface nodes |",
            "|---|---|---|---|---|",
        ]
    )
    for s in steps:
        st = per_step_summary[s]
        lines.append(
            f"| `{s}` | `{', '.join(st['omp_dom_regions'])}` | {st['omp_dom_surface_nodes']} | "
            f"`{', '.join(st['pig_dom_regions'])}` | {st['pig_dom_surface_nodes']} |"
        )
    lines.append("")

    with open(args.md_out, "w", encoding="utf-8") as fh:
        fh.write("\n".join(lines))

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
