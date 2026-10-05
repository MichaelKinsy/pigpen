"""Unit-layer mutation run for the Jev port.

Each mutant in mutations.json is applied to a scratch copy of the extension and the shared
client, then the port's Go tests run against it. `pigeq mutate --unit` cannot run this port
(its go.work lists PiG's SDK as a replace, which leaves a shared library Package unresolvable),
so this script writes a go.work that uses the SDK, the extension and the library as modules.

    python3 components/jev/port/mutate-unit.py [mutant names...]

Environment: PIG_SDK_DIR (PiG's staged Go SDK; default: `$PIG reload --sdk-path`, with PIG
defaulting to `pig`), GO (default: `go` on PATH), MUTATE_WORK (scratch directory; default: a
new temporary directory), MUTATIONS (another mutation list; default: mutations.json here). Run it with the same isolated HOME/PIG_HOME as any other pig run.
"""
import json, os, shutil, subprocess, sys, tempfile, time

HERE = os.path.dirname(os.path.abspath(__file__))
JEV = os.path.dirname(HERE)                                   # components/jev
TYPESAFE = os.path.join(os.path.dirname(JEV), "typesafe")     # components/typesafe
GO = os.environ.get("GO", "go")
SDK = os.environ.get("PIG_SDK_DIR") or subprocess.run(
    [os.environ.get("PIG", "pig"), "reload", "--sdk-path"], capture_output=True, text=True, check=True
).stdout.strip().splitlines()[-1]
WORK = os.environ.get("MUTATE_WORK") or tempfile.mkdtemp(prefix="jev-mutate-")
muts = json.load(open(os.environ.get("MUTATIONS") or os.path.join(HERE, "mutations.json")))
env = dict(os.environ, GOFLAGS="", GOTOOLCHAIN="local")


def prep():
    d = os.path.join(WORK, "u")
    shutil.rmtree(d, ignore_errors=True)
    os.makedirs(d)
    shutil.copytree(JEV, os.path.join(d, "jev"), ignore=shutil.ignore_patterns("port"))
    shutil.copytree(TYPESAFE, os.path.join(d, "typesafe"), ignore=shutil.ignore_patterns("port"))
    return d


results = []
only = sys.argv[1:]
for m in muts:
    if only and m["name"] not in only:
        continue
    d = prep()
    p = os.path.join(d, "jev", "extensions", "jev", m["file"])
    s = open(p).read()
    assert s.count(m["find"]) == 1, m["name"]
    open(p, "w").write(s.replace(m["find"], m["replace"], 1))
    work = os.path.join(d, "go.work")
    open(work, "w").write(f"go 1.26\n\nuse (\n\t{SDK}\n\t{d}/jev/extensions/jev\n\t{d}/typesafe\n)\n")
    t = time.time()
    r = subprocess.run([GO, "test", "-count=1", "-timeout", "120s", "./..."], cwd=os.path.join(d, "jev", "extensions", "jev"),
                       env=dict(env, GOWORK=work), capture_output=True, text=True)
    out = r.stdout + r.stderr
    if r.returncode == 0:
        status, detail = "SURVIVED", ""
    elif "build failed" in out or "setup failed" in out:
        status, detail = "INVALID", out.strip().split("\n")[0]
    else:
        status = "KILLED"
        detail = next((l.strip() for l in out.split("\n") if l.startswith("--- FAIL") or l.startswith("panic:")), "FAIL")
    print(f"{status} {m['name']} [{time.time()-t:.0f}s] {detail}", flush=True)
    results.append((m["name"], status))
print(sum(1 for _, s in results if s == "KILLED"), "killed of", len(results),
      "survived:", [n for n, s in results if s == "SURVIVED"], "invalid:", [n for n, s in results if s == "INVALID"])
