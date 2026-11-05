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

1. **SBCL 2.6.9** for PiG's Lisp extension runtime. `pig setup` shows what is missing; `PIG_SBCL` names another executable.
   Official binaries: <https://www.sbcl.org/platform-table.html>.
2. **Autolith** installed and on `PATH`: `nix run github:lambda-symbolics/autolith`, or the installer on
   <https://autolith.rocks>. It was developed against Autolith 0.67.24.
3. **A model login** for Autolith (it keeps its own) and one for PiG. With a GitHub Copilot login the bundled Autolith roles fail
   (`Unsupported reasoning effort "medium" for model copilot/auto`); give the project a role that pins a Copilot model and
   `:reasoning-effort :none` in `.autolith/agents/`.
4. **PiG 0.7.0 or later**: Common Lisp extensions first ship in 0.7.0.

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

## Binary: builds, but does not start under PiG 0.7.0

`pig piglet build` with PiG 0.7.0 builds the Binary. It is statically linked (`CGO_ENABLED=0` is the default when nothing in the
Binary uses cgo; `file` reports `statically linked`, `ldd` `not a dynamic executable`), and `--version` prints `0.7.0+1.5.0`. The
build warns `cell "packed-lisp:..." is not embedded; the Piglet Binary will build it at runtime and needs a toolchain`, and the
Binary then stops at its first real start:

```
pig: baked Piglet Binary binary-packed extensions do not match its plan: missing [autolith-bridge], unexpected []
```

PiG 0.7.0 records a Lisp member in the Binary's baked plan as binary-packed but does not embed its cell, and the startup check
fails closed. This is in PiG 0.7.0, not in this Piglet's sources; the Piglet runs from source, as above.
CI builds the Binary on every run and records this startup result (`scripts/measure-lisp-piglet.mjs`), so the line changes when a
PiG release embeds Lisp cells. Nothing here is signed or published.

## Measured size and first-load time

Measured on **imladris** (Linux 6.8 x86-64, Intel Xeon 6746E, a shared host at load average 13 during the final runs; PiG
`v0.7.0` built from commit `beb56447c476`, Go 1.27.3, SBCL 2.6.9 official binary, PiG and cache directories in a scratch HOME,
`PIG_TEST_FAUX` model so no network is used), `linux/amd64`, 2026-11-05. Not measured on any other platform.

| What | Result |
| --- | --- |
| Binary (`pig piglet build ... --targets linux/amd64`, static, stripped) | 64,016,544 bytes (61.1 MiB) |
| Lisp image PiG builds at first load (`cells/lisp/<hash>/runner`, an uncompressed SBCL core with the SDK and the bridge) | 48,174,568 bytes (45.9 MiB) |
| First load, cold: empty PIG_HOME and cache, build the image, load the extension, answer a turn | **16.81 s** median of 9 (16.69 to 17.05 s) |
| Same run with the image cached | **0.076 s** median of 9 (0.070 to 0.081 s) |

The cold run includes the image build, which is repeated when the extension source or the SDK changes (`/reload` builds it
again). It was measured on the Piglet source without `fuse-anchor`, so that no Go cell build is in it
(`--without fuse-anchor`); the Binary could not be used for the runs for the reason above. Reproduce:

```sh
PIG_BIN=<pig 0.7.0> PIG_SBCL=<sbcl> node scripts/measure-lisp-piglet.mjs \
  --piglet piglets/pig-autolith/piglet.yaml --without fuse-anchor --binary <built Binary> --runs 9
```

## Platform status

The manifest lists five build targets. Only `linux/amd64` was built and run (the Binary build and the source run above).
`darwin/arm64`, `darwin/amd64`, `linux/arm64` and `windows/amd64` are listed and not built or run for this Piglet, so no platform is
a support claim. The CI platform matrix leaves this Piglet out (it needs SBCL); the job `autolith-binary` covers it on linux/amd64.
