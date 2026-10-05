# Extension porting Package

This Package supplies `pigpen-pi-extension-port`, a strict, proof-checked procedure for
porting a Pi TypeScript extension to a **Go** PiG extension. It contains no extension and
needs no Node runtime to install; the proof steps use the
[`extension-equivalence`](../extension-equivalence/README.md) Package.

The workflow, in order: SDK gap check, tests first (a fake PiG host over `net.Pipe`, and
scripted scenarios recorded from the original under Pi), red, Go implementation, a
differential check that the original under Pi and the port under PiG produce identical
traces, a mutation check, a Piglet Binary build with the extension fused, and a Package
with provenance back to the original and its license.

| File | Content |
|---|---|
| `skills/pigpen-pi-extension-port/SKILL.md` | The workflow and completion checklist, with the L-rules from PiG's own port. |
| `references/ts-to-go-patterns.md` | Event mapping, async and Promise, dialogs, exec and child processes, error text, strings and numbers, Windows paths, known SDK gaps and host differences. |
| `references/fakehost_test.go.txt` | The fake-host test harness to copy into a port (`fakehost_test.go`). |

## Use independently

From the Pigpen checkout root:

```sh
pig package validate ./components/extension-port
pig install ./components/extension-port
```

Then invoke the Skill in a PiG session:

```text
/skill:pigpen-pi-extension-port port <original.ts> to <target-directory>
```

Pigpen's own ports are dispatched from [`ports/ports.json`](../../ports/README.md): give the Skill one row,
`/skill:pigpen-pi-extension-port port row <id>`, and it takes the pinned upstream, target, license and credit from
the row and keeps the row's status (`porting`, then `review`) up to date.

The local install references this checkout; keep it at its installed path and use `pig remove`
with the same path to remove it. The [`pig-extension-porter`](../../piglets/pig-extension-porter/README.md)
Piglet selects this Skill together with the equivalence extension, from a generated local stage
(`npm run stage`); that does not install the Package into user settings.

The Skill guides an agent. It does not certify a port or run an automatic translator. The
package keyword `pig-package` lets pi-in-go.dev's Package catalog discover it once published;
it is `private` until the owner publishes it. No public package or Piglet Binary has been released.

## License and credits

[MIT](LICENSE). The procedure adapts PiG's upstream-first porter and translation rules and PiG's
0.99 port lessons; the herdr Go port contributed the fake-host and fake-CLI test patterns. See
[CREDITS.md](CREDITS.md).
