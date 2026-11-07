import os, random, sys, tempfile, unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import analyze  # noqa: E402

UID = "abcd1234"
MIB = 1 << 20


def leaf(name, ws_mib, cpu_ms, peak_mib=None):
    v = {"working_set": ws_mib * MIB, "cpu_usec": cpu_ms * 1000}
    if peak_mib:
        v["peak"] = peak_mib * MIB
    return f"worker|pod|sub:{UID}-{name}", v


def sample(t, *leaves):
    return {"t": t, "series": dict(leaves)}


def disappearing_leaf_series():
    """The sandbox leaf is gone at the last sample (a checkpoint leaf with little CPU is all that is left)."""
    return [
        sample(10.00, leaf("_sandbox", 20, 100, 21)),
        sample(10.25, leaf("_sandbox", 90, 400, 95)),
        sample(10.50, leaf("_sandbox", 100, 520, 110)),
        sample(10.75, leaf("_ckpt", 3, 15)),
    ]


class CpuMemoryTest(unittest.TestCase):
    def test_cpu_is_sum_of_leaf_maxima_even_when_a_leaf_is_gone_at_the_end(self):
        act = analyze.activations(analyze.actor_series(disappearing_leaf_series(), UID))
        self.assertEqual(len(act), 1)
        rec = analyze.summarize_activation(act[0])
        # old behaviour: cpu of the leaves at the last sample = 15 ms, an impossible value for a process start
        self.assertEqual(rec["cpu_ms"], 535.0)

    def test_cpu_sums_maxima_of_concurrent_leaves(self):
        s = [
            sample(1.0, leaf("_sandbox", 10, 100), leaf("_gofer", 1, 10)),
            sample(1.25, leaf("_sandbox", 12, 300), leaf("_gofer", 1, 30)),
            sample(1.5, leaf("_sandbox", 12, 250)),  # gofer gone; sandbox value lower than its earlier maximum
        ]
        rec = analyze.summarize_activation(analyze.activations(analyze.actor_series(s, UID))[0])
        self.assertEqual(rec["cpu_ms"], 330.0)

    def test_memory_is_max_over_samples_of_the_per_sample_leaf_sum(self):
        s = [
            sample(1.0, leaf("_sandbox", 40, 1, 41), leaf("_ckpt", 5, 1)),
            sample(1.25, leaf("_sandbox", 60, 2, 63)),
            sample(1.5, leaf("_ckpt", 7, 3)),
        ]
        rec = analyze.summarize_activation(analyze.activations(analyze.actor_series(s, UID))[0])
        self.assertEqual(rec["ws_max_mib"], 60.0)  # 45, 60, 7: the sum per sample, then the maximum
        self.assertEqual(rec["ws_start_mib"], 45.0)
        self.assertEqual(rec["sandbox_ws_max_mib"], 60.0)
        self.assertEqual(rec["peak_mib"], 63.0)

    def test_gap_splits_activations_and_cpu_is_per_activation(self):
        s = [sample(1.0, leaf("_sandbox", 10, 200)), sample(1.25, leaf("_sandbox", 10, 300)),
             sample(9.0, leaf("_sandbox", 10, 50)), sample(9.25, leaf("_ckpt", 1, 5))]
        acts = analyze.activations(analyze.actor_series(s, UID))
        self.assertEqual([analyze.summarize_activation(a)["cpu_ms"] for a in acts], [300.0, 55.0])

    def test_other_actors_are_ignored(self):
        s = [{"t": 1.0, "series": {"worker|pod|sub:ffff0000-_sandbox": {"working_set": MIB, "cpu_usec": 999999}, **dict([leaf("_sandbox", 5, 7)])}}]
        rec = analyze.summarize_activation(analyze.actor_series(s, UID) and analyze.activations(analyze.actor_series(s, UID))[0])
        self.assertEqual(rec["cpu_ms"], 7.0)


def synthetic_run(n_runs=6, per_run=8):
    rows, samples, t = [], [], 100.0
    for i in range(n_runs):
        uid = f"{i:08x}"
        t0 = t
        for k in range(per_run):
            # a few kilobytes of unrelated series per sample, to make the file big enough to need the budget
            series = {f"controller|c{j}|pod": {"working_set": random.Random(i * 1000 + k * 10 + j).randrange(1 << 30), "cpu_usec": random.Random(j + k).randrange(1 << 40)}
                      for j in range(40)}
            series[f"worker|pod|sub:{uid}-_sandbox"] = {"working_set": (10 + k) * MIB, "cpu_usec": (100 + k * 50) * 1000}
            samples.append({"t": round(t, 3), "series": series})
            t += 0.25
        rows.append({"warm": i == 0, "uid": uid, "activations": [{"t0": t0, "t1": t - 0.25}]})
        t += 5
    return rows, samples


class ShipRawTest(unittest.TestCase):
    def roundtrip(self, budget):
        rows, samples = synthetic_run()
        with tempfile.TemporaryDirectory() as d:
            out = os.path.join(d, "mem.jsonl.gz")
            info = analyze.ship_raw({"interval": 0.25}, samples, rows, out, budget)
            meta, got = analyze.load_mem(out)
            return info, meta, got, os.path.getsize(out), samples

    def test_full_file_when_it_fits(self):
        info, meta, got, size, samples = self.roundtrip(10_000_000)
        self.assertEqual(info["kind"], "full")
        self.assertEqual(got, samples)
        self.assertEqual(meta["shipped"], "full")
        self.assertEqual(meta["interval"], 0.25)

    def test_subset_of_first_activations_when_over_budget(self):
        _, _, _, full_size, _ = self.roundtrip(10_000_000)
        info, meta, got, size, samples = self.roundtrip(full_size - 1)
        self.assertEqual(info["kind"], "first-activations")
        self.assertLess(size, full_size)
        self.assertEqual(len(got), 5 * 8)  # the warmup run (run 0) is dropped
        self.assertEqual(meta["total_samples"], len(samples))
        self.assertTrue(info["within_budget"])

    def test_actor_leaves_only_as_last_resort_and_cpu_still_derivable(self):
        rows, samples = synthetic_run()
        with tempfile.TemporaryDirectory() as d:
            out = os.path.join(d, "m.gz")
            info = analyze.ship_raw({}, samples, rows, out, 1)
            self.assertEqual(info["kind"], "first-activations-actor-leaves-only")
            self.assertFalse(info["within_budget"])
            _, got = analyze.load_mem(out)
            uid = rows[1]["uid"]
            rec = analyze.summarize_activation(analyze.activations(analyze.actor_series(got, uid))[0])
            self.assertEqual(rec["cpu_ms"], 100 + 7 * 50)
            self.assertEqual(rec["ws_max_mib"], 17.0)


class PressureTest(unittest.TestCase):
    def test_over_gate_sessions_are_listed_and_warmups_are_not(self):
        with tempfile.TemporaryDirectory() as d:
            open(os.path.join(d, "pressure.tsv"), "w").write("w1\ta\t80.0\t180\t1\nr1\ta\t10.5\t0\t0\nr1\tb\t61.2\t180\t1\n")
            pr = analyze.load_pressure(d)
            self.assertEqual(pr[("r1", "b")], {"psi": 61.2, "waited_s": 180, "over": True})
            rows = [{"agent": "a", "warm": True, "round": 1, "pressure": pr[("w1", "a")]},
                    {"agent": "a", "warm": False, "round": 1, "pressure": pr[("r1", "a")]},
                    {"agent": "b", "warm": False, "round": 1, "pressure": pr[("r1", "b")]},
                    {"agent": "c", "warm": False, "round": 1, "pressure": None}]
            over = analyze.over_gate_sessions(rows)
            self.assertEqual([(o["agent"], o["round"]) for o in over], [("b", 1)])
            self.assertEqual(analyze.load_pressure(os.path.join(d, "none")), {})


if __name__ == "__main__":
    unittest.main()
