---
issue: 924
issue_title: "pi-permission-system: sed/awk are unconditionally excluded from the pure-reader core, so print-only invocations still consult external_directory_write"
---

# Retro: #924 — sed/awk are unconditionally excluded from the pure-reader core

## Stage: Planning (2026-09-29T06:33:38Z)

### Session summary

Planned `sed` and `awk` as presumed pure readers whose claim an allowlist prover withdraws (option walk, a fail-closed `sed` script grammar, an `awk` program-text scan, any computed word).
A corpus spike over 981 review-log commands measured 913/1013 `sed` and 134/218 `awk` units proven read-only.
Filed [#992] (computed words in `find`/`fd`/`sort`) and recorded it as a new Phase 15 step after #924.

### Observations

- The issue's flag-only framing (`RETRACTION_GUARDS`-style) is insufficient: a `sed` `w` or `awk` `print > FILENAME` can write an operand, so the guard must prove the script, not just the options.
- A computed argument can spell `-i` (GNU `sed` permutes options), so any computed word withdraws; `commandArgumentWords` did not carry that fact, hence the new `ArgWord { value, computed }` in `node-text.ts`.
- Found a pre-existing bypass: `isTransparentWrapper` passes raw quoted `CommandWord.text`, so `xargs find . '-delete'` and `xargs sort '-o' /tmp/x` get `floorExemption: "core-reader"` (measured via `BashProgram.parse`).
  The operator chose to fix it here as a leading `fix:` step.
- Operator decisions: `sed` + `awk` only (not `gawk`/`nawk`); fold the quoted bypass; file the `find`/`fd`/`sort` computed-word gap separately (#992, new step after #924).
- A GNU-vs-BSD divergence (`[/]` is a bracket on BSD, a delimiter on GNU) is handled by withdrawing whenever a delimiter sits inside a bracket expression in a regex section; the prototype wrongly applied the rule to replacements too.
- A full-suite spike with `sed`/`awk` added to the roster failed only the roster/parity tests, two "outside the core" rows, and one `token-collection.test.ts` attribution test.
- The plan adds an ADR 0013 §7 amendment (script/program-content proofs), which the roadmap's `Target:` did not name.
- The `[#880]` roadmap constraint cites `xargs sed -n` as floored; it goes stale once `sed -n` is core, so the docs step updates the example.

#### Deferred tidyings

- `scripts/measure-core-coverage.mjs`: its header claims a drift check in `command-effects.test.ts` that does not exist.

## Stage: Implementation — TDD (2026-09-29T13:46:04Z)

### Session summary

All seven planned TDD steps landed, plus one reviewer-driven fix and its docs commit: the guard-predicate reshape, the `prove()` test helper, the quoted-option wrapper bypass fix, `sed` in two steps (option walk and minimal grammar, then the full grammar), `awk`, and the docs (configuration, ADR 0013 §7 amendment, architecture `✅`).
The `pi-permission-system` suite went from 4824 to 4992 tests (+168).

### Observations

- Deviation, step 4: the `computed` rule needed two additions to be sound.
  `readArgWord` also marks a word computed when the shell rewrites its spelling (an escape, a glob, an ANSI-C string), and `commandArgumentWords` now reads every named non-prefix, non-redirect child, not only `ARG_NODE_TYPES`.
  Without the second, a bare `$opt` vanished from the argument list, so `sed -n 1p $opt ~/x/f` proved read-only while `$opt` could be `-i`.
- Deviation, step 5: the corpus re-run measured 901/1013 `sed` units proven, not the plan's "913 or more"; the gap is bare `$f` operands the widened argument list now sees.
- Three test cases in the plan were wrong about the dialect rule: `s/[]/]x/y/` and `s,[^,]*,x,` put the delimiter inside a bracket, so they withdraw.
  A label swallows a `}` (`:done}`), so the proven block form needs a newline before `}`.
- Killing mutations that did not kill as planned: adding `w` to the read-only command set left every test green, because the trailing-text rule already refuses `w out`.
  The load-bearing mutations were deleting the allowlist check (kills `v`, `}`) and relaxing the trailing rule, which needed a new fidelity pin (`pd`).
  Likewise, accepting `w` as an `s` flag survives through the trailing rule, while accepting `e` kills.
- The plan's step-5 bracket mutation needed a new case where only GNU's reading writes: `s/[/]/w out/`.
- Pre-completion reviewer, round 1: WARN, because comma-form brace expansion (`{-i,-n}`, `-n{,i}`) was not marked computed.
  Fixed in `fix(pi-permission-system): a brace-expanded argument withdraws sed's and awk's read claim`.
  `tree-sitter-bash` splits a comma brace into a `concatenation` of plain words, so the check reads the concatenation's text; a first attempt on `word` nodes also flagged `find -exec … {} +`'s empty placeholder, which bash does not expand.
- Pre-completion reviewer, delta round: PASS.
- A full-suite run once failed two `test/authority/` forwarding tests at about 87 s each; a clean re-run passed, which matches the package skill's host-load note.
- Issue [#880]'s body still cites `xargs sed -n` as floored; the roadmap entry was updated, the issue body was not.

## Stage: Final Retrospective (2026-09-29T16:35:24Z)

### Session summary

One session ran all four stages on trunk: planning, TDD (seven planned steps plus one reviewer-driven fix), ship (`pi-permission-system-v35.0.3`), and this retro.
`sed` and `awk` joined the pure-reader core behind allowlist proofs, and a pre-existing wrapper-path bypass (a quoted `'-delete'` skipping the floor) was fixed along the way.

### Observations

#### What went well

- Real-corpus spikes produced every number that reached the design, the ADR, and the planning gate: the 981 review-log commands run through the real `tree-sitter` parser gave 913/1013 and 134/218 before any code, and 901/1013 after it.
- A consumer-level spike during planning (`BashProgram.parse` over `xargs find . '-delete'`) found a real floor bypass the issue never mentioned, and the operator folded it in at the gate.
- The mandatory mutation step caught two plan-predicted killing mutations that were vacuous (adding `w` to the command set, accepting `w` as an `s` flag), which led to a fidelity pin (`pd`) that no planned test covered.
- The reviewer's re-derivation mandate ("enumerate your own candidate inputs") produced the one finding, brace expansion, that no planned test and no author check had considered.

#### What caused friction (agent side)

- `missing-context`: the planning spike built its argument words through the production filter (`ARG_NODE_TYPES`), so it inherited that filter's blind spot.
  A bare `$opt` is a `simple_expansion`, dropped before the computed check could see it.
  Self-identified mid-TDD (step 4).
  Impact: an unplanned soundness change to `commandArgumentWords`, a lower relief figure (901, not 913 or more), and a plan claim ("any computed word withdraws") that was false as planned.
- `premature-convergence`: three test inputs and two killing mutations in the plan were written from expectation, not run through the prototype that existed at planning time.
  `s/[]/]x/y/` and `s,[^,]*,x,` violate the plan's own bracket rule; `:done}` swallows its brace; `w` mutations are shadowed by the trailing-text rule.
  Impact: about six extra red/green runs during TDD and one new test, with no rework to shipped code.
- `premature-convergence`: the first brace-expansion fix matched `word` nodes before dumping the parse tree.
  `tree-sitter-bash` splits `{-i,-n}` into a `concatenation` of plain words, and the regex also flagged `find -exec … {} +`'s empty placeholder.
  Impact: one failed attempt and a 900 s tool timeout from chaining the full suite, `check`, `lint`, `fallow`, and `commit` in one call.
- `instruction-violation` (self-identified): `\u2014` and `\u2026` escapes were written into an `Edit` body twice, in the ADR 0013 amendment and inside retro code spans.
  The lint gate skips code spans, so the second needed a hand fix and an amend.
  Impact: two scripted repairs, and no escape shipped.
- `other`: the ship stage's close comment says "a majority of `awk` invocations now prove read-only", which was never re-measured after implementation (134/218 was the prototype).
  It also addresses the self-filed issue's author as "your".
  Impact: an imprecise public claim, with no rework.

#### What caused friction (user side)

- The first `/ship` attempt stalled on empty assistant turns across a model switch, and the operator re-sent the command ("Trying again").
  No work was lost, since nothing ran before the stall.

### Diagnostic details

- **Model-performance correlation**: planning, TDD, and retro ran on `claude-opus-5-5`; ship ran on `claude-sonnet-5`, which suits a scripted flow.
  Its one judgment output, the close comment, carried the unmeasured "majority" claim.
  Both `pre-completion-reviewer` rounds ran on `claude-sonnet-5` and took 12837 s and 7841 s of wall time (1.1M and 339k tokens).
  The first round did produce the one real finding, but the wall time is far beyond what the review's surface warrants and is worth watching across issues.
- **Feedback-loop gap analysis**: `check` ran after every interface-changing step and the full package suite before every commit, which is incremental.
  The one gap was chaining the full suite with the commit in a single 900 s-bounded call.

### Changes made

1. `.pi/prompts/plan-issue.md`: the parser/matcher bullet under Test Impact Analysis now says to run the TDD Order's named cases and killing mutations through an existing prototype, and to record the observed outcome.
2. `.pi/skills/reproduction/SKILL.md`: "Build it from real artifacts" now warns that a probe selecting its input through the code under test's own filter inherits that filter's blind spot.

[#880]: https://github.com/gotgenes/pi-packages/issues/880

[#992]: https://github.com/gotgenes/pi-packages/issues/992
