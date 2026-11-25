# Credits

`oh-my-pig` is an homage to **omp** (oh-my-pi), https://github.com/can1357/oh-my-pi, by **Can Bölük** and
**Stencil Labs, Inc.**, a fork of **pi** by **Mario Zechner** (MIT, [licenses/oh-my-pi-LICENSE](licenses/oh-my-pi-LICENSE)).
It is not affiliated with them and they have not reviewed it.

- The composition and its frontend member are by Michael Kinsy (MIT, see the repository [LICENSE](../../LICENSE)).
  The member is pig-tern's with omp's look on: see [pig-tern's credits](../pig-tern/CREDITS.md) for Stencil's
  Go SDK for Tern, which a Binary redistributes.
- The node roles and shapes the member draws in omp's look follow omp's Tern-native renderer
  (`packages/tui/src` at release tag `v19.1.1`, commit `31ffee513f27a01bba7d4dfbe12bae239cdc646a`), so that Tern's built-in styles
  for omp apply. No omp source is copied into the member.
- The tools, prompt and commands come from the [`oh-my-pig` extension](../../components/oh-my-pig/CREDITS.md),
  a Go port of parts of omp at the same commit.
- A Binary of this Piglet redistributes two third-party bundles that the extension embeds, both unmodified:
  - omp's JS cell rewriter, built with **`@babel/parser`** `7.29.9` by The Babel Team
    (https://github.com/babel/babel, commit `54405f212b4e6a40f0e1dfba9293b0e3f3dd20e3`, MIT,
    [licenses/babel-parser-LICENSE](licenses/babel-parser-LICENSE));
  - an ARIA-snapshot bundle that omp builds from **Playwright** `1.61.0` by **Microsoft Corporation**
    (https://github.com/microsoft/playwright, commit `1cc5a90cfa3eaa430b1a991963100f95126caa47`, Apache-2.0,
    [licenses/playwright-LICENSE](licenses/playwright-LICENSE), notice at
    [licenses/playwright-NOTICE](licenses/playwright-NOTICE)).

  The extension's [credits](../../components/oh-my-pig/CREDITS.md) say where each comes from in omp.
