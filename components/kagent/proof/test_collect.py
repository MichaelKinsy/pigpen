import json, os, subprocess, sys, tempfile, unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import collect  # noqa: E402


def mk(path, files):
    os.makedirs(path, exist_ok=True)
    for k, v in files.items():
        open(os.path.join(path, k), "w").write(v)


class CollectTest(unittest.TestCase):
    def test_sample_and_report(self):
        with tempfile.TemporaryDirectory() as d:
            uid = "11111111-2222-3333-4444-555555555555"
            pod = f"{d}/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod{uid.replace('-', '_')}.slice"
            mk(pod, {"memory.current": "1000000000\n", "memory.stat": "anon 100\nshmem 700000000\ninactive_file 200000000\nfile 300000000\n",
                     "memory.peak": "1100000000\n", "cpu.stat": "usage_usec 5000000\n", "pids.current": "7\n",
                     "memory.pressure": "some avg10=1.50 avg60=0.00 avg300=0.00 total=99\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n",
                     "cgroup.procs": ""})
            mk(pod + "/sandbox0123456789abcdef", {"memory.current": "600000000\n", "memory.stat": "inactive_file 100\n", "cgroup.procs": ""})
            fake = os.path.join(d, "pods.json")
            json.dump({"t1": [{"name": "ns/p1", "uid": uid, "containers": {}}]}, open(fake, "w"))
            out = os.path.join(d, "mem.jsonl")
            subprocess.check_call([sys.executable, os.path.join(HERE, "collect.py"), "run", "--out", out, "--target", "t1:ns=ns,sel=x",
                                   "--cgroup-root", d, "--fake-pods", fake, "--interval", "0.05", "--duration", "0.4"])
            meta, samples = collect.load_mem(out)
            self.assertGreaterEqual(len(samples), 3)
            s = samples[0]["series"]
            self.assertEqual(s["t1|ns/p1|pod"]["working_set"], 800000000)  # current - inactive_file
            self.assertEqual(s["t1|ns/p1|pod"]["psi_some_avg10"], 1.5)
            self.assertIn("t1|ns/p1|leaf:sandbox012345678", s)
            rep = collect.build_report(out)
            row = rep["phases"][0]
            self.assertEqual(row["target"], "t1")
            self.assertAlmostEqual(row["pod_working_set"]["median_mib"], 762.9, 1)
            self.assertIn("| all | t1 |", collect.to_markdown(rep))

    def test_usage_lines(self):
        rec = {"time": "x", "level": "INFO", "msg": "Actor usage sampled", "ate.actor.uid": "u1", "ate.actor.name": "a",
               "ate.template.name": "tpl", "ate.sandbox.class": "gvisor", "ate.stats.source": "cgroup", "ate.stats.kind": "periodic",
               "ate.actor.epoch": 1, "ate.stats.memory.usage": 40, "ate.stats.memory.working_set": 30, "ate.stats.cpu.time": 0.2}
        rec2 = dict(rec, **{"ate.stats.memory.working_set": 50})
        pend = dict(rec, **{"ate.stats.source": "unspecified"})
        out = collect.parse_usage_lines([json.dumps(rec), json.dumps(rec2), json.dumps(pend), "noise"])
        self.assertEqual(len(out), 1)
        self.assertEqual(out[0]["ws_max"], 50)
        self.assertEqual(out[0]["samples"], 2)

    def test_timing_lines(self):
        r = {"msg": "Restore timing breakdown", "ate.template.name": "t", "ate.snapshot.kind": "golden", "ate.snapshot.fidelity": "memory",
             "ate.sandbox.class": "gvisor", "ate.actor.restore.duration.total": 0.4, "ate.actor.restore.duration.download": 0.3}
        r2 = dict(r, **{"ate.actor.restore.duration.total": 0.6})
        bad = dict(r, **{"error.type": "DeadlineExceeded"})
        w = {"msg": "Restore timing breakdown", "ate.template.name": "t", "ate.snapshot.kind": "golden", "ate.snapshot.fidelity": "memory",
             "ate.sandbox.class": "gvisor", "ateworker.actor.restore.duration.vm_restore": 0.1}
        out = collect.parse_timing_lines([json.dumps(x) for x in (r, r2, bad, w)])
        atelet = [o for o in out if o["layer"] == "ate"][0]
        self.assertEqual(atelet["phases"]["total"]["n"], 2)
        self.assertAlmostEqual(atelet["phases"]["total"]["median_s"], 0.5)
        self.assertEqual([o["layer"] for o in out].count("ateworker"), 1)


if __name__ == "__main__":
    unittest.main()
