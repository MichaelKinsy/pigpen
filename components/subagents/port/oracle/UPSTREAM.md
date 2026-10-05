# The oracle: pi-subagents

`git archive` of https://github.com/nicobailon/pi-subagents at commit `8a403efba6975988cc0488ec8bb941db5ef1a19e` (v0.73.1, MIT, by Nico Bailon),
byte for byte, except:

- `banner.png` (a 1.4 MB image) is left out;
- this file is added.

The 180 files of `test/fixtures/pi-coding-agent-shim/dist/` and `test/fixtures/pi-coding-agent-shim/node_modules/` are the
original's (it tracks them); this repository ignores `dist/` and `node_modules/`, so they are committed with `git add -f`.

Install and run its own suite: `npm ci --ignore-scripts && npm test` (Node 24). At this commit the suite has 3442 tests: 3428 pass, 13 are
skipped and 1 fails (`profiles helpers` > `applies profile models and thinking to user agents without frontmatter pins`), and it fails the
same way in a pristine clone of the repository.
