# Credits

The Skills in this Package are **obra/superpowers**, https://github.com/obra/superpowers, by **Jesse Vincent**
(MIT, Copyright (c) 2025 Jesse Vincent), at release v6.4.2, commit `8ca22dba9a94f28898bbce59f2537ff4d87c747d`.

- The unmodified `skills/` tree and the license are kept in [`port/oracle/`](port/oracle) (license at
  [`port/oracle/LICENSE`](port/oracle/LICENSE)).
- [`port/ADAPTATIONS.md`](port/ADAPTATIONS.md) records every change: the Skills take the `pigpen-superpowers-`
  prefix on `name` and directory (Pigpen's Skill gate), and the 39 references of the form `superpowers:<skill>` name
  the renamed Skills. [`port/adapt.py`](port/adapt.py) makes the change from the original and nothing else.
- The adaptation (the script, the Package files) is by Michael Kinsy, MIT. Superpowers is Jesse Vincent's work; the
  method, the wording and every Skill belong to him and the contributors of the repository.
- Not included, and said so in `port/ADAPTATIONS.md`: the Node extension that injects the bootstrap, the hooks and the
  other harness manifests.
