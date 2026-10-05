// pig-music is a member of the pig-with-batteries composition, selected the same way as the other members (a Package staged
// from components/, no sibling paths), listed in the catalog and the index, and described as off until /music.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, it } from 'node:test';
import { parse } from 'yaml';
import { root } from './go-modules.mjs';

const read = (path) => readFileSync(join(root, path), 'utf8');
const piglet = parse(read('piglets/pig-with-batteries/piglet.yaml'));
const index = JSON.parse(read('index.json'));
const catalog = JSON.parse(read('piglets/pig-with-batteries/catalog.json'));
const entry = index.entries.find((p) => p.name === 'pig-with-batteries');

describe('pig-music in pig-with-batteries', () => {
  it('is selected as a Package from components/ and as an extension that registers no tools', () => {
    assert.equal(piglet.packages['pig-music'], 'local:../../components/pig-music');
    const member = piglet.extensions.find((e) => e.name === 'pig-music');
    assert.ok(member, 'no pig-music extension in the manifest');
    assert.deepEqual(member.origins, ['package:pig-music']);
    assert.deepEqual(member.tools, [], 'pig-music registers a command only');
  });

  it('is the only new reach outside the Piglet: every local Package stays under components/', () => {
    for (const [name, source] of Object.entries(piglet.packages)) {
      assert.match(source, /^local:\.\.\/\.\.\/components\/[a-z0-9-]+$/, `${name}: ${source}`);
    }
  });

  it('is described as off until /music in the catalog notes, the description and the index', () => {
    assert.match(piglet.description, /pig-music/);
    assert.match(piglet.description, /\/music/);
    const notes = catalog.notes.join('\n');
    assert.match(notes, /pig-music/);
    assert.match(notes, /nothing (starts|runs)[^.]*until you (type|run) `?\/music/i);
    assert.ok(entry, 'pig-with-batteries is not in index.json');
    assert.match(JSON.stringify(entry), /pig-music/);
  });

  it('is documented in the Piglet README (what it adds, dependencies, cookies consent) and the release notes', () => {
    const readme = read('piglets/pig-with-batteries/README.md');
    assert.match(readme, /\| `pig-music` \| `package:pig-music`/);
    assert.match(readme, /mpv/);
    assert.match(readme, /yt-dlp/);
    assert.match(readme, /cookie/i);
    assert.match(readme, /until you (type|run) `\/music`/);
    assert.match(read('RELEASE-NOTES.md'), /pig-with-batteries[^\n]*pig-music/);
  });

  // Review of M9: inside PiG, /music and its quick commands drive mpv whatever `engine` says (the engine choice is wired into
  // the pigmusic command only), so in this Binary the native engine is never a fallback; and a session that starts while an
  // mpv from an earlier one is still playing follows it (footer) without a /music. The texts must say both.
  it('does not promise a native fallback inside PiG, and names the one thing it does at start', () => {
    const row = read('piglets/pig-with-batteries/README.md').split('\n').find((l) => l.startsWith('| `pig-music` |'));
    const notes = catalog.notes.join('\n');
    for (const [where, text] of [['README row', row], ['catalog notes', notes]]) {
      assert.doesNotMatch(text, /else native|or its (pure-Go )?native engine/, `${where} promises the native engine to /music`);
      assert.match(text, /mpv/, where);
      assert.match(text, /already playing/, `${where} does not say what happens when an earlier mpv is still playing`);
    }
    assert.match(row, /`pigmusic`/, 'the README row does not say where the native engine is');
  });

  // Review of M9b: the native engine left the extension (it is in the separate pigmusic program), but inside PiG nothing
  // reaches pigmusic at all (/music and its quick commands force mpv), and no doctor inside PiG says how to build it.
  it('says where the native engine is without promising a route to it from /music', () => {
    const row = read('piglets/pig-with-batteries/README.md').split('\n').find((l) => l.startsWith('| `pig-music` |'));
    assert.doesNotMatch(row, /its code is in this Binary/, 'the README row says the native engine is in the Binary');
    assert.match(row, /not\*\* in this Binary|not in this Binary/, 'the README row does not say the native engine is outside the Binary');
    const component = read('components/pig-music/README.md');
    assert.doesNotMatch(component, /quick commands that run the command line\s+in-process, link neither: they speak to `pigmusic serve`/,
      'the component README says the quick commands speak to pigmusic serve; they drive mpv');
    for (const [where, text] of [['component README', component], ['README row', row]]) {
      assert.doesNotMatch(text, /doctor says how/, `${where}: no doctor inside PiG says how to build pigmusic`);
    }
  });

  it('targets the pig that builds it', () => {
    assert.match(JSON.parse(read('scripts/pig-requirement.json')).pig, /^0\.4\./);
  });
});
