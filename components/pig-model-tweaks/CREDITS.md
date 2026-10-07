# Credits

`extensions/pig-model-tweaks` is a Go port of the three model extensions of
**pi-tweaks**, https://github.com/liyu1981/pi-tweaks, by **Yu Li**
(MIT, Copyright (c) 2026 Yu Li).

- Original: `@liyu1981/pi-tweaks` 0.1.1, commit `65e6ccfbb77956aa15ce34e7ca9735e1d476c292`
- The unmodified original (the three extensions and the shared `src/` they use)
  is kept under [`port/oracle/`](port/oracle) as the equivalence oracle, with its
  license at [`port/oracle/LICENSE`](port/oracle/LICENSE).
- Ported: `pt-remember-model` → `pmt-remember-model`,
  `pt-openrouter-lock-provider` → `pmt-openrouter-lock-provider`,
  `pt-model-guard` → `pmt-model-guard-pref`. Names take the `pmt-` prefix and the
  settings file is `pmt-settings.json` where the original used `pt-` and
  `pi-tweaks-settings.json`.
- Not ported: `pt-subagent`, the original's fourth extension (added after the
  pinned release), which spawns isolated child `pi` processes; it is a separate
  concern from model selection.

The Go code and its tests were written for this Package by Yu Li. Behavior,
wording and defaults follow the original; the deliberate differences are listed
in the Package README. No file of the original is modified; the port is a
separate implementation.
