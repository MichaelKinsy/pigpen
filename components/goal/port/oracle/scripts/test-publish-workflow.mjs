import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const workflow = readFileSync(new URL('../.github/workflows/publish.yml', import.meta.url), 'utf8');
const step = workflow.split('      - name: Publish exact tarball with OIDC and verify registry')[1].split('      - name: Create GitHub release')[0];
const script = step.split("<<'NODE'\n")[1].split('\n          NODE')[0].replace(/^\s*import .*;\n/gm, '');
const run = new (Object.getPrototypeOf(async function () {}).constructor)('fs', 'assert', 'execFileSync', 'fetch', 'process', 'setTimeout', 'console', script);
const pkg = {name: 'pi-goal-x', version: '0.31.8', integrity: 'sha512-fixture', shasum: 'fixture'};
const metadata = {version: pkg.version, dist: {integrity: pkg.integrity, shasum: pkg.shasum}};

async function simulate({existing = false, mismatch = false, unavailable = false, latest = '0.31.7', delay = 0} = {}) {
 const result = {publishes: 0, sleeps: 0, receipts: 0};
 let versions = 0, tags = 0;
 const response = (status, body) => ({status, ok: status === 200, json: async () => body});
 const fetch = async url => {
  if (url.endsWith('/latest')) return response(200, {version: tags++ > delay ? pkg.version : latest});
  versions++;
  if (!existing && (versions === 1 || unavailable || versions <= delay + 1)) return response(404);
  return response(200, mismatch ? {...metadata, dist: {...metadata.dist, integrity: 'different'}} : metadata);
 };
 const fs = {readFileSync: () => JSON.stringify([pkg]), writeFileSync: (_path, data) => {assert.deepEqual(JSON.parse(data), metadata); result.receipts++;}};
 try {
  await run(fs, assert, (command, args) => {assert.equal(command, 'npm'); assert.equal(args[0], 'publish'); result.publishes++;}, fetch, {env: {RUNNER_TEMP: '/fixture', TARBALL: '/fixture/package.tgz'}}, callback => {result.sleeps++; callback();}, {log() {}});
 } catch (error) {result.error = error;}
 return result;
}

test('publication tolerates metadata and latest propagation beyond one minute', async () => {
 const r = await simulate({delay: 15});
 assert.equal(r.error, undefined);
 assert.equal(r.publishes, 1);
 assert.equal(r.receipts, 1);
 assert.ok(r.sleeps > 24);
});
test('retry verifies an identical published package without republishing', async () => {
 const r = await simulate({existing: true});
 assert.equal(r.error, undefined);
 assert.equal(r.publishes, 0);
 assert.equal(r.receipts, 1);
});
test('retry rejects a different tarball without publication', async () => {
 const r = await simulate({existing: true, mismatch: true});
 assert.match(r.error.message, /differs/);
 assert.equal(r.publishes, 0);
});
test('a new publication cannot move latest backwards', async () => {
 const r = await simulate({latest: '0.32.0'});
 assert.match(r.error.message, /backwards/);
 assert.equal(r.publishes, 0);
});
test('unavailable metadata has a bounded timeout and no second publish', async () => {
 const r = await simulate({unavailable: true});
 assert.match(r.error.message, /five minutes/);
 assert.equal(r.sleeps, 60);
 assert.equal(r.publishes, 1);
 assert.equal(r.receipts, 0);
});
test('failed-job retries use the successful ranking artifact output', () => {
 assert.match(workflow, /artifact-ids: \$\{\{ needs\.ranking\.outputs\.artifact_id \}\}/);
 assert.match(workflow, /artifact_id: \$\{\{ steps\.ranking_artifact\.outputs\.artifact-id \}\}/);
 assert.match(workflow, /name: Save release ranking for package\n {8}id: ranking_artifact/);
});
