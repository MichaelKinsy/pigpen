// `pig install --validate-only` validates one extension (its resolver reads the source as an extension root).
// Pointed at a Package root it answers valid:false ("declares pi.extensions directories with no extension entry
// file"), for every component, because a Package's pi.extensions names extension directories and Pi's package
// rules load only the entry files in them (PiG follows Pi here). A Package is checked by `pig package validate`;
// its extensions by `pig install <extension dir> --validate-only` (scripts/validate-manifests.mjs does both).
// So no document may show the Package-root form.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';
import { root } from './go-modules.mjs';

/** The sources `pig install ... --validate-only ...` names on one line of a document (each code span on its own). */
export function validateOnlySources(line) {
  const spans = line.split('`');
  if (spans.length > 2) return spans.filter((_, i) => i % 2 === 1).flatMap((span) => commandSources(span));
  return commandSources(line);
}

function commandSources(line) {
  if (!/pig install\b/.test(line) || !/--validate-only/.test(line)) return [];
  return line.slice(line.indexOf('pig install') + 'pig install'.length).split(/\s+/)
    .map((word) => word.replace(/^['"`]|['"`.,;)]*$/g, ''))
    .filter((word) => /^\.{0,2}\/?[\w.-]+\/[\w./-]*$|^\.\/[\w.-]+$/.test(word)); // path-like words only, not prose or flags
}

/** Whether a source is a Package root: components/<name> or piglets/<name> with nothing below it. */
export const isPackageRoot = (source) => /^(?:\.\/)?(?:components|piglets)\/[^/]+\/?$/.test(source);

test('the sources are read the way the commands are written', () => {
  assert.deepEqual(validateOnlySources('pig install ./components/x/extensions/y --validate-only --json'), ['./components/x/extensions/y']);
  assert.deepEqual(validateOnlySources('`pig install components/pig-snake --validate-only` and `pig package validate` pass.'), ['components/pig-snake']);
  assert.deepEqual(validateOnlySources('`pig install components/a/extensions/a --validate-only` and `pig package validate components/a`'), ['components/a/extensions/a']);
  assert.deepEqual(validateOnlySources('pig package validate ./components/x'), []);
  assert.ok(isPackageRoot('components/pig-snake') && isPackageRoot('./components/pig-snake/'));
  assert.ok(!isPackageRoot('./components/pig-snake/extensions/pig-snake') && !isPackageRoot('./ext-a'));
});

test('no document shows `pig install --validate-only` on a Package root', () => {
  const files = execFileSync('git', ['ls-files', '*.md'], { cwd: root, encoding: 'utf8' }).split('\n').filter(Boolean);
  const found = [];
  for (const file of files) {
    readFileSync(join(root, file), 'utf8').split('\n').forEach((line, index) => {
      for (const source of validateOnlySources(line)) if (isPackageRoot(source)) found.push(`${file}:${index + 1}: ${source}`);
    });
  }
  assert.deepEqual(found, [], 'validate a Package with `pig package validate <root>`, its extension with `pig install <extension dir> --validate-only`');
});
