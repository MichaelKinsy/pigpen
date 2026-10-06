// A throwaway npm registry on 127.0.0.1 that serves packed tarballs, so a test can `pig install npm:<name>` and
// `pig piglet add npm:<name>` the way a user would without publishing anything. Tests only; it accepts no uploads.
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { gunzipSync } from 'node:zlib';

/** package.json of a .tgz (the first `package/package.json` entry of the tar stream). */
function manifestOf(tarball) {
  const tar = gunzipSync(tarball);
  for (let offset = 0; offset + 512 <= tar.length;) {
    const name = tar.toString('utf8', offset, offset + 100).replace(/\0.*$/, '');
    const size = parseInt(tar.toString('utf8', offset + 124, offset + 136).replace(/\0.*$/, '').trim() || '0', 8);
    if (name === 'package/package.json') return JSON.parse(tar.toString('utf8', offset + 512, offset + 512 + size));
    offset += 512 + Math.ceil(size / 512) * 512;
  }
  throw new Error('tarball has no package/package.json');
}

/** Serve `tarballs` (paths to .tgz files); resolves {url, close}. */
export async function startRegistry(tarballs) {
  const packages = new Map();
  for (const file of tarballs) {
    const data = readFileSync(file);
    const manifest = manifestOf(data);
    const entry = packages.get(manifest.name) ?? { versions: {}, files: new Map() };
    entry.versions[manifest.version] = { ...manifest, dist: { shasum: createHash('sha1').update(data).digest('hex'), integrity: 'sha512-' + createHash('sha512').update(data).digest('base64') } };
    entry.files.set(`${manifest.name.replace('/', '%2f')}-${manifest.version}.tgz`, data);
    packages.set(manifest.name, entry);
  }
  let base = '';
  const server = createServer((request, response) => {
    const path = decodeURIComponent(request.url.split('?')[0]).replace(/^\//, '');
    const tarball = [...packages.values()].flatMap((p) => [...p.files]).find(([name]) => decodeURIComponent(name) === path.replace(/^.*\/-\//, ''));
    if (request.method === 'GET' && path.includes('/-/') && tarball) { response.writeHead(200, { 'content-type': 'application/octet-stream' }); response.end(tarball[1]); return; }
    const entry = packages.get(path);
    if (request.method !== 'GET' || !entry) { response.writeHead(404, { 'content-type': 'application/json' }); response.end('{"error":"not found"}'); return; }
    const versions = Object.fromEntries(Object.entries(entry.versions).map(([v, m]) => [v, { ...m, dist: { ...m.dist, tarball: `${base}/${m.name}/-/${m.name.split('/').pop()}-${v}.tgz` } }]));
    const latest = Object.keys(versions).sort().at(-1);
    response.writeHead(200, { 'content-type': 'application/json' });
    response.end(JSON.stringify({ name: path, 'dist-tags': { latest }, versions }));
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  base = `http://127.0.0.1:${server.address().port}`;
  return { url: base, close: () => new Promise((resolve) => server.close(resolve)) };
}

/** As a separate process (a test that drives pig with spawnSync would block a registry in its own process): prints the URL, serves until killed. */
if (process.argv[1] && import.meta.url === new URL(`file://${process.argv[1]}`).href) {
  const registry = await startRegistry(process.argv.slice(2));
  process.stdout.write(registry.url + '\n');
}
