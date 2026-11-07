#!/usr/bin/env python3
"""Assembles the Go card probe: the port's own ui*.go (and the few foundation files they call) plus probe.go.tmpl,
in a module of its own that `pig -e DIR` builds like any extension.

usage: build_probe.py EXTENSION_DIR OUTDIR
"""
import glob, os, shutil, sys

ext, out = sys.argv[1], sys.argv[2]
here = os.path.dirname(os.path.abspath(__file__))
shutil.rmtree(out, ignore_errors=True)
os.makedirs(out)
names = ["contracts.go", "dyn.go", "js.go", "message_text.go"] + sorted(os.path.basename(p) for p in glob.glob(os.path.join(ext, "ui*.go")) if not p.endswith("_test.go"))
for name in names:
    shutil.copy(os.path.join(ext, name), os.path.join(out, name))
shutil.copy(os.path.join(here, "probe", "probe.go.tmpl"), os.path.join(out, "probe.go"))
mod = open(os.path.join(ext, "go.mod")).read().replace("module github.com/MichaelKinsy/pigpen/posthorse", "module github.com/MichaelKinsy/pigpen/posthorse-probe")
open(os.path.join(out, "go.mod"), "w").write(mod)
