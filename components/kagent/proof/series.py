#!/usr/bin/env python3
"""Per-run records and summaries for reject.sh and restart.sh.

  series.py reject-check --basket FILE --mock-log FILE --reason TEXT [--expect-sha SHA] --run N
      one JSON line: the basket result of one rejected-approval session joined with what the mock's request log saw
  series.py summary --kind reject|restart --in FILE.jsonl --out-json F --out-md F
      the summary; first-attempt success is reported separately from eventual success

Standard library only.
"""
import argparse, hashlib, json, re, sys

DONE_RE = re.compile(r"APPROVE-DONE ([0-9a-f]{64})")


def q(vals, p):
    s = sorted(vals)
    if not s:
        return None
    pos = p * (len(s) - 1)
    lo = int(pos)
    return s[-1] if lo + 1 >= len(s) else s[lo] + (pos - lo) * (s[lo + 1] - s[lo])


def mock_entries(lines):
    out = []
    for ln in lines:
        ln = ln.strip()
        if not ln.startswith("{"):
            continue
        try:
            out.append(json.loads(ln))
        except ValueError:
            pass
    return out


def reject_record(run, basket_doc, mock_lines, reason, expect_sha=None):
    """Join one rejected-approval basket run with the mock's log lines of the same time window.

    Checks: the session reached the final answer after the rejection; the mock saw the reason text in the tool result it was
    asked to answer; the answer's sha is the sha of exactly that logged text (so it is the rejected path's sha, not the
    approved path's, whose tool result is the probe output); optionally it equals an expected sha."""
    r = (basket_doc.get("runs") or [{}])[0]
    turns = r.get("turns") or []
    retries = sum(t.get("send_retries", 0) for t in turns)
    text = "\n".join(t.get("text", "") for t in turns[-1:])
    m = DONE_RE.search(text)
    answer_sha = m.group(1) if m else None
    seen = [e for e in mock_entries(mock_lines) if e.get("results")]
    final = [e for e in seen if str(e.get("text", "")).startswith("APPROVE-DONE")]
    reason_visible = any(reason in e["results"] for e in seen)
    logged_sha, sha_ok = None, False
    for e in final:
        mm = DONE_RE.search(e["text"])
        if mm and mm.group(1) == answer_sha:
            logged_sha = mm.group(1)
            sha_ok = (not e.get("results_truncated")) and hashlib.sha256(e["results"].encode()).hexdigest() == answer_sha
            break
    tm = r.get("timings_ms", {})
    first = "success" if (r.get("ok") and retries == 0) else ("retried" if r.get("ok") else "failed")
    rec = {
        "run": run, "ok": bool(r.get("ok")), "approval": r.get("approval"), "error": r.get("error"),
        "first_attempt": first, "send_retries": retries,
        "answer_sha": answer_sha, "mock_saw_answer_sha": logged_sha is not None,
        "reason_visible_in_mock_log": reason_visible, "sha_is_sha_of_logged_results": sha_ok,
        "timing_ms": {k: round(tm[k]) for k in ("turn1_input_required", "approval_to_done", "turn1_end_to_end") if k in tm},
    }
    if expect_sha:
        rec["sha_equals_expected"] = answer_sha == expect_sha
    rec["pass"] = bool(rec["ok"] and rec["approval"] == "rejected" and answer_sha and reason_visible and sha_ok
                       and rec.get("sha_equals_expected", True))
    return rec


def stat(vals, nd=0):
    v = [x for x in vals if x is not None]
    if not v:
        return None
    return {"n": len(v), "median": round(q(v, .5), nd), "p90": round(q(v, .9), nd), "max": round(max(v), nd)}


def summarize_reject(recs):
    n = len(recs)
    return {"kind": "reject", "runs": n, "pass": sum(1 for r in recs if r["pass"]),
            "first_attempt_success": sum(1 for r in recs if r["first_attempt"] == "success"),
            "reason_visible": sum(1 for r in recs if r["reason_visible_in_mock_log"]),
            "sha_is_sha_of_logged_results": sum(1 for r in recs if r["sha_is_sha_of_logged_results"]),
            "distinct_answer_shas": sorted({r["answer_sha"] for r in recs if r["answer_sha"]}),
            "approval_to_done_ms": stat([r["timing_ms"].get("approval_to_done") for r in recs]),
            "failures": [{"run": r["run"], "error": r["error"], "first_attempt": r["first_attempt"]} for r in recs if not r["pass"]]}


def outcome_key(a):
    k = a["outcome"]
    if a.get("rpc_code") is not None:
        k += f" {a['rpc_code']}"
    elif a.get("http_status"):
        k += f" {a['http_status']}"
    return k


def summarize_restart(recs):
    out = {"kind": "restart", "variants": {}}
    for v in sorted({r["variant"] for r in recs}):
        rs = [r for r in recs if r["variant"] == v]
        ser = [r["series"] for r in rs]
        n = len(rs)
        first_ok = sum(1 for s in ser if s.get("first_attempt_success"))
        eventual = sum(1 for s in ser if s.get("eventual_success"))
        breakdown = {}
        for s in ser:
            k = outcome_key(s["first_attempt"])
            breakdown[k] = breakdown.get(k, 0) + 1
        ok = [s for s in ser if s.get("eventual_success")]
        out["variants"][v] = {
            "runs": n, "first_attempt_success": first_ok, "first_attempt_success_rate": round(first_ok / n, 3) if n else None,
            "eventual_success": eventual, "eventual_success_rate": round(eventual / n, 3) if n else None,
            "first_attempt_outcomes": dict(sorted(breakdown.items())),
            "attempts_to_success": stat([s["attempts_to_success"] for s in ok]),
            "ms_to_success": stat([s["ms_to_success"] for s in ok]),
            "pod_ready_s": stat([r.get("pod_ready_s") for r in rs], 1),
            "recall_after_resume_ok": (sum(1 for r in rs if r.get("recall_ok")) if v == "approval" else None),
        }
    return out


def to_md(sm):
    L = []
    if sm["kind"] == "reject":
        L += [f"Rejected-approval series: {sm['runs']} runs, {sm['pass']} passed.", "",
              f"- first attempt succeeded without a gateway retry: {sm['first_attempt_success']}/{sm['runs']}",
              f"- rejection reason visible in the mock's request log: {sm['reason_visible']}/{sm['runs']}",
              f"- answer sha equals the sha of the logged rejection text: {sm['sha_is_sha_of_logged_results']}/{sm['runs']}",
              f"- distinct answer shas: {len(sm['distinct_answer_shas'])}"]
        if sm["approval_to_done_ms"]:
            L.append(f"- rejection to done (ms): median {sm['approval_to_done_ms']['median']}, p90 {sm['approval_to_done_ms']['p90']}, max {sm['approval_to_done_ms']['max']}")
        for f in sm["failures"]:
            L.append(f"- failed run {f['run']}: {f['first_attempt']} {f['error'] or ''}")
        return "\n".join(L) + "\n"
    L += ["Worker pod replacement series. The first attempt after the new pod is Ready is reported separately from eventual success (retries with backoff).", "",
          "| variant | runs | first attempt ok | eventual ok | first-attempt outcomes | attempts to success (median / max) | ms to success (median / p90 / max) | pod ready, s (median) |",
          "|---|---:|---:|---:|---|---|---|---:|"]
    for v, x in sm["variants"].items():
        at, ms = x["attempts_to_success"], x["ms_to_success"]
        L.append(f"| {v} | {x['runs']} | {x['first_attempt_success']}/{x['runs']} ({x['first_attempt_success_rate']:.0%}) | {x['eventual_success']}/{x['runs']} ({x['eventual_success_rate']:.0%}) | "
                 f"{', '.join(f'{k}: {c}' for k, c in x['first_attempt_outcomes'].items())} | "
                 f"{'%s / %s' % (at['median'], at['max']) if at else 'n/a'} | {'%s / %s / %s' % (ms['median'], ms['p90'], ms['max']) if ms else 'n/a'} | "
                 f"{x['pod_ready_s']['median'] if x['pod_ready_s'] else 'n/a'} |")
    return "\n".join(L) + "\n"


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    rc = sub.add_parser("reject-check")
    rc.add_argument("--basket", required=True)
    rc.add_argument("--mock-log", required=True)
    rc.add_argument("--reason", required=True)
    rc.add_argument("--expect-sha")
    rc.add_argument("--run", type=int, required=True)
    sm = sub.add_parser("summary")
    sm.add_argument("--kind", choices=["reject", "restart"], required=True)
    sm.add_argument("--in", dest="inp", required=True)
    sm.add_argument("--out-json", required=True)
    sm.add_argument("--out-md", required=True)
    a = ap.parse_args()
    if a.cmd == "reject-check":
        try:
            doc = json.load(open(a.basket))
        except (OSError, ValueError):
            doc = {}
        print(json.dumps(reject_record(a.run, doc, open(a.mock_log).read().splitlines(), a.reason, a.expect_sha)))
        return
    recs = [json.loads(x) for x in open(a.inp) if x.strip()]
    s = summarize_reject(recs) if a.kind == "reject" else summarize_restart(recs)
    json.dump(s, open(a.out_json, "w"), indent=1)
    open(a.out_md, "w").write(to_md(s))
    sys.stdout.write(to_md(s))


if __name__ == "__main__":
    main()
