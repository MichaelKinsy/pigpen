# pig-autolith

PiG with **autolith-bridge**, a Common Lisp extension that hands work to [Autolith](https://autolith.rocks), a self-modifying
Common Lisp coding agent. The extension is original to this repository; it only runs the `autolith` program you install.

Autolith by Lambda Symbolics, autolith.rocks; no endorsement implied. See [CREDITS.md](CREDITS.md).

## What it adds

- `autolith_job` (prompt, optional role, timeout, permissions `auto` or `sandbox`): writes a job file to a mode-700 temporary
  directory, runs `autolith --permissions P run-job ... --events sexp`, reads the events with `*read-eval*` off, shows the run
  state, the current tool and counters in a widget and the status line, and returns Autolith's contracted result (`summary`,
  `evidence`) or an error with its category and status. Esc cancels the job: the extension starts Autolith in its own process
  group and signals the whole group (SIGTERM, then SIGKILL after 4 s), because Autolith's launcher defers SIGTERM while its SBCL
  child runs and would otherwise leave orphans.
- `autolith_session` (prompt, optional model, timeout): starts a detached primary Autolith session (`autolith --detached`), sends
  the prompt with `autolith localgroup tell`, polls its status and reads its conversation file. A `run-job` child has no
  `self.*` tools, so live self-modification needs a primary session.
- `/autolith [role=NAME] prompt`: runs a job from the prompt and posts the result into the conversation.

Nothing runs until the model calls a tool or you run `/autolith`. The extension counts tool calls by call id (Autolith reports a
finished call twice) and resets its counters when Autolith's launcher re-runs a failed job on clean source.

## Requirements

This Piglet does not bundle any of these. Without them the tools report the missing program.

1. **SBCL 2.6.9** to build the Lisp image (building the Binary, or running the Piglet from source). A Binary built with PiG 0.7.1
   carries the image. `pig setup` shows what is missing; `PIG_SBCL` names another executable.
   Official binaries: <https://www.sbcl.org/platform-table.html>.
2. **Autolith** installed and on `PATH`: `nix run github:lambda-symbolics/autolith`, or the installer on
   <https://autolith.rocks>. It was developed against Autolith 0.67.24.
3. **A model login** for Autolith (it keeps its own) and one for PiG. With a GitHub Copilot login the bundled Autolith roles fail
   (`Unsupported reasoning effort "medium" for model copilot/auto`); give the project a role that pins a Copilot model and
   `:reasoning-effort :none` in `.autolith/agents/`.
4. **PiG 0.7.1 or later.** Common Lisp extensions first ship in 0.7.0, but 0.7.0 has a known issue: it builds a Piglet Binary with
   a Lisp member and does not embed the Lisp cell, so that Binary stops at startup (see below). Running the Piglet from source works on 0.7.0.

Autolith's `--permissions auto` denied a plain test command in one real run, where `sandbox` ran it; the tool has a `permissions`
parameter for that.

## The Piglet

`piglet.yaml` selects two extensions and strips every extension runtime except Lisp and Go:

- `autolith-bridge` (`extensions/autolith-bridge/`, an ASDF system), loaded from source into a cached SBCL image.
- `fuse-anchor` (`extensions/fuse-anchor/`), an empty Go factory with no tools, commands or hooks. PiG refuses a Binary build
  with nothing to fuse, and a Lisp member is not fused, so this is what lets the Piglet build a Binary.
- `strip.features: [node-extensions, extension-sdk-rust, extension-sdk-python, self-update]`: no Node, Rust or Python extension
  runtime. The Go SDK stays (the Binary is PiG itself) and so does the Lisp SDK.
- A closed surface (`discovery.extensions: []`, `skills: []`), as the other named compositions have.

Tests of the extension (event reader, run state, job file, result envelope, a real session conversation), offline, no Autolith or
model:

```sh
sbcl --script piglets/pig-autolith/extensions/autolith-bridge/tests/parser-tests.lisp <PiG checkout>/extensions/sdk-lisp/pig-sdk.asd
```

## Try it

```sh
npm run stage
pig piglet validate dist/staged/piglets/pig-autolith/piglet.yaml
pig --piglet dist/staged/piglets/pig-autolith/piglet.yaml
```

The first start builds the Lisp image (below); later starts reuse it.

## Binary

`pig piglet build` produces a statically linked Binary (`CGO_ENABLED=0` is the default when nothing in the Binary uses cgo; `file`
reports `statically linked`, `ldd` `not a dynamic executable`). With PiG 0.7.1 the Binary embeds the Lisp cell, so it starts with
no SBCL build step and registers `autolith_job`, `autolith_session` and `/autolith`. The Lisp cell is the image below (an SBCL core),
which is why the Binary is larger than a Go-only Piglet. The built Binary started and registered its tools on a PATH with no `sbcl`
(checked on imladris); building it, and running the Piglet from source, need SBCL. Autolith and a model login are still needed for
the tools to do anything.

**PiG 0.7.0 known issue.** 0.7.0 plans a Lisp member as binary-packed but does not embed its cell (the build warns
`cell "packed-lisp:..." is not embedded`), and the Binary stops at startup:

```
pig: baked Piglet Binary binary-packed extensions do not match its plan: missing [autolith-bridge], unexpected []
```

PiG 0.7.1 fixes this (`fix/071-lisp-packed`). Pigpen builds this Piglet with PiG at commit `7c2c1da2bcba` of that branch until 0.7.1
ships. Nothing here is signed or published.

## Measured size and first-load time

Measured on **imladris** (Linux 6.8 x86-64, Intel Xeon 6746E, a shared host at load average 17 to 33 during the final runs; PiG
built from commit `7c2c1da2bcba` of `fix/071-lisp-packed`, Go 1.27.3, SBCL 2.6.9 official binary for the image build, PiG and cache
directories in a scratch HOME, `PIG_TEST_FAUX` model so no network is used), `linux/amd64`, 2026-11-05. Not measured on any other
platform.

| What | Result |
| --- | --- |
| Binary (`pig piglet build ... --targets linux/amd64`, static, stripped, Lisp cell embedded) | 112,193,696 bytes (107.0 MiB) |
| Lisp image (`cells/lisp/<hash>/runner`, an uncompressed SBCL core with the SDK and the bridge) | 48,207,344 bytes (46.0 MiB) |
| Binary first start, cold: empty PIG_HOME, extract the embedded cell, load the extension, register the tools, answer a faux turn | **0.260 s** median of 9 (0.245 to 0.269 s) |
| Binary start with the cell already extracted | **0.164 s** median of 9 |
| Piglet source, first load, cold: empty PIG_HOME and cache, build the image, load the extension, answer a turn | **16.86 s** median of 9 (16.65 to 16.99 s) |
| Piglet source with the image cached | **0.087 s** median of 9 (0.078 to 0.093 s) |

The Binary rows are the built Binary running a faux turn in JSON mode, which lists the tools the model is offered; the run fails
unless `autolith_job` and `autolith_session` are in it. The source rows were measured without `fuse-anchor` (`--without
fuse-anchor`), so that no Go cell build is in them; the image build is repeated when the extension source or the SDK changes
(`/reload` builds it again). The Binary skips that build because the image is embedded. Reproduce:

```sh
PIG_BIN=<pig built from the pinned commit> PIG_SBCL=<sbcl> node scripts/measure-lisp-piglet.mjs \
  --piglet piglets/pig-autolith/piglet.yaml --without fuse-anchor \
  --binary <built Binary> --expect-tools autolith_job,autolith_session --runs 9
```

## Platform status

The manifest lists five build targets. Only `linux/amd64` was built and run (the Binary and the source run above).
`darwin/arm64`, `darwin/amd64`, `linux/arm64` and `windows/amd64` are listed and not built or run for this Piglet, so no platform is
a support claim. The CI platform matrix leaves this Piglet out (building it needs SBCL); the job `autolith-binary` builds it and starts it on linux/amd64.
