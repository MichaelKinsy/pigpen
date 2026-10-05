/** Direct previous-release comparison with real SDK dependencies; no model calls. */
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { performance } from 'node:perf_hooks';

const [root, label, output] = process.argv.slice(2);
if (!root || !label || !output) throw new Error('Usage: release-comparison.mjs SOURCE_ROOT LABEL OUTPUT_JSON');
const source = name => import(pathToFileURL(path.join(root, 'extensions', name)).href);
const {goalPrompt} = await source('prompts/goal-prompts.ts');
const {deriveGoalDashboardModel} = await source('widgets/goal-dashboard-model.ts');
const {renderExpandedDashboard} = await source('widgets/goal-dashboard-renderer.ts');
const {deriveGoalActivity} = await source('goal-activity.ts');
const ledger = await source('goal-ledger.ts');
const rows = [];
function measure(id, fn, iterations = 100) {
 for (let i=0; i<10; i++) fn();
 const samples = [];
 global.gc?.();
 for (let s=0; s<21; s++) {
  const start = performance.now();
  for (let i=0; i<iterations; i++) fn();
  samples.push((performance.now()-start)/iterations);
 }
 const sorted = [...samples].sort((a,b)=>a-b);
 rows.push({id, iterations, samples, p50:sorted[10], p95:sorted[19]});
}
const cwd = fs.mkdtempSync(path.join(os.tmpdir(), 'goal-release-bench-'));
try {
 fs.mkdirSync(path.join(cwd,'.pi/goals'), {recursive:true});
 const goal = {
  id:'release-fixture', status:'active', autoContinue:true, sisyphus:false,
  createdAt:'2026-09-07T00:00:00Z', updatedAt:'2026-09-07T00:00:00Z',
  usage:{tokensUsed:0, activeSeconds:0},
  objective:'Detailed requirement with Unicode 🧭. '.repeat(1000),
  taskList:{blockCompletion:true, proposedAt:'2026-09-07', tasks:Array.from({length:50}, (_,i)=>({
   id:`t${i}`, title:`Task ${i}: `+'meaningful detail '.repeat(20), status:'pending',
   verificationContract:'Check every requested artifact. '.repeat(50),
  }))},
 };
 measure('prompt.warm.large', ()=>goalPrompt(goal));
 const theme = {fg:(_color,text)=>text, bg:(_color,text)=>text, bold:text=>text, strikethrough:text=>text};
 const model = deriveGoalDashboardModel(goal,{focused:true,otherOpenGoals:0});
 measure('render.expanded.120', ()=>renderExpandedDashboard(model,theme,120));
 const events = Array.from({length:100000}, (_,i)=>({type:'task_complete',goalId:i%2?goal.id:'other',taskId:`t${i}`,evidence:'verified',at:new Date(1700000000000+i*1000).toISOString()}));
 fs.writeFileSync(path.join(cwd,'.pi/goals/goal_events.jsonl'),events.map(e=>JSON.stringify(e)).join('\n')+'\n');
 ledger.invalidateGoalLedgerCache();
 ledger.readGoalLedger({cwd});
 const recent = ()=>ledger.goalActivityEvents ? ledger.goalActivityEvents({cwd},goal.id) : ledger.readGoalLedger({cwd}).events;
 measure('activity.100000', ()=>deriveGoalActivity(recent(),goal.id),10);
} finally {
 fs.rmSync(cwd,{recursive:true,force:true});
}
const result = {label,node:process.version,sdk:'0.84.1 (real)',method:'21 samples after warm-up; milliseconds per operation; raw samples retained; no concurrent benchmarks or model calls',rows};
fs.writeFileSync(output,JSON.stringify(result,null,2)+'\n');
console.log(JSON.stringify({label,rows:rows.map(({id,p50,p95})=>({id,p50,p95}))}));
