<p align="center"><img src="docs/assets/pigpen-banner.jpg" alt="Pigpen banner: a pixel-art police lineup of PiG pigs holding name placards (pig-with-batteries, pig-warden, pig-games, pig-music, a2a / acp, herdr, jev, pig-porter, pig-tern) guarded by the Go gopher. The usual suspects: Piglets and Packages for PiG."></p>

<h1 align="center">Pigpen</h1>

<p align="center">
Piglets and Packages for <a href="https://pi-in-go.dev">PiG</a>, the Go implementation of <a href="https://github.com/earendil-works/pi">Pi</a>.
<br>
<a href="https://pi-in-go.dev/piglets/catalog/"><b>Browse Piglets</b></a> ·
<a href="https://pi-in-go.dev/packages/"><b>Browse Packages</b></a> ·
<a href="https://pi-in-go.dev/piglets/build/"><b>Build a Piglet</b></a> ·
<a href="https://pi-in-go.dev/docs/latest/piglets">Docs</a>
</p>

---

**Piglet**: a named agent, such as PiG plus a set of tools, prompts and extensions, that you add from its source or pull as one signed Binary.
**Package**: one extension, skill or tool you install into any PiG.

Every Piglet in the table below selects only Go SDK extensions, so it fuses into one executable and its extensions need no Node.js. `npm run check` enforces this from the manifests. Two bench Piglets that are not listed here are the recorded exceptions, in [go-only-exceptions.json](go-only-exceptions.json). Packages can be Go or TypeScript.

## Install

These commands were run against PiG 0.7.0 on 2026-11-07. The signed Binaries that `pig piglet pull` fetches for the Piglets below were built on PiG 0.5.3 (Pi 1.4.0): their `--version` prints `0.5.3+1.4.0.74f46864`.

```sh
pig piglet pull github:MichaelKinsy/pigpen/pig-with-batteries@0.1.2   # a Piglet, as a signed Binary

pig install npm:@pi-in-go/pigpen-websearch                           # a Package, into your PiG
```

The pull prints where it put the Binary, under `~/.pig/artifacts/piglets/`. Run that file the way you run `pig`. `pig piglet pull` needs the full `github:MichaelKinsy/pigpen/<name>@<version>` form, and the Release column below gives the version for each Piglet.

The full list, with what each one does, is on **[pi-in-go.dev/piglets](https://pi-in-go.dev/piglets/catalog/)**.

## Piglets

| Piglet | Release | What you get |
|---|---|---|
| [pig-with-batteries](piglets/pig-with-batteries) | 0.1.2 | Default PiG with curated extensions: herdr state, `/context` `/tools` `/cost` `/prompts`, web search, games, music, and a typed judge and A2A that stay off until you enable them |
| [pig-essentials](piglets/pig-essentials) | 0.1.1 | Web search, sub-agents, tasks, structured questions, ponytail mode, Superpowers skills. One binary, no Node.js |
| [pig-warden](piglets/pig-warden) | 0.1.1 | Holds irreversible calls before they run and flags off-task calls and stuck loops. Off until `/warden enable` |
| [pig-porter](piglets/pig-porter) | 0.1.1 | Port a Pi TypeScript extension to Go with an equivalence check. Extends `pig-extension-porter`, which is the same today |
| [pig-tern](piglets/pig-tern) | 0.1.0 | PiG drawn natively in [Tern](https://docs.stencil.so/tern/) |
| [herdr](piglets/herdr) | 0.1.1 | Idle / working / blocked state in [herdr](https://herdr.dev) |
| [acp](piglets/acp) | 0.1.1 | Let an Agent Client Protocol client such as Zed drive PiG, through the `pig-acp` companion |
| [a2a](piglets/a2a) | 0.1.1 | Call A2A agents from tools, and serve PiG tasks to them when you configure a listener |
| [pig-games](piglets/pig-games) | 0.1.1 | Two pixel games, `/runner` and `/angry-pigs` |
| [pig-music](piglets/pig-music) | 0.1.1 | A YouTube Music player, `/music`, using yt-dlp and mpv |

[All Piglets →](https://pi-in-go.dev/piglets/catalog/)

## Port a Pi extension

PiG loads Pi TypeScript extensions in a Node.js process (22.13 or newer) through its compatible runtime modules. To make one native Go, run **pig-porter**. Its skill checks the SDK for gaps, writes the tests first, implements the Go version, checks equivalence against the original, runs a mutation check, builds the binary, and writes the Package with provenance.

```sh
pig piglet pull github:MichaelKinsy/pigpen/pig-porter@0.1.1
```

Run the Binary it prints. Every port is listed in [ports/README.md](ports/README.md), with its upstream, pinned commit, licence and credit. Want one ported? [Open an issue](https://github.com/MichaelKinsy/pigpen/issues/new/choose).

## More

- [CONTRIBUTING.md](CONTRIBUTING.md): changes, checks, the PiG pin and sign-off. Pigpen targets the current PiG release, and every port keeps the original's licence and credits its author.
- [docs/components.md](docs/components.md): what each component does and how it was proven.
- [docs/development.md](docs/development.md): running Pigpen locally, tests and CI.
- [docs/releases.md](docs/releases.md): install forms, what is published, the release process and how the site reads the index.
- [docs/layout.md](docs/layout.md): the repository tree.
- [SECURITY.md](SECURITY.md): private vulnerability reports.

## Origins

Pigpen is for PiG, which Michael Kinsy created working at Hewlett Packard Enterprise. [Pi](https://github.com/earendil-works/pi) is the reference implementation that PiG follows. Pigpen is not an official Pi project and the Pi maintainers do not endorse it.

## License

MIT, except ports, which keep their original licence (noted on each Package page and in [ports/README.md](ports/README.md)). See [docs/components.md](docs/components.md#licences-and-credits) for the split.

<!-- licenses:begin (generated by npm run generate; do not edit) -->

Packages whose licence is not plain MIT (read from each `package.json`; every other component is MIT):

| Package | Licence | Use |
|---|---|---|
| [`components/herdr`](components/herdr) | MIT AND Apache-2.0 | permissive; may be bundled |

<!-- licenses:end -->
