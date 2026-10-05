/** Campaign-local evidence gate; run the fresh campaign first, never reuse older campaigns' timings. */
import fs from 'node:fs';
import path from 'node:path';
const dir = path.resolve('specs/2026-09-07-comprehensive-optimization');
const read = name => JSON.parse(fs.readFileSync(path.join(dir,name+'.json'),'utf8'));
const failures=[];
for (const prefix of ['CPU','SDK-CPU']) {
 const before=new Map(read(prefix+'-BEFORE').rows.map(row=>[row.id,row]));
 for (const after of read(prefix+'-AFTER').rows) {
  const prior=before.get(after.id); if(!prior){failures.push(`Missing ${prefix} baseline: ${after.id}`);continue;}
  if(after.p50>Math.max(prior.p50*1.2,prior.p50+.015)) failures.push(`${prefix} regression: ${after.id}`);
  if(after.fsOps!==null && after.fsOps>prior.fsOps) failures.push(`${prefix} filesystem regression: ${after.id}`);
  const target=after.id.startsWith('detail.history')?20:after.id==='auditor.stream.tail.1000000'?50:after.id==='prompt.warm.large'?10:after.id.startsWith('render.expanded')?2:after.id.startsWith('activity.tail')?2:1;
  if(target>1 && prior.p50/after.p50<target) failures.push(`${prefix} speed target ${target}x missed: ${after.id}`);
 }
}
// Alternating baseline/candidate/candidate/baseline avoids relying on one thermally biased run.
const trials=[read('RUNTIME-REPEAT-1-BEFORE'),read('RUNTIME-REPEAT-2-AFTER'),read('RUNTIME-REPEAT-3-AFTER'),read('RUNTIME-REPEAT-4-BEFORE')];
for(const row of trials[0].rows) {
 const values=trials.map(t=>t.rows.find(r=>r.id===row.id));
 if(values.some(v=>!v)){failures.push(`Missing runtime row ${row.id}`);continue;}
 const before=(values[0].p50+values[3].p50)/2; const after=(values[1].p50+values[2].p50)/2;
 if(after>Math.max(before*1.2,before+.2))failures.push(`Runtime regression: ${row.id}: ${before} -> ${after}`);
 if(Math.max(values[1].fsOps,values[2].fsOps)>Math.max(values[0].fsOps,values[3].fsOps))failures.push(`Runtime filesystem regression: ${row.id}`);
}
const contextBefore=new Map(read('CONTEXT-BEFORE').fixtures.map(r=>[r.fixture,r]));
for(const row of read('CONTEXT-AFTER').fixtures) {
 if(row.breakdown.extensionAttributableChars>contextBefore.get(row.fixture).breakdown.extensionAttributableChars)failures.push(`Context regression: ${row.fixture}`);
}
if(failures.length) {console.error(failures.join('\n'));process.exitCode=1;}
else console.log('PASS: repeated CPU/real-SDK targets, alternating runtime comparison, filesystem counts and context sizes.');
