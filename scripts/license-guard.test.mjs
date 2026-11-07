// The licence guard on fixture trees: a GPL Package fused into an MIT Piglet is refused, GPL into GPL and MIT into GPL are
// allowed, and the refusal reaches the stage, the release plan, the catalogue and the README.
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';
import { parse, stringify } from 'yaml';
import { generateIndex, staleFiles } from './generate-index.mjs';
import { fusionProblems, readmeProblems } from './license-guard.mjs';
import { npmName } from './npm-packages.mjs';
import { recordPackage } from './packages.mjs';
import { listingEntry } from './listings.mjs';
import { LICENSES_BEGIN, LICENSES_END, renderLicensesBlock, withLicensesBlock } from './licenses.mjs';

const scripts = fileURLToPath(new URL('.', import.meta.url));
const temp = mkdtempSync(join(tmpdir(), 'pigpen-licguard-'));
after(() => rmSync(temp, { recursive: true, force: true }));
let counter = 0;

/** A minimal repository: a LICENSE, components {name: licence-or-undefined} and Piglets {name: {license, packages, extending (YAML), body (YAML)}}. */
function repository({ components, piglets }) {
  const dir = join(temp, `repo${counter++}`);
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, 'LICENSE'), 'MIT License\n');
  for (const [name, license] of Object.entries(components)) {
    const base = join(dir, 'components', name);
    mkdirSync(base, { recursive: true });
    writeFileSync(join(base, 'package.json'), JSON.stringify({ name, version: '0.1.0', ...(license ? { license } : {}) }));
    writeFileSync(join(base, 'README.md'), `# ${name}\n\nLicence: ${license ?? 'none'}.\n`);
  }
  for (const [name, { license, packages = {}, extending = '', body = 'extensions: []\n' }] of Object.entries(piglets)) {
    const base = join(dir, 'piglets', name);
    mkdirSync(base, { recursive: true });
    const list = Object.entries(packages).map(([alias, source]) => `  ${alias}: ${source}`).join('\n');
    writeFileSync(join(base, 'piglet.yaml'), `name: ${name}\ndescription: fixture\n${extending}release:\n  version: 0.1.0\nbuild:\n  targets: [linux/amd64]\n${list ? `packages:\n${list}\n` : ''}${body}`);
    writeFileSync(join(base, 'catalog.json'), JSON.stringify({ featured: false, updatedAt: '2026-11-07', languages: ['go'], notes: [] }));
    writeFileSync(join(base, 'provenance.json'), JSON.stringify({ origin: 'original', authors: ['x'], license, licenseFile: '../../LICENSE', upstreams: [] }));
  }
  return dir;
}
/** A copy of the scripts next to a fixture tree, so a script that reads the repository it lives in reads the fixture. */
const copied = (dir) => {
  cpSync(scripts, join(dir, 'scripts'), { recursive: true, filter: (path) => !/node_modules|fixtures|e2e|perf/.test(path) });
  symlinkSync(join(scripts, '..', 'node_modules'), join(dir, 'node_modules'), 'dir');
  for (const file of ['index.schema.json', 'listing.schema.json']) cpSync(join(scripts, '..', file), join(dir, file));
  writeFileSync(join(dir, 'catalog.config.json'), JSON.stringify({ repository: 'MichaelKinsy/pigpen', ref: 'main' }));
  return dir;
};

const piglet = (license, ...aliases) => ({ license, packages: Object.fromEntries(aliases.map((alias) => [alias, `local:../../components/${alias}`])) });

describe('fusionProblems', () => {
  it('refuses a GPL-3.0-or-later Package fused into an MIT Piglet, naming both licences', () => {
    const dir = repository({ components: { 'pi-opencode-free': 'GPL-3.0-or-later' }, piglets: { batteries: piglet('MIT', 'pi-opencode-free') } });
    const problems = fusionProblems(dir);
    assert.equal(problems.length, 1);
    assert.match(problems[0], /^refused: Package "pi-opencode-free" \(GPL-3\.0-or-later\) cannot be fused into the Piglet "batteries" \(MIT\): GPL-3\.0-or-later \(copyleft\) would force the whole work, Piglet and Binary, to GPL-3\.0-or-later, not MIT\. Keep it a standalone, opt-in Package/);
  });
  it('allows GPL into the same GPL and MIT into a GPL Piglet', () => {
    const dir = repository({ components: { gpl: 'GPL-3.0-or-later', mit: 'MIT' }, piglets: { copyleft: piglet('GPL-3.0-or-later', 'gpl', 'mit'), plain: piglet('MIT', 'mit') } });
    assert.deepEqual(fusionProblems(dir), []);
  });
  it('refuses GPL into a GPL-2.0-only Piglet (no relicensing path)', () => {
    const dir = repository({ components: { gpl: 'GPL-3.0-or-later' }, piglets: { old: piglet('GPL-2.0-only', 'gpl') } });
    assert.match(fusionProblems(dir)[0], /cannot be relicensed as GPL-2\.0-only/);
  });
  it('refuses a Package with no licence, an unknown one, and a Piglet with no licence', () => {
    const dir = repository({ components: { none: undefined, odd: 'Some-Custom-1.0', fine: 'MIT' }, piglets: { a: piglet('MIT', 'none'), b: piglet('MIT', 'odd'), c: piglet(undefined, 'fine') } });
    const problems = fusionProblems(dir);
    assert.match(problems.find((p) => p.includes('"none"')), /no licence is declared/);
    assert.match(problems.find((p) => p.includes('"odd"')), /not in Pigpen's licence compatibility table/);
    assert.ok(problems.some((p) => /Piglet "c" declares no licence/.test(p)));
    assert.ok(problems.some((p) => /Piglet "c"/.test(p) && /"fine"/.test(p)), 'the Piglet without a licence refuses every Package too');
  });
  it('refuses a Package from a source whose licence cannot be read, and skips the Piglet\'s own resources', () => {
    const dir = repository({ components: {}, piglets: { remote: { license: 'MIT', packages: { thing: 'npm:thing@1.0.0', own: 'local:./extensions/own' } } } });
    const problems = fusionProblems(dir);
    assert.equal(problems.length, 1);
    assert.match(problems[0], /Piglet "remote" selects Package "thing" \(written in "remote"\) from "npm:thing@1\.0\.0", whose licence cannot be read here/);
  });
  it('checks only the Piglets named', () => {
    const dir = repository({ components: { gpl: 'GPL-3.0-only' }, piglets: { bad: piglet('MIT', 'gpl'), good: piglet('GPL-3.0-only', 'gpl') } });
    assert.equal(fusionProblems(dir).length, 1);
    assert.deepEqual(fusionProblems(dir, ['good']), []);
    assert.equal(fusionProblems(dir, ['bad']).length, 1);
  });
  it('passes on this repository today', () => assert.deepEqual(fusionProblems(), []));
});

describe('extends', () => {
  const base = (license, ...aliases) => piglet(license, ...aliases);
  const child = (license, source = 'local:../base/piglet.yaml', extra = '') => ({ license, extending: `extends:\n  source: ${source}\n${extra}`, body: '' });

  it('checks the Packages a Piglet inherits, against the licence of the Piglet that is built', () => {
    const dir = repository({ components: { gpl: 'GPL-3.0-or-later' }, piglets: { base: base('GPL-3.0-or-later', 'gpl'), child: child('MIT') } });
    assert.deepEqual(fusionProblems(dir, ['base']), []);
    const problems = fusionProblems(dir, ['child']);
    assert.ok(problems.some((p) => /^refused: Package "gpl" \(GPL-3\.0-or-later\) cannot be fused into the Piglet "child" \(MIT\)/.test(p)), problems.join('\n'));
    assert.ok(problems.some((p) => /^refused: Piglet "base" \(GPL-3\.0-or-later\) cannot be fused into the Piglet "child" \(MIT\).*Extend a Piglet whose licence is compatible/.test(p)), problems.join('\n'));
  });
  it('follows the whole chain', () => {
    const dir = repository({ components: { gpl: 'GPL-3.0-or-later' }, piglets: {
      root: base('GPL-3.0-or-later', 'gpl'),
      middle: { license: 'GPL-3.0-or-later', extending: 'extends:\n  source: local:../root/piglet.yaml\n', body: '' },
      leaf: { license: 'MIT', extending: 'extends:\n  source: local:../middle/piglet.yaml\n', body: '' },
    } });
    assert.deepEqual(fusionProblems(dir, ['middle']), []);
    const problems = fusionProblems(dir, ['leaf']);
    assert.ok(problems.some((p) => p.includes('Package "gpl"') && p.includes('"leaf"')), problems.join('\n'));
  });
  it('allows a child under the same GPL as its base', () => {
    const dir = repository({ components: { gpl: 'GPL-3.0-or-later', mit: 'MIT' }, piglets: { base: base('GPL-3.0-or-later', 'gpl'), child: { ...piglet('GPL-3.0-or-later', 'mit'), extending: 'extends:\n  source: local:../base/piglet.yaml\n' } } });
    assert.deepEqual(fusionProblems(dir), []);
  });
  it('still checks a base Package that the child removes or replaces: the base\'s staged directory is linked either way', () => {
    // The jev/typesafe shape: a dual-licensed base whose extension links a GPL library Package; an MIT child removes or replaces it.
    const components = { typesafe: 'GPL-3.0-or-later', mit: 'MIT' };
    const dual = piglet('MIT OR GPL-3.0-or-later', 'typesafe');
    const kept = repository({ components, piglets: { base: dual, child: child('MIT') } });
    const removed = repository({ components, piglets: { base: dual, child: child('MIT', 'local:../base/piglet.yaml', '  remove:\n    packages: [typesafe]\n') } });
    const replaced = repository({ components, piglets: { base: dual, child: { license: 'MIT', extending: 'extends:\n  source: local:../base/piglet.yaml\n', packages: { typesafe: 'local:../../components/mit' }, body: '' } } });
    assert.deepEqual(fusionProblems(kept, ['base']), [], 'the dual-licensed base alone may carry it');
    for (const dir of [kept, removed, replaced]) {
      const problems = fusionProblems(dir, ['child']);
      assert.ok(problems.some((p) => /^refused: Package "typesafe" \(GPL-3\.0-or-later\) cannot be fused into the Piglet "child" \(MIT\)/.test(p)), problems.join('\n'));
    }
  });
  it('lets a child remove or replace an inherited Package that is fine anyway', () => {
    const components = { fine: 'MIT', other: 'Apache-2.0' };
    const dir = repository({ components, piglets: { base: base('MIT', 'fine'), child: { license: 'MIT', extending: 'extends:\n  source: local:../base/piglet.yaml\n  remove:\n    packages: [fine]\n', packages: { fine: 'local:../../components/other' }, body: '' } } });
    assert.deepEqual(fusionProblems(dir, ['child']), []);
  });
  it('resolves an inherited Package from the base\'s own directory, wherever the base sits', () => {
    const dir = repository({ components: { gpl: 'GPL-3.0-or-later' }, piglets: { child: child('MIT', 'local:./nested/deeper/piglet.yaml') } });
    const nested = join(dir, 'piglets/child/nested/deeper');
    mkdirSync(nested, { recursive: true });
    writeFileSync(join(nested, 'piglet.yaml'), 'name: nested\ndescription: fixture\nrelease:\n  version: 0.1.0\npackages:\n  gpl: local:../../../../components/gpl\nextensions: []\n');
    writeFileSync(join(nested, 'provenance.json'), JSON.stringify({ origin: 'original', authors: ['x'], license: 'GPL-3.0-or-later', licenseFile: 'x', upstreams: [] }));
    const problems = fusionProblems(dir, ['child']);
    assert.ok(problems.some((p) => /^refused: Package "gpl" \(GPL-3\.0-or-later\) cannot be fused into the Piglet "child" \(MIT\)/.test(p)), problems.join('\n'));
    assert.ok(!problems.some((p) => /cannot be read here/.test(p)), problems.join('\n'));
  });
  it('refuses a chain longer than PiG\'s extends limit, but not one at the limit', () => {
    const chain = (length) => {
      const piglets = {};
      for (let i = 0; i < length; i++) piglets[`p${i}`] = i === length - 1 ? { license: 'MIT', body: '' } : { license: 'MIT', extending: `extends:\n  source: local:../p${i + 1}/piglet.yaml\n`, body: '' };
      return repository({ components: {}, piglets });
    };
    assert.deepEqual(fusionProblems(chain(9), ['p0']), [], 'nine Piglets: depth 8, PiG allows it');
    const problems = fusionProblems(chain(10), ['p0']);
    assert.equal(problems.length, 1, problems.join('\n'));
    assert.match(problems[0], /which is deeper than PiG's extends limit of 8/);
  });
  it('refuses a base that is a directory with its own message, not a crash', () => {
    const dir = repository({ components: {}, piglets: { base: piglet('MIT'), child: child('MIT', 'local:../base') } });
    const problems = fusionProblems(dir, ['child']);
    assert.equal(problems.length, 1);
    assert.match(problems[0], /^refused: Piglet "child" extends "local:\.\.\/base", which is a directory; extends needs a Piglet YAML file/);
  });
  it('checks the origin a child redefines for an inherited extension or skill, not the base\'s', () => {
    const components = { mit: 'MIT' };
    for (const kind of ['extensions', 'skills']) {
      const baseBody = `${kind}:\n  - name: e\n    origins: [package:mit]\n`;
      const redefined = `${kind}:\n  - name: e\n    origins: [npm:ffmpeg-static]\n`;
      const dir = repository({ components, piglets: { base: { ...piglet('MIT', 'mit'), body: baseBody }, child: { license: 'MIT', extending: 'extends:\n  source: local:../base/piglet.yaml\n', body: redefined } } });
      assert.deepEqual(fusionProblems(dir, ['base']), [], 'the base on its own is clean');
      const problems = fusionProblems(dir, ['child']);
      assert.equal(problems.length, 1, `${kind}: ${problems.join('\n')}`);
      assert.match(problems[0], new RegExp(`^refused: Piglet "child" takes ${kind === 'skills' ? 'skill' : 'extension'} "e" \\(written in "child"\\) from the origin "npm:ffmpeg-static", whose licence cannot be read here`));
    }
  });
  it('refuses a base it cannot read here instead of passing it', () => {
    const wanted = { 'npm:piglet-someone@1.0.0': 'a local: Piglet whose manifest', 'git:https://example.com/x.git': 'a local: Piglet whose manifest', 'local:../missing/piglet.yaml': 'a manifest inside this repository', 'local:../../../outside.yaml': 'a manifest inside this repository' };
    for (const source of Object.keys(wanted)) {
      const dir = repository({ components: {}, piglets: { child: child('MIT', source) } });
      const problems = fusionProblems(dir);
      assert.equal(problems.length, 1, source);
      assert.match(problems[0], new RegExp(`^refused: Piglet "child" extends ${JSON.stringify(source).replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}, which is not ${wanted[source]}`), problems[0]);
    }
  });
  it('refuses an extends cycle', () => {
    const dir = repository({ components: {}, piglets: {
      a: { license: 'MIT', extending: 'extends:\n  source: local:../b/piglet.yaml\n', body: '' },
      b: { license: 'MIT', extending: 'extends:\n  source: local:../a/piglet.yaml\n', body: '' },
    } });
    assert.ok(fusionProblems(dir, ['a']).some((p) => /extends cycle/.test(p)));
  });
  it('refuses the pig-porter / pig-extension-porter bypass: a GPL base Package behind an MIT derived Piglet', () => {
    // The real compositions of this repository: pig-porter has no packages of its own and extends pig-extension-porter.
    const dir = repository({ components: { 'pi-opencode-free': 'GPL-3.0-or-later' }, piglets: {} });
    for (const name of ['pig-porter', 'pig-extension-porter']) cpSync(join(scripts, '..', 'piglets', name), join(dir, 'piglets', name), { recursive: true });
    for (const name of ['extension-port', 'extension-equivalence']) cpSync(join(scripts, '..', 'components', name), join(dir, 'components', name), { recursive: true });
    assert.deepEqual(fusionProblems(dir), [], 'the real pair is clean');
    const manifest = join(dir, 'piglets/pig-extension-porter/piglet.yaml');
    const base = parse(readFileSync(manifest, 'utf8'));
    base.packages['opencode-free'] = 'local:../../components/pi-opencode-free';
    base.extensions = [...(base.extensions ?? []), { name: 'pi-opencode-free', origins: ['package:opencode-free'], tools: [] }];
    writeFileSync(manifest, stringify(base));
    const provenance = join(dir, 'piglets/pig-extension-porter/provenance.json');
    writeFileSync(provenance, readFileSync(provenance, 'utf8').replace('"MIT"', '"GPL-3.0-or-later"'));
    const problems = fusionProblems(dir, ['pig-porter']);
    assert.ok(problems.some((p) => /^refused: Package "pi-opencode-free" \(GPL-3\.0-or-later\) cannot be fused into the Piglet "pig-porter" \(MIT\)/.test(p)), problems.join('\n'));
    assert.deepEqual(fusionProblems(dir, ['pig-extension-porter']), [], 'the GPL base on its own is fine');
  });
});

describe('extension and skill origins', () => {
  const withOrigins = (license, lines) => ({ license, body: lines });
  it('checks an npm: origin that names a Package of the Pigpen catalogue by its component licence', () => {
    const dir = repository({ components: { gpl: 'GPL-3.0-or-later' }, piglets: {
      direct: withOrigins('MIT', `extensions:\n  - name: gpl\n    origins: [${`npm:${npmName('gpl')}@0.1.0`}]\n`),
      fine: withOrigins('GPL-3.0-or-later', `extensions:\n  - name: gpl\n    origins: [${`npm:${npmName('gpl')}@0.1.0`}]\n`),
    } });
    const problems = fusionProblems(dir);
    assert.equal(problems.length, 1, problems.join('\n'));
    assert.match(problems[0], /^refused: Package "gpl" \(GPL-3\.0-or-later\) cannot be fused into the Piglet "direct" \(MIT\)/);
  });
  it('accepts a catalogue npm name only plain, with an exact version or latest', () => {
    const name = npmName('gpl');
    for (const spec of [`npm:${name}`, `npm:${name}@0.1.0`, `npm:${name}@latest`, `npm:${name}@1.2.3-rc.1`]) {
      const dir = repository({ components: { gpl: 'GPL-3.0-or-later' }, piglets: { p: withOrigins('MIT', `extensions:\n  - name: g\n    origins: ["${spec}"]\n`) } });
      assert.match(fusionProblems(dir)[0], /^refused: Package "gpl" \(GPL-3\.0-or-later\) cannot be fused into the Piglet "p" \(MIT\)/, spec);
      const clean = repository({ components: { gpl: 'MIT' }, piglets: { p: withOrigins('MIT', `extensions:\n  - name: g\n    origins: ["${spec}"]\n`) } });
      assert.deepEqual(fusionProblems(clean), [], spec);
    }
  });
  it('refuses a catalogue npm name with an alias, query, range, tag or other suffix, because npm installs other code then', () => {
    const name = npmName('herdr');
    const specs = [`npm:${name}@npm:ffmpeg-static@5.2.0`, `npm:${name}@0.1.1?registry=https://registry.invalid`, `npm:${name}?registry=https://registry.invalid`,
      `npm:${name}@^0.1.0`, `npm:${name}@0.1`, `npm:${name}@beta`, `npm:${name}@0.1.0@extra`, `npm:${name}@https://example.com/x.tgz`, `npm:${name}@0.1.0#x`];
    for (const spec of specs) {
      const origin = repository({ components: { herdr: 'MIT' }, piglets: { p: withOrigins('MIT', `extensions:\n  - name: h\n    origins: ["${spec}"]\n`) } });
      const asPackage = repository({ components: { herdr: 'MIT' }, piglets: { p: { license: 'MIT', packages: { h: spec } } } });
      for (const dir of [origin, asPackage]) {
        const problems = fusionProblems(dir);
        assert.equal(problems.length, 1, `${spec}: ${problems.join('\n')}`);
        assert.match(problems[0], /whose licence cannot be read here \(an npm: source of a catalogue Package may only be the plain name, optionally followed by @<exact version> or @latest; an alias \(@npm:\.\.\.\), a \?query such as registry=/, spec);
      }
    }
  });
  it('does not map an npm name under another scope onto a component', () => {
    const dir = repository({ components: { gpl: 'GPL-3.0-or-later', mit: 'MIT' }, piglets: { p: withOrigins('MIT', 'extensions:\n  - name: g\n    origins: ["npm:@anyone/pigpen-mit@1.0.0"]\n') } });
    const problems = fusionProblems(dir);
    assert.equal(problems.length, 1);
    assert.match(problems[0], /not a Package of the Pigpen catalogue/);
  });
  it('refuses an origin whose Package is not in the catalogue, and asks for a Package entry', () => {
    for (const origin of ['npm:@someone/else@1.0.0', 'git:https://example.com/x.git', 'https://example.com/x.tgz']) {
      for (const kind of ['extensions', 'skills']) {
        const dir = repository({ components: {}, piglets: { p: withOrigins('MIT', `${kind}:\n  - name: thing\n    origins: [${origin}]\n`) } });
        const problems = fusionProblems(dir);
        assert.equal(problems.length, 1, `${kind} ${origin}`);
        assert.match(problems[0], new RegExp(`^refused: Piglet "p" takes ${kind === 'skills' ? 'skill' : 'extension'} "thing" \\(written in "p"\\) from the origin .*whose licence cannot be read here.*Add it as a Package under components/<name>`), problems[0]);
      }
    }
  });
  it('accepts package: origins and the Piglet\'s own local: resources, and checks a local: origin into components/', () => {
    const dir = repository({ components: { gpl: 'GPL-3.0-or-later', mit: 'MIT' }, piglets: {
      own: { ...piglet('MIT', 'mit'), body: 'extensions:\n  - name: a\n    origins: [package:mit]\n  - name: b\n    origins: [local:./extensions/b]\nskills:\n  - name: s\n    origins: [local:./skills/s]\n' },
      shared: withOrigins('MIT', 'extensions:\n  - name: g\n    origins: [local:../../components/gpl]\n'),
    } });
    assert.deepEqual(fusionProblems(dir, ['own']), []);
    assert.match(fusionProblems(dir, ['shared'])[0], /^refused: Package "gpl"/);
  });
});

describe('a dual-licensed Piglet (OR)', () => {
  it('needs one alternative that accepts every Package, not one per Package', () => {
    const dir = repository({ components: { two: 'GPL-2.0-only', three: 'GPL-3.0-only', mit: 'MIT' }, piglets: {
      both: piglet('GPL-2.0-only OR GPL-3.0-only', 'two', 'three'),
      'only-two': piglet('GPL-2.0-only OR GPL-3.0-only', 'two', 'mit'),
      'only-three': piglet('GPL-2.0-only OR GPL-3.0-only', 'three', 'mit'),
    } });
    const problems = fusionProblems(dir);
    assert.equal(problems.length, 1, problems.join('\n'));
    assert.match(problems[0], /^refused: the parts fused into the Piglet "both" \(two GPL-2\.0-only, three GPL-3\.0-only\) cannot be carried by any one alternative of its licence \(GPL-2\.0-only OR GPL-3\.0-only\)/);
    // and each of the two Packages alone is fine
    assert.deepEqual(fusionProblems(dir, ['only-two', 'only-three']), []);
  });
});

describe('the Package page and the README block', () => {
  it('requires a copyleft Package README to state its licence', () => {
    const dir = repository({ components: { gpl: 'GPL-3.0-or-later', mit: 'MIT' }, piglets: {} });
    assert.deepEqual(readmeProblems(dir), []);
    writeFileSync(join(dir, 'components/gpl/README.md'), '# gpl\n');
    assert.match(readmeProblems(dir)[0], /components\/gpl\/README\.md must state the licence GPL-3\.0-or-later/);
  });
  it('matches the licence on SPDX token boundaries, not as a substring', () => {
    const dir = repository({ components: { gpl: 'GPL-3.0-or-later' }, piglets: {} });
    for (const text of ['Licence: LGPL-3.0-or-later.', 'Licence: AGPL-3.0-or-later.', 'Licence: GPL-3.0-or-later-extra.', 'Licence: GPL-3.0.']) {
      writeFileSync(join(dir, 'components/gpl/README.md'), `# gpl\n\n${text}\n`);
      assert.equal(readmeProblems(dir).length, 1, text);
    }
    for (const text of ['Licence: GPL-3.0-or-later.', '(GPL-3.0-or-later)', 'under the GPL-3.0-or-later, see CREDITS']) {
      writeFileSync(join(dir, 'components/gpl/README.md'), `# gpl\n\n${text}\n`);
      assert.deepEqual(readmeProblems(dir), [], text);
    }
  });
  it('does not call a missing or unknown licence permissive in the README block', () => {
    const dir = repository({ components: { none: undefined, odd: 'Some-Custom-1.0', weak: 'MPL-2.0', fine: 'Apache-2.0' }, piglets: {} });
    const block = renderLicensesBlock(dir);
    assert.match(block, /components\/none`\]\(components\/none\) \| none declared \| no licence declared; the guard refuses it/);
    assert.match(block, /components\/odd`\]\(components\/odd\) \| Some-Custom-1\.0 \| licence not in the compatibility table; the guard refuses it/);
    assert.match(block, /components\/weak`\]\(components\/weak\) \| MPL-2\.0 \| standalone, opt-in/);
    assert.match(block, /components\/fine`\]\(components\/fine\) \| Apache-2\.0 \| permissive; may be bundled/);
    assert.equal((block.match(/may be bundled/g) ?? []).length, 1);
  });
  it('lists non-MIT Packages from their own metadata and replaces the block in place', () => {
    const dir = repository({ components: { gpl: 'GPL-3.0-or-later', mit: 'MIT', both: 'MIT AND Apache-2.0' }, piglets: {} });
    const block = renderLicensesBlock(dir);
    assert.match(block, /components\/both`\]\(components\/both\) \| MIT AND Apache-2\.0 \| permissive; may be bundled/);
    assert.match(block, /components\/gpl`\]\(components\/gpl\) \| GPL-3\.0-or-later \| standalone, opt-in; never bundled/);
    assert.doesNotMatch(block, /components\/mit/);
    const readme = `before\n${LICENSES_BEGIN}\nstale\n${LICENSES_END}\nafter\n`;
    const updated = withLicensesBlock(readme, dir);
    assert.ok(updated.startsWith('before\n') && updated.endsWith('\nafter\n') && !updated.includes('stale'));
    assert.throws(() => withLicensesBlock('no markers', dir), /needs the/);
  });
  it('puts the licence note in the catalogue entry of a non-MIT listing and none on an MIT one', () => {
    const fixture = JSON.parse(readFileSync(join(scripts, 'fixtures/listings/example-hello.json'), 'utf8'));
    assert.ok(listingEntry({ ...fixture, license: 'GPL-3.0-or-later' }).notes.some((note) => /^Licence: GPL-3\.0-or-later \(copyleft, not MIT\)\./.test(note)));
    assert.ok(!listingEntry({ ...fixture, license: 'MIT' }).notes.some((note) => note.startsWith('Licence:')));
  });
  describe('the generated index and README', () => {
    const git = (cwd, ...args) => execFileSync('git', ['-c', 'commit.gpgsign=false', '-c', 'user.name=T', '-c', 'user.email=t@example.com', ...args], { cwd, encoding: 'utf8', env: { ...process.env, GIT_CONFIG_GLOBAL: '/dev/null', GIT_CONFIG_SYSTEM: '/dev/null' } }).trim();
    const tree = () => {
      const dir = repository({ components: { gpl: 'GPL-3.0-or-later', mit: 'MIT' }, piglets: { copyleft: piglet('GPL-3.0-or-later', 'gpl'), plain: piglet('MIT', 'mit') } });
      mkdirSync(join(dir, 'releases/packages'), { recursive: true });
      writeFileSync(join(dir, 'README.md'), `# fixture\n\n${LICENSES_BEGIN}\n${LICENSES_END}\n`);
      writeFileSync(join(dir, 'components/gpl/package.json'), JSON.stringify({ name: 'pigpen-gpl', version: '0.1.0', description: 'A GPL Package', license: 'GPL-3.0-or-later', pi: { extensions: ['extensions/gpl'] } }));
      writeFileSync(join(dir, 'index.json'), '{}\n');
      git(dir, 'init', '-q', '-b', 'main');
      git(dir, 'add', '-A');
      git(dir, 'commit', '-q', '-m', 'one');
      git(dir, 'tag', 'components/gpl/v0.1.0');
      writeFileSync(join(dir, 'releases/packages/gpl.json'), JSON.stringify(recordPackage('gpl', '0.1.0', { directory: dir })));
      return dir;
    };
    const entries = (dir) => Object.fromEntries(generateIndex(dir, 'MichaelKinsy/pigpen').entries.map((entry) => [entry.id, entry]));
    const settle = (dir) => {
      for (const [file, text] of Object.entries({ 'index.json': JSON.stringify(generateIndex(dir, 'MichaelKinsy/pigpen'), null, 2) + '\n', 'README.md': withLicensesBlock(readFileSync(join(dir, 'README.md'), 'utf8'), dir) })) writeFileSync(join(dir, file), text);
    };

    it('puts the licence note on a copyleft Piglet and a copyleft Package, and none on an MIT Piglet', () => {
      const found = entries(tree());
      assert.ok(found.copyleft.notes.some((note) => /^Licence: GPL-3\.0-or-later \(copyleft, not MIT\)\. Every Package this Piglet selects has to be licence-compatible/.test(note)), found.copyleft.notes.join('\n'));
      assert.ok(!found.copyleft.notes.some((note) => /standalone, opt-in/.test(note)));
      assert.ok(!found.plain.notes.some((note) => note.startsWith('Licence:')));
      assert.ok(found['pigpen-gpl'].notes.some((note) => /^Licence: GPL-3\.0-or-later \(copyleft, not MIT\)\. It is a standalone, opt-in Package/.test(note)), found['pigpen-gpl'].notes.join('\n'));
      assert.equal(found['pigpen-gpl'].license, 'GPL-3.0-or-later');
    });
    it('reports a stale README licence block or a stale index.json, and nothing when both are current', () => {
      const dir = tree();
      assert.deepEqual(staleFiles(dir).sort(), ['README.md', 'index.json']);
      settle(dir);
      assert.deepEqual(staleFiles(dir), []);
      assert.match(readFileSync(join(dir, 'README.md'), 'utf8'), /components\/gpl`\]\(components\/gpl\) \| GPL-3\.0-or-later \| standalone, opt-in/);
      writeFileSync(join(dir, 'README.md'), readFileSync(join(dir, 'README.md'), 'utf8').replace('GPL-3.0-or-later', 'GPL-2.0-only'));
      assert.deepEqual(staleFiles(dir), ['README.md']);
      settle(dir);
      writeFileSync(join(dir, 'index.json'), '{}\n');
      assert.deepEqual(staleFiles(dir), ['index.json']);
    });
    it('generate-index --check fails on either stale file, and passes when both are current', () => {
      const dir = copied(tree());
      const check = () => spawnSync(process.execPath, [join(dir, 'scripts/generate-index.mjs'), '--check'], { encoding: 'utf8', env: { PATH: process.env.PATH } });
      settle(dir);
      assert.equal(check().status, 0, check().stderr);
      const readme = readFileSync(join(dir, 'README.md'), 'utf8');
      writeFileSync(join(dir, 'README.md'), readme.replace('GPL-3.0-or-later', 'GPL-2.0-only'));
      const stale = check();
      assert.notEqual(stale.status, 0);
      assert.match(stale.stderr, /The licence list in README\.md is stale/);
      writeFileSync(join(dir, 'README.md'), readme);
      writeFileSync(join(dir, 'index.json'), '{}\n');
      assert.match(check().stderr, /index\.json is stale/);
    });
  });
  it('keeps this repository\'s README block and index.json current', () => {
    const result = spawnSync(process.execPath, [join(scripts, 'generate-index.mjs'), '--check'], { encoding: 'utf8' });
    assert.equal(result.status, 0, result.stderr);
  });
});

describe('where the guard is wired', () => {
  const run = (script, args, env = {}) => spawnSync(process.execPath, [join(scripts, script), ...args], { encoding: 'utf8', env: { PATH: process.env.PATH, ...env } });
  const bad = () => repository({ components: { gpl: 'GPL-3.0-or-later' }, piglets: { batteries: piglet('MIT', 'gpl') } });

  it('the CLI exits 1 with the message on a refusal and 0 on a clean tree', () => {
    const refused = run('license-guard.mjs', ['--root', bad()]);
    assert.equal(refused.status, 1);
    assert.match(refused.stderr, /refused: Package "gpl" \(GPL-3\.0-or-later\) cannot be fused into the Piglet "batteries" \(MIT\)/);
    const fine = repository({ components: { mit: 'MIT' }, piglets: { batteries: piglet('GPL-3.0-or-later', 'mit') } });
    const accepted = run('license-guard.mjs', ['--root', fine]);
    assert.equal(accepted.status, 0, accepted.stderr);
  });
  it('npm run stage (stage-piglets) refuses before staging anything', () => {
    const dir = bad();
    const result = run('stage-piglets.mjs', [dir]);
    assert.equal(result.status, 1);
    assert.match(result.stderr, /refused: Package "gpl"/);
    assert.throws(() => readFileSync(join(dir, 'dist/staged/piglets/batteries/piglet.yaml')));
  });
  it('stages a compatible tree', () => {
    const dir = repository({ components: { gpl: 'GPL-3.0-or-later' }, piglets: { copyleft: piglet('GPL-3.0-or-later', 'gpl') } });
    const result = run('stage-piglets.mjs', [dir]);
    assert.equal(result.status, 0, result.stderr);
  });
  const REFUSED = /refused: Package "gpl" \(GPL-3\.0-or-later\) cannot be fused into the Piglet "batteries" \(MIT\)/;

  it('the release plan (build-matrix) refuses the tagged Piglet when it is incompatible', () => {
    const dir = copied(bad());
    const result = spawnSync(process.execPath, [join(dir, 'scripts/build-matrix.mjs')], { encoding: 'utf8', env: { PATH: process.env.PATH, GITHUB_REF_TYPE: 'tag', GITHUB_REF_NAME: 'batteries/v0.1.0', GITHUB_OUTPUT: join(dir, 'out') } });
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, REFUSED);
  });
  it('validate-manifests refuses before it asks pig for anything', () => {
    const dir = copied(bad());
    const result = spawnSync(process.execPath, [join(dir, 'scripts/validate-manifests.mjs')], { encoding: 'utf8', env: { PATH: process.env.PATH } });
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, REFUSED);
  });
  it('the CLI also fails a copyleft Package whose README does not state its licence', () => {
    const dir = repository({ components: { gpl: 'GPL-3.0-or-later' }, piglets: { batteries: piglet('GPL-3.0-or-later', 'gpl') } });
    writeFileSync(join(dir, 'components/gpl/README.md'), '# gpl\n');
    const result = run('license-guard.mjs', ['--root', dir]);
    assert.equal(result.status, 1);
    assert.match(result.stderr, /components\/gpl\/README\.md must state the licence GPL-3\.0-or-later/);
  });
  it('is part of npm run quality (the CI gate) and of npm run check', () => {
    const scriptsOf = JSON.parse(readFileSync(join(scripts, '..', 'package.json'), 'utf8')).scripts;
    assert.match(scriptsOf.quality, /(?:^|&& )node scripts\/license-guard\.mjs(?: &&|$)/);
    assert.match(scriptsOf.check, /npm run quality/);
    assert.match(scriptsOf.test, /scripts\/licenses\.test\.mjs scripts\/license-guard\.test\.mjs/);
    assert.match(readFileSync(join(scripts, '..', '.github/workflows/ci.yml'), 'utf8'), /- run: npm run check\n/);
  });
});
