# pig-with-batteries

The owner's first official Piglet: default PiG plus curated extensions, with
protocol bridges planned for later. **This directory is a scaffold**, not the
finished composition or a published release. No curated resources have been
selected on the owner's behalf.

- `piglet.yaml`: authoritative PiG manifest; name, description, release version,
  resource selections and build targets live here.
- `catalog.json`: presentation-only fields that do not belong in PiG's closed
  manifest schema (status, featured flag, review date, languages, caveats).
- `extensions/`, `skills/`, `prompts/`: owned resource source directories.

Keep `status: planned` until curation, validation, source distribution and signed
releases work. `release.version: 0.1.0` reserves a development version; it does
not assert that a release exists. The index generator deliberately does not
turn build targets into supported binary platforms.

Local inspection after building a compatible PiG:

```sh
pig piglet validate piglets/pig-with-batteries/piglet.yaml
pig --piglet piglets/pig-with-batteries/piglet.yaml
```

The scaffold selects only `seed-check`, an empty Go factory with no tools,
commands or hooks. This fixture makes native build/sign/verify checks possible:
PiG refuses Binary builds with zero extensions. It is not a curated battery and
must be replaced before publication. Omitted tools use PiG's defaults; empty
discovery prevents accidental inclusion of a maintainer's local resources.
See the root [release blockers](../../RELEASE-BLOCKERS.md) before publication.
