# Credits

The `rea` and `rea-dashboard` members are original work by Michael Kinsy (MIT, see [LICENSE](LICENSE)). They contain no
copied source. A Piglet Binary built with this Package embeds, unmodified, and redistributes:

- **REA (Reverse Engineer Anything)**, the npm package `rea-agents` **6.8.0** (tag `rea-agents-6.8.0`, commit
  `160d821ac9a0310afcb8d1fc427862dd6508add5`), © morluto, **MIT** ([licenses/rea-agents-LICENSE](licenses/rea-agents-LICENSE)).
  <https://github.com/morluto/rea>. The payload carries the published tarball as is, including REA's own `LICENSE`,
  `THIRD_PARTY_NOTICES.md`, `DISCLAIMER.md` and `licenses/`, and the production dependencies its tagged
  `package-lock.json` ([locks/](locks/)) resolves, each with the license files its npm package ships.
- **Node.js 24.22.2** (tag `v24.22.2`, commit `f7f273a9bb6445f6f7fb6349105e7532972045fb`), © Node.js contributors, **MIT**
  with the bundled-dependency notices in [licenses/node-LICENSE](licenses/node-LICENSE), which the payload also carries as
  `node/LICENSE`. <https://github.com/nodejs/node>; the binaries are the official release archives from <https://nodejs.org/dist/>.
- **klauspost/compress** v1.19.2 (zstd decoder, linked into the `rea` member), © Klaus Post and contributors, BSD-3-Clause
  and Apache-2.0 parts as recorded in its module; listed in `extensions/rea/go.mod` and `go.sum`.

REA's skill `reverse-engineer-anything` ships in [skills/](skills/) as
`pigpen-reverse-engineer-anything`: byte for byte, except the frontmatter `name`, which Pigpen's skill rule prefixes
([scripts/skill.mjs](scripts/skill.mjs)). `scripts/payload.mjs` fails when the copy drifts from the pinned package's.
