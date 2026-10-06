// The porting Skill ships a fake-host test harness as a template. Every copy in a
// port must equal the template except for its package clause, so a fix lands once.
import assert from 'node:assert/strict';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, it } from 'node:test';
import { goModules, goWorkText, root } from './go-modules.mjs';

const template = readFileSync(join(root, 'components/extension-port/skills/pigpen-pi-extension-port/references/fakehost_test.go.txt'), 'utf8');

describe('fake host template', () => {
  it('has the placeholder package clause', () => {
    assert.match(template, /^package PORTPKG_test$/m);
  });
  for (const module of goModules()) {
    const copy = join(module, 'fakehost_test.go');
    if (!existsSync(copy)) continue;
    it(`${module.slice(root.length)} uses the template unchanged`, () => {
      const text = readFileSync(copy, 'utf8');
      const clause = /^package (\w+?)(_test)?$/m.exec(text);
      assert.ok(clause, 'copy needs a package clause');
      // An external test package (X_test) is the template as it is; a port whose fake-host tests drive unexported
      // seams (a manager, a registry) keeps the harness in its internal package (X), the clause being the only change.
      const expected = clause[2] ? template : template.replace('package PORTPKG_test', 'package PORTPKG');
      assert.equal(text, expected.replaceAll('PORTPKG', clause[1]));
    });
  }
});

// The twin helpers are a template too: a copy in a port differs only in its package clause.
describe('twin template', () => {
  const twin = readFileSync(join(root, 'components/extension-port/skills/pigpen-pi-extension-port/references/twin_test.go.txt'), 'utf8');
  it('has the placeholder package clause and the two helpers pigeq twins reads', () => {
    assert.match(twin, /^package PORTPKG$/m);
    assert.match(twin, /^func tw\(t \*testing\.T, file, title string, fn func\(t \*testing\.T\)\) \{$/m);
    assert.match(twin, /^func tskip\(t \*testing\.T, file, title, reason string\) \{$/m);
  });
  for (const module of goModules()) {
    const copy = join(module, 'twin_test.go');
    if (!existsSync(copy)) continue;
    it(`${module.slice(root.length)} uses the twin template unchanged`, () => {
      const name = /^package (\w+)$/m.exec(readFileSync(copy, 'utf8'))?.[1];
      assert.equal(readFileSync(copy, 'utf8'), twin.replaceAll('PORTPKG', name));
    });
  }
});

describe('port layout', () => {
  // Extension ports prove equivalence with scenarios and golden traces. A protocol-adapter or library
  // port (no scenarios) has its own layout check below.
  for (const port of readdirSync(join(root, 'components')).filter((n) => existsSync(join(root, 'components', n, 'port/scenarios')))) {
    it(`${port}: scenarios, golden traces and the original line up`, () => {
      const dir = join(root, 'components', port, 'port');
      // Every port records itself in PORT.md.
      assert.ok(existsSync(join(dir, 'PORT.md')), 'the port needs a PORT.md record');
      const scenarios = readdirSync(join(dir, 'scenarios')).filter((f) => f.endsWith('.json')).map((f) => f.slice(0, -5)).sort();
      const golden = readdirSync(join(dir, 'golden')).filter((f) => f.endsWith('.jsonl')).map((f) => f.slice(0, -6)).sort();
      assert.deepEqual(golden, scenarios, 'each scenario needs exactly one golden trace');
      for (const name of scenarios) {
        assert.equal(JSON.parse(readFileSync(join(dir, 'scenarios', `${name}.json`), 'utf8')).name, name, 'scenario name must match its file');
        const header = JSON.parse(readFileSync(join(dir, 'golden', `${name}.jsonl`), 'utf8').split('\n')[0]);
        // Three oracle kinds (pigeq record): the TypeScript original under Pi, an original Go
        // extension under PiG, or the port itself. Only the last is not proof of equivalence,
        // and the port's PORT.md must say so.
        assert.ok(['pi-ts', 'pig-go-upstream', 'pig-go-self'].includes(header.lane), `unknown oracle kind ${header.lane}`);
        assert.match(header.host, header.lane === 'pi-ts' ? /^pi \d+\.\d+\.\d+/ : /^pig /);
        if (header.lane === 'pig-go-self') {
          assert.match(readFileSync(join(dir, 'PORT.md'), 'utf8'), /self-recorded/i, 'a self-recorded golden trace must be labelled in PORT.md');
        }
      }
    });
  }
});

describe('adapter port layout', () => {
  for (const port of readdirSync(join(root, 'components')).filter((n) => existsSync(join(root, 'components', n, 'port')) && !existsSync(join(root, 'components', n, 'port/scenarios')))) {
    it(`${port}: the record, the unmodified original and the mutations line up`, () => {
      const dir = join(root, 'components', port, 'port');
      assert.ok(existsSync(join(dir, 'PORT.md')), 'port/PORT.md is the port record');
      // A Go-to-Go library relocation (its PORT.md says so) names its upstream by commit and blob id
      // instead of vendoring it; any other adapter port carries the unmodified original.
      const record = readFileSync(join(dir, 'PORT.md'), 'utf8');
      // A library Package (no extensions/ directory: pig-play, the library half of a port) has no extension to
      // prove; its PORT.md names where the original lives.
      const library = !existsSync(join(root, 'components', port, 'extensions'));
      const noOriginal = /Go-to-Go relocation/.test(record) || library;
      if (!noOriginal && !existsSync(join(dir, 'upstream'))) {
        assert.ok(existsSync(join(dir, 'oracle')) && readdirSync(join(dir, 'oracle')).length > 0, 'port/oracle holds the unmodified original');
      }
      const mutations = join(dir, 'mutations.json');
      if (existsSync(mutations)) {
        const list = JSON.parse(readFileSync(mutations, 'utf8'));
        assert.ok(Array.isArray(list) && list.length > 0, 'mutations.json must list mutations');
        const names = list.map((m) => m.name);
        assert.equal(new Set(names).size, names.length, 'mutation names must be unique');
        for (const m of list) {
          assert.ok(m.name && m.file && m.find && m.find !== m.replace, `mutation ${m.name}: needs name, file and a find text that differs from replace`);
        }
      }
    });
  }
});

// The Skill and the harness README quote `pigeq` commands and flags. A command or flag the
// CLI does not have sends the porter down a dead end, so every quoted one must exist.
describe('pigeq documentation matches the CLI', () => {
  const main = readFileSync(join(root, 'components/extension-equivalence/extensions/extension-equivalence/cmd/pigeq/main.go'), 'utf8');
  const usage = /usage: pigeq ([a-z|]+) /.exec(main)?.[1].split('|') ?? [];
  const flags = new Set([...main.matchAll(/(?:fs\.(?:String|Bool|Func|Int|Duration)|\bflag\.\w+)\(\s*"([a-z-]+)"/g)].map((m) => m[1]));
  const docs = {
    'the Skill': readFileSync(join(root, 'components/extension-port/skills/pigpen-pi-extension-port/SKILL.md'), 'utf8'),
    'the harness README': readFileSync(join(root, 'components/extension-equivalence/README.md'), 'utf8'),
  };
  it('knows its commands and flags', () => {
    for (const name of ['run', 'record', 'check', 'mutate', 'gaps', 'diff', 'env', 'source', 'twins', 'llm']) assert.ok(usage.includes(name), `usage lacks ${name}`);
    for (const name of ['ts', 'go', 'go-oracle', 'self', 'root', 'module', 'from', 'rev', 'script', 'ledger', 'tests', 'step-timeout']) assert.ok(flags.has(name), `flag --${name} not defined`);
  });
  // The session's variables come from `pigeq env` (Skill: "Set up the session"). A command that uses a
  // variable the script does not export (`--pi $PI` with PI unset) turns into `--pi --pig ...` and fails.
  const envGo = readFileSync(join(root, 'components/extension-equivalence/extensions/extension-equivalence/eq/env.go'), 'utf8');
  const exported = new Set([...envGo.matchAll(/export\("([A-Z_]+)"|line\("export ([A-Z_]+)=/g)].map((m) => m[1] ?? m[2]));
  it('pigeq env exports the session variables', () => {
    for (const name of ['PIGEQ_PI', 'PIGEQ_PIG', 'PIG_SDK_DIR', 'PIG_SOURCE_ROOT']) assert.ok(exported.has(name), `pigeq env does not export ${name}`);
  });
  for (const [where, text] of Object.entries(docs)) {
    it(`${where} quotes pigeq commands with variables pigeq env exports`, () => {
      for (const m of text.matchAll(/^[^\S\n]*(?:\$ )?pigeq [a-z]+(?:[^\n]*\\\n)*[^\n]*/gm)) {
        for (const v of m[0].matchAll(/\$\{?([A-Z_][A-Z0-9_]*)/g)) assert.ok(exported.has(v[1]), `${where}: "${m[0].trim()}" uses $${v[1]}, which pigeq env does not export`);
      }
    });
    it(`${where} names only existing commands and flags`, () => {
      for (const m of text.matchAll(/^[^\S\n]*(?:\$ )?pigeq ([a-z]+)([^\n]*)/gm)) {
        assert.ok(usage.includes(m[1]), `${where}: "pigeq ${m[1]}" is not a command (${usage.join(', ')})`);
        for (const f of m[2].matchAll(/--([a-z][a-z-]*)/g)) assert.ok(flags.has(f[1]), `${where}: "pigeq ${m[1]} --${f[1]}": no such flag`);
      }
    });
  }
});

// scripts/go-modules.mjs writes the go.work the repository tests use; pigeq env writes the same
// shape (eq.GoWorkFor). The two must agree: a library another module requires is replaced, not used.
describe('go.work for the repository tests', () => {
  it('uses every module and gives each a versioned replace, with the SDK replaced', () => {
    const tmp = mkdtempSync(join(tmpdir(), 'gowork-'));
    const mod = (name, body) => { const dir = join(tmp, name); mkdirSync(dir); writeFileSync(join(dir, 'go.mod'), body); return dir; };
    const sdk = mod('sdk', 'module github.com/MichaelKinsy/PiG/extensions/sdk\n\ngo 1.26\n');
    const lib = mod('lib', 'module example.org/lib\n\ngo 1.26.0\n');
    const ext = mod('ext', 'module example.org/ext\n\ngo 1.26\n\nrequire example.org/lib v0.0.0\n');
    const bare = join(tmp, 'no-go-mod');
    const text = goWorkText(sdk, [ext, lib, bare]);
    rmSync(tmp, { recursive: true, force: true }); // only the paths are compared below
    assert.match(text, /^go 1\.26\.0\n/);
    assert.ok(text.includes(`use (\n\t${ext}\n\t${lib}\n\t${bare}\n)`), text);
    assert.ok(text.includes(`replace example.org/lib v0.0.0 => ${lib}\n`) && text.includes(`replace example.org/ext v0.0.0 => ${ext}\n`), text);
    assert.ok(text.includes(`replace github.com/MichaelKinsy/PiG/extensions/sdk => ${sdk}\n`), text);
    assert.equal(text.split('replace ').length - 1, 3, 'a directory without go.mod gets no replace');
  });
});
