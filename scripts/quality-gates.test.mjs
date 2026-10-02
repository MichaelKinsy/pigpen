import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const validator = fileURLToPath(new URL('./quality-gates.mjs', import.meta.url));
const original = { origin: 'original', authors: ['Example Author'], license: 'MIT', licenseFile: '../../LICENSE', upstreams: [] };
const skill = '---\nname: pigpen-review\ndescription: "Review code: report defects."\n---\nReview the requested change.\n';

// Exercise the same CLI used by contributors and CI, including its exit status.
test('quality gate accepts attributed resources and rejects broken distributions', () => {
  const workspace = mkdtempSync(join(tmpdir(), 'pigpen-quality-'));
  const base = join(workspace, 'base');
  const put = (root, path, text) => {
    mkdirSync(dirname(join(root, path)), { recursive: true });
    writeFileSync(join(root, path), text);
  };
  const metadata = (root, patch) => put(root, 'piglets/alpha/provenance.json', JSON.stringify({ ...original, ...patch }));
  const upstream = {
    name: 'Example Extension', authors: ['Upstream Author'], url: 'https://example.org/upstream',
    revision: 'a'.repeat(40), path: 'extensions/search', license: 'MIT',
    licenseFile: 'licenses/upstream.txt', attributionFile: 'CREDITS.md',
  };
  try {
    put(base, 'LICENSE', 'MIT License\nCopyright Example Author\nPermission is hereby granted.\n');
    put(base, 'piglets/alpha/piglet.yaml', 'name: alpha\n');
    metadata(base, {});
    put(base, 'piglets/alpha/skills/pigpen-review/SKILL.md', skill);
    put(base, 'piglets/alpha/extensions/search/extension.go', 'package search\nfunc Search() string { return "result" }\n');
    put(base, 'components/visuals/provenance.json', JSON.stringify(original));
    put(base, 'components/visuals/assets/pig/frame.txt', '..oo..\n.(..).\n');
    put(base, 'piglets/alpha/licenses/upstream.txt', 'MIT License\nCopyright Upstream Author\nPermission is hereby granted.\n');
    put(base, 'piglets/alpha/CREDITS.md', 'Ported from Example Extension by Upstream Author.\nhttps://example.org/upstream\n');
    const cases = [
      ['valid original', () => {}, null],
      ['valid port', root => metadata(root, { origin: 'ported', upstreams: [upstream] }), null],
      ['folded YAML', root => put(root, 'piglets/alpha/skills/pigpen-review/SKILL.md', skill.replace('"Review code: report defects."', '>\n  Review code:\n  report defects.')), null],
      ['unquoted colon', root => put(root, 'piglets/alpha/skills/pigpen-review/SKILL.md', skill.replace('"Review code: report defects."', 'Review code: report defects.')), /SKILL.md.*YAML/s],
      ['duplicate YAML key', root => put(root, 'piglets/alpha/skills/pigpen-review/SKILL.md', skill.replace('name: pigpen-review', 'name: pigpen-review\nname: pigpen-other')), /SKILL.md.*YAML/s],
      ['missing frontmatter', root => put(root, 'piglets/alpha/skills/pigpen-review/SKILL.md', '# Review'), /frontmatter/],
      ['non-mapping YAML', root => put(root, 'piglets/alpha/skills/pigpen-review/SKILL.md', '---\n- review\n---\n'), /mapping/],
      ['non-string description', root => put(root, 'piglets/alpha/skills/pigpen-review/SKILL.md', skill.replace('"Review code: report defects."', '42')), /description/],
      ['common user name', root => put(root, 'piglets/alpha/skills/pigpen-review/SKILL.md', skill.replace('pigpen-review', 'commit')), /pigpen-/],
      ['directory mismatch', root => put(root, 'piglets/alpha/skills/pigpen-review/SKILL.md', skill.replace('pigpen-review', 'pigpen-other')), /directory/],
      ['skill outside piglets', root => put(root, 'skills/pigpen-extra/SKILL.md', 'broken'), /frontmatter/],
      ['duplicate skill name', root => put(root, 'components/visuals/skills/pigpen-review/SKILL.md', skill), /Duplicate skill/],
      ['renamed resource copy', root => cpSync(join(root, 'piglets/alpha/extensions/search'), join(root, 'components/visuals/extensions/renamed'), { recursive: true }), /Duplicate component content/],
      ['same resource identity', root => put(root, 'components/visuals/extensions/search/main.go', 'package different\n'), /Duplicate component name/],
      ['copied asset', root => put(root, 'piglets/alpha/assets/renamed/frame.txt', '..oo..\n.(..).\n'), /Duplicate component content/],
      ['copied prompt', root => { put(root, 'piglets/alpha/prompts/review.md', 'Review this patch.'); put(root, 'components/visuals/prompts/audit.md', 'Review this patch.'); }, /Duplicate component content/],
      ['missing provenance', root => rmSync(join(root, 'piglets/alpha/provenance.json')), /provenance.json/],
      ['missing component provenance', root => rmSync(join(root, 'components/visuals/provenance.json')), /provenance.json/],
      ['missing license', root => rmSync(join(root, 'LICENSE')), /LICENSE/],
      ['empty license', root => put(root, 'LICENSE', ' '), /empty/],
      ['unlicensed', root => metadata(root, { license: 'UNLICENSED' }), /license/],
      ['missing authors', root => metadata(root, { authors: [] }), /authors/],
      ['port without upstream', root => metadata(root, { origin: 'ported' }), /upstreams/],
      ['original with upstream', root => metadata(root, { upstreams: [upstream] }), /upstreams/],
      // A work built on a library it links and redistributes (pigpen-a2a: a2a-go, Apache-2.0) is still
      // original: `dependencies` credits the library without making the work a port.
      ['original with a credited dependency', root => metadata(root, { dependencies: [{ ...upstream, path: undefined }] }), null],
      ['dependency needs a pinned revision', root => metadata(root, { dependencies: [{ ...upstream, revision: 'v1.0.0' }] }), /revision/],
      ['dependency without its credit', root => { metadata(root, { dependencies: [upstream] }); put(root, 'piglets/alpha/CREDITS.md', 'Thanks.'); }, /attribution/],
      ['dependency without its license file', root => { metadata(root, { dependencies: [upstream] }); rmSync(join(root, 'piglets/alpha/licenses/upstream.txt')); }, /upstream.txt/],
      ['dependency over http', root => metadata(root, { dependencies: [{ ...upstream, url: 'http://example.org/upstream' }] }), /HTTPS/],
      // Every lane worktree has an untracked .upstream symlink (lane infrastructure); a checkout does not.
      ['lane .upstream symlink at the root', root => symlinkSync('/tmp', join(root, '.upstream')), null],
      ['mutable upstream revision', root => metadata(root, { origin: 'ported', upstreams: [{ ...upstream, revision: 'main' }] }), /revision/],
      ['missing upstream credit', root => { metadata(root, { origin: 'ported', upstreams: [upstream] }); put(root, 'piglets/alpha/CREDITS.md', 'Thanks.'); }, /attribution/],
      ['missing upstream license', root => { metadata(root, { origin: 'ported', upstreams: [upstream] }); rmSync(join(root, 'piglets/alpha/licenses/upstream.txt')); }, /upstream.txt/],
      ['escaping license', root => metadata(root, { licenseFile: '../../../outside.txt' }), /escapes/],
      ['shared references', root => { put(root, 'piglets/beta/piglet.yaml', 'name: beta\npackages:\n  visuals: ../../components/visuals\n'); put(root, 'piglets/beta/provenance.json', JSON.stringify(original)); }, null],
      ['cross-piglet copy', root => { put(root, 'piglets/beta/provenance.json', JSON.stringify(original)); cpSync(join(root, 'piglets/alpha/extensions/search'), join(root, 'piglets/beta/extensions/renamed'), { recursive: true }); }, /Duplicate component content/],
      ['license symlink in excluded tree', root => { mkdirSync(join(root, 'dist')); symlinkSync(join(base, 'LICENSE'), join(root, 'dist/LICENSE')); metadata(root, { licenseFile: '../../dist/LICENSE' }); }, /symlink/],
      ['resource symlink', root => symlinkSync('/tmp', join(root, 'piglets/alpha/extensions/escape')), /symlink/],
    ];
    for (const [name, mutate, error] of cases) {
      const root = join(workspace, name.replaceAll(' ', '-'));
      cpSync(base, root, { recursive: true });
      mutate(root);
      const result = spawnSync(process.execPath, [validator, root], { encoding: 'utf8' });
      assert.ifError(result.error);
      const output = result.stdout + result.stderr;
      assert.equal(result.status, error ? 1 : 0, `${name}: ${output}`);
      assert.match(output, error || /Quality gates passed/, name);
      assert.equal(readFileSync(join(root, 'piglets/alpha/piglet.yaml'), 'utf8'), 'name: alpha\n');
    }
    console.log(`Verified ${cases.length} CLI cases on ${process.version}`);
  } finally {
    rmSync(workspace, { recursive: true, force: true });
  }
});
