/** Acceptance gate for the isolated 2026-09-07 campaign. No model calls. */
import fs from 'node:fs';
const directory = new URL('../../specs/2026-09-07-runtime-token-optimization/', import.meta.url);
const read = name => JSON.parse(fs.readFileSync(new URL(name, directory), 'utf8'));
const before = read('RUNTIME-BEFORE.json');
const after = read('RUNTIME-AFTER.json');
const cb = new Map(read('CONTEXT-BEFORE.json').fixtures.map(row => [row.fixture, row]));
const ca = read('CONTEXT-AFTER.json').fixtures;
const active = ca.filter(row => row.semantic.goalActiveMarker === 1);
const oldChars = active.reduce((sum, row) => sum + cb.get(row.fixture).breakdown.extensionAttributableChars, 0);
const newChars = active.reduce((sum, row) => sum + row.breakdown.extensionAttributableChars, 0);
const failures = [];
if (newChars > oldChars * 0.75) failures.push('Active workflow context reduction is below 25%.');
for (const row of ca) {
 if (row.breakdown.extensionAttributableChars > cb.get(row.fixture).breakdown.extensionAttributableChars * 1.05 + 100) failures.push(`Context regressed: ${row.fixture}`);
}
const map = new Map(after.rows.map(row => [row.id, row]));
for (const row of before.rows) {
 const next = map.get(row.id);
 if (!next) {failures.push(`Missing runtime row: ${row.id}`);continue;}
 // Absolute 2ms floor avoids pretending tiny differences are meaningful.
 // Above the floor, require p50 within 20%; p95 has a separate 50% noise allowance.
 if (next.p50 > Math.max(row.p50 * 1.2, row.p50 + 2)) failures.push(`p50 regression: ${row.id} (${row.p50.toFixed(3)} -> ${next.p50.toFixed(3)}ms)`);
 if (next.p95 > Math.max(row.p95 * 1.5, row.p95 + 2)) failures.push(`p95 regression: ${row.id}`);
 if (/^(activity|dashboard|before_agent_start)\./.test(row.id) && next.fsOps !== 0) failures.push(`Warm read performed filesystem operations: ${row.id}`);
 if (/^append\./.test(row.id) && next.fsOps > 2) failures.push(`Append exceeds two ordinary filesystem operations: ${row.id}`);
}
const long = before.rows.find(row => row.id === 'activity.100000');
const speedup = long.p50 / map.get(long.id).p50;
if (speedup < 2) failures.push('100k-event activity speedup is below 2x.');
const result = {passed: failures.length === 0, activeFixtures: active.length, beforeExtensionChars: oldChars, afterExtensionChars: newChars, contextReductionPercent: 100 * (1 - newChars / oldChars), activitySpeedup: speedup, failures};
fs.writeFileSync(new URL('GATE-RESULTS.json', directory), JSON.stringify(result, null, 2) + '\n');
console.log(JSON.stringify(result, null, 2));
if (failures.length) process.exitCode = 1;
