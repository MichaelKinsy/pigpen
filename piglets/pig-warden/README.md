# pig-warden

PiG with [warden](../../components/warden/README.md), a Go port of pi-warden (Ryan Gapac, MIT):
guardrails that **steer instead of interrupt**. **Nothing happens until you type `/warden enable`.**

| Command | What it does |
|---|---|
| `/warden enable` | Shows what warden reads and what a judged check would send, and asks for the judge: TypeSafe Jev, your session model, or offline patterns only. |
| `/warden test` | Runs four synthetic dangerous calls through the guard and shows what would happen. Executes nothing. |
| `/warden status`, `mode`, `backend`, `trace`, `disable` | See the state, change how a held call is handled, switch the judge, read the recent decisions, turn it off. |

Warden holds irreversible calls before they run and tells the agent why (offline: force-push, `rm -rf` of a home or
absolute path, `DROP`, `git reset --hard`; with a judge, the judge's irreversible score decides, see the Package README); it flags calls unrelated to your request, breaks stuck loops and asks for
evidence when the agent claims it is done. It registers no tool. The TypeSafe client library Package
[`typesafe`](../../components/typesafe/README.md) is selected as a library, not as a member.

## Run from a checkout

```sh
npm ci --ignore-scripts
npm run stage
pig piglet validate dist/staged/piglets/pig-warden/piglet.yaml
pig --piglet dist/staged/piglets/pig-warden/piglet.yaml
```

Build a Piglet Binary (needs a git checkout of the PiG source the `pig` was built from):

```sh
PIG_SOURCE_ROOT=/path/to/pig-git-checkout \
  pig piglet build dist/staged/piglets/pig-warden/piglet.yaml --format binary --targets <os>/<arch> --out ./pig-warden
```

The Binary fuses the extension (`extensionRealization: fused`). Linux/amd64 is the development default; no release
is published and no support is claimed.

## Credits

Warden: [components/warden/CREDITS.md](../../components/warden/CREDITS.md). The composition is MIT, Michael Kinsy.
