#!/usr/bin/env python3
"""Unit tests for the oh-my-pig proof/oracle harness (oracle.py & harness_isolation.py).

Covers oracle harness fixes and audit regressions:
- child surface selection ignores tool-less label requests and picks the child request with tools
- Details comparison is included in per-call verdict, and unexplained details fail calls even under listed text/timing (Audit #1)
- side-aware macOS `/private` path normalization catches unstandardised paths (Audit #2)
- narrow `agent_loop_error` and `todo` error-only details matchers (Audit #3)
- removed text-stripping normalizers (`service_persistent`, `pig_agent_search_dir`, `js_hidden_frames`) (Audit #11)
- surface allowlist (`surface_allowlist.json`), totals, and non-zero exit (Audit #12)
- type-aware details key masks & unit-preserving text duration masks (Audit #13)
- bounded `timing` band (`[1.0s, 1.3s]`), details check, and non-zero exit unless `--allow-timing` (Audit #14)
- `allowlist_refs.py` required_subject validation & `PIG_SRC` / `ORACLE_TOOL_DIRS` hygiene (Audit #17)
- `NO_CONSUMER_AT_TAG`/`PORT_EXTRA` classes, `None` vs `{}` diff, scoped read & pinned daemon entries (Audit #18, #22)
- positional unified-diff header parsing & anchored text regexes (Audit #19, #23)
- widget entries restricted to live/authoring states so fallback views are not hidden (Audit #10)
"""

from __future__ import annotations

import json
import os
import re
import sys
import tempfile
import unittest
from pathlib import Path

sys.dont_write_bytecode = True

PROOF_DIR = Path(__file__).resolve().parent
if str(PROOF_DIR) not in sys.path:
    sys.path.insert(0, str(PROOF_DIR))

import allowlist_refs  # noqa: E402
import harness_isolation  # noqa: E402
import ansi_capture  # noqa: E402
import compare  # noqa: E402
import llm_server  # noqa: E402
import oracle  # noqa: E402


class TestHarnessFixes(unittest.TestCase):
    def test_child_surface_selects_request_with_tools(self) -> None:
        """Finding #22: ignore omp's tool-less task-label request; capture the child request carrying tools."""
        state = oracle.OracleSessionState(
            turns=[[{"tool": "wait", "arguments": {}}]],
            parent_marker="[oracle-case:child-surface-test]",
            child_reply="CHILD_OK",
            child_delay_ms=0,
            child_error=None,
            replacements={},
        )
        srv = oracle.OracleLLMServer(state)
        port = srv.start()
        try:
            import urllib.request

            url = f"http://127.0.0.1:{port}/v1/chat/completions"

            # 1. omp's task-label request: non-parent, NO tools
            label_body = json.dumps({
                "model": "proof-1",
                "messages": [
                    {"role": "system", "content": "# Task\nLabel the delegated work in one sentence."},
                    {"role": "user", "content": "Reply with OK."},
                ],
            }).encode("utf-8")
            req1 = urllib.request.Request(url, data=label_body, headers={"Content-Type": "application/json"})
            with urllib.request.urlopen(req1, timeout=5) as resp:
                resp.read()

            # Still None because the label request had no tools
            self.assertIsNone(state.child_advertised_tools)
            self.assertIsNone(state.child_system_prompt)

            # 2. Real subagent child request: non-parent, WITH tools
            child_body = json.dumps({
                "model": "proof-1",
                "messages": [
                    {"role": "system", "content": "You are a worker subagent."},
                    {"role": "user", "content": "Complete assignment thoroughly:\n\nReply with OK."},
                ],
                "tools": [
                    {"type": "function", "function": {"name": "read", "description": "Read", "parameters": {"type": "object"}}},
                    {"type": "function", "function": {"name": "yield", "description": "Yield", "parameters": {"type": "object"}}},
                ],
            }).encode("utf-8")
            req2 = urllib.request.Request(url, data=child_body, headers={"Content-Type": "application/json"})
            with urllib.request.urlopen(req2, timeout=5) as resp:
                resp.read()

            self.assertEqual(state.child_system_prompt, "You are a worker subagent.")
            self.assertEqual([t["name"] for t in (state.child_advertised_tools or [])], ["read", "yield"])
        finally:
            srv.stop()

    def test_compare_case_details_verdict_classes(self) -> None:
        """Finding #23: `identical` requires text, isError AND details to match; details-only diffs are a distinct class."""
        case = {
            "name": "details-test",
            "calls": [
                {"tool": "read", "arguments": {"path": "a.txt"}},
                {"tool": "bash", "arguments": {"command": "echo hi"}},
                {"tool": "write", "arguments": {"path": "b.txt", "content": "x"}},
            ],
        }
        omp_res = {
            "rc": 0, "setup_rc": 0, "timed_out": False, "network_attempts": [], "leaked_pids": [],
            "added_messages_normalized": [],
            "calls": [
                {
                    "index": 0, "turn_index": 0, "tool": "read", "arguments": {"path": "a.txt"},
                    "captured": True, "raw_result": "ok", "normalized_result": "ok",
                    "normalizations_applied": [], "is_error": False,
                    "details_normalized": {"totalLines": 1},
                },
                {
                    "index": 1, "turn_index": 1, "tool": "bash", "arguments": {"command": "echo hi"},
                    "captured": True, "raw_result": "hi\n", "normalized_result": "hi\n",
                    "normalizations_applied": [], "is_error": False,
                    "details_normalized": {"timeoutSeconds": 300, "trace": {"v": 1}},
                },
                {
                    "index": 2, "turn_index": 2, "tool": "write", "arguments": {"path": "b.txt", "content": "x"},
                    "captured": True, "raw_result": "ok-omp", "normalized_result": "ok-omp",
                    "normalizations_applied": [], "is_error": False,
                    "details_normalized": {"path": "b.txt"},
                },
            ],
        }
        pig_res = {
            "rc": 0, "setup_rc": 0, "timed_out": False, "network_attempts": [], "leaked_pids": [],
            "added_messages_normalized": [],
            "calls": [
                {
                    "index": 0, "turn_index": 0, "tool": "read", "arguments": {"path": "a.txt"},
                    "captured": True, "raw_result": "ok", "normalized_result": "ok",
                    "normalizations_applied": [], "is_error": False,
                    "details_normalized": {"totalLines": 1},
                },
                {
                    "index": 1, "turn_index": 1, "tool": "bash", "arguments": {"command": "echo hi"},
                    "captured": True, "raw_result": "hi\n", "normalized_result": "hi\n",
                    "normalizations_applied": [], "is_error": False,
                    "details_normalized": {"timeoutSeconds": 300},
                },
                {
                    "index": 2, "turn_index": 2, "tool": "write", "arguments": {"path": "b.txt", "content": "x"},
                    "captured": True, "raw_result": "ok-pig", "normalized_result": "ok-pig",
                    "normalizations_applied": [], "is_error": False,
                    "details_normalized": {"path": "b.txt"},
                },
            ],
        }
        cmp = oracle.compare_case(case, omp_res, pig_res)
        self.assertEqual(cmp["calls"][0]["verdict"], "identical")
        self.assertEqual(cmp["calls"][0]["details_verdict"], "identical")
        self.assertEqual(cmp["calls"][0]["details_diff"], "")

        self.assertEqual(cmp["calls"][1]["verdict"], "details-only")
        self.assertEqual(cmp["calls"][1]["details_verdict"], "differs")
        self.assertIn("trace", cmp["calls"][1]["details_diff"])

        self.assertEqual(cmp["calls"][2]["verdict"], "differs")
        self.assertEqual(cmp["calls"][2]["details_verdict"], "identical")

    def test_missing_tool_precheck_fails_loudly(self) -> None:
        """Missing external tools required by a case must fail loudly with `missing tool`, never silently diff."""
        proxy = harness_isolation.RefusingProxyServer()
        proxy.start()
        try:
            with tempfile.TemporaryDirectory(prefix="oracle-test-") as td:
                case = {
                    "name": "needs-nonexistent",
                    "requires_tools": ["definitely-no-such-binary-xyz-9999"],
                    "calls": [{"tool": "bash", "arguments": {"command": "true"}}],
                }
                res = oracle.run_side(
                    "omp",
                    "/bin/true",
                    case,
                    Path(td),
                    10,
                    shared_natives_dir=None,
                    proxy=proxy,
                    run_tag="test-missing-tool",
                )
                self.assertEqual(res.get("missing_tools"), ["definitely-no-such-binary-xyz-9999"])
                self.assertIn("missing tool", res.get("stderr", ""))
                self.assertEqual(res["rc"], -1)
                cmp = oracle.compare_case(case, res, None)
                self.assertEqual(cmp["calls"][0]["verdict"], "missing_tool")
                self.assertTrue(oracle._case_failed(cmp))
        finally:
            proxy.stop()

    def test_pig_offline_config_has_web_keyless_false(self) -> None:
        cfg = harness_isolation.pig_offline_config_yml()
        self.assertIn("webKeyless: false", cfg)
        self.assertIn("fetch: native", cfg)
        cfg_extra = harness_isolation.pig_offline_config_yml("astGrep:\n  enabled: true")
        self.assertIn("astGrep:", cfg_extra)
        self.assertIn("webKeyless: false", cfg_extra)

    def test_normalizations_side_aware_private_and_no_implicit_text_stripping(self) -> None:
        # Audit #11: ` — persistent`, JS hidden frames, and `~/.pig/agent/agents, ` are NO LONGER stripped by normalizers.
        t1, rules1 = oracle.normalize_text(
            "oracle-svc [service] — ready — up 12ms — pid 42 — persistent",
            work_dir="/private/tmp/w",
            home_dir="/private/tmp/h",
            tmp_dir="/private/tmp/t",
        )
        self.assertEqual(t1, "oracle-svc [service] — ready — up <DURATION ms> — pid <PID> — persistent")
        self.assertNotIn("service_persistent", rules1)

        t2, rules2 = oracle.normalize_text(
            "    (4 omp frames hidden; catch the error and print e.stack to see them)",
            work_dir="/tmp/w",
            home_dir="/tmp/h",
            tmp_dir="/tmp/t",
        )
        t3, _ = oracle.normalize_text(
            "    (6 omp frames hidden; catch the error and print e.stack to see them)",
            work_dir="/tmp/w",
            home_dir="/tmp/h",
            tmp_dir="/tmp/t",
        )
        self.assertNotEqual(t2, t3)
        self.assertNotIn("js_hidden_frames", rules2)

        t4, rules4 = oracle.normalize_text(
            'Task Bad failed preflight: Unknown agent "x". Searched: ~/.pig/agent/agents, ~/.omp/agent/agents',
            work_dir="/tmp/w",
            home_dir="/tmp/h",
            tmp_dir="/tmp/t",
        )
        self.assertIn("~/.pig/agent/agents", t4)
        self.assertNotIn("pig_agent_search_dir", rules4)

        # Audit #2: side-aware macOS `/private/tmp` vs `/tmp` normalization must NOT equalize them.
        if sys.platform == "darwin":
            real_tmp = os.path.realpath("/tmp")
            if real_tmp.startswith("/private/"):
                w_real = real_tmp + "/oracle-alias-probe"
                w_short = "/tmp/oracle-alias-probe"
                Path(w_real).mkdir(parents=True, exist_ok=True)
                try:
                    n_omp, r_omp = oracle.normalize_text(
                        f"CWD: {w_short}",
                        work_dir=w_real,
                        home_dir=real_tmp,
                        tmp_dir=real_tmp,
                    )
                    n_pig, r_pig = oracle.normalize_text(
                        f"CWD: {w_real}",
                        work_dir=w_real,
                        home_dir=real_tmp,
                        tmp_dir=real_tmp,
                    )
                    self.assertEqual(n_omp, "CWD: <WORKSPACE>")
                    self.assertEqual(n_pig, "CWD: /private<WORKSPACE>")
                    self.assertNotEqual(n_omp, n_pig)
                    self.assertIn("temp_paths", r_omp)
                    self.assertIn("temp_paths", r_pig)
                finally:
                    Path(w_real).rmdir()

    def test_augment_tool_path_uses_env_var_not_hardcoded_dir(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            env = {"PATH": "/usr/bin:/bin"}
            harness_isolation.augment_tool_path(env, (td,))
            self.assertTrue(env["PATH"].startswith(td + os.pathsep))
            # Idempotent
            harness_isolation.augment_tool_path(env, (td,))
            self.assertEqual(env["PATH"].split(os.pathsep).count(td), 1)

            # When ORACLE_TOOL_DIRS is unset, default augment_tool_path makes no changes
            old = os.environ.pop("ORACLE_TOOL_DIRS", None)
            try:
                env2 = {"PATH": "/usr/bin:/bin"}
                harness_isolation.augment_tool_path(env2)
                self.assertEqual(env2["PATH"], "/usr/bin:/bin")

                os.environ["ORACLE_TOOL_DIRS"] = td
                harness_isolation.augment_tool_path(env2)
                self.assertTrue(env2["PATH"].startswith(td + os.pathsep))
            finally:
                if old is not None:
                    os.environ["ORACLE_TOOL_DIRS"] = old
                else:
                    os.environ.pop("ORACLE_TOOL_DIRS", None)
    def test_all_cases_valid_and_skill_fixture_in_agents_skills(self) -> None:
        files, cases = oracle.load_cases([str(PROOF_DIR / "cases")])
        self.assertGreaterEqual(len(cases), 45)
        names = [c["name"] for c in cases]
        self.assertEqual(len(names), len(set(names)), "Duplicate case names found")

        by_name = {c["name"]: c for c in cases}
        # Skill fixture must be in .agents/skills, NOT .omp/skills
        skill_case = by_name["internal-urls-read-write"]
        self.assertIn(".agents/skills/oracle-demo", skill_case["setup"])
        self.assertNotIn(".omp/skills", skill_case["setup"])

        # Cases requiring ast-grep / gopls must declare requires_tools
        for cname in ("lsp-ast-edit-debug-direct", "mounted-commands-in-bash", "ast-grep-and-edit-resolve-reject"):
            self.assertIn("ast-grep", by_name[cname].get("requires_tools", []))
        self.assertIn("gopls", by_name["lsp-gopls-actions"].get("requires_tools", []))

        # Round-4 cases & invariants
        self.assertIn("bash-chunked-column-cap", by_name)
        self.assertIn("approvals-write-mode", by_name)
        self.assertIn("parallel-tool-calls", by_name)
        # bash-notify-streams-cancel must wait before reading streams and inspect stream 1 + proc://
        bnsc_tools = [(c["tool"], c.get("arguments", {}).get("path", "")) for c in by_name["bash-notify-streams-cancel"]["calls"]]
        self.assertIn(("read", "proc://bg_2/streams/1"), bnsc_tools)
        self.assertIn(("read", "proc://"), bnsc_tools)
        # task-delayed-cancel must wait briefly before killing so child is in-flight
        tdc_calls = by_name["task-delayed-cancel"]["calls"]
        self.assertEqual(tdc_calls[1]["tool"], "bash")
        self.assertEqual(tdc_calls[2]["tool"], "write")
        # internal-urls-conflict-out-cfg must read cfg:// and test read-only URL writes
        iu_calls = [(c["tool"], c.get("arguments", {}).get("path", "")) for c in by_name["internal-urls-conflict-out-cfg"]["calls"]]
        self.assertIn(("read", "cfg://read/defaultLimit"), iu_calls)
        self.assertIn(("write", "history://foo"), iu_calls)

    def test_recorded_call_intent_comparison_and_proc_duration(self) -> None:
        """Round 4: capture tool_execution_start args/intent and report recorded_call diffs."""
        omp_stdout = "\n".join([
            json.dumps({"type": "tool_execution_start", "toolCallId": "oracle_call_0", "toolName": "read", "args": {"path": "a.txt"}, "intent": "Reading a"}),
            json.dumps({"type": "tool_execution_end", "toolCallId": "oracle_call_0", "toolName": "read", "isError": False, "result": {"content": [{"type": "text", "text": "ok"}], "details": {}}}),
        ])
        pig_stdout = "\n".join([
            json.dumps({"type": "tool_execution_start", "toolCallId": "oracle_call_0", "toolName": "read", "args": {"i": "Reading a", "path": "a.txt"}}),
            json.dumps({"type": "tool_execution_end", "toolCallId": "oracle_call_0", "toolName": "read", "isError": False, "result": {"content": [{"type": "text", "text": "ok"}], "details": {}}}),
        ])
        oe = oracle._parse_json_mode_events(omp_stdout)["oracle_call_0"]
        pe = oracle._parse_json_mode_events(pig_stdout)["oracle_call_0"]
        self.assertEqual(oe["startArgs"], {"path": "a.txt"})
        self.assertEqual(oe["startIntent"], "Reading a")
        self.assertEqual(pe["startArgs"], {"i": "Reading a", "path": "a.txt"})
        self.assertNotIn("startIntent", pe)

        case = {"name": "intent-rec", "calls": [{"tool": "read", "arguments": {"i": "Reading a", "path": "a.txt"}}]}
        omp_res = {
            "rc": 0, "setup_rc": 0, "timed_out": False, "network_attempts": [], "leaked_pids": [],
            "added_messages_normalized": [],
            "calls": [{
                "index": 0, "turn_index": 0, "tool": "read", "arguments": {"i": "Reading a", "path": "a.txt"},
                "captured": True, "raw_result": "ok", "normalized_result": "ok",
                "normalizations_applied": [], "is_error": False, "details_normalized": {},
                "recorded_args": oe["startArgs"], "recorded_intent": oe.get("startIntent"),
            }],
        }
        pig_res = {
            "rc": 0, "setup_rc": 0, "timed_out": False, "network_attempts": [], "leaked_pids": [],
            "added_messages_normalized": [],
            "calls": [{
                "index": 0, "turn_index": 0, "tool": "read", "arguments": {"i": "Reading a", "path": "a.txt"},
                "captured": True, "raw_result": "ok", "normalized_result": "ok",
                "normalizations_applied": [], "is_error": False, "details_normalized": {},
                "recorded_args": pe["startArgs"], "recorded_intent": pe.get("startIntent"),
            }],
        }
        cmp = oracle.compare_case(case, omp_res, pig_res)
        self.assertEqual(cmp["calls"][0]["recorded_call_verdict"], "recorded-differs")
        self.assertIn("Reading a", cmp["calls"][0]["recorded_call_diff"])

        # With recorded_allowlist, intent-in-args becomes recorded-listed; extra arg diff stays recorded-differs
        r_allow = oracle.load_recorded_allowlist()
        cmp_listed = oracle.compare_case(case, omp_res, pig_res, recorded_allowlist=r_allow)
        self.assertEqual(cmp_listed["calls"][0]["recorded_call_verdict"], "recorded-listed")
        self.assertEqual(cmp_listed["calls"][0]["recorded_allowlist_hits"], [0])

        pig_extra = json.loads(json.dumps(pig_res))
        pig_extra["calls"][0]["recorded_args"]["extra"] = 1
        cmp_extra = oracle.compare_case(case, omp_res, pig_extra, recorded_allowlist=r_allow)
        self.assertEqual(cmp_extra["calls"][0]["recorded_call_verdict"], "recorded-differs")
        self.assertEqual(cmp_extra["calls"][0]["recorded_allowlist_hits"], [])
        # Verify proc_duration preserves units (`<DURATION ms>` vs `<DURATION s>`) and task_dir_hex stays 16-hex only
        txt, rules = oracle.normalize_text(
            "bg_3 [bash] running up 227ms — sleep 30\nbg_3 [bash] cancelled in 2s — sleep 30\nomp-task-3596013130 omp-task-0123456789abcdef",
            work_dir="/tmp/w", home_dir="/tmp/h", tmp_dir="/tmp/t",
        )
        self.assertIn("running up <DURATION ms>", txt)
        self.assertIn("cancelled in <DURATION s>", txt)
        self.assertIn("proc_duration", rules)
        # Do NOT widen task_dir_hex to decimal; decimal suffix stays visible while 16-hex normalizes
        self.assertIn("omp-task-3596013130", txt)
        self.assertIn("omp-task-<TASK_HEX>", txt)
    def test_details_allowlist_subtraction_and_sibling_isolation(self) -> None:
        """Round 5: allowlist path glob must not swallow sibling keys; extra unexplained key stays details-only."""
        # 1. Path matcher segment boundary: glob does not cross `.` or match siblings
        self.assertTrue(oracle._match_details_path("proc.daemon.*", "proc.daemon.command"))
        self.assertFalse(oracle._match_details_path("proc.daemon.*", "proc.daemon.policy.hold"))
        self.assertFalse(oracle._match_details_path("proc.daemon.*", "proc.daemon_extra.command"))
        self.assertFalse(oracle._match_details_path("meta.pagedSource", "meta.source"))
        self.assertFalse(oracle._match_details_path("meta.pagedSource", "meta.pagedSource.extra"))
        self.assertTrue(oracle._match_details_path("jobs[].widget", "jobs[].widget"))
        self.assertFalse(oracle._match_details_path("jobs[].widget", "jobs[].widgets"))

        allow = [
            {"tool": "bash", "path": "trace", "side": "omp-only", "class": "LISTED", "ref": "internal/shell/UPSTREAM.md:116", "why": "trace"},
            {"tool": "read", "path": "meta.pagedSource", "side": "omp-only", "class": "NO_CONSUMER_AT_TAG", "ref": "internal/files/UPSTREAM.md:94", "why": "pagedSource"},
            {"tool": "write", "path": "touchesNoFiles", "side": "omp-only", "class": "NO_CONSUMER_AT_TAG", "ref": "internal/files/UPSTREAM.md:95", "why": "touchesNoFiles"},
        ]
        case = {
            "name": "allow-sub",
            "calls": [
                {"tool": "bash", "arguments": {"command": "echo 1"}},
                {"tool": "read", "arguments": {"path": "a.txt"}},
                {"tool": "read", "arguments": {"path": "b.txt"}},
                {"tool": "write", "arguments": {"path": "out://", "content": "hi"}},
            ],
        }
        def _mk(idx: int, tool: str, det: object) -> dict:
            return {
                "index": idx, "turn_index": idx, "tool": tool, "arguments": {},
                "captured": True, "raw_result": "ok", "normalized_result": "ok",
                "normalizations_applied": [], "is_error": False, "details_normalized": det,
            }
        omp_res = {
            "rc": 0, "setup_rc": 0, "timed_out": False, "network_attempts": [], "leaked_pids": [],
            "added_messages_normalized": [],
            "calls": [
                # Call 0: only allowlisted `trace` differs -> details-listed
                _mk(0, "bash", {"timeoutSeconds": 300, "trace": {"v": 1}}),
                # Call 1: allowlisted `meta.pagedSource` PLUS unexplained sibling `meta.extra` -> details-only
                _mk(1, "read", {"meta": {"source": {"type": "path"}, "pagedSource": True, "extra": 1}}),
                # Call 2: allowlisted `meta.pagedSource` ONLY -> details-listed (does not swallow sibling)
                _mk(2, "read", {"meta": {"source": {"type": "path"}, "pagedSource": True}}),
                # Call 3: omp={'touchesNoFiles': True} vs pig={} -> details-listed (None vs {} is now a diff!)
                _mk(3, "write", {"touchesNoFiles": True}),
            ],
        }
        pig_res = {
            "rc": 0, "setup_rc": 0, "timed_out": False, "network_attempts": [], "leaked_pids": [],
            "added_messages_normalized": [],
            "calls": [
                _mk(0, "bash", {"timeoutSeconds": 300}),
                _mk(1, "read", {"meta": {"source": {"type": "path"}}}),
                _mk(2, "read", {"meta": {"source": {"type": "path"}}}),
                _mk(3, "write", {}),
            ],
        }
        cmp = oracle.compare_case(case, omp_res, pig_res, details_allowlist=allow)
        self.assertEqual(cmp["calls"][0]["verdict"], "details-listed")
        self.assertEqual(cmp["calls"][0]["details_verdict"], "listed")
        self.assertEqual(cmp["calls"][0]["details_unexplained"], [])
        self.assertEqual(cmp["calls"][0]["details_allowlist_hits"], [0])

        # Extra unexplained sibling key MUST keep verdict as details-only
        self.assertEqual(cmp["calls"][1]["verdict"], "details-only")
        self.assertEqual(cmp["calls"][1]["details_verdict"], "differs")
        self.assertEqual([d["path"] for d in cmp["calls"][1]["details_unexplained"]], ["meta.extra"])
        self.assertEqual(cmp["calls"][1]["details_allowlist_hits"], [1])

        self.assertEqual(cmp["calls"][2]["verdict"], "details-listed")
        self.assertEqual(cmp["calls"][3]["verdict"], "details-listed")

        # Audit #18: None vs {} MUST be reported as a difference (never silently listed)
        _, unexp_null_vs_empty, _ = oracle.classify_details_diffs("write", None, {}, allow)
        self.assertEqual(len(unexp_null_vs_empty), 1)
        self.assertEqual(unexp_null_vs_empty[0]["path"], "<root>")

        # Wrong side in allowlist must NOT match (omp-only rule must not swallow port-only key)
        _, unexp_wrong_side, _ = oracle.classify_details_diffs("bash", {"timeoutSeconds": 300}, {"timeoutSeconds": 300, "trace": 1}, allow)
        self.assertEqual(len(unexp_wrong_side), 1)
        self.assertEqual(unexp_wrong_side[0]["side"], "port-only")
    def test_text_allowlist_subtraction_and_repo_allowlists_valid(self) -> None:
        """Round 5: text allowlist produces differs-listed only when EVERY changed line matches; repo JSONs are valid."""
        t_allow = [
            {
                "case": "c1",
                "call_index": 0,
                "tool": "read",
                "regex": "^- ssh://.*$",
                "class": "LISTED",
                "ref": "internal/urls/UPSTREAM.md:75",
                "why": "ssh omitted",
            }
        ]
        diff_ok = "--- omp\n+++ oh-my-pig\n@@ -1,2 +1 @@\n keep\n-- ssh://host\n"
        listed, hits = oracle.classify_text_diff("c1", 0, "read", diff_ok, t_allow)
        self.assertTrue(listed)
        self.assertEqual(hits, [0])

        # Extra unexpected changed line must NOT be swallowed
        diff_extra = "--- omp\n+++ oh-my-pig\n@@ -1,2 +1 @@\n-- ssh://host\n+unexpected line\n"
        listed2, hits2 = oracle.classify_text_diff("c1", 0, "read", diff_extra, t_allow)
        self.assertFalse(listed2)
        self.assertEqual(hits2, [])

        # Allowlisted text line with an UNLISTED isError mismatch MUST stay `differs`
        case_err = {"name": "c1", "calls": [{"tool": "read", "arguments": {"path": "help://read/urls"}}]}
        omp_res = {
            "rc": 0, "setup_rc": 0, "timed_out": False, "network_attempts": [], "leaked_pids": [],
            "added_messages_normalized": [],
            "calls": [{
                "index": 0, "turn_index": 0, "tool": "read", "arguments": {},
                "captured": True, "raw_result": "keep\n- ssh://host\n", "normalized_result": "keep\n- ssh://host\n",
                "normalizations_applied": [], "is_error": False, "details_normalized": {},
            }],
        }
        pig_res = {
            "rc": 0, "setup_rc": 0, "timed_out": False, "network_attempts": [], "leaked_pids": [],
            "added_messages_normalized": [],
            "calls": [{
                "index": 0, "turn_index": 0, "tool": "read", "arguments": {},
                "captured": True, "raw_result": "keep\n", "normalized_result": "keep\n",
                "normalizations_applied": [], "is_error": True, "details_normalized": {},
            }],
        }
        cmp_unlisted_err = oracle.compare_case(case_err, omp_res, pig_res, text_allowlist=t_allow)
        self.assertEqual(cmp_unlisted_err["calls"][0]["verdict"], "differs")
        self.assertEqual(cmp_unlisted_err["calls"][0]["text_allowlist_hits"], [])

        # Wrong polarity [True, False] must ALSO stay `differs`
        t_allow_wrong_pol = [{**t_allow[0], "is_error_mismatch": [True, False]}]
        cmp_wrong_pol = oracle.compare_case(case_err, omp_res, pig_res, text_allowlist=t_allow_wrong_pol)
        self.assertEqual(cmp_wrong_pol["calls"][0]["verdict"], "differs")

        # Matching explicit is_error_mismatch [False, True] yields `differs-listed`
        t_allow_with_err = [{**t_allow[0], "is_error_mismatch": [False, True]}]
        cmp_listed_err = oracle.compare_case(case_err, omp_res, pig_res, text_allowlist=t_allow_with_err)
        self.assertEqual(cmp_listed_err["calls"][0]["verdict"], "differs-listed")
        self.assertEqual(cmp_listed_err["calls"][0]["text_allowlist_hits"], [0])

        # Verify shipped bash-notify-streams-cancel #5 and #6 text_allowlist entries match exact diffs
        t_entries = oracle.load_text_allowlist()
        diff_c5 = (
            "--- omp\n+++ oh-my-pig\n@@ -1 +1 @@\n"
            "-proc://bg_2/streams/1 open 9B — (echo pipe_one; sleep 1.5; echo pipe_two) → cat\n"
            "+bg_2 has piped nothing between `|` stages and redirected nothing into a file yet. Read proc://bg_2 for its output.\n"
        )
        ok5, hits5 = oracle.classify_text_diff("bash-notify-streams-cancel", 5, "read", diff_c5, t_entries)
        self.assertTrue(ok5)
        self.assertEqual(len(hits5), 1)

        diff_c6 = (
            "--- omp\n+++ oh-my-pig\n@@ -1 +1 @@\n"
            "-pipe_one\n"
            "+Stream not found: proc://bg_2/streams/1. List them with proc://bg_2/streams.\n"
            "\n[isError differs: omp=False vs oh-my-pig=True]\n"
        )
        ok6, hits6 = oracle.classify_text_diff(
            "bash-notify-streams-cancel", 6, "read", diff_c6, t_entries,
            is_error_differs=True, omp_is_error=False, pig_is_error=True,
        )
        self.assertTrue(ok6)
        self.assertEqual(len(hits6), 1)

        # Validate all shipped oracle allowlists load via line-independent (ref_file, ref_term) resolution
        d_entries = oracle.load_details_allowlist()
        self.assertGreater(len(d_entries), 0)
        self.assertGreater(len(t_entries), 0)
        for e in d_entries:
            for k in ("tool", "path", "side", "class", "ref_file", "ref_term", "ref", "why"):
                self.assertIn(k, e)
            self.assertIn(e["side"], ("omp-only", "port-only", "value"))
            self.assertIn(e["class"], oracle.VALID_DETAILS_CLASSES)
            self.assertNotEqual(e["class"], "NOT_OBSERVABLE")
            if not e.get("ref_unverified"):
                self.assertIsInstance(e["ref_line"], int)
                self.assertRegex(e["ref"], r":\d+$")

        # Verify internal-urls-read-write#5 matches exact literal ssh:// and proc://streams lines (and hits all 3 refs)
        diff_urls5 = (
            "--- omp\n+++ oh-my-pig\n@@ -1,4 +1,3 @@\n"
            "-- proc://<id>/streams lists a bash job's pipes and the files it redirects output into, proc://<id>/streams/<n> reads what stream n carried so far. Write sends stdin to services only (empty = Enter); proc://<id>/mode: persist|session|detached (services only); start long-lived work as a named service when it needs stdin or lifetime controls. Completed jobs evicted (process-local).\n"
            "-- ssh://host/<path>: max 1 MiB. /~/path and /~ use the connected remote account's HOME, not local HOME; /~user is unsupported, /%7E/ is literal. Encode : ? # as %3A %3F %23. Needs verified POSIX shell; else bash remote SSH or sshfs.\n"
            "+- proc://<id>/streams is not available in the system-shell port (live intermediate `|` pipe and redirected-file tapping requires omp's embedded shell VM). Write sends stdin to services only (empty = Enter); proc://<id>/mode: session only (persist/detached require an out-of-process daemon); start long-lived work as a named service when it needs stdin. Completed jobs evicted (process-local).\n"
            " - issue:// / pr://\n"
        )
        ok_u5, hits_u5 = oracle.classify_text_diff("internal-urls-read-write", 5, "read", diff_urls5, t_entries)
        self.assertTrue(ok_u5)
        self.assertEqual(len(hits_u5), 3)
        # An extra unallowlisted line on #5 must still stay differs
        ok_u5_bad, _ = oracle.classify_text_diff(
            "internal-urls-read-write", 5, "read", diff_urls5 + "+- extra unexpected line\n", t_entries
        )
        self.assertFalse(ok_u5_bad)

        for e in t_entries:
            for k in ("case", "call_index", "tool", "regex", "class", "ref_file", "ref_term", "ref", "why"):
                self.assertIn(k, e)
            self.assertIn(e["class"], oracle.VALID_TEXT_CLASSES)
            re.compile(e["regex"])
            if not e.get("ref_unverified"):
                self.assertIsInstance(e["ref_line"], int)
                self.assertRegex(e["ref"], r":\d+$")

        r_entries = oracle.load_recorded_allowlist()
        self.assertGreater(len(r_entries), 0)
        for e in r_entries:
            for k in ("tool", "kind", "class", "ref_file", "ref_term", "ref", "why"):
                self.assertIn(k, e)
            self.assertIn(e["class"], oracle.VALID_RECORDED_CLASSES)
            if not e.get("ref_unverified"):
                self.assertIsInstance(e["ref_line"], int)
                self.assertRegex(e["ref"], r":\d+$")

        s_entries = oracle.load_surface_allowlist()
        self.assertGreater(len(s_entries), 0)
        for e in s_entries:
            for k in ("kind", "class", "ref_file", "ref_term", "ref", "why"):
                self.assertIn(k, e)
            self.assertIn(e["class"], oracle.VALID_SURFACE_CLASSES)
            if not e.get("ref_unverified"):
                self.assertIsInstance(e["ref_line"], int)
                self.assertRegex(e["ref"], r":\d+$")
    def test_allowlist_ref_resolution_failures_and_pig_checkout_skip(self) -> None:
        """Line-independent resolver fails on 0 matches, >1 ambiguous matches, or hardcoded line refs, and skips absent PiG checkout with a note."""
        with tempfile.TemporaryDirectory(prefix="allowref-test-") as td:
            root = Path(td)
            doc = root / "UPSTREAM.md"
            doc.write_text(
                "| Feature | Why |\n"
                "|---|---|\n"
                "| alpha widget | foo |\n"
                "| beta widget | bar |\n"
                "| gamma only | baz |\n",
                encoding="utf-8",
            )
            # 1. Unique match resolves to exact line
            ref, lno, unv = allowlist_refs.resolve_ref("UPSTREAM.md", ["alpha", "widget"], repo_root=root, ext_root=root)
            self.assertEqual((ref, lno, unv), ("UPSTREAM.md:3", 3, False))

            # 2. 0 matches raises AllowlistRefError
            with self.assertRaises(allowlist_refs.AllowlistRefError):
                allowlist_refs.resolve_ref("UPSTREAM.md", ["nonexistent"], repo_root=root, ext_root=root)

            # 3. >1 matches (ambiguous) raises AllowlistRefError
            with self.assertRaises(allowlist_refs.AllowlistRefError):
                allowlist_refs.resolve_ref("UPSTREAM.md", "widget", repo_root=root, ext_root=root)

            # 4. Hardcoded line number in ref_file raises AllowlistRefError
            with self.assertRaises(allowlist_refs.AllowlistRefError):
                allowlist_refs.resolve_ref("UPSTREAM.md:3", "alpha", repo_root=root, ext_root=root)

            # 5. Legacy hardcoded `ref` without ref_file/ref_term raises AllowlistRefError
            with self.assertRaises(allowlist_refs.AllowlistRefError):
                allowlist_refs.resolve_allowlist_entries(
                    [{"ref": "UPSTREAM.md:3"}], source_name="test.json", repo_root=root, ext_root=root
                )

            # 6. Absent external PiG checkout skips with visible unverified marker (does NOT raise)
            missing_pig = root / "no-such-pig-checkout"
            ref_p, lno_p, unv_p = allowlist_refs.resolve_ref(
                "PiG agent/tool_execution.go",
                "errorToolResult",
                repo_root=root,
                ext_root=root,
                pig_root=missing_pig,
            )
            self.assertTrue(unv_p)
            self.assertIsNone(lno_p)
            self.assertIn("PiG checkout absent", ref_p)
    def test_wall_time_timing_class_and_both_sides_preservation(self) -> None:
        """One-sided wall-time notice yields `timing` (never silently identical or differs); both-sided preserves blank lines and order."""
        # 1. normalize_text replaces ONLY the number, never strips the line
        raw_pty_wt = "notty\n\n\nWall time: 1.02 seconds\npty requested but unavailable in this environment; ran without a terminal"
        norm_pty_wt, rules = oracle.normalize_text(raw_pty_wt, work_dir="/tmp/w", home_dir="/tmp/h", tmp_dir="/tmp/t")
        self.assertEqual(
            norm_pty_wt,
            "notty\n\n\nWall time: <DURATION> seconds\npty requested but unavailable in this environment; ran without a terminal",
        )
        self.assertIn("wall_time", rules)

        def _mk(idx: int, raw: str, is_err: bool = False) -> dict:
            norm, applied = oracle.normalize_text(raw, work_dir="/tmp/w", home_dir="/tmp/h", tmp_dir="/tmp/t")
            return {
                "index": idx, "turn_index": idx, "tool": "bash", "arguments": {},
                "captured": True, "raw_result": raw, "normalized_result": norm,
                "normalizations_applied": applied, "is_error": is_err, "details_normalized": {},
            }

        case = {
            "name": "wall-time-test",
            "calls": [
                {"tool": "bash", "arguments": {"command": "echo a", "pty": True}},
                {"tool": "bash", "arguments": {"command": "echo b"}},
                {"tool": "bash", "arguments": {"command": "sleep 1.4; echo c", "pty": True}},
                {"tool": "bash", "arguments": {"command": "sleep 1.4; echo d", "pty": True}},
                {"tool": "bash", "arguments": {"command": "echo e", "pty": True}},
            ],
        }
        omp_res = {
            "rc": 0, "setup_rc": 0, "timed_out": False, "network_attempts": [], "leaked_pids": [],
            "parent_request_count": 6, "child_request_count": 0, "elapsed_ms": 100,
            "added_messages_normalized": [],
            "calls": [
                # 0: one-sided wall-time + pty notice (exact bash-cwd-spill#4 proof7 case) -> timing
                _mk(0, "notty\n\n\npty requested but unavailable in this environment; ran without a terminal"),
                # 1: one-sided sole wall-time notice (omp emitted it) -> timing
                _mk(1, "hello\n\n\nWall time: 1.05 seconds"),
                # 2: BOTH sides emit wall-time + pty notice in same order/blanks -> identical
                _mk(2, "notty\n\n\nWall time: 1.41 seconds\npty requested but unavailable in this environment; ran without a terminal"),
                # 3: BOTH sides emit wall-time + pty notice, but wrong blank line / order on port -> differs (NOT timing!)
                _mk(3, "notty\n\n\nWall time: 1.41 seconds\npty requested but unavailable in this environment; ran without a terminal"),
                # 4: one-sided wall-time PLUS a real output difference -> differs (NOT timing!)
                _mk(4, "notty\n\n\npty requested but unavailable in this environment; ran without a terminal"),
            ],
        }
        pig_res = {
            "rc": 0, "setup_rc": 0, "timed_out": False, "network_attempts": [], "leaked_pids": [],
            "parent_request_count": 6, "child_request_count": 0, "elapsed_ms": 100,
            "added_messages_normalized": [],
            "calls": [
                _mk(0, "notty\n\n\nWall time: 1.02 seconds\npty requested but unavailable in this environment; ran without a terminal"),
                _mk(1, "hello\n"),
                _mk(2, "notty\n\n\nWall time: 1.49 seconds\npty requested but unavailable in this environment; ran without a terminal"),
                _mk(3, "notty\n\n\npty requested but unavailable in this environment; ran without a terminal\nWall time: 1.49 seconds"),
                _mk(4, "DIFFERENT\n\n\nWall time: 1.02 seconds\npty requested but unavailable in this environment; ran without a terminal"),
            ],
        }
        cmp = oracle.compare_case(case, omp_res, pig_res)
        self.assertEqual(cmp["calls"][0]["verdict"], "timing")
        self.assertEqual(cmp["calls"][1]["verdict"], "timing")
        self.assertEqual(cmp["calls"][2]["verdict"], "identical")
        self.assertEqual(cmp["calls"][3]["verdict"], "differs")
        self.assertEqual(cmp["calls"][4]["verdict"], "differs")

        # Markdown report lists timing calls explicitly
        fake_report = {
            "mode": "both", "sandbox": "test", "input_files": [], "normalizations": oracle.NORMALIZATIONS,
            "totals": {
                "cases": 1, "cases_failed": 0, "calls": 5, "calls_identical": 1,
                "calls_details_listed": 0, "calls_details_only": 0, "calls_differs_listed": 0,
                "calls_timing": 2, "calls_differs": 2, "calls_missing": 0, "calls_single": 0,
                "recorded_identical": 5, "recorded_listed": 0, "recorded_differs": 0,
                "network_attempts": 0, "leaked_pids": 0,
            },
            "network_attempts": [], "leaked_pids": [], "surface_snapshots": [],
            "cases": [cmp],
        }
        md = oracle.render_markdown(fake_report)
        self.assertIn("2 timing", md)
        self.assertIn("## Timing-Threshold Calls (`timing`)", md)
        self.assertIn("`wall-time-test#0`", md)
        self.assertIn("`wall-time-test#1`", md)


    def test_audit_f1_unexplained_details_fail_under_listed_text_and_timing(self) -> None:
        """Audit #1 demonstrating inputs: unexplained details MUST produce `differs`, never `differs-listed` or `timing`."""
        t_entries = oracle.load_text_allowlist()
        d_entries = oracle.load_details_allowlist()

        # Demo 1: read-structured#0 with listed jq text + garbage port details -> differs (NOT differs-listed)
        case = {"name": "read-structured", "calls": [{"tool": "read", "arguments": {"path": "data.json?q=.items[0].name"}}]}
        omp_res = {
            "rc": 0, "setup_rc": 0, "timed_out": False, "network_attempts": [], "leaked_pids": [],
            "added_messages_normalized": [],
            "calls": [{
                "index": 0, "turn_index": 0, "tool": "read", "arguments": {"path": "data.json?q=.items[0].name"},
                "captured": True, "raw_result": '"a"', "normalized_result": '"a"',
                "normalizations_applied": [], "is_error": False,
                "details_normalized": {"resolvedPath": "<WORKSPACE>/data.json", "meta": {"source": {"type": "path", "value": "<WORKSPACE>/data.json"}}},
            }],
        }
        pig_res = {
            "rc": 0, "setup_rc": 0, "timed_out": False, "network_attempts": [], "leaked_pids": [],
            "added_messages_normalized": [],
            "calls": [{
                "index": 0, "turn_index": 0, "tool": "read", "arguments": {"path": "data.json?q=.items[0].name"},
                "captured": True,
                "raw_result": "JSON/JSONL `?q=` jq filter queries are not supported in oh-my-pig (read 'data.json' with line selectors or use jq via bash instead).",
                "normalized_result": "JSON/JSONL `?q=` jq filter queries are not supported in oh-my-pig (read 'data.json' with line selectors or use jq via bash instead).",
                "normalizations_applied": [], "is_error": True,
                "details_normalized": {"isDirectory": True, "garbage": [1, 2, 3], "exitCode": 137},
            }],
        }
        cmp_bad = oracle.compare_case(case, omp_res, pig_res, details_allowlist=d_entries, text_allowlist=t_entries)
        self.assertEqual(cmp_bad["calls"][0]["verdict"], "differs")
        self.assertEqual(cmp_bad["calls"][0]["details_verdict"], "differs")
        self.assertTrue(len(cmp_bad["calls"][0]["details_unexplained"]) >= 3)

        # With expected empty port details (`details_paths: ["meta", "resolvedPath"]`), it IS differs-listed
        pig_ok = json.loads(json.dumps(pig_res))
        pig_ok["calls"][0]["details_normalized"] = {}
        cmp_ok = oracle.compare_case(case, omp_res, pig_ok, details_allowlist=d_entries, text_allowlist=t_entries)
        self.assertEqual(cmp_ok["calls"][0]["verdict"], "differs-listed")
        self.assertEqual(cmp_ok["calls"][0]["details_verdict"], "listed")

        # Demo 2: bash with one-sided Wall time notice AND exitCode 0 vs 99 -> differs (NOT timing)
        b_case = {"name": "bash-timing", "calls": [{"tool": "bash", "arguments": {"command": "echo x"}}]}
        b_omp = {
            "rc": 0, "setup_rc": 0, "timed_out": False, "network_attempts": [], "leaked_pids": [],
            "added_messages_normalized": [],
            "calls": [{
                "index": 0, "turn_index": 0, "tool": "bash", "arguments": {"command": "echo x"},
                "captured": True, "raw_result": "hi\n\n\nWall time: 1.05 seconds",
                "normalized_result": "hi\n\n\nWall time: <DURATION> seconds",
                "normalizations_applied": ["wall_time"], "is_error": False,
                "details_normalized": {"exitCode": 0, "wallTimeNoticeEmitted": True},
                "raw_wall_time_ms": 1050.0,
            }],
        }
        b_pig = {
            "rc": 0, "setup_rc": 0, "timed_out": False, "network_attempts": [], "leaked_pids": [],
            "added_messages_normalized": [],
            "calls": [{
                "index": 0, "turn_index": 0, "tool": "bash", "arguments": {"command": "echo x"},
                "captured": True, "raw_result": "hi\n", "normalized_result": "hi\n",
                "normalizations_applied": [], "is_error": False,
                "details_normalized": {"exitCode": 99, "wallTimeNoticeEmitted": False},
                "raw_wall_time_ms": 950.0,
            }],
        }
        b_cmp = oracle.compare_case(b_case, b_omp, b_pig, details_allowlist=d_entries, text_allowlist=t_entries)
        self.assertEqual(b_cmp["calls"][0]["verdict"], "differs")
        self.assertEqual(b_cmp["calls"][0]["details_verdict"], "differs")

    def test_audit_f3_f10_f18_narrow_details_matchers(self) -> None:
        """Audit #3, #10, #18 demonstrating inputs: narrow HOST error matchers, fallback widgets, scoped read & pinned daemon entries."""
        d_entries = oracle.load_details_allowlist()

        # Audit #3: grep/web_search tool-emitted `error` and task `isError` MUST NOT be swallowed by agent_loop_error
        _, unexp_grep, _ = oracle.classify_details_diffs(
            "grep", {"error": "ripgrep failed", "fileCount": 0}, {"fileCount": 0}, d_entries,
            omp_is_error=True, pig_is_error=True, omp_text="ripgrep failed",
        )
        self.assertEqual([d["path"] for d in unexp_grep], ["error"])

        _, unexp_task, _ = oracle.classify_details_diffs(
            "task", {"isError": True, "results": []}, {"results": []}, d_entries,
            omp_is_error=True, pig_is_error=True, omp_text="failed",
        )
        self.assertEqual([d["path"] for d in unexp_task], ["isError"])

        # Genuine agent-loop error shape DOES match
        _, unexp_loop, hits_loop = oracle.classify_details_diffs(
            "ask", {"isError": True, "error": "Tool ask not found"}, {}, d_entries,
            omp_is_error=True, pig_is_error=True, omp_text="Tool ask not found",
        )
        self.assertEqual(unexp_loop, [])
        self.assertEqual(len(hits_loop), 1)

        # Audit #3: successful todo call with empty port details MUST NOT be swallowed
        _, unexp_todo_ok, _ = oracle.classify_details_diffs(
            "todo", {"phases": [{"name": "Tasks", "tasks": []}], "storage": "session"}, {}, d_entries,
            omp_is_error=False, pig_is_error=False,
        )
        self.assertEqual(sorted(d["path"] for d in unexp_todo_ok), ["phases", "storage"])

        # Failing todo call with exact {phases, storage} vs {} DOES match
        _, unexp_todo_err, _ = oracle.classify_details_diffs(
            "todo", {"phases": [{"name": "Tasks", "tasks": []}], "storage": "memory"}, {}, d_entries,
            omp_is_error=True, pig_is_error=True,
        )
        self.assertEqual(unexp_todo_err, [])

        # Audit #4: read resolvedPath entry removed -> unexplained
        _, unexp_rp, _ = oracle.classify_details_diffs(
            "read", {"resolvedPath": "/w/.agents/skills/x/SKILL.md", "totalLines": 6}, {"totalLines": 6}, d_entries,
        )
        self.assertEqual([d["path"] for d in unexp_rp], ["resolvedPath"])

        # Audit #10: fallback job widget is NOT allowlisted; only live/authoring are allowlisted
        _, unexp_fb, _ = oracle.classify_details_diffs(
            "wait",
            {"jobs": [{"id": "bg_1", "widget": {"title": "t", "blocks": [], "state": "fallback"}}]},
            {"jobs": [{"id": "bg_1"}]},
            d_entries,
        )
        self.assertEqual([d["path"] for d in unexp_fb], ["jobs[].widget"])

        _, unexp_live, _ = oracle.classify_details_diffs(
            "wait",
            {"jobs": [{"id": "bg_1", "widget": {"title": "t", "blocks": [], "state": "live"}}]},
            {"jobs": [{"id": "bg_1"}]},
            d_entries,
        )
        self.assertEqual(unexp_live, [])

        # Audit #18: read meta with extra keys (truncation/diagnostics) MUST NOT be swallowed
        _, unexp_meta, _ = oracle.classify_details_diffs(
            "read",
            {"resolvedPath": "<WORKSPACE>/a.txt"},
            {"resolvedPath": "<WORKSPACE>/a.txt", "meta": {"truncation": {"truncatedBy": "bytes"}, "diagnostics": {"summary": "3 errors"}}},
            d_entries,
        )
        self.assertEqual([d["path"] for d in unexp_meta], ["meta"])

        # Audit #18: read isDirectory on regular file MUST NOT be swallowed
        _, unexp_isdir, _ = oracle.classify_details_diffs(
            "read",
            {"meta": {"source": {"type": "path", "value": "<WORKSPACE>/a.txt"}}},
            {"meta": {"source": {"type": "path", "value": "<WORKSPACE>/a.txt"}}, "isDirectory": True},
            d_entries,
        )
        self.assertEqual([d["path"] for d in unexp_isdir], ["isDirectory"])

        # Audit #18: read totalLines/fileSize on a directory listing MUST NOT be swallowed
        _, unexp_dir_tl, _ = oracle.classify_details_diffs(
            "read",
            {"isDirectory": True},
            {"isDirectory": True, "totalLines": 10, "fileSize": 100},
            d_entries,
            arguments={"path": "sub"},
        )
        self.assertEqual(sorted(d["path"] for d in unexp_dir_tl), ["fileSize", "totalLines"])

        # Audit #18: daemon value entries pinned (`id: ""` and `policy.hold: 12345` MUST fail)
        _, unexp_dval, _ = oracle.classify_details_diffs(
            "read",
            {"proc": {"daemons": [{"id": "<UUID>", "policy": {"hold": "persist"}}]}},
            {"proc": {"daemons": [{"id": "", "policy": {"hold": 12345}}]}},
            d_entries,
        )
        self.assertEqual(sorted(d["path"] for d in unexp_dval), ["proc.daemons[].id", "proc.daemons[].policy.hold"])

    def test_audit_f13_type_aware_details_masks_and_unit_preserving_text_masks(self) -> None:
        """Audit #13 demonstrating inputs: details key masks check type/scope; text duration masks preserve units & nil UUID."""
        def _n(obj: object) -> object:
            return oracle.normalize_obj(obj, work_dir="/tmp/w", home_dir="/tmp/h", tmp_dir="/tmp/t")

        # wallTimeNoticeEmitted true vs false MUST differ
        self.assertNotEqual(_n({"wallTimeNoticeEmitted": True}), _n({"wallTimeNoticeEmitted": False}))
        # durationMs number vs string MUST differ
        self.assertNotEqual(_n({"durationMs": 1200}), _n({"durationMs": "1.2s"}))
        # pid number vs null MUST differ
        self.assertNotEqual(_n({"pid": 4242}), _n({"pid": None}))
        # timestamp null vs number MUST differ
        self.assertNotEqual(_n({"timestamp": None}), _n({"timestamp": 1}))
        # run / at / seq outside `trace` MUST NOT be masked
        self.assertNotEqual(_n({"run": {"argv": ["ls"]}}), _n({"run": "x"}))
        self.assertNotEqual(_n({"at": "line 3"}), _n({"at": "line 9"}))
        self.assertNotEqual(_n({"seq": 1}), _n({"seq": 7}))
        # Inside `trace`, numeric seq/t_ns ARE masked
        self.assertEqual(_n({"trace": {"seq": 1, "t_ns": 100}}), _n({"trace": {"seq": 7, "t_ns": 999}}))
        # Rate text numeric vs NaN MUST differ
        self.assertNotEqual(_n({"label": "lines/s", "text": "3.1"}), _n({"label": "lines/s", "text": "NaN"}))
        self.assertEqual(_n({"label": "lines/s", "text": "3.1"}), _n({"label": "lines/s", "text": "9.4"}))
        # Unit-preserving text duration masks & nil UUID preservation
        t_ms, _ = oracle.normalize_text('duration="120ms" completed in 3ms 6ms wall / 4ms api', work_dir="/tmp/w", home_dir="/tmp/h", tmp_dir="/tmp/t")
        t_s, _ = oracle.normalize_text('duration="2m" completed in 300s 9s wall / 100m api', work_dir="/tmp/w", home_dir="/tmp/h", tmp_dir="/tmp/t")
        self.assertNotEqual(t_ms, t_s)
        u_nil, _ = oracle.normalize_text("00000000-0000-0000-0000-000000000000", work_dir="/tmp/w", home_dir="/tmp/h", tmp_dir="/tmp/t")
        u_real, _ = oracle.normalize_text("3e1307ed-1e01-4dca-a84b-1600b024471a", work_dir="/tmp/w", home_dir="/tmp/h", tmp_dir="/tmp/t")
        self.assertNotEqual(u_nil, u_real)
        self.assertEqual(u_nil, "00000000-0000-0000-0000-000000000000")

    def test_audit_f14_bounded_timing_and_exit_code(self) -> None:
        """Audit #14 demonstrating inputs: 30s one-sided wall time is `differs`, not `timing`; `timing` exits 1 unless --allow-timing."""
        # 30s command where port omits wall-time notice -> NOT timing
        self.assertFalse(
            oracle._is_one_sided_wall_time_diff(
                "ok\n\n\nWall time: <DURATION> seconds",
                "ok\n",
                omp_raw="ok\n\n\nWall time: 30.00 seconds",
                pig_raw="ok\n",
                omp_wall_ms=30000.0,
                pig_wall_ms=30000.0,
            )
        )
        # Within 1.0–1.3s band and other side < 1000ms -> timing
        self.assertTrue(
            oracle._is_one_sided_wall_time_diff(
                "ok\n\n\nWall time: <DURATION> seconds",
                "ok\n",
                omp_raw="ok\n\n\nWall time: 1.08 seconds",
                pig_raw="ok\n",
                omp_wall_ms=1080.0,
                pig_wall_ms=970.0,
            )
        )
        # Other side also >= 1000ms (failed to emit notice!) -> NOT timing
        self.assertFalse(
            oracle._is_one_sided_wall_time_diff(
                "ok\n\n\nWall time: <DURATION> seconds",
                "ok\n",
                omp_raw="ok\n\n\nWall time: 1.08 seconds",
                pig_raw="ok\n",
                omp_wall_ms=1080.0,
                pig_wall_ms=1050.0,
            )
        )
        # Exit code fails on timing unless allow_timing=True
        totals = {"cases_failed": 0, "network_attempts": 0, "leaked_pids": 0, "calls_differs": 0, "calls_details_only": 0, "recorded_differs": 0, "surfaces_differs": 0, "child_surfaces_differs": 0, "followups_differs": 0, "calls_timing": 1}
        self.assertEqual(oracle.oracle_exit_code(totals, mode="both"), 1)
        self.assertEqual(oracle.oracle_exit_code(totals, mode="both", allow_timing=True), 0)

    def test_audit_f19_positional_diff_headers_and_anchored_text_regexes(self) -> None:
        """Audit #19 demonstrating inputs: content lines starting with `--`/`++` are checked, and open-tailed regexes are rejected."""
        t_entries = oracle.load_text_allowlist()
        # Demo 1: removed `--raw prints bytes` and added `++ injected line` MUST NOT be skipped as headers
        diff_injected = oracle.unified_diff(
            "- ssh://host/<path>: max 1 MiB. /~/path and /~ use the connected remote account's HOME, not local HOME; /~user is unsupported, /%7E/ is literal. Encode : ? # as %3A %3F %23. Needs verified POSIX shell; else bash remote SSH or sshfs.\n--raw prints bytes\n",
            "++ injected line\n",
        )
        ok, _ = oracle.classify_text_diff("internal-urls-read-write", 5, "read", diff_injected, t_entries)
        self.assertFalse(ok)

        # Demo 2: open-tail injection on proc://streams line MUST fail
        diff_tail = oracle.unified_diff(
            "- proc://<id>/streams lists a bash job's pipes rm -rf / is recommended; proc://<id>/kill is unsupported\n",
            "- proc://<id>/streams is not available in the system-shell port\n",
        )
        ok_tail, _ = oracle.classify_text_diff("internal-urls-read-write", 5, "read", diff_tail, t_entries)
        self.assertFalse(ok_tail)

        # Demo 3: wrong members on Available: line MUST fail
        diff_avail = oracle.unified_diff("Available: demo-rule, wrong-rule\n", "Available: none\n")
        ok_avail, _ = oracle.classify_text_diff("internal-urls-rule", 1, "read", diff_avail, t_entries)
        self.assertFalse(ok_avail)

        # Validator rejects open-tailed `.*$` regexes
        with self.assertRaises(ValueError):
            oracle._validate_anchored_regex("^- ssh://host/<path>:.*$", "test[0]")

    def test_audit_f17_allowlist_ref_required_subject_check(self) -> None:
        """Audit #17: allowlist_refs rejects a resolved line that does not name the differing field/subject."""
        with tempfile.TemporaryDirectory(prefix="allowsubj-test-") as td:
            root = Path(td)
            doc = root / "UPSTREAM.md"
            doc.write_text(
                "| Host-owned terminal colours | theme note |\n"
                "| Read `maxBytes` field | explained |\n",
                encoding="utf-8",
            )
            # Matching a line that does NOT mention `displayTarget` raises AllowlistRefError
            with self.assertRaises(allowlist_refs.AllowlistRefError):
                allowlist_refs.resolve_ref(
                    "UPSTREAM.md",
                    "Host-owned terminal colours",
                    repo_root=root,
                    ext_root=root,
                    required_subject="displayTarget",
                )
            # Matching a line that DOES mention `maxBytes` succeeds
            ref, lno, unv = allowlist_refs.resolve_ref(
                "UPSTREAM.md",
                "maxBytes",
                repo_root=root,
                ext_root=root,
                required_subject="maxBytes",
            )
            self.assertEqual((ref, lno, unv), ("UPSTREAM.md:2", 2, False))

    def test_audit_f12_surface_classification_and_exit_code(self) -> None:
        """Audit #12: surface diffs (system prompt, tool descriptions/schemas, child surfaces, followups) are classified and fail exit when unlisted."""
        s_entries = oracle.load_surface_allowlist()
        # Listed system prompt role line passes
        snap = {
            "label": "surface-0",
            "case": "ask-headless",
            "system_prompt": {
                "verdict": "differs",
                "omp_len": 40,
                "pig_len": 44,
                "diff": oracle.unified_diff(
                    "You are omp's trusted coding assistant.\n",
                    "You are oh-my-pig's trusted coding assistant.\n",
                    "omp:system_prompt",
                    "oh-my-pig:system_prompt",
                ),
            },
        }
        cls_ok = oracle.classify_surface_snapshot(snap, s_entries)
        self.assertEqual(cls_ok["verdict"], "listed")
        self.assertEqual(cls_ok["system_prompt"]["verdict"], "listed")

        # Unlisted extra line flips snapshot to `differs` and triggers non-zero exit
        snap_bad = json.loads(json.dumps(snap))
        snap_bad["system_prompt"]["diff"] = oracle.unified_diff(
            "You are omp's trusted coding assistant.\nKeep this secret rule.\n",
            "You are oh-my-pig's trusted coding assistant.\n",
            "omp:system_prompt",
            "oh-my-pig:system_prompt",
        )
        cls_bad = oracle.classify_surface_snapshot(snap_bad, s_entries)
        self.assertEqual(cls_bad["verdict"], "differs")
        totals_bad = {"cases_failed": 0, "network_attempts": 0, "leaked_pids": 0, "calls_differs": 0, "calls_details_only": 0, "recorded_differs": 0, "surfaces_differs": 1, "child_surfaces_differs": 0, "followups_differs": 0, "calls_timing": 0}
        self.assertEqual(oracle.oracle_exit_code(totals_bad, mode="both"), 1)

    def test_r16_piped_widget_lines_child_eval_and_real_model_only_stale(self) -> None:
        """Verify piped-job fallback widget `lines` scoping, child `eval` description listing, and `real_model_only` stale exclusion."""
        d_entries = oracle.load_details_allowlist()
        s_entries = oracle.load_surface_allowlist()
        t_entries = oracle.load_text_allowlist()

        # 1. Piped bash job bg_2 in bash-notify-streams-cancel#7: lines "4" vs "2" matches
        piped_omp = {
            "jobs": [{
                "id": "bg_2", "type": "bash", "label": "(echo pipe_one; sleep 1.5; echo pipe_two) | cat",
                "widget": {"blocks": [{"kind": "stat", "items": [{"label": "lines", "text": "4"}, {"label": "lines/s", "text": "<RATE>"}]}], "state": "fallback"},
            }]
        }
        piped_pig = {
            "jobs": [{
                "id": "bg_2", "type": "bash", "label": "(echo pipe_one; sleep 1.5; echo pipe_two) | cat",
                "widget": {"blocks": [{"kind": "stat", "items": [{"label": "lines", "text": "2"}, {"label": "lines/s", "text": "<RATE>"}]}], "state": "fallback"},
            }]
        }
        _, unexp_ok, hits_ok = oracle.classify_details_diffs(
            "wait", piped_omp, piped_pig, d_entries, case_name="bash-notify-streams-cancel", call_index=7
        )
        self.assertEqual(unexp_ok, [])
        self.assertEqual(len(hits_ok), 1)

        # Mutated value ("4" vs "3") or non-piped job or wrong stat item MUST fail
        piped_bad_val = json.loads(json.dumps(piped_pig))
        piped_bad_val["jobs"][0]["widget"]["blocks"][0]["items"][0]["text"] = "3"
        _, unexp_bad_val, _ = oracle.classify_details_diffs(
            "wait", piped_omp, piped_bad_val, d_entries, case_name="bash-notify-streams-cancel", call_index=7
        )
        self.assertEqual([d["path"] for d in unexp_bad_val], ["jobs[].widget.blocks[].items[].text"])

        non_piped_omp = json.loads(json.dumps(piped_omp))
        non_piped_pig = json.loads(json.dumps(piped_pig))
        non_piped_omp["jobs"][0]["label"] = "echo one; echo two"
        non_piped_pig["jobs"][0]["label"] = "echo one; echo two"
        _, unexp_non_piped, _ = oracle.classify_details_diffs(
            "wait", non_piped_omp, non_piped_pig, d_entries, case_name="bash-notify-streams-cancel", call_index=7
        )
        self.assertEqual([d["path"] for d in unexp_non_piped], ["jobs[].widget.blocks[].items[].text"])

        # 2. Child eval Session tool names line is listed ONLY for child surfaces, NOT parent surfaces
        eval_diff = oracle.unified_diff(
            "Session tool names: read, bash, edit, eval, glob, grep, task, wait, web_search, write, ast_edit, debug, lsp.\n",
            "Session tool names: read, bash, edit, eval, glob, grep, wait, web_search, write, ast_edit, debug, lsp.\n",
        )
        ok_child, _, _ = oracle.classify_surface_diff(("child_tool_description", "tool_description"), eval_diff, s_entries, target="eval")
        ok_parent, _, _ = oracle.classify_surface_diff("tool_description", eval_diff, s_entries, target="eval")
        self.assertTrue(ok_child)
        self.assertFalse(ok_parent)

        # 3. task-errors-and-agent-urls#0 Searched: entry matches exact diff and rejects extra text
        diff_searched = oracle.unified_diff(
            'Task Bad failed preflight: Unknown agent "no-such-agent". Available: scout, reviewer, security-reviewer, task, sonic. Searched: ~/.omp/agent/agents\n',
            'Task Bad failed preflight: Unknown agent "no-such-agent". Available: scout, reviewer, security-reviewer, task, sonic. Searched: ~/.pig/agent/agents, ~/.omp/agent/agents\n',
        )
        ok_s, _ = oracle.classify_text_diff("task-errors-and-agent-urls", 0, "task", diff_searched, t_entries)
        self.assertTrue(ok_s)
        diff_searched_bad = oracle.unified_diff(
            'Task Bad failed preflight: Unknown agent "no-such-agent". Available: scout, reviewer, security-reviewer, task, sonic. Searched: ~/.omp/agent/agents\n',
            'Task Bad failed preflight: Unknown agent "no-such-agent". Available: scout, reviewer, security-reviewer, task, sonic. Searched: ~/.wrong/agents\n',
        )
        ok_s_bad, _ = oracle.classify_text_diff("task-errors-and-agent-urls", 0, "task", diff_searched_bad, t_entries)
        self.assertFalse(ok_s_bad)

        # 4. real_model_only entries are excluded from details_allowlist_stale
        totals, d_usage, _, _, _ = oracle._compute_totals_and_usage(
            case_reports=[], surface_snapshots=[],
            details_allowlist=[{"tool": "wait", "path": "jobs[].widget", "side": "omp-only", "class": "LISTED", "ref": "x:1", "real_model_only": True}],
            text_allowlist=[], recorded_allowlist=[], surface_allowlist=[],
            network_attempts=[], leaked_pids=[],
        )
        self.assertEqual(totals["details_allowlist_stale"], 0)
        self.assertTrue(d_usage[0]["real_model_only"])
        self.assertFalse(d_usage[0]["stale"])

    def test_child_surfaces_paired_by_id_arrival_order_peer_order_and_missing(self) -> None:
        """Batch children pair by agent id regardless of arrival order; literal peer order diffs and one-sided missing children fail."""
        import urllib.request

        prompt_one = (
            "You are omp's trusted coding assistant.\n"
            "# Peers\n"
            'Message peers via `write` with `path: "agent://<id>"` and `content`. Your id is `One`. Peers when this list was written:\n'
            "- `Two` — task (sub, running)\n"
            "- `Main` — main (main, running)\n"
        )
        prompt_two = (
            "You are omp's trusted coding assistant.\n"
            "# Peers\n"
            'Message peers via `write` with `path: "agent://<id>"` and `content`. Your id is `Two`. Peers when this list was written:\n'
            "- `One` — task (sub, running)\n"
            "- `Main` — main (main, running)\n"
        )
        tools_payload = [
            {"type": "function", "function": {"name": "read", "description": "Read", "parameters": {"type": "object"}}},
        ]
        tools_norm = [{"name": "read", "description": "Read", "parameters": {"type": "object"}}]

        # 1. Server captures ALL children keyed by id even when they arrive in opposite orders on each side
        def _capture_in_order(prompts: list[str]) -> dict[str, dict]:
            state = oracle.OracleSessionState(
                turns=[], parent_marker="[oracle-case:batch]", child_reply="OK",
                child_delay_ms=0, child_error=None, replacements={},
            )
            srv = oracle.OracleLLMServer(state)
            port = srv.start()
            try:
                url = f"http://127.0.0.1:{port}/v1/chat/completions"
                for sp in prompts:
                    body = json.dumps({
                        "model": "proof-1",
                        "messages": [
                            {"role": "system", "content": sp},
                            {"role": "user", "content": "Reply."},
                        ],
                        "tools": tools_payload,
                    }).encode("utf-8")
                    req = urllib.request.Request(url, data=body, headers={"Content-Type": "application/json"})
                    with urllib.request.urlopen(req, timeout=5) as resp:
                        resp.read()
                return {
                    cid: {"id": cid, "system_prompt_normalized": c["system_prompt"], "tools": c["tools"]}
                    for cid, c in state.child_surfaces_by_id.items()
                }
            finally:
                srv.stop()

        omp_surfs = _capture_in_order([prompt_one, prompt_two])
        pig_surfs = _capture_in_order([prompt_two, prompt_one])
        self.assertEqual(sorted(omp_surfs.keys()), ["One", "Two"])
        self.assertEqual(sorted(pig_surfs.keys()), ["One", "Two"])

        def _side(surfs: dict[str, dict]) -> dict:
            return {
                "rc": 0, "setup_rc": 0, "timed_out": False, "network_attempts": [], "leaked_pids": [],
                "added_messages_normalized": [], "calls": [], "child_surfaces": surfs,
            }

        case = {"name": "batch-order-test", "calls": []}
        cmp_same = oracle.compare_case(case, _side(omp_surfs), _side(pig_surfs))
        self.assertEqual(cmp_same["child_surface"]["verdict"], "identical")
        self.assertEqual(cmp_same["child_surface"]["ids"], ["One", "Two"])
        self.assertEqual(cmp_same["child_surface"]["by_id"]["One"]["verdict"], "identical")
        self.assertEqual(cmp_same["child_surface"]["by_id"]["Two"]["verdict"], "identical")

        # 2. Same ids, different peer order inside One's prompt -> MUST still be `differs` (literal comparison)
        prompt_one_bad_order = (
            "You are omp's trusted coding assistant.\n"
            "# Peers\n"
            'Message peers via `write` with `path: "agent://<id>"` and `content`. Your id is `One`. Peers when this list was written:\n'
            "- `Main` — main (main, running)\n"
            "- `Two` — task (sub, running)\n"
        )
        pig_bad = {
            "One": {"id": "One", "system_prompt_normalized": prompt_one_bad_order, "tools": tools_norm},
            "Two": {"id": "Two", "system_prompt_normalized": prompt_two, "tools": tools_norm},
        }
        cmp_bad = oracle.compare_case(case, _side(omp_surfs), _side(pig_bad))
        self.assertEqual(cmp_bad["child_surface"]["verdict"], "differs")
        self.assertEqual(cmp_bad["child_surface"]["by_id"]["One"]["verdict"], "differs")
        self.assertEqual(cmp_bad["child_surface"]["by_id"]["Two"]["verdict"], "identical")

        # 3. Child present on one side only -> verdict `missing` and non-zero exit code
        pig_missing = {"One": {"id": "One", "system_prompt_normalized": prompt_one, "tools": tools_norm}}
        cmp_miss = oracle.compare_case(case, _side(omp_surfs), _side(pig_missing))
        self.assertEqual(cmp_miss["child_surface"]["verdict"], "missing")
        self.assertEqual(cmp_miss["child_surface"]["by_id"]["Two"]["verdict"], "missing")
        self.assertEqual(cmp_miss["child_surface"]["by_id"]["Two"]["missing_side"], "pig")
        totals_miss, _, _, _, _ = oracle._compute_totals_and_usage(
            case_reports=[cmp_miss], surface_snapshots=[],
            details_allowlist=[], text_allowlist=[], recorded_allowlist=[], surface_allowlist=[],
            network_attempts=[], leaked_pids=[],
        )
        self.assertEqual(totals_miss["child_surfaces_identical"], 1)
        self.assertEqual(totals_miss["child_surfaces_missing"], 1)
        self.assertEqual(oracle.oracle_exit_code(totals_miss, mode="both"), 1)

    def test_peers_sibling_idle_vs_running_narrow_surface_rule(self) -> None:
        """`peers_sibling_idle_vs_running` lists ONLY same-batch task sibling idle(omp) vs running(pig) in # Peers; rejects reversed, Main, wrong id, or reordered roster."""
        s_entries = oracle.load_surface_allowlist()

        def _mk_prompt(peers_lines: list[str], self_id: str = "Two") -> str:
            return (
                "You are omp's trusted coding assistant.\n"
                "⁂ Coop\n"
                "You are operating on a piece of work assigned to you by the main agent.\n"
                "# Peers\n"
                f'Message peers via `write` with `path: "agent://<id>"` and `content`. Your id is `{self_id}`. Peers when this list was written:\n'
                + "\n".join(peers_lines)
                + "\n\nYour assignment sets your files and scope; stay inside it.\n"
            )

        # 1. Exact expected pair: omp has Main(running), One(idle); port has One(running), Main(running) -> listed
        omp_ok = _mk_prompt(["- `Main` — main (main, running)", "- `One` — task (sub, idle)"])
        pig_ok = _mk_prompt(["- `One` — task (sub, running)", "- `Main` — main (main, running)"])
        diff_ok = oracle.unified_diff(omp_ok, pig_ok, "omp:child_sys:Two", "oh-my-pig:child_sys:Two")
        ok, hits, unexp = oracle.classify_surface_diff(
            "child_system_prompt", diff_ok, s_entries,
            target="child", case_name="task-errors-and-agent-urls",
            self_child_id="Two", sibling_child_ids=["One"],
        )
        self.assertTrue(ok, f"expected listed, got unexp={unexp}")
        self.assertEqual(len(hits), 1)

        # 2. Reversed statuses (omp running, port idle) -> differs
        diff_rev = oracle.unified_diff(pig_ok, omp_ok, "omp:child_sys:Two", "oh-my-pig:child_sys:Two")
        ok_rev, _, _ = oracle.classify_surface_diff(
            "child_system_prompt", diff_rev, s_entries,
            target="child", case_name="task-errors-and-agent-urls",
            self_child_id="Two", sibling_child_ids=["One"],
        )
        self.assertFalse(ok_rev)

        # 3. Different peer id (omp One idle, port Three running; or unknown sibling Three) -> differs
        pig_other_id = _mk_prompt(["- `Three` — task (sub, running)", "- `Main` — main (main, running)"])
        diff_other = oracle.unified_diff(omp_ok, pig_other_id, "omp:child_sys:Two", "oh-my-pig:child_sys:Two")
        ok_other, _, _ = oracle.classify_surface_diff(
            "child_system_prompt", diff_other, s_entries,
            target="child", case_name="task-errors-and-agent-urls",
            self_child_id="Two", sibling_child_ids=["One", "Three"],
        )
        self.assertFalse(ok_other)

        # Non-sibling peer id (not in sibling_child_ids) -> differs
        ok_nonsib, _, _ = oracle.classify_surface_diff(
            "child_system_prompt", diff_ok, s_entries,
            target="child", case_name="task-errors-and-agent-urls",
            self_child_id="Two", sibling_child_ids=["Other"],
        )
        self.assertFalse(ok_nonsib)

        # 4. Main status change -> differs
        omp_main = _mk_prompt(["- `One` — task (sub, running)", "- `Main` — main (main, idle)"])
        pig_main = _mk_prompt(["- `One` — task (sub, running)", "- `Main` — main (main, running)"])
        diff_main = oracle.unified_diff(omp_main, pig_main, "omp:child_sys:Two", "oh-my-pig:child_sys:Two")
        ok_main, _, _ = oracle.classify_surface_diff(
            "child_system_prompt", diff_main, s_entries,
            target="child", case_name="task-errors-and-agent-urls",
            self_child_id="Two", sibling_child_ids=["One"],
        )
        self.assertFalse(ok_main)

        # 5. Reordered roster (idle placed before running, or two running peers swapped) -> differs
        omp_reord = _mk_prompt(["- `One` — task (sub, idle)", "- `Main` — main (main, running)"])
        diff_reord = oracle.unified_diff(omp_reord, pig_ok, "omp:child_sys:Two", "oh-my-pig:child_sys:Two")
        ok_reord, _, _ = oracle.classify_surface_diff(
            "child_system_prompt", diff_reord, s_entries,
            target="child", case_name="task-errors-and-agent-urls",
            self_child_id="Two", sibling_child_ids=["One"],
        )
        self.assertFalse(ok_reord)

        omp_swap = _mk_prompt(["- `One` — task (sub, running)", "- `Main` — main (main, running)"])
        pig_swap = _mk_prompt(["- `Main` — main (main, running)", "- `One` — task (sub, running)"])
        diff_swap = oracle.unified_diff(omp_swap, pig_swap, "omp:child_sys:Two", "oh-my-pig:child_sys:Two")
        ok_swap, _, _ = oracle.classify_surface_diff(
            "child_system_prompt", diff_swap, s_entries,
            target="child", case_name="task-errors-and-agent-urls",
            self_child_id="Two", sibling_child_ids=["One"],
        )
        self.assertFalse(ok_swap)

class TestDrawingHarnessFixes(unittest.TestCase):
    """Unit tests covering every drawing-triage harness fix (compare.py, llm_server.py, ansi_capture.py)."""

    def test_compare_exact_tool_match_does_not_match_ask_inside_task(self) -> None:
        """Finding #9: `ask` must NEVER match a `task` card by substring."""
        doc = compare.TspDocument(
            surface_id="s:1",
            surface_role="omp.session",
            nodes={
                "s:1": compare.TspNode(id="s:1", kind="surface", props={"role": "omp.session"}, children=["main"]),
                "main": compare.TspNode(id="main", kind="col", props={}, children=["t1", "t2"]),
                "t1": compare.TspNode(
                    id="t1", kind="tool",
                    props={"role": "omp.tool.task", "title": "Task", "target": "1 agent"},
                    children=[],
                ),
                "t2": compare.TspNode(
                    id="t2", kind="tool",
                    props={"role": "omp.tool.ask", "title": "Ask", "target": "Choose a mode:"},
                    children=[],
                ),
            },
        )
        ask_nodes = compare.find_tool_nodes(doc, "ask")
        self.assertEqual([n.id for n in ask_nodes], ["t2"])
        task_nodes = compare.find_tool_nodes(doc, "task")
        self.assertEqual([n.id for n in task_nodes], ["t1"])

        # Without an ask node, `find_tool_nodes(doc, "ask")` MUST return [] (never fall back to task)
        doc_only_task = compare.TspDocument(
            surface_id="s:1",
            surface_role="omp.session",
            nodes={
                "s:1": compare.TspNode(id="s:1", kind="surface", props={"role": "omp.session"}, children=["main"]),
                "main": compare.TspNode(id="main", kind="col", props={}, children=["t1"]),
                "t1": compare.TspNode(
                    id="t1", kind="tool",
                    props={"role": "omp.tool.task", "title": "Task", "target": "1 agent"},
                    children=[],
                ),
            },
        )
        self.assertEqual(compare.find_tool_nodes(doc_only_task, "ask"), [])

    def test_compare_head_props_catch_edit_target_and_no_composer_bar_fallback(self) -> None:
        """Finding #9 + #2: compare_part diffs head props (e.g. target path) and find_status_nodes skips omp.composer.bar."""
        doc_omp = compare.TspDocument(
            surface_id="s:1",
            surface_role="omp.session",
            nodes={
                "s:1": compare.TspNode(id="s:1", kind="surface", props={"role": "omp.session"}, children=["main", "dock"]),
                "main": compare.TspNode(id="main", kind="col", props={}, children=["e1"]),
                "e1": compare.TspNode(
                    id="e1", kind="tool",
                    props={"role": "omp.tool.edit", "target": "/tmp/run/omp/work/notes.txt:2", "status": "done"},
                    children=["d1"],
                ),
                "d1": compare.TspNode(id="d1", kind="diff", props={}, children=[]),
                "dock": compare.TspNode(id="dock", kind="col", props={}, children=["ed"]),
                "ed": compare.TspNode(id="ed", kind="col", props={"role": "omp.editor"}, children=["bar"]),
                "bar": compare.TspNode(id="bar", kind="row", props={"role": "omp.composer.bar"}, children=["st"]),
                "st": compare.TspNode(id="st", kind="status", props={"role": "omp.composer.extras"}, children=[]),
            },
        )
        doc_pig = compare.TspDocument(
            surface_id="pig",
            surface_role="pig.session",
            nodes={
                "pig": compare.TspNode(id="pig", kind="surface", props={"role": "pig.session"}, children=["main"]),
                "main": compare.TspNode(id="main", kind="col", props={}, children=["e1"]),
                "e1": compare.TspNode(
                    id="e1", kind="tool",
                    props={"role": "omp.tool.edit", "target": "notes.txt:2", "status": "done"},
                    children=["d1"],
                ),
                "d1": compare.TspNode(id="d1", kind="diff", props={}, children=[]),
            },
        )
        dom = {"regions": {"sf-main": [{"kind": "tool", "class": "sf sf-tool", "children": []}]}}
        omp_info = compare.summarize_part(doc_omp, dom, "sf-main", [doc_omp.nodes["e1"]])
        pig_info = compare.summarize_part(doc_pig, dom, "sf-main", [doc_pig.nodes["e1"]])
        res = compare.compare_part("tool card: edit", "05-tool-edit", doc_omp, doc_pig, omp_info, pig_info)
        self.assertEqual(res["verdict"], "differs")
        self.assertIn("target", res["details"])
        self.assertIn("<WORK>/notes.txt:2", res["details"])
        # find_status_nodes must NOT return the composer's inner status bar
        self.assertEqual(compare.find_status_nodes(doc_omp), [])

    def test_compare_subdiff_allowlist_and_extra_prop_yields_differs(self) -> None:
        """Narrow drawing allowlist: allowlisted sub-diff alone => `expected`, plus ONE extra prop diff or tampered command below `$kind` => `differs`."""
        allow = compare.load_drawing_allowlist()

        def _make_bash_doc(
            cmd_kind: str,
            extra_output_prop: dict | None = None,
            cmd_text: str = "printf 'alpha\\nbeta\\n'",
            cmd_lang: str = "bash",
            cmd_wrap: bool | None = True,
            cmd_preview: dict | None = None,
        ) -> compare.TspDocument:
            out_props = {"role": "omp.tool.bash.output", "follow": True, "preview": {"lines": 10}, "text": "alpha\nbeta"}
            if extra_output_prop:
                out_props.update(extra_output_prop)
            if cmd_kind == "shell":
                cmd_props: dict = {"role": "omp.tool.bash.command", "lang": "bash", "preview": {"lines": 6}, "presentation": {"view": "pseudo"}}
            else:
                cmd_props = {"role": "omp.tool.bash.command", "lang": cmd_lang, "text": cmd_text}
                if cmd_wrap is not None:
                    cmd_props["wrap"] = cmd_wrap
                cmd_props["preview"] = cmd_preview if cmd_preview is not None else {"lines": 6}
            return compare.TspDocument(
                surface_id="s:1",
                surface_role="omp.session",
                nodes={
                    "s:1": compare.TspNode(id="s:1", kind="surface", props={"role": "omp.session"}, children=["main"]),
                    "main": compare.TspNode(id="main", kind="col", props={}, children=["t1"]),
                    "t1": compare.TspNode(
                        id="t1", kind="tool",
                        props={
                            "role": "omp.tool.bash", "name": "bash", "title": "Bash",
                            "command": "printf 'alpha\\nbeta\\n'",
                            "frame": "inline", "status": "done", "collapsed": True, "collapsible": True,
                        },
                        children=["run"],
                    ),
                    "run": compare.TspNode(id="run", kind="col", props={"role": "omp.run", "gap": "none"}, children=["cmd", "out"]),
                    "cmd": compare.TspNode(id="cmd", kind=cmd_kind, props=cmd_props, children=[]),
                    "out": compare.TspNode(id="out", kind="ansi", props=out_props, children=[]),
                },
            )

        dom = {"regions": {"sf-main": [{"kind": "tool", "class": "sf sf-tool", "children": []}]}}
        omp_doc = _make_bash_doc("shell")
        pig_ok = _make_bash_doc("code")
        omp_info = compare.summarize_part(omp_doc, dom, "sf-main", [omp_doc.nodes["t1"]])
        pig_ok_info = compare.summarize_part(pig_ok, dom, "sf-main", [pig_ok.nodes["t1"]])

        # 1. ONLY the allowlisted `shell` vs `code` sub-diff with matching command/lang/wrap/preview -> expected
        res_expected = compare.compare_part("tool card: bash", "04-tool-bash", omp_doc, pig_ok, omp_info, pig_ok_info, allowlist=allow)
        self.assertEqual(res_expected["verdict"], "expected")
        self.assertEqual(len(res_expected["allowlisted_diffs"]), 1)
        self.assertEqual(len(res_expected["unexpected_diffs"]), 0)

        # 2. Audit Finding 8 demonstrating input: port bash command mutated to `rm -rf / # WRONG COMMAND`,
        #    lang='cobol', wrap removed -> MUST be `differs`, NEVER swallowed by `$kind`!
        pig_wrong_cmd = _make_bash_doc("code", cmd_text="rm -rf / # WRONG COMMAND", cmd_lang="cobol", cmd_wrap=None)
        pig_wrong_info = compare.summarize_part(pig_wrong_cmd, dom, "sf-main", [pig_wrong_cmd.nodes["t1"]])
        res_wrong = compare.compare_part("tool card: bash", "04-tool-bash", omp_doc, pig_wrong_cmd, omp_info, pig_wrong_info, allowlist=allow)
        self.assertEqual(res_wrong["verdict"], "differs")
        unexp_props = {d["prop"] for d in res_wrong["unexpected_diffs"]}
        self.assertIn("$projection.command", unexp_props)
        self.assertIn("lang", unexp_props)
        self.assertIn("wrap", unexp_props)

        # 3. Same allowlisted sub-diff PLUS one extra prop difference on a sibling node (`preview.lines: 10` vs `5`) -> differs!
        pig_extra = _make_bash_doc("code", extra_output_prop={"preview": {"lines": 5}})
        pig_extra_info = compare.summarize_part(pig_extra, dom, "sf-main", [pig_extra.nodes["t1"]])
        res_differs = compare.compare_part("tool card: bash", "04-tool-bash", omp_doc, pig_extra, omp_info, pig_extra_info, allowlist=allow)
        self.assertEqual(res_differs["verdict"], "differs")
        self.assertEqual(len(res_differs["allowlisted_diffs"]), 1)
        self.assertEqual(len(res_differs["unexpected_diffs"]), 1)
        self.assertEqual(res_differs["unexpected_diffs"][0]["node_path"], "col[omp.run] > ansi[omp.tool.bash.output]")
        self.assertEqual(res_differs["unexpected_diffs"][0]["prop"], "preview")

        # 4. Allowlisted host-absent part (`slash menu`) yields `expected`
        doc_omp_sm = compare.TspDocument(
            surface_id="s:1", surface_role="omp.session",
            nodes={
                "s:1": compare.TspNode(id="s:1", kind="surface", props={"role": "omp.session"}, children=["layer"]),
                "layer": compare.TspNode(id="layer", kind="col", props={}, children=["ov"]),
                "ov": compare.TspNode(id="ov", kind="overlay", props={"role": "omp.autocomplete"}, children=[]),
            },
        )
        dom_sm = {"regions": {"sf-layer": [{"kind": "overlay", "class": "sf sf-overlay", "children": []}]}}
        sm_info = compare.summarize_part(doc_omp_sm, dom_sm, "sf-layer", [doc_omp_sm.nodes["ov"]])
        res_sm = compare.compare_part("slash menu", "17a-slash-menu", doc_omp_sm, pig_ok, sm_info, None, allowlist=allow)
        self.assertEqual(res_sm["verdict"], "expected")

        # 5. Every repo drawing_allowlist.json & terminal_allowlist.json entry resolves uniquely via (ref_file, ref_term)
        for fname, entries in [
            ("drawing_allowlist.json", allow),
            ("terminal_allowlist.json", ansi_capture.load_terminal_allowlist()),
        ]:
            self.assertGreater(len(entries), 0, f"{fname} must not be empty")
            for e in entries:
                self.assertIn("ref_file", e)
                self.assertIn("ref_term", e)
                self.assertNotIn(":", e["ref_file"])
                if not e.get("ref_unverified"):
                    self.assertIsInstance(e["ref_line"], int)
                    self.assertRegex(e["ref"], r":\d+$")

    def test_compare_audit_finding8_pinned_values_and_ask_options_projection(self) -> None:
        """Audit Finding 8: pinned `value` entries reject mutated wordmark/version/logo/tip/model/thinking; ask `$kind` still compares title & options."""
        allow = compare.load_drawing_allowlist()
        dom_card = {"regions": {"sf-main": [{"kind": "card", "class": "sf sf-card", "children": []}]}}

        def _make_welcome(alt: str, wordmark: list, version: list, tip: str) -> compare.TspDocument:
            return compare.TspDocument(
                surface_id="s:1", surface_role="omp.session",
                nodes={
                    "s:1": compare.TspNode(id="s:1", kind="surface", props={"role": "omp.session"}, children=["main"]),
                    "main": compare.TspNode(id="main", kind="col", props={}, children=["w"]),
                    "w": compare.TspNode(id="w", kind="card", props={"role": "omp.welcome"}, children=["lk", "tp"]),
                    "lk": compare.TspNode(id="lk", kind="row", props={"role": "omp.welcome.lockup"}, children=["lg", "mk"]),
                    "lg": compare.TspNode(id="lg", kind="image", props={"role": "omp.welcome.logo", "alt": alt}, children=[]),
                    "mk": compare.TspNode(id="mk", kind="col", props={"role": "omp.welcome.mark"}, children=["wm", "vr"]),
                    "wm": compare.TspNode(id="wm", kind="text", props={"role": "omp.welcome.wordmark", "spans": wordmark}, children=[]),
                    "vr": compare.TspNode(id="vr", kind="text", props={"role": "omp.welcome.version", "spans": version}, children=[]),
                    "tp": compare.TspNode(id="tp", kind="row", props={"role": "omp.welcome.tip"}, children=["tt"]),
                    "tt": compare.TspNode(id="tt", kind="text", props={"role": "omp.welcome.tip-text", "text": tip}, children=[]),
                },
            )

        omp_w = _make_welcome("omp", [{"t": "omp", "s": "strong"}], [{"t": "v19.1.1", "s": "dim mono"}], 'Tired of typing "keep going"? Just send a \'.\'')
        pig_ok_w = _make_welcome("oh-my-pig", [{"t": "oh-my-pig", "s": "strong"}], [{"t": "v0.1.0", "s": "dim mono"}], "Use /jobs to inspect or cancel background tasks and services")
        res_ok = compare.compare_part(
            "welcome banner", "00-started", omp_w, pig_ok_w,
            compare.summarize_part(omp_w, dom_card, "sf-main", [omp_w.nodes["w"]]),
            compare.summarize_part(pig_ok_w, dom_card, "sf-main", [pig_ok_w.nodes["w"]]),
            allowlist=allow,
        )
        self.assertEqual(res_ok["verdict"], "expected")

        # Audit Finding 8 demonstrating input (3): wrong wordmark style, empty version, empty tip, empty logo alt -> differs!
        pig_bad_w = _make_welcome("", [{"t": "totally wrong", "s": "error blink"}], [{"t": "", "s": "dim mono"}], "")
        res_bad = compare.compare_part(
            "welcome banner", "00-started", omp_w, pig_bad_w,
            compare.summarize_part(omp_w, dom_card, "sf-main", [omp_w.nodes["w"]]),
            compare.summarize_part(pig_bad_w, dom_card, "sf-main", [pig_bad_w.nodes["w"]]),
            allowlist=allow,
        )
        self.assertEqual(res_bad["verdict"], "differs")
        self.assertEqual(len(res_bad["unexpected_diffs"]), 4)

        # Audit Finding 8 demonstrating input (2): ask form with ALL options removed from picker -> differs!
        omp_ask = compare.TspDocument(
            surface_id="s:1", surface_role="omp.session",
            nodes={
                "s:1": compare.TspNode(id="s:1", kind="surface", props={"role": "omp.session"}, children=["main"]),
                "main": compare.TspNode(id="main", kind="col", props={}, children=["card"]),
                "card": compare.TspNode(id="card", kind="col", props={"role": "omp.ask.card"}, children=["q", "o1", "o2"]),
                "q": compare.TspNode(id="q", kind="text", props={"role": "omp.ask.text", "spans": [{"t": "Choose a mode:"}]}, children=[]),
                "o1": compare.TspNode(id="o1", kind="item", props={"role": "omp.ask.option", "label": [{"t": "Fast"}]}, children=[]),
                "o2": compare.TspNode(id="o2", kind="item", props={"role": "omp.ask.option", "label": [{"t": "Thorough"}]}, children=[]),
            },
        )
        pig_ask_empty = compare.TspDocument(
            surface_id="pig", surface_role="pig.session",
            nodes={
                "pig": compare.TspNode(id="pig", kind="surface", props={"role": "pig.session"}, children=["layer"]),
                "layer": compare.TspNode(id="layer", kind="col", props={}, children=["pk"]),
                "pk": compare.TspNode(id="pk", kind="picker", props={"title": "Choose a mode:", "items": []}, children=[]),
            },
        )
        dom_ask = {"regions": {"sf-main": [{"kind": "col", "class": "sf sf-col", "children": []}], "sf-layer": [{"kind": "picker", "class": "sf sf-picker", "children": []}]}}
        res_ask = compare.compare_part(
            "ask form (live)", "08a-ask-form", omp_ask, pig_ask_empty,
            compare.summarize_part(omp_ask, dom_ask, "sf-main", [omp_ask.nodes["card"]]),
            compare.summarize_part(pig_ask_empty, dom_ask, "sf-layer", [pig_ask_empty.nodes["pk"]]),
            allowlist=allow,
        )
        self.assertEqual(res_ask["verdict"], "differs")
        self.assertTrue(any(d["prop"] == "$projection.options" for d in res_ask["unexpected_diffs"]))

    def test_compare_audit_finding9_count_and_finding21_ephemeral_and_noop(self) -> None:
        """Audit Findings 9 & 21: part count diffs flag `$count`; `took`/`age`/`stopped` presence & non-numeric type are caught; no-op defaults are counted."""
        dom = {"regions": {"sf-main": [{"kind": "tool", "class": "sf sf-tool", "children": []}]}}
        omp_doc = compare.TspDocument(
            surface_id="s:1", surface_role="omp.session",
            nodes={
                "s:1": compare.TspNode(id="s:1", kind="surface", props={"role": "omp.session"}, children=["main"]),
                "main": compare.TspNode(id="main", kind="col", props={}, children=["t1"]),
                "t1": compare.TspNode(id="t1", kind="tool", props={"role": "omp.tool.todo", "took": 10, "meta": []}, children=[]),
            },
        )
        # Port has non-numeric `took` and 2 todo cards -> both `$count` and `took` type caught, and `meta:[]` counted as noop_default!
        pig_doc = compare.TspDocument(
            surface_id="pig", surface_role="pig.session",
            nodes={
                "pig": compare.TspNode(id="pig", kind="surface", props={"role": "pig.session"}, children=["main"]),
                "main": compare.TspNode(id="main", kind="col", props={}, children=["t1", "t2"]),
                "t1": compare.TspNode(id="t1", kind="tool", props={"role": "omp.tool.todo", "took": "not-a-number"}, children=[]),
                "t2": compare.TspNode(id="t2", kind="tool", props={"role": "omp.tool.todo", "took": "not-a-number"}, children=[]),
            },
        )
        res = compare.compare_part(
            "tool card: todo", "07-tool-todo", omp_doc, pig_doc,
            compare.summarize_part(omp_doc, dom, "sf-main", [omp_doc.nodes["t1"]]),
            compare.summarize_part(pig_doc, dom, "sf-main", [pig_doc.nodes["t1"], pig_doc.nodes["t2"]]),
        )
        self.assertEqual(res["verdict"], "differs")
        unexp_props = {d["prop"] for d in res["unexpected_diffs"]}
        self.assertIn("$count", unexp_props)
        self.assertIn("took", unexp_props)
        self.assertEqual(len(res["noop_default_diffs"]), 1)
        self.assertEqual(res["noop_default_diffs"][0]["prop"], "meta")

        # `/private/tmp` vs `/tmp` must NOT normalize to the same string (Finding 2)
        self.assertEqual(compare.normalize_prop_value("/tmp/run/omp/work/notes.txt"), "<WORK>/notes.txt")
        self.assertEqual(compare.normalize_prop_value("/private/tmp/run/pig/work/notes.txt"), "/private<WORK>/notes.txt")

    def test_compare_thinking_head_timing_class_bounded_per_finding14(self) -> None:
        """Audit Finding 14: only two durations within 5s classify as `timing`; `Thought` vs `Thought for ...` and `1s` vs `3h` MUST be `differs`."""
        def _make_thinking_doc(head_text: str, head_style: str = "muted", body_text: str = "Reasoning.") -> compare.TspDocument:
            return compare.TspDocument(
                surface_id="s:1",
                surface_role="omp.session",
                nodes={
                    "s:1": compare.TspNode(id="s:1", kind="surface", props={"role": "omp.session"}, children=["main"]),
                    "main": compare.TspNode(id="main", kind="col", props={}, children=["sec"]),
                    "sec": compare.TspNode(
                        id="sec", kind="section",
                        props={"role": "omp.thinking", "collapsible": True, "collapsed": True},
                        children=["hd", "bd"],
                    ),
                    "hd": compare.TspNode(id="hd", kind="text", props={"spans": [{"t": head_text, "s": head_style}]}, children=[]),
                    "bd": compare.TspNode(id="bd", kind="md", props={"text": body_text}, children=[]),
                },
            )

        dom = {"regions": {"sf-main": [{"kind": "section", "class": "sf sf-section", "children": []}]}}
        omp_1s = _make_thinking_doc("Thought for 1s")
        pig_2s = _make_thinking_doc("Thought for 2s")
        omp_info = compare.summarize_part(omp_1s, dom, "sf-main", [omp_1s.nodes["sec"]])
        pig_2s_info = compare.summarize_part(pig_2s, dom, "sf-main", [pig_2s.nodes["sec"]])

        # 1. Both sides carry durations within <=5s -> `timing`
        res_timing = compare.compare_part("thinking block", "02b-thinking", omp_1s, pig_2s, omp_info, pig_2s_info)
        self.assertEqual(res_timing["verdict"], "timing")
        self.assertEqual(len(res_timing["timing_diffs"]), 1)
        self.assertEqual(len(res_timing["unexpected_diffs"]), 0)
        self.assertIn("[TIMING]", res_timing["details"])

        # 2. Audit Finding 14 demonstrating input: `Thought for 2h 5m` vs `Thought` -> MUST be `differs`!
        omp_long = _make_thinking_doc("Thought for 2h 5m")
        pig_bare = _make_thinking_doc("Thought")
        res_bare = compare.compare_part(
            "thinking block", "02b-thinking", omp_long, pig_bare,
            compare.summarize_part(omp_long, dom, "sf-main", [omp_long.nodes["sec"]]),
            compare.summarize_part(pig_bare, dom, "sf-main", [pig_bare.nodes["sec"]]),
        )
        self.assertEqual(res_bare["verdict"], "differs")
        self.assertEqual(len(res_bare["timing_diffs"]), 0)

        # 3. Audit Finding 14 demonstrating input: `Thought for 1s` vs `Thought for 3h` -> MUST be `differs`!
        pig_3h = _make_thinking_doc("Thought for 3h")
        res_3h = compare.compare_part(
            "thinking block", "02b-thinking", omp_1s, pig_3h, omp_info,
            compare.summarize_part(pig_3h, dom, "sf-main", [pig_3h.nodes["sec"]]),
        )
        self.assertEqual(res_3h["verdict"], "differs")
        self.assertEqual(len(res_3h["timing_diffs"]), 0)

        # 4. Same timing diff PLUS an extra body text diff -> `differs`!
        pig_body_diff = _make_thinking_doc("Thought for 2s", body_text="Different reasoning.")
        res_extra = compare.compare_part(
            "thinking block", "02b-thinking", omp_1s, pig_body_diff, omp_info,
            compare.summarize_part(pig_body_diff, dom, "sf-main", [pig_body_diff.nodes["sec"]]),
        )
        self.assertEqual(res_extra["verdict"], "differs")
        self.assertEqual(len(res_extra["timing_diffs"]), 1)
        self.assertEqual(len(res_extra["unexpected_diffs"]), 1)

        # 5. Style mismatch on head span (`accent` vs `muted`) is NOT timing -> `differs`!
        pig_style_diff = _make_thinking_doc("Thought for 2s", head_style="accent")
        res_style = compare.compare_part(
            "thinking block", "02b-thinking", omp_1s, pig_style_diff, omp_info,
            compare.summarize_part(pig_style_diff, dom, "sf-main", [pig_style_diff.nodes["sec"]]),
        )
        self.assertEqual(res_style["verdict"], "differs")
        self.assertEqual(len(res_style["timing_diffs"]), 0)

    def test_terminal_allowlist_paired_bash_and_audit_finding19_inputs(self) -> None:
        """Audit Findings 19 & 25: bash visualizer requires exact paired commands; wrong command / unit swap / ++ line / bad tip / blank rows are caught or counted."""
        allow = ansi_capture.load_terminal_allowlist()
        omp_txt = "│   print(\"alpha\\\\nbeta\\\\n\")                               │\n"
        pig_ok = "│ $ printf 'alpha\\nbeta\\n'                                 │\n"
        rep_exp = ansi_capture.classify_terminal_diff(omp_txt, pig_ok, allow)
        self.assertEqual(rep_exp["verdict"], "expected")
        self.assertEqual(rep_exp["summary"]["expected"], 1)
        self.assertEqual(rep_exp["summary"]["unexplained_lines"], 0)

        # Audit Finding 19 demonstrating input (3): port row changed to `rm -rf ~` -> MUST be `differs`!
        pig_wrong_bash = "│ $ printf WRONG; rm -rf ~   # not the command that ran    │\n"
        rep_wrong_bash = ansi_capture.classify_terminal_diff(omp_txt, pig_wrong_bash, allow)
        self.assertEqual(rep_wrong_bash["verdict"], "differs")
        self.assertEqual(rep_wrong_bash["summary"]["unexplained_lines"], 2)

        # Adding one unexplained line in the same hunk must flip the hunk to `differs`!
        pig_bad = pig_ok + "│ UNEXPECTED_EXTRA_LINE                                    │\n"
        rep_diff = ansi_capture.classify_terminal_diff(omp_txt, pig_bad, allow)
        self.assertEqual(rep_diff["verdict"], "differs")
        self.assertEqual(rep_diff["summary"]["unexplained_lines"], 1)

        # Audit Finding 19 demonstrating input (1): content lines starting with `--` or `++` are NEVER skipped as diff headers!
        rep_dashes = ansi_capture.classify_terminal_diff("--removed-flag\n", "++injected-line\n", allow)
        self.assertEqual(rep_dashes["verdict"], "differs")
        self.assertEqual(rep_dashes["summary"]["unexplained_lines"], 2)

        # Blank rows added inside a hunk are counted explicitly in `blank_row_diff`!
        rep_blanks = ansi_capture.classify_terminal_diff(omp_txt, pig_ok + "\n\n\n", allow)
        self.assertEqual(rep_blanks["blank_row_diff"]["pig_blank_lines_added"], 3)
        self.assertEqual(rep_blanks["blank_row_diff"]["total_blank_rows_changed"], 3)

    def test_ansi_width_preserving_normalization_durations_side_and_welcome_tip_block(self) -> None:
        """Unit-preserving durations, /private preservation, width guard, and validated welcome tip block (Findings 2, 19, 25)."""
        # 1a. Equal raw 80-col lines with same-unit durations (`210ms` vs `95ms`) and same-prefix `/tmp/...` paths -> equal!
        omp_dur = "╰─── ⏱ 210ms/5m " + ("─" * 63) + "╯"
        pig_dur = "╰─── ⏱ 95ms/5m " + ("─" * 64) + "╯"
        self.assertEqual(ansi_capture.display_width(omp_dur), 80)
        self.assertEqual(ansi_capture.display_width(pig_dur), 80)

        omp_edit = "╭─── ✎ Edit: 🗒 /tmp/run/omp/work/notes.txt ⟦+1/-1⟧ " + ("─" * 28) + "╮"
        pig_edit = "╭─── ✎ Edit: 🗒 /tmp/run/pig/work/notes.txt ⟦+1/-1⟧ " + ("─" * 28) + "╮"
        self.assertEqual(ansi_capture.display_width(omp_edit), 80)
        self.assertEqual(ansi_capture.display_width(pig_edit), 80)

        omp_eval = "╭─── 🐍 ✔ Proof Python eval · (170ms) " + ("─" * 40) + "╮"
        pig_eval = "╭─── 🐍 ✔ Proof Python eval · (95ms) " + ("─" * 41) + "╮"
        self.assertEqual(ansi_capture.display_width(omp_eval), ansi_capture.display_width(pig_eval))

        omp_wait = "└─ ✔ ⟦task⟧ ProofChild · 44ms\n π > ◑ Proof 1 > 🗑 omp-proof11-ansi/omp/work ▶─1%─128K─"
        pig_wait = "└─ ✔ ⟦task⟧ ProofChild · 37ms\n π > ◑ Proof 1 > 🗑 omp-proof11-ansi/pig/work ▶─1%─128K─"

        a_in = "\n".join([omp_dur, omp_edit, omp_eval, omp_wait]) + "\n"
        b_in = "\n".join([pig_dur, pig_edit, pig_eval, pig_wait]) + "\n"
        a_norm, b_norm = ansi_capture.width_preserving_normalize_pair(a_in, b_in)
        self.assertEqual(a_norm, b_norm)

        # 1b. Audit Finding 19 demonstrating input (5): `⏱ 71ms/5m` vs `⏱ 710s/5m` at equal width -> MUST NOT equalize!
        omp_ms = "╰─── ⏱ 71ms/5m " + ("─" * 64) + "╯"
        pig_s = "╰─── ⏱ 710s/5m " + ("─" * 64) + "╯"
        self.assertEqual(ansi_capture.display_width(omp_ms), ansi_capture.display_width(pig_s))
        a_u, b_u = ansi_capture.width_preserving_normalize_pair(omp_ms + "\n", pig_s + "\n")
        self.assertNotEqual(a_u, b_u)

        # 1c. Audit Finding 2: `/tmp/...` vs `/private/tmp/...` MUST remain a visible diff!
        pig_priv = "╭─── ✎ Edit: 🗒 /private/tmp/run/pig/work/notes.txt ⟦+1/-1⟧ " + ("─" * 20) + "╮"
        a_p, b_p = ansi_capture.width_preserving_normalize_pair(omp_edit + "\n", pig_priv + "\n")
        self.assertNotEqual(a_p, b_p)
        self.assertIn("/private<WORK>", b_p)

        # 1d. Unequal raw display widths (real width bug: 80 vs 79 cols) -> MUST remain a visible diff!
        pig_short_bug = "╰─── ⏱ 95ms/5m " + ("─" * 63) + "╯"
        self.assertEqual(ansi_capture.display_width(pig_short_bug), 79)
        a_bug, b_bug = ansi_capture.width_preserving_normalize_pair(omp_dur + "\n", pig_short_bug + "\n")
        self.assertNotEqual(a_bug, b_bug)

        # 2. Valid tips from omp's tips.txt and port's welcomeTips (<=72 cols) match `welcome-tip-block`.
        allow = ansi_capture.load_terminal_allowlist()
        omp_welcome = (
            "                               ██       v19.1.1\n\n"
            "    Tip: Press ←← to drill into a running or finished agent and inspect its\n"
            "                           tool calls and transcript\n\n"
            " Show a short markdown example.\n"
        )
        pig_welcome = (
            "                               ██       v0.1.0\n\n"
            "    Tip: Say workflowz in your message to drive the task with parallel\n"
            "                           subagents in eval\n\n"
            " Show a short markdown example.\n"
        )
        rep_w = ansi_capture.classify_terminal_diff(omp_welcome, pig_welcome, allow)
        self.assertEqual(rep_w["verdict"], "expected")
        self.assertIn(
            "Say workflowz in your message to drive the task with parallel subagents in eval",
            ansi_capture.PIG_ANSI_TIP_SET,
        )

        # Narrowed host-provider-error-text matches host transcript lines + omp raw-http-request,
        # but MUST reject leaked text and MUST NOT match the port-drawn `✘ 400: {...}` banner line!
        omp_err = (
            " Error: 400 Proof simulated provider error (type=invalid_request_error)\n"
            "   raw-http-request=/private/tmp/omp-proof15-ansi/omp/home/.omp/logs/http-400-r\n"
            "   equests/1795592395335-3sptyfx42yhjf.json\n"
        )
        pig_err_ok = (
            " Error: 400: {\"message\":\"Proof simulated provider\n"
            " error\",\"type\":\"invalid_request_error\"}\n"
            " If this looks like a pig bug, /bug sends a report to the developers.\n"
        )
        self.assertEqual(ansi_capture.classify_terminal_diff(omp_err, pig_err_ok, allow)["verdict"], "expected")
        pig_err_leak = pig_err_ok.replace(
            'Error: 400: {"message":"Proof simulated provider',
            'Error: 400: {"message":"Proof simulated provider <<<Authorization: Bearer sk-secret',
        )
        self.assertEqual(ansi_capture.classify_terminal_diff(omp_err, pig_err_leak, allow)["verdict"], "differs")
        omp_banner = " ✘ 400 Proof simulated provider error (type=invalid_request_error)\n"
        pig_banner_raw = " ✘ 400: {\"message\":\"Proof simulated provider\n   error\",\"type\":\"invalid_request_error\"}\n"
        self.assertEqual(ansi_capture.classify_terminal_diff(omp_banner, pig_banner_raw, allow)["verdict"], "differs")

        # Narrowed host-startup-notices pins exact hidden tools (`powershell, ls`); hiding `read, bash` MUST fail!
        pig_hid_ok = " Piglet oh-my-pig hid 2 tools from the registry:\n - built-in tools: powershell, ls (the Piglet's `tools` list does not name it)\n"
        pig_hid_bad = " Piglet oh-my-pig hid 57 tools from the registry:\n - built-in tools: read, bash, edit (the Piglet's `tools` list does not name it)\n"
        self.assertEqual(ansi_capture.classify_terminal_diff("", pig_hid_ok, allow)["verdict"], "expected")
        self.assertEqual(ansi_capture.classify_terminal_diff("", pig_hid_bad, allow)["verdict"], "differs")

        # Audit Finding 19 demonstrating input (4): nonexistent command tip or >72 cols wide -> MUST be `differs`!
        pig_fake_tip = (
            "                               ██       v0.1.0\n\n"
            "    Tip: Try /this-command-does-not-exist right now " + ("x" * 100) + "\n\n"
            " Show a short markdown example.\n"
        )
        rep_fake = ansi_capture.classify_terminal_diff(omp_welcome, pig_fake_tip, allow)
        self.assertEqual(rep_fake["verdict"], "differs")
        self.assertGreater(rep_fake["summary"]["unexplained_lines"], 0)

        # An extra differing line AFTER the blank line must NOT be swallowed by welcome-tip-block
        pig_welcome_extra = pig_welcome + " UNEXPECTED_AFTER_BANNER\n"
        rep_w_bad = ansi_capture.classify_terminal_diff(omp_welcome, pig_welcome_extra, allow)
        self.assertEqual(rep_w_bad["verdict"], "differs")
    def test_ansi_vtscreen_csi_3j_clears_scrollback_and_counts_clears(self) -> None:
        """CSI 3J erases saved scrollback lines (not active screen), CSI 2J clears active screen, and both counts are tracked."""
        vt = ansi_capture.VTScreen(rows=3, cols=20)
        vt.feed("line1\r\nline2\r\nline3\r\nline4\r\nline5")
        self.assertEqual(vt.scrollback, ["line1", "line2"])
        self.assertEqual(vt.full_screen_clears, 0)
        self.assertEqual(vt.scrollback_clears, 0)

        vt.feed("\x1b[3J")
        self.assertEqual(vt.scrollback, [])
        self.assertEqual(vt.scrollback_clears, 1)
        self.assertEqual(vt.full_screen_clears, 0)
        self.assertIn("line5", vt.render())

        vt.feed("\r\nline6\r\nline7\r\nline8")
        self.assertGreater(len(vt.scrollback), 0)
        vt.feed("\x1b[2J\x1b[H\x1b[3Jfresh")
        self.assertEqual(vt.scrollback, [])
        self.assertEqual(vt.full_screen_clears, 1)
        self.assertEqual(vt.scrollback_clears, 2)
        self.assertEqual(vt.render().splitlines()[0], "fresh")

    def test_ansi_screen_region_alignment_viewport_offset_and_stock_editor_frame(self) -> None:
        """Dock vs bottom-aligned transcript isolates viewport offset; stock editor frame is side-specific and rejects omp `╰─ leftover`."""
        allow = ansi_capture.load_terminal_allowlist()
        omp_screen = (
            " Trigger a simulated provider error.\n"
            "\n"
            " Plan mode enabled. Plan file: local://PLAN.md\n"
            "\n"
            " TODO\n"
            "  ├─ Verify · 2/2\n"
            "  │  ├─ ☑ Check read card\n"
            "  │  └─ ☑ Check todo card\n"
            "  └─────\n"
            "\n"
            " π > ◑ Proof 1 > 🗺 Plan > 🗑 omp-proof13-ansi/omp/work ▶─1%────────────╎─┃──128K─\n"
            "╰─\n"
        )
        pig_screen = (
            "\n"
            " Plan mode enabled. Plan file: local://PLAN.md\n"
            "\n"
            " TODO\n"
            "  ├─ Verify · 2/2\n"
            "  │  ├─ ☑ Check read card\n"
            "  │  └─ ☑ Check todo card\n"
            "  └─────\n"
            "\n"
            " π > ◑ Proof 1 > 🗺 Plan > 🗑 omp-proof13-ansi/pig/work ▶─1%────────────╎─┃──128K─\n"
            "────────────────────────────────────────────────────────────────────────────────\n"
            "\n"
            "────────────────────────────────────────────────────────────────────────────────\n"
        )
        rep = ansi_capture.classify_screen_regions(omp_screen, pig_screen, allow)
        self.assertEqual(rep["verdict"], "expected")
        self.assertEqual(rep["summary"]["unexplained_lines"], 0)
        self.assertEqual(rep["dock_height"], {"omp": 8, "pig": 10, "delta": 2})
        self.assertEqual(rep["viewport_offset_lines"], [" Trigger a simulated provider error."])

        # Finding 25: omp prompt row with leftover text `╰─ leftover` MUST NOT be swallowed!
        omp_leftover = omp_screen.replace("╰─\n", "╰─ leftover text\n")
        rep_leftover = ansi_capture.classify_screen_regions(omp_leftover, pig_screen, allow)
        self.assertEqual(rep_leftover["verdict"], "differs")
        self.assertGreater(rep_leftover["summary"]["unexplained_lines"], 0)

        # Arbitrary stray rule line in the transcript MUST NOT be swallowed by `host-stock-editor-frame`!
        pig_stray_rule = "────────────────────────────────────────────────────────────────────────────────\n" + pig_screen
        rep_bad = ansi_capture.classify_screen_regions(omp_screen, pig_stray_rule, allow)
        self.assertEqual(rep_bad["verdict"], "differs")
        self.assertGreater(rep_bad["summary"]["unexplained_lines"], 0)

    def test_ansi_stale_pre_resize_frame_omp_only_and_rejects_port_garbage(self) -> None:
        """Audit Finding 15: stale pre-resize frame matches ONLY on omp's side when canonical dock equals active dock; port garbage is NEVER masked."""
        allow = ansi_capture.load_terminal_allowlist()
        omp_with_stale = (
            " Plan mode enabled. Plan file: local://PLAN.md\n"
            "\n"
            " TODO\n"
            "  ├─ Verify · 2/2\n"
            "  └─────\n"
            "\n"
            " π > ◑ Proof 1 > 🗺 Plan > 🗑 omp-proof13-ansi/omp/work ▶─1%────────────╎─┃──128K─\n"
            "╰─\n"
            " Plan mode enabled. Plan file: local://PLAN.md\n"
            "\n"
            " TODO\n"
            "  ├─ Verify · 2/2\n"
            "  └─────\n"
            "\n"
            " π > ◑ Proof 1 > 🗺 Plan > 🗑 omp-proof13-ansi/omp/work ▶─1%──────────────────────────────────────────╎─────┃────────128K─\n"
            "╰─\n"
        )
        pig_clean = (
            " Plan mode enabled. Plan file: local://PLAN.md\n"
            "\n"
            " TODO\n"
            "  ├─ Verify · 2/2\n"
            "  └─────\n"
            "\n"
            " π > ◑ Proof 1 > 🗺 Plan > 🗑 omp-proof13-ansi/pig/work ▶─1%────────────╎─┃──128K─\n"
            "────────────────────────────────────────────────────────────────────────────────\n"
            "\n"
            "────────────────────────────────────────────────────────────────────────────────\n"
        )
        a_n, b_n = ansi_capture.width_preserving_normalize_pair(omp_with_stale, pig_clean)
        rep = ansi_capture.classify_terminal_diff(a_n, b_n, allow)
        self.assertEqual(rep["verdict"], "expected")
        self.assertEqual(rep["summary"]["unexplained_lines"], 0)
        self.assertIsNotNone(rep["stale_pre_resize_frame"])
        self.assertEqual(rep["stale_pre_resize_frame"]["omp"]["stale_line_count"], 6)
        self.assertIn("omp-stale-pre-resize-frame", rep["hunks"][0]["names"])
        self.assertEqual(rep["summary"]["matched_by_class"].get("OMP_ARTIFACT"), 6)

        # Audit Finding 15 demonstrating input: appending a wider dock with GARBAGE to the PORT's screen
        # MUST NOT be masked by `omp-stale-pre-resize-frame`!
        pig_with_garbage = pig_clean + (
            " TODO\n"
            "  ├─ GARBAGE LEFT ON SCREEN BY A PORT REDRAW BUG\n"
            "  └─────\n"
            " π > ◑ Proof 1 > 🗺 Plan > 🗑 omp-proof13-ansi/pig/work ▶─1%──────────────────────────────────────────╎─────┃────────128K─\n"
            "────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────\n"
            "\n"
            "────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────\n"
        )
        rep_garb = ansi_capture.classify_screen_regions(omp_with_stale, pig_with_garbage, allow)
        self.assertEqual(rep_garb["verdict"], "differs")
        self.assertGreater(rep_garb["summary"]["unexplained_lines"], 0)
        self.assertTrue(
            any("GARBAGE LEFT ON SCREEN BY A PORT REDRAW BUG" in ul for h in rep_garb["hunks"] for ul in h["unexplained"])
        )

        # Even on omp's side, a wider dock whose content does NOT equal the active dock MUST NOT be masked!
        omp_bad_stale = omp_with_stale.replace(
            "  ├─ Verify · 2/2\n  └─────\n\n π > ◑ Proof 1 > 🗺 Plan > 🗑 omp-proof13-ansi/omp/work ▶─1%──────────────────────────────────────────",
            "  ├─ DIFFERENT STALE CONTENT\n  └─────\n\n π > ◑ Proof 1 > 🗺 Plan > 🗑 omp-proof13-ansi/omp/work ▶─1%──────────────────────────────────────────",
        )
        a_b, b_b = ansi_capture.width_preserving_normalize_pair(omp_bad_stale, pig_clean)
        rep_omp_bad = ansi_capture.classify_terminal_diff(a_b, b_b, allow)
        self.assertEqual(rep_omp_bad["verdict"], "differs")
        self.assertGreater(rep_omp_bad["summary"]["unexplained_lines"], 0)

        # Also verify VTScreen.resize clears both _main and _alt so ?1049l never restores a stale 40x120 grid
        vt = ansi_capture.VTScreen(rows=4, cols=20)
        vt.feed("old-120-line")
        vt.resize(2, 10)
        vt.feed("\x1b[?1049hALT\x1b[?1049l\x1b[HNEW")
        self.assertEqual(len(vt.render().splitlines()), 2)
        self.assertEqual(vt.render().splitlines()[0], "NEW")

    def test_ansi_sgr_diff_detects_stripped_colours(self) -> None:
        """Finding 19 (7): stripping every SGR sequence from the port yields a counted SGR diff."""
        omp_raw = b"\x1b[38;2;0;180;255mhello\x1b[0m"
        pig_raw = b"hello"
        sgr_diff = ansi_capture.compare_sgr_palettes(omp_raw, pig_raw)
        self.assertEqual(sgr_diff["verdict"], "differs")
        self.assertEqual(sgr_diff["total_distinct_color_diffs"], 1)
        self.assertEqual(sgr_diff["deltas"]["truecolor_fg"]["omp_only"], 1)
    def test_llm_server_sets_aside_judge_requests_and_tracks_completed(self) -> None:
        """Finding #10: judge requests (`[]` / `['submit_judgment']`) do not advance turn cursor; /health reports completed & in_flight."""
        import http.server
        import threading
        import urllib.request

        turns = [{"text": "Turn 0 reply"}, {"text": "Turn 1 reply"}]
        state = llm_server.ScriptedLLMServer(turns=turns, log_path=None)
        httpd = http.server.ThreadingHTTPServer(("127.0.0.1", 0), llm_server.make_handler(state))
        port = httpd.server_address[1]
        t = threading.Thread(target=httpd.serve_forever, daemon=True)
        t.start()
        try:
            base = f"http://127.0.0.1:{port}"
            # 1. Judge request with submit_judgment tool must NOT advance next/completed
            judge_body = json.dumps({
                "model": "proof-1",
                "messages": [
                    {"role": "user", "content": "State:\n<solution_space>one fixed reply</solution_space>\nAnswer with exactly one of: `low`, `medium`, `high`"},
                ],
                "tools": [{"type": "function", "function": {"name": "submit_judgment", "parameters": {"type": "object"}}}],
            }).encode("utf-8")
            req = urllib.request.Request(f"{base}/v1/chat/completions", data=judge_body, headers={"Content-Type": "application/json"})
            with urllib.request.urlopen(req, timeout=5) as resp:
                body_txt = resp.read().decode("utf-8")
            self.assertIn("submit_judgment", body_txt)
            with urllib.request.urlopen(f"{base}/health", timeout=5) as resp:
                h1 = json.loads(resp.read().decode("utf-8"))
            self.assertEqual(h1["next"], 0)
            self.assertEqual(h1["completed"], 0)
            self.assertEqual(h1["in_flight"], 0)

            # 2. Normal conversation turn advances both next and completed to 1
            conv_body = json.dumps({
                "model": "proof-1",
                "messages": [{"role": "user", "content": "Hello"}],
                "tools": [{"type": "function", "function": {"name": "read", "parameters": {"type": "object"}}}],
            }).encode("utf-8")
            req2 = urllib.request.Request(f"{base}/v1/chat/completions", data=conv_body, headers={"Content-Type": "application/json"})
            with urllib.request.urlopen(req2, timeout=5) as resp:
                conv_txt = resp.read().decode("utf-8")
            self.assertIn("Turn 0 reply", conv_txt)
            with urllib.request.urlopen(f"{base}/health", timeout=5) as resp:
                h2 = json.loads(resp.read().decode("utf-8"))
            self.assertEqual(h2["next"], 1)
            self.assertEqual(h2["completed"], 1)
            self.assertEqual(h2["in_flight"], 0)
        finally:
            httpd.shutdown()
            httpd.server_close()

    def test_ansi_capture_char_width_normalization_and_sgr_summary(self) -> None:
        """Finding #11 + #12: U+1F5D1 (🗑) & U+1F5D2 (🗒) are width 1 (so 120-col status line preserves `128K─`); stopwatch normalized; SGR summarized."""
        vt = ansi_capture.VTScreen(rows=4, cols=120)
        self.assertEqual(vt._char_width("\U0001F5D1"), 1)  # 🗑
        self.assertEqual(vt._char_width("\U0001F5D2"), 1)  # 🗒
        self.assertEqual(vt._char_width("\U0001F4A1"), 2)  # 💡 (EAW=W)
        # Feed a 120-char line containing 🗑 ending in `128K─` with autowrap disabled
        line = " π > ◑ Proof 1 > 🗑 omp-proof/omp/work " + ("─" * 77) + "128K─"
        self.assertEqual(len(line), 120)
        vt.autowrap = False
        vt.feed(line)
        rendered = vt.render().splitlines()[0]
        self.assertTrue(rendered.endswith("128K─"), f"expected trailing 128K─, got {rendered[-12:]!r}")

        # Stopwatch (unit-preserving) + temp path normalization
        norm = ansi_capture.normalize_screen_text("╭─── $ printf (/tmp/omp-x/omp/work) ⏱ 182ms/5m ───╮")
        self.assertEqual(norm, "╭─── $ printf (<WORK>) ⏱ <DUR>ms/5m ───╮")

        # SGR summary distinguishes truecolor vs 256-color and detects alt-screen / mouse modes
        sgr = ansi_capture.summarize_sgr_palette(b"\x1b[?1049h\x1b[?1000h\x1b[38;2;0;180;255mhi\x1b[38;5;2mok\x1b[0m")
        self.assertTrue(sgr["alt_screen"])
        self.assertEqual(sgr["mouse_modes"], ["?1000h"])
        self.assertEqual(sgr["truecolor_fg_count"], 1)
        self.assertEqual(sgr["ansi256_fg_count"], 1)

    def test_compare_frame_alignment_extra_card_extra_subtree_reordered_pair(self) -> None:
        """Round 6: LCS region alignment catches (1) extra card on one side, (2) extra subtree inside matched card, and (3) reordered pair, and fails exit code."""
        allow = compare.load_drawing_allowlist()

        def _make_doc(main_children: list[str], extra_nodes: dict[str, compare.TspNode] | None = None) -> compare.TspDocument:
            nodes: dict[str, compare.TspNode] = {
                "s:1": compare.TspNode(id="s:1", kind="surface", props={"role": "omp.session"}, children=["main", "dock", "layer"]),
                "main": compare.TspNode(id="main", kind="col", props={}, children=main_children),
                "dock": compare.TspNode(id="dock", kind="col", props={}, children=[]),
                "layer": compare.TspNode(id="layer", kind="col", props={}, children=[]),
                "u1": compare.TspNode(id="u1", kind="card", props={"role": "omp.user"}, children=["u1m"]),
                "u1m": compare.TspNode(id="u1m", kind="md", props={"text": "First prompt"}, children=[]),
                "a1": compare.TspNode(id="a1", kind="col", props={"role": "omp.assistant"}, children=["a1m"]),
                "a1m": compare.TspNode(id="a1m", kind="md", props={"text": "First reply"}, children=[]),
                "t1": compare.TspNode(id="t1", kind="tool", props={"role": "omp.tool.read", "name": "read", "target": "notes.txt"}, children=[]),
                "t2": compare.TspNode(id="t2", kind="tool", props={"role": "omp.tool.write", "name": "write", "target": "out.txt"}, children=[]),
            }
            if extra_nodes:
                nodes.update(extra_nodes)
            return compare.TspDocument(surface_id="s:1", surface_role="omp.session", nodes=nodes)

        # 1. Extra card on port side -> reported as unexplained unmatched child with kind, role, index, text
        omp_base = _make_doc(["u1", "a1"])
        pig_extra_card = _make_doc(["u1", "t1", "a1"])
        res1 = compare.align_and_classify_step_regions("01-markdown", omp_base, pig_extra_card, allow)
        self.assertEqual(res1["unexplained_unmatched_count"], 1)
        u_item = res1["unmatched_children"][0]
        self.assertEqual(u_item["region"], "main")
        self.assertEqual(u_item["side"], "port-only")
        self.assertEqual(u_item["index"], 1)
        self.assertEqual(u_item["kind"], "tool")
        self.assertEqual(u_item["role"], "omp.tool.read")
        self.assertIn("notes.txt", u_item["text"])
        self.assertEqual(u_item["status"], "unexplained")

        # 2. Extra subtree inside a matched card (matched tool card with 3 extra visible nodes on port)
        omp_tool = _make_doc(["t1"])
        pig_tool_bloat = _make_doc(
            ["t1"],
            extra_nodes={
                "t1": compare.TspNode(id="t1", kind="tool", props={"role": "omp.tool.read", "name": "read", "target": "notes.txt"}, children=["sub"]),
                "sub": compare.TspNode(id="sub", kind="col", props={"role": "omp.tool.extra"}, children=["c1", "c2"]),
                "c1": compare.TspNode(id="c1", kind="text", props={"text": "extra 1"}, children=[]),
                "c2": compare.TspNode(id="c2", kind="text", props={"text": "extra 2"}, children=[]),
            },
        )
        res2 = compare.align_and_classify_step_regions("03-tool-read", omp_tool, pig_tool_bloat, allow)
        self.assertEqual(res2["unexplained_unmatched_count"], 0)
        self.assertEqual(res2["unexplained_count_delta_count"], 1)
        cd = res2["matched_count_deltas"][0]
        self.assertEqual(cd["omp_visible_nodes"], 1)
        self.assertEqual(cd["pig_visible_nodes"], 4)
        self.assertEqual(cd["delta"], -3)
        self.assertEqual(cd["unaccounted_delta"], 0)
        self.assertEqual(cd["status"], "unexplained")

        # 3. Reordered pair ([t1, t2] vs [t2, t1]): LCS keeps one in order and flags the displaced pair as 2 unmatched children!
        omp_ord = _make_doc(["t1", "t2"])
        pig_rev = _make_doc(["t2", "t1"])
        res3 = compare.align_and_classify_step_regions("12-tool-write-search", omp_ord, pig_rev, allow)
        self.assertEqual(res3["unexplained_unmatched_count"], 2)
        sides = sorted(u["side"] for u in res3["unmatched_children"])
        self.assertEqual(sides, ["omp-only", "port-only"])

        # 4. Allowlisted child inside matched user card (`row[omp.user.tools]` with 5 nodes) accounts for +5 delta as `expected`!
        omp_user_with_tools = _make_doc(
            ["u1"],
            extra_nodes={
                "u1": compare.TspNode(id="u1", kind="card", props={"role": "omp.user"}, children=["ut", "u1m"]),
                "ut": compare.TspNode(id="ut", kind="row", props={"role": "omp.user.tools"}, children=["i1", "i2", "t_a", "t_b"]),
                "i1": compare.TspNode(id="i1", kind="icon", props={"role": "omp.user.tool"}, children=[]),
                "i2": compare.TspNode(id="i2", kind="icon", props={"role": "omp.user.tool"}, children=[]),
                "t_a": compare.TspNode(id="t_a", kind="text", props={"text": "·"}, children=[]),
                "t_b": compare.TspNode(id="t_b", kind="text", props={"role": "omp.user.time", "text": "12:00"}, children=[]),
            },
        )
        pig_user_clean = _make_doc(["u1"])
        res4 = compare.align_and_classify_step_regions("01-markdown", omp_user_with_tools, pig_user_clean, allow)
        self.assertEqual(res4["unexplained_unmatched_count"], 0)
        self.assertEqual(res4["unexplained_count_delta_count"], 0)
        self.assertEqual(res4["expected_count_delta_count"], 1)
        self.assertEqual(res4["matched_count_deltas"][0]["delta"], 5)
        self.assertEqual(res4["matched_count_deltas"][0]["unaccounted_delta"], 0)

        # 5. Terminal allowlist enforces `ref_subject` (rejects entry pointing to an unrelated row)
        with tempfile.TemporaryDirectory() as td:
            bad_path = Path(td) / "bad_term.json"
            bad_path.write_text(
                json.dumps([{
                    "name": "host-startup-notices",
                    "pig_regex": "^\\[Extensions\\]$",
                    "class": "HOST",
                    "ref_file": "components/oh-my-pig/extensions/oh-my-pig/internal/ansi/UPSTREAM.md",
                    "ref_term": ["Host-owned terminal colours", "piglet.yaml"],
                    "ref_subject": ["quietStartup", "hid 2 tools"],
                    "why": "Wrong row test",
                }]),
                encoding="utf-8",
            )
            with self.assertRaises(allowlist_refs.AllowlistRefError):
                ansi_capture.load_terminal_allowlist(bad_path)

    def test_ansi_cell_colour_animated_stops_blank_rows_and_alt_screen_verification(self) -> None:
        """Round 7 demonstrating inputs:
        (1) port-drawn card title in the wrong colour with identical text -> unexplained,
        (2) welcome cell in a colour outside omp's validated gradient stops -> unexplained,
        (3) welcome cell inside omp's gradient stops (animated frame) -> expected,
        (4) extra blank row inside a port-drawn card -> unexplained,
        (5) alt-screen entered outside resize window or by port -> unexplained; strictly inside resize on omp -> expected.
        """
        allow = ansi_capture.load_terminal_allowlist()

        def _render_styled(ansi_stream: str, rows: int = 8, cols: int = 80) -> list[list]:
            vt = ansi_capture.VTScreen(rows=rows, cols=cols)
            vt.feed(ansi_stream)
            return vt.render_styled_rows()

        # 1. Port-drawn card title in wrong colour (`rgb:255;0;0` red instead of omp `rgb:0;180;255` accent) with identical text -> UNEXPLAINED!
        omp_card = _render_styled(
            "\x1b[48;2;15;18;22m\x1b[38;2;31;37;45m╭─── \x1b[38;2;0;180;255m? Ask\x1b[38;2;31;37;45m ───╮\x1b[0m\r\n"
            "\x1b[48;2;15;18;22m\x1b[38;2;31;37;45m╰────────────╯\x1b[0m"
        )
        pig_wrong_title = _render_styled(
            "\x1b[48;2;15;18;22m\x1b[38;2;31;37;45m╭─── \x1b[38;2;255;0;0m? Ask\x1b[38;2;31;37;45m ───╮\x1b[0m\r\n"
            "\x1b[48;2;15;18;22m\x1b[38;2;31;37;45m╰────────────╯\x1b[0m"
        )
        cc_wrong = ansi_capture.classify_cell_colours(omp_card, pig_wrong_title, allow)
        self.assertEqual(cc_wrong["verdict"], "differs")
        self.assertGreater(cc_wrong["unexplained"], 0)
        self.assertEqual(cc_wrong["unexplained_runs"][0]["region"], "card:ask")
        self.assertEqual(" ".join(r["text"] for r in cc_wrong["unexplained_runs"]), "? Ask")
        self.assertEqual(cc_wrong["unexplained_runs"][0]["omp_fg"], "rgb:0;180;255")
        self.assertEqual(cc_wrong["unexplained_runs"][0]["pig_fg"], "rgb:255;0;0")
        # 2. Welcome logo cell inside omp's animated gradient stops (e.g. mid-sweep stop `rgb:147;98;244` vs resting `rgb:248;79;204`) -> EXPECTED!
        omp_w_anim = _render_styled("   \x1b[38;2;147;98;244m████████████\x1b[0m")
        pig_w_rest = _render_styled("   \x1b[38;2;248;79;204m████████████\x1b[0m")
        cc_w_ok = ansi_capture.classify_cell_colours(omp_w_anim, pig_w_rest, allow)
        self.assertEqual(cc_w_ok["verdict"], "expected")
        self.assertEqual(cc_w_ok["unexplained"], 0)
        self.assertEqual(cc_w_ok["expected"], 1)

        # 3. Welcome logo cell in a colour OUTSIDE omp's gradient stops (`rgb:0;255;0` lime green) -> UNEXPLAINED!
        pig_w_bad = _render_styled("   \x1b[38;2;0;255;0m████████████\x1b[0m")
        cc_w_bad = ansi_capture.classify_cell_colours(omp_w_anim, pig_w_bad, allow)
        self.assertEqual(cc_w_bad["verdict"], "differs")
        self.assertEqual(cc_w_bad["unexplained"], 1)
        self.assertEqual(cc_w_bad["unexplained_runs"][0]["region"], "welcome")
        self.assertEqual(cc_w_bad["unexplained_runs"][0]["pig_fg"], "rgb:0;255;0")
        self.assertFalse(ansi_capture.is_in_omp_welcome_gradient_stops("rgb:0;255;0"))
        self.assertTrue(ansi_capture.is_in_omp_welcome_gradient_stops("rgb:147;98;244"))

        # 4. Extra blank row inside a port-drawn card -> UNEXPLAINED!
        omp_card_txt = "╭─── ? Ask ───╮\n│  Choose:    │\n╰─────────────╯\n"
        pig_card_blank = "╭─── ? Ask ───╮\n│  Choose:    │\n\n╰─────────────╯\n"
        rep_blank = ansi_capture.classify_terminal_diff(omp_card_txt, pig_card_blank, allow)
        self.assertEqual(rep_blank["verdict"], "differs")
        self.assertEqual(rep_blank["blank_row_diff"]["unexplained"], 1)
        self.assertEqual(rep_blank["summary"]["blank_unexplained"], 1)

        # 5. Terminal modes: omp alt-screen strictly inside recorded resize window -> expected;
        #    omp alt-screen outside resize window -> unexplained; port entering alt-screen -> unexplained!
        raw_ok_omp = b"hello world" + b"\x1b[?1049hRESIZE\x1b[?1049l\x1b[2J\x1b[3Jafter"
        raw_ok_pig = b"hello world\x1b[2J\x1b[3Jafter"
        m_ok = ansi_capture.verify_terminal_modes(
            raw_ok_omp, raw_ok_pig, allow, omp_resize_offsets=[{"start": 10, "end": 40, "rows": 24, "cols": 80}]
        )
        self.assertEqual(m_ok["verdict"], "expected")
        self.assertEqual(m_ok["unexplained"], 0)

        # Outside resize window (entered at byte 11, resize didn't start until byte 50) -> UNEXPLAINED!
        m_early = ansi_capture.verify_terminal_modes(
            raw_ok_omp, raw_ok_pig, allow, omp_resize_offsets=[{"start": 50, "end": 80, "rows": 24, "cols": 80}]
        )
        self.assertEqual(m_early["verdict"], "differs")
        self.assertEqual(m_early["unexplained"], 1)

        # Port entering alt-screen -> UNEXPLAINED!
        m_pig_alt = ansi_capture.verify_terminal_modes(raw_ok_omp, b"\x1b[?1049hPIG\x1b[?1049l", allow)
        self.assertEqual(m_pig_alt["verdict"], "differs")
        self.assertEqual(m_pig_alt["unexplained"], 1)

    def test_compare_round8_empty_runs_wrapped_errors_nested_part_and_welcome_tip_form(self) -> None:
        """Round 8 demonstrating inputs:
        (1) equal runs of empty anonymous `col`s pair 1:1; 9 vs 8 leaves exactly 1 unmatched,
        (2) two wrapped `col[omp.assistant](card[omp.error])` pair even with differing raw `head` node IDs,
        (3) a `spacer` on one side only is unmatched; matching `spacer`s on both sides pair,
        (4) nested `row[omp.working.stop]` under `row[omp.hud.activity] > col[omp.hud.status] > row[omp.working]` resolves to `working row` entry,
        (5) empty `col[omp.assistant]` is allowlisted ONLY when next non-empty sibling is a `tool` card (NOT before a user message),
        (6) welcome tip validates BOTH tip membership AND wire form (keycap spans vs plain text),
        (7) `visible_text` reads `ansi` node `lines` array (and TSP `text`/`splice` ops).
        """
        allow = compare.load_drawing_allowlist()

        def _dock_doc(n_empty_before: int, n_empty_after: int, include_stop: bool = False) -> compare.TspDocument:
            nodes: dict[str, compare.TspNode] = {
                "s:1": compare.TspNode(id="s:1", kind="surface", props={"role": "omp.session"}, children=["main", "dock", "layer"]),
                "main": compare.TspNode(id="main", kind="col", props={}, children=[]),
                "layer": compare.TspNode(id="layer", kind="col", props={}, children=[]),
            }
            dock_kids: list[str] = []
            for i in range(n_empty_before):
                cid = f"eb{i}"
                nodes[cid] = compare.TspNode(id=cid, kind="col", props={"key": f"slot{i}"}, children=[])
                dock_kids.append(cid)
            # Anchor: `row[omp.hud.activity] > col[omp.hud.status] > row[omp.working]`
            wk_kids = ["sp"]
            nodes["sp"] = compare.TspNode(id="sp", kind="spinner", props={"style": "starburst"}, children=[])
            if include_stop:
                wk_kids.append("stop")
                nodes["stop"] = compare.TspNode(id="stop", kind="row", props={"role": "omp.working.stop"}, children=["sk", "st"])
                nodes["sk"] = compare.TspNode(id="sk", kind="kbd", props={"keys": ["escape"]}, children=[])
                nodes["st"] = compare.TspNode(id="st", kind="text", props={"text": "Stop"}, children=[])
            nodes["wk"] = compare.TspNode(id="wk", kind="row", props={"role": "omp.working"}, children=wk_kids)
            nodes["hs"] = compare.TspNode(id="hs", kind="col", props={"role": "omp.hud.status"}, children=["wk"])
            nodes["act"] = compare.TspNode(id="act", kind="row", props={"role": "omp.hud.activity"}, children=["hs"])
            dock_kids.append("act")
            for i in range(n_empty_after):
                cid = f"ea{i}"
                nodes[cid] = compare.TspNode(id=cid, kind="col", props={"key": f"after{i}"}, children=[])
                dock_kids.append(cid)
            nodes["dock"] = compare.TspNode(id="dock", kind="col", props={}, children=dock_kids)
            return compare.TspDocument(surface_id="s:1", surface_role="omp.session", nodes=nodes)

        # (1a) Equal runs (9 empty cols before anchor, 2 after) + nested `row[omp.working.stop]` -> 0 unexplained unmatched, 0 unexplained count deltas!
        omp_9 = _dock_doc(9, 2, include_stop=True)
        pig_9 = _dock_doc(9, 2, include_stop=False)
        res_eq = compare.align_and_classify_step_regions("02a-working", omp_9, pig_9, allow)
        self.assertEqual(res_eq["unexplained_unmatched_count"], 0)
        self.assertEqual(res_eq["unexplained_count_delta_count"], 0)
        self.assertEqual(res_eq["expected_count_delta_count"], 1)
        self.assertEqual(res_eq["matched_count_deltas"][0]["delta"], 3)
        self.assertEqual(res_eq["matched_count_deltas"][0]["unaccounted_delta"], 0)

        # (1b) Run of 9 vs 8 empty cols -> leaves EXACTLY 1 unexplained unmatched!
        pig_8 = _dock_doc(8, 2, include_stop=False)
        res_9v8 = compare.align_and_classify_step_regions("02a-working", omp_9, pig_8, allow)
        self.assertEqual(res_9v8["unexplained_unmatched_count"], 1)
        self.assertEqual(res_9v8["unmatched_children"][0]["kind"], "col")
        self.assertEqual(res_9v8["unmatched_children"][0]["side"], "omp-only")

        # (2) Two wrapped `col[omp.assistant](card[omp.error])` with differing raw `head` IDs pair cleanly!
        def _err_doc(head_id: str, with_spacer: bool = True) -> compare.TspDocument:
            m_kids = (["sp0"] if with_spacer else []) + ["w_err"]
            nodes: dict[str, compare.TspNode] = {
                "s:1": compare.TspNode(id="s:1", kind="surface", props={"role": "omp.session"}, children=["main", "dock", "layer"]),
                "main": compare.TspNode(id="main", kind="col", props={}, children=m_kids),
                "dock": compare.TspNode(id="dock", kind="col", props={}, children=[]),
                "layer": compare.TspNode(id="layer", kind="col", props={}, children=[]),
                "sp0": compare.TspNode(id="sp0", kind="spacer", props={"size": "sm"}, children=[]),
                "w_err": compare.TspNode(id="w_err", kind="col", props={"role": "omp.assistant"}, children=["ec"]),
                "ec": compare.TspNode(id="ec", kind="card", props={"role": "omp.error", "head": head_id}, children=["em"]),
                "em": compare.TspNode(id="em", kind="text", props={"role": "omp.error.message", "spans": [{"t": "Proof simulated provider error (type=invalid_request_error)", "s": "mono"}]}, children=[]),
            }
            return compare.TspDocument(surface_id="s:1", surface_role="omp.session", nodes=nodes)

        omp_err = _err_doc("2x.0/head", with_spacer=True)
        pig_err = _err_doc("n.n90.e.error.head", with_spacer=True)
        self.assertNotIn("2x.0/head", omp_err.visible_text("w_err"))
        res_err = compare.align_and_classify_step_regions("15-provider-error", omp_err, pig_err, allow)
        self.assertEqual(res_err["unexplained_unmatched_count"], 0)
        self.assertEqual(res_err["unexplained_count_delta_count"], 0)

        # (3) A `spacer` on one side only is unmatched!
        pig_no_sp = _err_doc("n.n90.e.error.head", with_spacer=False)
        res_sp = compare.align_and_classify_step_regions("15-provider-error", omp_err, pig_no_sp, allow)
        self.assertEqual(res_sp["unexplained_unmatched_count"], 1)
        self.assertEqual(res_sp["unmatched_children"][0]["kind"], "spacer")

        # (5) Empty `col[omp.assistant]` before a `tool` card -> expected [HOST]; before a `card[omp.user]` -> UNEXPLAINED!
        def _empty_asst_doc(next_is_tool: bool, has_empty: bool) -> compare.TspDocument:
            m_kids = (["ea"] if has_empty else []) + ["nxt"]
            nxt_node = (
                compare.TspNode(id="nxt", kind="tool", props={"role": "omp.tool.read", "name": "read", "target": "notes.txt"}, children=[])
                if next_is_tool
                else compare.TspNode(id="nxt", kind="card", props={"role": "omp.user"}, children=["nm"])
            )
            nodes: dict[str, compare.TspNode] = {
                "s:1": compare.TspNode(id="s:1", kind="surface", props={"role": "omp.session"}, children=["main", "dock", "layer"]),
                "main": compare.TspNode(id="main", kind="col", props={}, children=m_kids),
                "dock": compare.TspNode(id="dock", kind="col", props={}, children=[]),
                "layer": compare.TspNode(id="layer", kind="col", props={}, children=[]),
                "ea": compare.TspNode(id="ea", kind="col", props={"role": "omp.assistant"}, children=[]),
                "nxt": nxt_node,
                "nm": compare.TspNode(id="nm", kind="md", props={"text": "Hello"}, children=[]),
            }
            return compare.TspDocument(surface_id="s:1", surface_role="omp.session", nodes=nodes)

        res_ea_tool = compare.align_and_classify_step_regions("03-tool-read", _empty_asst_doc(True, True), _empty_asst_doc(True, False), allow)
        self.assertEqual(res_ea_tool["unexplained_unmatched_count"], 0)
        self.assertEqual(res_ea_tool["expected_unmatched_count"], 1)

        res_ea_user = compare.align_and_classify_step_regions("03-tool-read", _empty_asst_doc(False, True), _empty_asst_doc(False, False), allow)
        self.assertEqual(res_ea_user["unexplained_unmatched_count"], 1)

        # (6) Welcome tip: omp `{key:ctrl+r}` in icon spans vs pig plain tip in `text` -> expected;
        #     port encoding a `{key:shift+tab}` PUA glyph in plain `text` (wrong form) -> differs!
        dom_card = {"regions": {"sf-main": [{"kind": "card", "class": "sf sf-card", "children": []}]}}
        def _tip_doc(tip_props: dict) -> compare.TspDocument:
            return compare.TspDocument(
                surface_id="s:1", surface_role="omp.session",
                nodes={
                    "s:1": compare.TspNode(id="s:1", kind="surface", props={"role": "omp.session"}, children=["main"]),
                    "main": compare.TspNode(id="main", kind="col", props={}, children=["w"]),
                    "w": compare.TspNode(id="w", kind="card", props={"role": "omp.welcome"}, children=["tp"]),
                    "tp": compare.TspNode(id="tp", kind="row", props={"role": "omp.welcome.tip"}, children=["tt"]),
                    "tt": compare.TspNode(id="tt", kind="text", props={"role": "omp.welcome.tip-text", "wrap": "word", **tip_props}, children=[]),
                },
            )
        omp_icon_tip = _tip_doc({"spans": [{"t": "Press"}, {"t": "\U000f0634", "s": "icon"}, {"t": "R to search your prompt history and reuse a past message"}]})
        pig_plain_tip = _tip_doc({"text": 'Tired of typing "keep going"? Just send a \'.\''})
        res_tip_ok = compare.compare_part(
            "welcome banner", "00-started", omp_icon_tip, pig_plain_tip,
            compare.summarize_part(omp_icon_tip, dom_card, "sf-main", [omp_icon_tip.nodes["w"]]),
            compare.summarize_part(pig_plain_tip, dom_card, "sf-main", [pig_plain_tip.nodes["w"]]),
            allowlist=allow,
        )
        self.assertEqual(res_tip_ok["verdict"], "expected")

        pig_bad_form = _tip_doc({"text": "Press \U000f0636\U000f0312 to cycle through reasoning effort levels"})
        res_tip_bad = compare.compare_part(
            "welcome banner", "00-started", omp_icon_tip, pig_bad_form,
            compare.summarize_part(omp_icon_tip, dom_card, "sf-main", [omp_icon_tip.nodes["w"]]),
            compare.summarize_part(pig_bad_form, dom_card, "sf-main", [pig_bad_form.nodes["w"]]),
            allowlist=allow,
        )
        self.assertEqual(res_tip_bad["verdict"], "differs")

        # (7) B14: `visible_text` reads `ansi` `lines` list, and `apply_record` handles `text` + `splice` ops.
        doc_ansi = compare.TspDocument(
            surface_id="s:1", surface_role="omp.session",
            nodes={"a1": compare.TspNode(id="a1", kind="ansi", props={"lines": ["→ settings", "  model"]}, children=[])},
        )
        self.assertIn("settings", doc_ansi.visible_text("a1"))
        doc_ansi.apply_record({"dir": "out", "verb": "f", "body": {"sf": "s:1", "ops": [
            ["text", "a1", "replace", "hello"],
            ["text", "a1", "append", " world"],
            ["splice", "a1", 0, 11, ""],
        ]}})
        self.assertEqual(doc_ansi.nodes["a1"].props["text"], "")

    def test_ansi_cell_sgr_attributes_and_bounded_timing_unit_crossing(self) -> None:
        """Round 9 terminal demonstrating inputs:
        (1) VTScreen tracks semantic SGR attributes (bold/faint/italic/underline/inverse/strike):
            a struck-through item on one side only (`\\x1b[9m` vs plain) with identical fg/bg is UNEXPLAINED;
            bold/italic mismatch on a port-drawn card with identical fg/bg is UNEXPLAINED;
            pinned `host-notify-info-colour` matches omp dim truecolor (`rgb:107;114;128`, attrs=[]) vs
            port `ctx.Notify(..., "info")` (`pig_fg=None`, attrs=["faint"]) on `Plan mode enabled. Plan file: ...`,
            while rejecting a non-faint or wrong-fg port status line.
        (2) Bounded terminal duration unit-crossing (`timing` class):
            `27ms/5m` vs `1.1s/5m` on an otherwise-identical bash footer -> `timing` (0 unexplained, 1 timing_pair);
            exits non-zero unless `--allow-timing` is given;
            `27ms/5m` vs `710s/5m` (above 5.0s bound) -> `differs` (unexplained);
            `27ms/5m` vs `1.1s/5m` with another character changed on the line -> `differs` (unexplained).
        """
        allow = ansi_capture.load_terminal_allowlist()

        def _render_styled(ansi_stream: str, rows: int = 8, cols: int = 80) -> list[list]:
            vt = ansi_capture.VTScreen(rows=rows, cols=cols)
            vt.feed(ansi_stream)
            return vt.render_styled_rows()

        # (1a) Struck-through todo item on one side only, identical fg/bg -> UNEXPLAINED
        omp_strike = _render_styled(" \x1b[38;2;156;163;176m\x1b[9m[x] Check read card\x1b[0m\r\n")
        pig_plain = _render_styled(" \x1b[38;2;156;163;176m[x] Check read card\x1b[0m\r\n")
        cc_strike = ansi_capture.classify_cell_colours(omp_strike, pig_plain, allow, screen_regions=False)
        self.assertEqual(cc_strike["verdict"], "differs")
        self.assertGreater(cc_strike["unexplained"], 0)
        self.assertEqual(cc_strike["unexplained_runs"][0]["omp_attrs"], ["strike"])
        self.assertEqual(cc_strike["unexplained_runs"][0]["pig_attrs"], [])

        # (1b) Missing bold on port card header, identical fg/bg -> UNEXPLAINED
        omp_bold = _render_styled("╭─ \x1b[1m\x1b[38;2;240;192;64mRead\x1b[0m\r\n")
        pig_nobold = _render_styled("╭─ \x1b[38;2;240;192;64mRead\x1b[0m\r\n")
        cc_bold = ansi_capture.classify_cell_colours(omp_bold, pig_nobold, allow, screen_regions=False)
        self.assertEqual(cc_bold["verdict"], "differs")
        self.assertEqual(cc_bold["unexplained"], 1)
        self.assertEqual(cc_bold["unexplained_runs"][0]["omp_attrs"], ["bold"])
        self.assertEqual(cc_bold["unexplained_runs"][0]["pig_attrs"], [])

        # (1c) Pinned `host-notify-info-colour`: omp dim rgb:107;114;128 vs port faint (\x1b[39m\x1b[2m) -> EXPECTED
        omp_plan = _render_styled(" \x1b[38;2;107;114;128mPlan mode enabled. Plan file: local://PLAN.md\x1b[39m\r\n")
        pig_plan_faint = _render_styled(" \x1b[39m\x1b[2mPlan mode enabled. Plan file: local://PLAN.md\x1b[22;39m\r\n")
        cc_plan_ok = ansi_capture.classify_cell_colours(omp_plan, pig_plan_faint, allow, screen_regions=False)
        self.assertEqual(cc_plan_ok["verdict"], "expected")
        self.assertEqual(cc_plan_ok["unexplained"], 0)
        self.assertGreater(cc_plan_ok["expected"], 0)

        # Without faint (plain default fg) on port `Plan mode enabled` -> UNEXPLAINED (attrs pinned!)
        pig_plan_nofaint = _render_styled(" Plan mode enabled. Plan file: local://PLAN.md\r\n")
        cc_plan_bad = ansi_capture.classify_cell_colours(omp_plan, pig_plan_nofaint, allow, screen_regions=False)
        self.assertEqual(cc_plan_bad["verdict"], "differs")
        self.assertGreater(cc_plan_bad["unexplained"], 0)

        # (2a) Bounded duration unit-crossing: 27ms vs 1.1s on equal-width bash card footer -> TIMING
        raw_omp_fast = "╰─── ⏱ 27ms/5m ────────────────────────────────────────────────────────────────╯\n"
        raw_pig_slow = "╰─── ⏱ 1.1s/5m ────────────────────────────────────────────────────────────────╯\n"
        norm_o, norm_p = ansi_capture.width_preserving_normalize_pair(raw_omp_fast, raw_pig_slow)
        rep_t = ansi_capture.classify_terminal_diff(
            norm_o, norm_p, allow, omp_raw_text=raw_omp_fast, pig_raw_text=raw_pig_slow
        )
        self.assertEqual(rep_t["verdict"], "timing")
        self.assertEqual(rep_t["summary"]["unexplained_lines"], 0)
        self.assertEqual(rep_t["summary"]["timing_pairs"], 1)
        self.assertEqual(rep_t["summary"]["timing"], 1)

        # (2b) Unbounded duration jump: 27ms vs 710s (above MAX_TERMINAL_TIMING_CROSS_S) -> DIFFERS
        raw_pig_huge = "╰─── ⏱ 710s/5m ────────────────────────────────────────────────────────────────╯\n"
        norm_o2, norm_p2 = ansi_capture.width_preserving_normalize_pair(raw_omp_fast, raw_pig_huge)
        rep_huge = ansi_capture.classify_terminal_diff(
            norm_o2, norm_p2, allow, omp_raw_text=raw_omp_fast, pig_raw_text=raw_pig_huge
        )
        self.assertEqual(rep_huge["verdict"], "differs")
        self.assertEqual(rep_huge["summary"]["timing_pairs"], 0)
        self.assertEqual(rep_huge["summary"]["unexplained_lines"], 2)

        # (2c) Unit crossing PLUS another text change on the same line -> DIFFERS (not timing!)
        raw_pig_other = "╰─── ⏱ 1.1s/9m ────────────────────────────────────────────────────────────────╯\n"
        norm_o3, norm_p3 = ansi_capture.width_preserving_normalize_pair(raw_omp_fast, raw_pig_other)
        rep_other = ansi_capture.classify_terminal_diff(
            norm_o3, norm_p3, allow, omp_raw_text=raw_omp_fast, pig_raw_text=raw_pig_other
        )
        self.assertEqual(rep_other["verdict"], "differs")
        self.assertEqual(rep_other["summary"]["timing_pairs"], 0)
        self.assertEqual(rep_other["summary"]["unexplained_lines"], 2)

        # (2d) CLI exit code: timing exits non-zero by default, 0 only with --allow-timing
        with tempfile.TemporaryDirectory() as td:
            p_omp = Path(td) / "omp.txt"
            p_pig = Path(td) / "pig.txt"
            p_out = Path(td) / "diff.txt"
            p_omp.write_text(raw_omp_fast, encoding="utf-8")
            p_pig.write_text(raw_pig_slow, encoding="utf-8")
            old_argv = sys.argv
            try:
                sys.argv = [
                    "ansi_capture.py", "diff",
                    "--omp-screen", str(p_omp), "--pig-screen", str(p_pig),
                    "--out", str(p_out),
                ]
                self.assertEqual(ansi_capture.main(), 1)
                sys.argv.append("--allow-timing")
                self.assertEqual(ansi_capture.main(), 0)
            finally:
                sys.argv = old_argv
    def test_ansi_raw_http_request_wrap_invariance_and_structural_dock_split(self) -> None:
        """Path-length / wrap-column invariance for omp's `raw-http-request=<log>.json` dump and structural dock split:
        - Same screen capture with 4 different log path lengths / wrap positions:
          (a) short path (unwrapped, 1 line),
          (b) gate-ansi wrap (`.../http-400-requ` + `ests/...json`, 2 lines),
          (c) proofNN wrap (`.../http-400-r` + `equests/...json`, 2 lines),
          (d) narrow wrap splitting INSIDE `raw-http-request=` itself (`raw-http-` + `request=/private/tmp/...` + `...json`, 3 lines)
          ALL produce `verdict == "expected"`, `unexplained_lines == 0`, `blank_unexplained == 0`,
          and identical structural `dock_height == {"omp": ..., "pig": 15}`.
        - Structural dock split works even with an EMPTY allowlist (never depends on allowlist matchers).
        - Pinned constraints remain strict: port-side `raw-http-request=` is UNEXPLAINED, and a
          non-`.json` / tampered omp continuation line is UNEXPLAINED.
        - Tern `compare.normalize_prop_value` masks `raw-http-request=<HTTP_LOG>` identically for all path lengths.
        """
        allow = ansi_capture.load_terminal_allowlist()

        def _make_screens(raw_http_wrapped_lines: list[str], tag: str = "omp-gate-ansi") -> tuple[str, str]:
            raw_block = "\n".join(raw_http_wrapped_lines)
            omp_screen = (
                " Trigger a simulated provider error.\n"
                "\n"
                "\n"
                " Error: 400 Proof simulated provider error (type=invalid_request_error)\n"
                f"{raw_block}\n"
                "\n"
                " Plan mode enabled. Plan file: local://PLAN.md\n"
                "\n"
                " TODO\n"
                "  ├─ Verify · 2/2\n"
                "  │  ├─ ☑ Check read card\n"
                "  │  └─ ☑ Check todo card\n"
                "  └─────\n"
                "\n"
                "────────────────────────────────────────────────────────────────────────────────\n"
                " ✘ 400 Proof simulated provider error (type=invalid_request_error)\n"
                f"{raw_block}\n"
                " Dismissed when you send your next message.\n"
                "────────────────────────────────────────────────────────────────────────────────\n"
                "\n"
                f" π > ◑ Proof 1 > 🗺 Plan > 🗑 {tag}/omp/work ▶─1%──────────────╎──┃──128K─\n"
                "╰─\n"
            )
            pig_screen = (
                " Trigger a simulated provider error.\n"
                "\n"
                "\n"
                " Error: 400: {\"message\":\"Proof simulated provider\n"
                " error\",\"type\":\"invalid_request_error\"}\n"
                " If this looks like a pig bug, /bug sends a report to the developers.\n"
                "\n"
                " Plan mode enabled. Plan file: local://PLAN.md\n"
                "\n"
                " TODO\n"
                "  ├─ Verify · 2/2\n"
                "  │  ├─ ☑ Check read card\n"
                "  │  └─ ☑ Check todo card\n"
                "  └─────\n"
                "\n"
                "────────────────────────────────────────────────────────────────────────────────\n"
                " ✘ 400 Proof simulated provider error (type=invalid_request_error)\n"
                " Dismissed when you send your next message.\n"
                "────────────────────────────────────────────────────────────────────────────────\n"
                "\n"
                f" π > ◑ Proof 1 > 🗺 Plan > 🗑 {tag}/pig/work ▶─1%──────────────╎──┃──128K─\n"
                "────────────────────────────────────────────────────────────────────────────────\n"
                "\n"
                "────────────────────────────────────────────────────────────────────────────────\n"
            )
            return omp_screen, pig_screen

        cases = [
            ("unwrapped-short", [
                "   raw-http-request=/tmp/a/omp/home/.omp/logs/http-400-requests/1-x.json",
            ], 14),
            ("wrap-gate-ansi", [
                "   raw-http-request=/private/tmp/omp-gate-ansi/omp/home/.omp/logs/http-400-requ",
                "   ests/1795608649436-rwqz074294ov.json",
            ], 15),
            ("wrap-proof26-ansi", [
                "   raw-http-request=/private/tmp/omp-proof26-ansi/omp/home/.omp/logs/http-400-r",
                "   equests/1795608649436-rwqz074294ov.json",
            ], 15),
            ("wrap-inside-prefix-3lines", [
                "   raw-http-",
                "   request=/var/tmp/very-long-nested-dir/omp/home/.omp/logs/http-400-requests/",
                "   1795608649436-rwqz074294ov.json",
            ], 16),
        ]
        for label, wrapped, expected_omp_dock_h in cases:
            with self.subTest(case=label):
                omp_s, pig_s = _make_screens(wrapped)
                # Structural split MUST include TODO + banner + band + prompt even without any allowlist!
                tr_o, dk_o, _ = ansi_capture.split_screen_regions(omp_s.splitlines(), side="omp")
                tr_p, dk_p, _ = ansi_capture.split_screen_regions(pig_s.splitlines(), side="pig")
                self.assertEqual(len(dk_o), expected_omp_dock_h)
                self.assertEqual(len(dk_p), 15)
                self.assertTrue(dk_o[0].strip().startswith("TODO"))
                self.assertTrue(dk_p[0].strip().startswith("TODO"))
                self.assertTrue(any("Plan mode enabled" in l for l in tr_o))
                self.assertTrue(any("Plan mode enabled" in l for l in tr_p))

                # Full screen-region classification
                rep_scr = ansi_capture.classify_screen_regions(omp_s, pig_s, allow)
                self.assertEqual(rep_scr["verdict"], "expected", f"{label} screen: {rep_scr}")
                self.assertEqual(rep_scr["summary"]["unexplained_lines"], 0)
                self.assertEqual(rep_scr["summary"]["blank_unexplained"], 0)
                self.assertEqual(rep_scr["dock_height"]["omp"], expected_omp_dock_h)
                self.assertEqual(rep_scr["dock_height"]["pig"], 15)

                # Full scrollback classification
                norm_o, norm_p = ansi_capture.width_preserving_normalize_pair(omp_s, pig_s)
                rep_sb = ansi_capture.classify_terminal_diff(norm_o, norm_p, allow, omp_raw_text=omp_s, pig_raw_text=pig_s)
                self.assertEqual(rep_sb["verdict"], "expected", f"{label} scrollback: {rep_sb}")
                self.assertEqual(rep_sb["summary"]["unexplained_lines"], 0)
                self.assertEqual(rep_sb["summary"]["blank_unexplained"], 0)

        # Negative check 1: port-side `raw-http-request=` is NEVER allowlisted
        bad_pig = (
            "   raw-http-request=/private/tmp/omp-gate-ansi/omp/home/.omp/logs/http-400-requ\n"
            "   ests/1795608649436-rwqz074294ov.json\n"
        )
        rep_pig = ansi_capture.classify_terminal_diff("", bad_pig, allow)
        self.assertEqual(rep_pig["verdict"], "differs")
        self.assertEqual(rep_pig["summary"]["unexplained_lines"], 2)

        # Negative check 2: tampered continuation not ending in .json is UNEXPLAINED on omp side
        bad_omp = (
            "   raw-http-request=/private/tmp/omp-gate-ansi/omp/home/.omp/logs/http-400-requ\n"
            "   ests/GARBAGE_NOT_JSON.txt\n"
        )
        rep_bad_omp = ansi_capture.classify_terminal_diff(bad_omp, "", allow)
        self.assertEqual(rep_bad_omp["verdict"], "differs")
        self.assertEqual(rep_bad_omp["summary"]["unexplained_lines"], 2)

        # Tern comparer check: normalize_prop_value masks arbitrary path lengths/prefixes to `<HTTP_LOG>`
        for p in (
            "/tmp/a/omp/home/.omp/logs/http-400-requests/1-x.json",
            "/private/tmp/omp-gate-ansi/omp/home/.omp/logs/http-400-requests/1795608649436-rwqz074294ov.json",
            "/var/tmp/deep/nested/dir/omp/home/.omp/logs/http-400-requests/1795608649436-rwqz074294ov.json",
        ):
            raw = f"Proof simulated provider error (type=invalid_request_error)\nraw-http-request={p}"
            self.assertEqual(
                compare.normalize_prop_value(raw),
                "Proof simulated provider error (type=invalid_request_error)\nraw-http-request=<HTTP_LOG>",
            )
    def test_exhaustive_welcome_tips_omp_and_port_on_tern_and_ansi(self) -> None:
        """Exhaustive welcome-tip validation so no random tip draw can fail a proof run by chance:
        - Renders EVERY tip of omp's canonical tips list (and `packages/tui/src/prompt/tips.txt` when
          `OMP_SRC` is set) across all 3 symbol presets (`nerd`, `unicode`, `ascii`) x 2 platforms
          (`darwin`, `linux`) through omp's exact `expandTipKeys` + `formatDoubleTap` + `compactText`
          + `normalizeIconSpans` rules (`welcome.ts:110-145,258`, `key-hint-format.ts:106-137`,
          `icons.ts:40-70,117-120`), asserting every single one validates on `side="omp"` for both
          Tern (`compare.validate_welcome_tip_node`) and terminal (`ansi_capture.find_welcome_tip_line_indices`).
        - Renders EVERY tip of the port's `ompWelcomeTips` (`piglets/pig-tern/frontend/omp_transcript.go`)
          through the port's `ompExpandTipKey` + `ompTipTextProps` rule, asserting every single one
          validates on `side="pig"` in `compare.validate_welcome_tip_node`.
        - Renders EVERY tip of the port's `welcomeTips` (`internal/ansi/chrome.go`) on both darwin and
          linux through `expandTipKeys`, asserting every single one validates on `side="pig"` in
          `ansi_capture.find_welcome_tip_line_indices`.
        """
        symbol_presets = {
            "unicode": {
                "key.ctrl": "Ctrl", "key.ctrlMac": "⌃", "key.shift": "⇧",
                "key.alt": "Alt", "key.altMac": "⌥", "key.super": "Super", "key.superMac": "⌘",
                "key.joiner": "",
                "key.enter": "⏎", "key.esc": "⎋", "key.tab": "⇥", "key.backspace": "⌫", "key.delete": "⌦",
                "key.space": "␣", "key.up": "↑", "key.down": "↓", "key.left": "←", "key.right": "→",
            },
            "nerd": {
                "key.ctrl": "Ctrl", "key.ctrlMac": "\U000f0634", "key.shift": "\U000f0636",
                "key.alt": "Alt", "key.altMac": "\U000f0635", "key.super": "Super", "key.superMac": "\U000f0633",
                "key.joiner": " ",
                "key.enter": "\U000f0311", "key.esc": "\U000f12b7", "key.tab": "\U000f0312",
                "key.backspace": "\U000f006e", "key.delete": "\U000f0e7e", "key.space": "\U000f1050",
                "key.up": "↑", "key.down": "↓", "key.left": "←", "key.right": "→",
            },
            "ascii": {
                "key.ctrl": "Ctrl", "key.ctrlMac": "Ctrl", "key.shift": "Shift",
                "key.alt": "Alt", "key.altMac": "Alt", "key.super": "Super", "key.superMac": "Super",
                "key.joiner": "",
                "key.enter": "Enter", "key.esc": "Esc", "key.tab": "Tab", "key.backspace": "Backspace",
                "key.delete": "Delete", "key.space": "Space", "key.up": "Up", "key.down": "Down",
                "key.left": "Left", "key.right": "Right",
            },
        }
        mod_syms = {
            "ctrl": ("key.ctrl", "key.ctrlMac"),
            "alt": ("key.alt", "key.altMac"),
            "shift": ("key.shift", "key.shift"),
            "super": ("key.super", "key.superMac"),
        }
        key_syms = {
            "esc": "key.esc", "escape": "key.esc", "enter": "key.enter", "return": "key.enter",
            "space": "key.space", "tab": "key.tab", "backspace": "key.backspace", "delete": "key.delete",
            "up": "key.up", "down": "key.down", "left": "key.left", "right": "key.right",
        }
        has_letter = re.compile(r"[A-Za-z0-9]")
        pua_re = re.compile(r"[\uE000-\uF8FF\U000F0000-\U000FFFFD\U00100000-\U0010FFFD]")
        pua_runs_re = re.compile(r"[\uE000-\uF8FF\U000F0000-\U000FFFFD\U00100000-\U0010FFFD]+")
        tip_link_re = re.compile(r"\[([^\]]+)\]\(([^\s)]+)\)")
        tip_key_re = re.compile(r"\{(key|keys|tap|action):([^}]+)\}")

        def _fmt_key(key: str, preset: str, mac: bool) -> str:
            sym = symbol_presets[preset]
            parts = key.split("+")
            base = parts.pop()
            out = ""
            for mod, (gen_k, mac_k) in mod_syms.items():
                if mod not in parts:
                    continue
                lbl = sym[mac_k if mac else gen_k]
                out += f"{lbl}+" if has_letter.search(lbl) else (lbl + sym["key.joiner"])
            lower = base.lower()
            if lower in mod_syms:
                out += sym[mod_syms[lower][1 if mac else 0]]
            elif lower in key_syms:
                out += sym[key_syms[lower]]
            elif len(base) == 1:
                out += base if not parts else base.upper()
            else:
                out += base[0].upper() + base[1:]
            return out

        def _fmt_double_tap(key: str, preset: str, mac: bool) -> str:
            sym = symbol_presets[preset]
            lbl = _fmt_key(key, preset, mac)
            sep = " " if has_letter.search(lbl) else sym["key.joiner"]
            return f"{lbl}{sep}{lbl}"

        def _expand_omp_keys(tip: str, preset: str, mac: bool) -> str:
            def _repl(m: re.Match[str]) -> str:
                kind, val = m.group(1), m.group(2)
                keys = val.split(",")
                if kind == "keys":
                    return "/".join(_fmt_key(k, preset, mac) for k in keys)
                if kind == "tap":
                    return _fmt_double_tap(keys[0], preset, mac)
                return _fmt_key(keys[0], preset, mac)
            return tip_key_re.sub(_repl, tip)

        def _normalize_icon_spans(spans: list[dict[str, object]]) -> list[dict[str, object]]:
            if not any(pua_re.search(str(sp["t"])) for sp in spans):
                return spans
            pieces: list[dict[str, object]] = []
            for sp in spans:
                txt = str(sp["t"])
                at = 0
                for m in pua_runs_re.finditer(txt):
                    if m.start() > at:
                        pieces.append({"span": {**sp, "t": txt[at:m.start()]}, "icon": False})
                    s_tok = str(sp.get("s") or "")
                    s_new = "icon" if not s_tok else (s_tok if "icon" in s_tok.split() else f"{s_tok} icon")
                    pieces.append({"span": {**sp, "t": m.group(0), "s": s_new}, "icon": True})
                    at = m.end()
                if at < len(txt):
                    pieces.append({"span": {**sp, "t": txt[at:]} if at > 0 else dict(sp), "icon": False})
            for idx, pc in enumerate(pieces):
                if not pc["icon"]:
                    continue
                if idx > 0 and not pieces[idx - 1]["icon"]:
                    prev_sp = pieces[idx - 1]["span"]
                    if str(prev_sp["t"]).endswith(" "):
                        prev_sp["t"] = str(prev_sp["t"])[:-1]
                if idx + 1 < len(pieces) and not pieces[idx + 1]["icon"]:
                    nxt_sp = pieces[idx + 1]["span"]
                    if str(nxt_sp["t"]).startswith(" "):
                        nxt_sp["t"] = str(nxt_sp["t"])[1:]
            return [pc["span"] for pc in pieces if str(pc["span"]["t"])]

        def _render_omp_tsp_tip(raw_tip: str, preset: str, mac: bool) -> dict[str, object]:
            expanded = _expand_omp_keys(raw_tip, preset, mac)
            spans: list[dict[str, object]] = []
            cur = 0
            for m in tip_link_re.finditer(expanded):
                if m.start() > cur:
                    spans.append({"t": expanded[cur:m.start()]})
                spans.append({"t": m.group(1), "href": m.group(2), "s": "link"})
                cur = m.end()
            if cur < len(expanded):
                spans.append({"t": expanded[cur:]})
            if len(spans) == 1 and "s" not in spans[0] and "href" not in spans[0]:
                t = str(spans[0]["t"])
                if pua_re.search(t):
                    return {"wrap": "word", "role": "omp.welcome.tip-text", "spans": _normalize_icon_spans([{"t": t}])}
                return {"wrap": "word", "role": "omp.welcome.tip-text", "text": t}
            return {"wrap": "word", "role": "omp.welcome.tip-text", "spans": _normalize_icon_spans(spans)}

        def _wrap_ansi_tip(body: str, width: int = 72) -> list[str]:
            words = f"Tip: {body}".split(" ")
            out: list[str] = []
            cur = ""
            for w in words:
                cand = w if not cur else f"{cur} {w}"
                if cur and ansi_capture.display_width(cand) > width:
                    out.append(cur)
                    cur = w
                else:
                    cur = cand
            if cur:
                out.append(cur)
            return ["  ██  v19.1.1", "", *[f"    {ln}" for ln in out], ""]

        # 1. omp tips (prefer live OMP_SRC/packages/tui/src/prompt/tips.txt if set, else canonical list)
        omp_src = os.environ.get("OMP_SRC", "").strip()
        tips_txt_path = Path(omp_src) / "packages" / "tui" / "src" / "prompt" / "tips.txt" if omp_src else None
        if tips_txt_path is not None and tips_txt_path.is_file():
            raw_omp_tips = [l.strip() for l in tips_txt_path.read_text(encoding="utf-8").splitlines() if l.strip()]
        else:
            raw_omp_tips = list(compare._OMP_WELCOME_TIPS_CANONICAL)
        self.assertGreaterEqual(len(raw_omp_tips), 34)

        for tip in raw_omp_tips:
            is_no_tern = tip.startswith("[NO-TERN]")
            stripped = re.sub(r"^\[NO-TERN\]\s*", "", tip)
            for preset in ("nerd", "unicode", "ascii"):
                for mac in (True, False):
                    if not is_no_tern:
                        props = _render_omp_tsp_tip(stripped, preset, mac)
                        node = compare.TspNode(id="tt", kind="text", props=props, children=[])
                        ok, msg = compare.validate_welcome_tip_node(node, "omp")
                        self.assertTrue(ok, f"omp Tern tip failed ({preset}, mac={mac}): {tip!r} -> {props!r} ({msg})")
                    # ANSI surface: both linkable (`label`) and fallback (`label (url)`) forms
                    exp_ansi = _expand_omp_keys(stripped, preset, mac)
                    for linked in (
                        tip_link_re.sub(r"\1 (\2)", exp_ansi),
                        tip_link_re.sub(r"\1", exp_ansi),
                    ):
                        lines = _wrap_ansi_tip(linked)
                        idxs = ansi_capture.find_welcome_tip_line_indices(lines, side="omp")
                        self.assertGreater(
                            len(idxs), 0,
                            f"omp ANSI tip failed ({preset}, mac={mac}): {tip!r} -> {linked!r}",
                        )

        # 2. Port Tern tips (`piglets/pig-tern/frontend/omp_transcript.go` -> ompWelcomeTips + ompTipTextProps)
        tern_go = Path(__file__).resolve().parent.parent.parent / "pig-tern" / "frontend" / "omp_transcript.go"
        self.assertTrue(tern_go.is_file(), f"missing {tern_go}")
        m_tern = re.search(r"var\s+ompWelcomeTips\s*=\s*\[\]string\{(.*?)\n\}", tern_go.read_text(encoding="utf-8"), re.S)
        self.assertIsNotNone(m_tern)
        port_tern_tips = [lit[0] or lit[1] for lit in re.findall(r"`([^`]+)`|\"((?:\\.|[^\"\\])*)\"", m_tern.group(1)) if (lit[0] or lit[1])]
        self.assertGreater(len(port_tern_tips), 0)
        port_tern_key_map = {
            "shift+tab": "\U000f0636 \U000f0312",
            "alt+p": "\U000f0635 P",
            "ctrl+p": "\U000f0634 P",
            "ctrl+r": "\U000f0634 R",
            "ctrl+d": "\U000f0634 D",
            "escape": "\U000f12b7",
            "esc": "\U000f12b7",
        }
        for tip in port_tern_tips:
            expanded = re.sub(
                r"\{key:([^}]+)\}",
                lambda mm: port_tern_key_map.get(mm.group(1).strip().lower(), mm.group(0)),
                tip,
            )
            if pua_re.search(expanded):
                props = {"wrap": "word", "role": "omp.welcome.tip-text", "spans": _normalize_icon_spans([{"t": expanded}])}
            else:
                props = {"wrap": "word", "role": "omp.welcome.tip-text", "text": expanded}
            node = compare.TspNode(id="tt", kind="text", props=props, children=[])
            ok, msg = compare.validate_welcome_tip_node(node, "pig")
            self.assertTrue(ok, f"port Tern tip failed: {tip!r} -> {props!r} ({msg})")

        # 3. Port ANSI tips (`internal/ansi/chrome.go` -> welcomeTips + expandTipKeys on darwin & linux)
        ansi_go = (
            Path(__file__).resolve().parent.parent.parent.parent
            / "components" / "oh-my-pig" / "extensions" / "oh-my-pig" / "internal" / "ansi" / "chrome.go"
        )
        self.assertTrue(ansi_go.is_file(), f"missing {ansi_go}")
        m_ansi = re.search(r"var\s+welcomeTips\s*=\s*\[\]string\{(.*?)\n\}", ansi_go.read_text(encoding="utf-8"), re.S)
        self.assertIsNotNone(m_ansi)
        port_ansi_tips = [lit[0] or lit[1] for lit in re.findall(r"`([^`]+)`|\"((?:\\.|[^\"\\])*)\"", m_ansi.group(1)) if (lit[0] or lit[1])]
        self.assertGreater(len(port_ansi_tips), 0)
        for tip in port_ansi_tips:
            for mac in (True, False):
                expanded = _expand_omp_keys(tip, "unicode", mac)
                lines = _wrap_ansi_tip(expanded)
                idxs = ansi_capture.find_welcome_tip_line_indices(lines, side="pig")
                self.assertGreater(len(idxs), 0, f"port ANSI tip failed (mac={mac}): {tip!r} -> {expanded!r}")
    def test_capture_validity_rejects_concatenated_prompts_and_unconsumed_turns(self) -> None:
        """Capture validity gate (terminal + Tern):
        - Feeds the saved bad capture shape (two prompts concatenated on one line,
          `Show a short markdown example.Compute 20 + 22 and explain briefly.`, and
          unconsumed server turns) into `validate_terminal_capture` and `ansi_capture.main() diff`.
        - Expects `capture_valid == False`, `verdict is None`, distinct exit code `2`
          (`CAPTURE_INCOMPLETE_EXIT_CODE`), stderr/error mentioning `capture incomplete on omp`
          and `prompts concatenated` / `turn ... not consumed`, and NO diff verdict written.
        - Valid capture passes (`capture_valid == True`) and proceeds to normal diff.
        - Tern `validate_tern_capture_dir` catches unconsumed turns or missing step snapshots.
        """
        steps = [
            {"send": "Show a short markdown example.", "expect_next": 1, "wait": 20.0},
            {"send": "Compute 20 + 22 and explain briefly.", "expect_next": 2, "wait": 25.0},
            {"send": "/plan", "enter": True, "wait": 10.0, "snapshot": "plan"},
        ]
        bad_scrollback = (
            " Show a short markdown example.Compute 20 + 22 and explain briefly.\n"
            " Markdown Parity\n"
            " Plan mode enabled. Plan file: local://PLAN.md\n"
        )
        good_scrollback = (
            " Show a short markdown example.\n"
            " Markdown Parity\n"
            " Compute 20 + 22 and explain briefly.\n"
            " The answer is 42.\n"
            " Plan mode enabled. Plan file: local://PLAN.md\n"
        )
        v_bad = ansi_capture.validate_terminal_capture(
            "omp", bad_scrollback, steps, completed_turns=1, in_flight=0, snapshots_present=["plan"]
        )
        self.assertFalse(v_bad["capture_valid"])
        self.assertIn("capture incomplete on omp", v_bad["message"])
        self.assertTrue(any("prompts concatenated" in e for e in v_bad["errors"]))
        self.assertTrue(any("turn 2 not consumed" in e for e in v_bad["errors"]))

        v_good = ansi_capture.validate_terminal_capture(
            "pig", good_scrollback, steps, completed_turns=2, in_flight=0, snapshots_present=["plan"]
        )
        self.assertTrue(v_good["capture_valid"])

        # CLI `ansi_capture.py diff` gate: must exit 2, write capture_valid=false & verdict=None (no diff verdict)
        with tempfile.TemporaryDirectory() as td:
            p_omp = Path(td) / "omp.scrollback.txt"
            p_pig = Path(td) / "pig.scrollback.txt"
            p_omp_meta = Path(td) / "omp.meta.json"
            p_pig_meta = Path(td) / "pig.meta.json"
            p_json = Path(td) / "diff.json"
            p_out = Path(td) / "diff.txt"
            p_omp.write_text(bad_scrollback, encoding="utf-8")
            p_pig.write_text(good_scrollback, encoding="utf-8")
            p_omp_meta.write_text(json.dumps({"completed_turns": 1, "in_flight": 0, "snapshots": ["plan"]}), encoding="utf-8")
            p_pig_meta.write_text(json.dumps({"completed_turns": 2, "in_flight": 0, "snapshots": ["plan"]}), encoding="utf-8")
            old_argv = sys.argv
            try:
                sys.argv = [
                    "ansi_capture.py", "diff",
                    "--omp-screen", str(p_omp), "--pig-screen", str(p_pig),
                    "--omp-scrollback", str(p_omp), "--pig-scrollback", str(p_pig),
                    "--omp-meta", str(p_omp_meta), "--pig-meta", str(p_pig_meta),
                    "--steps-json", json.dumps(steps),
                    "--json-out", str(p_json), "--out", str(p_out),
                ]
                rc = ansi_capture.main()
            finally:
                sys.argv = old_argv
            self.assertEqual(rc, ansi_capture.CAPTURE_INCOMPLETE_EXIT_CODE)
            rep = json.loads(p_json.read_text(encoding="utf-8"))
            self.assertFalse(rep["capture_valid"])
            self.assertIsNone(rep["verdict"])
            self.assertIn("capture incomplete on omp", rep["error"])
            self.assertNotIn("hunks", rep)

            # Prompt-ready detector works for both omp (`╰─`) and pig (two horizontal rules)
            omp_ready = " π > ◑ Proof 1 > 🗑 dir/omp/work ▶─1%───────128K─\n╰─\n"
            pig_ready = " π > ◑ Proof 1 > 🗑 dir/pig/work ▶─1%───────128K─\n" + ("─" * 40) + "\n\n" + ("─" * 40) + "\n"
            self.assertTrue(ansi_capture._screen_has_prompt_ready(omp_ready, side="omp"))
            self.assertFalse(ansi_capture._screen_has_prompt_ready("Loading...", side="omp"))
            self.assertTrue(ansi_capture._screen_has_prompt_ready(pig_ready, side="pig"))
            self.assertFalse(ansi_capture._screen_has_prompt_ready(" π > ◑ Proof 1 > 🗑 dir/pig/work ▶─1%───────128K─\n", side="pig"))

            # Tern capture validity check catches incomplete capture-meta or missing steps
            tdir = Path(td) / "tern_bad"
            tdir.mkdir()
            (tdir / "tsp.jsonl").write_text("{}\n", encoding="utf-8")
            (tdir / "capture-meta.json").write_text(json.dumps({"completed_turns": 15, "in_flight": 0, "expected_turns": 33}), encoding="utf-8")
            tval = compare.validate_tern_capture_dir("omp", str(tdir))
            self.assertFalse(tval["capture_valid"])
            self.assertIn("turn 33 not consumed", tval["message"])
if __name__ == "__main__":
    unittest.main()
