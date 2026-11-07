import hashlib, json, os, sys, unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import series  # noqa: E402

REASON = "rejected by basket"
REJ = "Tool call was rejected by user. Reason: " + REASON


def sha(s):
    return hashlib.sha256(s.encode()).hexdigest()


def basket(answer, approval="rejected", ok=True, retries=0):
    return {"runs": [{"ok": ok, "approval": approval, "turns": [{"text": "x", "send_retries": retries}, {"text": "x\n" + answer, "send_retries": 0}],
                      "timings_ms": {"approval_to_done": 812.4, "turn1_input_required": 700.2, "turn1_end_to_end": 1600.9}}]}


def mock_log(results, text, truncated=False):
    e = {"step": "final", "results": results, "text": text}
    if truncated:
        e["results_truncated"] = True
    return ["not json", json.dumps({"step": "probe:1", "call": "x"}), json.dumps(e)]


class RejectCheck(unittest.TestCase):
    def rec(self, doc, log, **kw):
        return series.reject_record(1, doc, log, REASON, **kw)

    def test_pass(self):
        ans = "APPROVE-DONE " + sha(REJ)
        r = self.rec(basket(ans), mock_log(REJ, ans), expect_sha=sha(REJ))
        self.assertTrue(r["pass"], r)
        self.assertEqual(r["first_attempt"], "success")
        self.assertEqual(r["timing_ms"]["approval_to_done"], 812)

    def test_reason_missing_from_mock_log_fails(self):
        ans = "APPROVE-DONE " + sha("denied")
        r = self.rec(basket(ans), mock_log("denied", ans))
        self.assertFalse(r["reason_visible_in_mock_log"])
        self.assertFalse(r["pass"])

    def test_answer_over_other_text_than_logged_fails(self):
        ans = "APPROVE-DONE " + sha("something else")
        r = self.rec(basket(ans), mock_log(REJ, "APPROVE-DONE " + sha("something else")))
        self.assertFalse(r["sha_is_sha_of_logged_results"])
        self.assertFalse(r["pass"])

    def test_not_rejected_or_wrong_expected_sha_fails(self):
        ans = "APPROVE-DONE " + sha(REJ)
        self.assertFalse(self.rec(basket(ans, approval="approved"), mock_log(REJ, ans))["pass"])
        self.assertFalse(self.rec(basket(ans), mock_log(REJ, ans), expect_sha="0" * 64)["pass"])

    def test_retried_and_failed_first_attempts(self):
        ans = "APPROVE-DONE " + sha(REJ)
        self.assertEqual(self.rec(basket(ans, retries=2), mock_log(REJ, ans))["first_attempt"], "retried")
        self.assertEqual(self.rec(basket("", ok=False), [])["first_attempt"], "failed")
        self.assertFalse(self.rec({}, [])["pass"])

    def test_truncated_log_cannot_prove_sha(self):
        ans = "APPROVE-DONE " + sha(REJ)
        self.assertFalse(self.rec(basket(ans), mock_log(REJ, ans, truncated=True))["pass"])

    def test_summary(self):
        ans = "APPROVE-DONE " + sha(REJ)
        good = self.rec(basket(ans), mock_log(REJ, ans))
        bad = series.reject_record(2, basket("", ok=False), [], REASON)
        s = series.summarize_reject([good, bad])
        self.assertEqual((s["runs"], s["pass"], s["first_attempt_success"], s["reason_visible"]), (2, 1, 1, 1))
        self.assertEqual(s["failures"][0]["run"], 2)
        self.assertIn("1 passed", series.to_md(s))


def run(variant, first_ok, outcome="jsonrpc_error", code=-32004, attempts=3, ms=900.0, ready=12.0):
    fa = {"outcome": outcome if not first_ok else "success", "n": 1}
    if code is not None and not first_ok:
        fa["rpc_code"] = code
    return {"variant": variant, "pod_ready_s": ready, "recall_ok": True,
            "series": {"first_attempt": fa, "first_attempt_success": first_ok, "eventual_success": attempts is not None,
                       "attempts_to_success": attempts, "ms_to_success": ms if attempts else None}}


class RestartSummary(unittest.TestCase):
    def test_first_attempt_rate_is_separate_from_eventual(self):
        recs = [run("idle", False), run("idle", False), run("idle", True, attempts=1, ms=300), run("idle", False, outcome="timeout", code=None, attempts=None),
                run("approval", False, attempts=2)]
        s = series.summarize_restart(recs)
        i = s["variants"]["idle"]
        self.assertEqual((i["runs"], i["first_attempt_success"], i["eventual_success"]), (4, 1, 3))
        self.assertEqual(i["first_attempt_success_rate"], 0.25)
        self.assertEqual(i["eventual_success_rate"], 0.75)
        self.assertEqual(i["first_attempt_outcomes"], {"jsonrpc_error -32004": 2, "success": 1, "timeout": 1})
        self.assertEqual(i["attempts_to_success"]["max"], 3)
        self.assertEqual(s["variants"]["approval"]["recall_after_resume_ok"], 1)
        md = series.to_md(s)
        self.assertIn("1/4 (25%)", md)
        self.assertIn("3/4 (75%)", md)


if __name__ == "__main__":
    unittest.main()
