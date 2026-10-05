Copy of https://github.com/nicobailon/pi-powerline-footer at commit 859dee671b633fb533b07ceba3e6c1ab1c43360a
(v0.19.1), byte for byte, except that `banner.png` is omitted.

License notice: (c) Nico Bailon, MIT as declared in package.json. The repository ships no LICENSE file; its package.json
declares `"license": "MIT"` and `"author": "Nico Bailon"`, and the owner ruled that this declaration is the grant.

Added here, not part of the original: this file, `LICENSE` (the notice above with the MIT text) and `.npmrc`
(`legacy-peer-deps=true`). The original's own `package-lock.json` is kept although the original's `.gitignore` lists it
(it is tracked upstream). `npm ci --ignore-scripts` in this directory installs the dev dependencies from it
(`@earendil-works/pi-*` 0.84.4, used only by `port/drive/drive.mjs` and the original's own tests; under Pi they come from
Pi itself).
