/** Opt-in, artifact-graded auditor comparison carrying the ORIGINAL campaign allowance forward. */
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {pathToFileURL} from 'node:url';
import {ModelRuntime} from '@earendil-works/pi-coding-agent';
import {EvaluationBudget} from './budget.mjs';

const [baselineRoot, priorFile, output] = process.argv.slice(2);
if (!baselineRoot || !priorFile || !output || fs.existsSync(output)) throw new Error('Usage: auditor-pair.mjs <baseline> <existing cumulative ledger> <new results file>');
const previous = JSON.parse(fs.readFileSync(priorFile,'utf8'));
if (previous.outstandingReservedUSD !== 0) throw new Error('Resolve outstanding reservations before extending the campaign');
const runtime = await ModelRuntime.create({allowModelNetwork:false});
const model = runtime.getModel('opencode-go','deepseek-v4-flash');
if (!model || !runtime.hasConfiguredAuth('opencode-go')) throw new Error('Selected model/auth unavailable; no request sent');
const budget = new EvaluationBudget({limit:5});
budget.spent = previous.chargedOrReservedUSD;
budget.requests = [...previous.requests];
budget.caseLimit = 8;
if (!budget.canRunPair(model)) throw new Error('Remaining ORIGINAL allowance cannot reserve the full pair');
budget.install(runtime);
const results = [];
const work = fs.mkdtempSync(path.join(os.tmpdir(),'pi-goal-auditor-pair-'));
const oldGlobal = process.env.PI_GOAL_GLOBAL_SETTINGS_FILE;
const oldProject = process.env.PI_GOAL_SETTINGS_FILE;
process.env.PI_GOAL_GLOBAL_SETTINGS_FILE = path.join(work,'global.json'); fs.writeFileSync(process.env.PI_GOAL_GLOBAL_SETTINGS_FILE,'{}');
const save = () => fs.writeFileSync(output,JSON.stringify({provider:model.provider,model:model.id,thinking:'high',limitUSD:5,priorResultsPath:priorFile,priorChargedOrReservedUSD:previous.chargedOrReservedUSD,priorRequestCount:previous.requests.length,chargedOrReservedUSD:budget.spent,outstandingReservedUSD:budget.reserved,results,requests:budget.requests},null,2)+'\n');
budget.onChange = save;
try {
 for (const [phase,root] of [['before',baselineRoot],['after',process.cwd()]]) {
  budget.caseRequests = 0;
  const {runGoalCompletionAuditor} = await import(pathToFileURL(path.join(root,'extensions/goal-auditor.ts')));
  const cwd = path.join(work,phase); fs.mkdirSync(cwd);
  process.env.PI_GOAL_SETTINGS_FILE = path.join(cwd,'settings.json');
  fs.writeFileSync(process.env.PI_GOAL_SETTINGS_FILE,JSON.stringify({provider:model.provider,model:model.id,thinkingLevel:'high'}));
  const ctx = {cwd,model,modelRegistry:{runtime,find:(provider,id)=>runtime.getModel(provider,id),getAvailable:()=>[model]}};
  const goal = {id:'auditor-smoke',objective:'In this fixture directory, final.txt must contain exactly ready-for-review followed by a newline. Verify that actual file. Do not access the network or files outside this directory.',status:'active',autoContinue:false,sisyphus:false,usage:{tokensUsed:0,activeSeconds:0},createdAt:'2026-09-07',updatedAt:'2026-09-07',verificationContract:'Read final.txt and check its exact content, including the trailing newline.'};
  for (const [stage,content,approved] of [['reject','draft\n',false],['rework','ready-for-review\n',true]]) {
   fs.writeFileSync(path.join(cwd,'final.txt'),content);
   const start = budget.requests.length; const started = Date.now(); const controller = new AbortController();
   const timer = setTimeout(()=>controller.abort(),120000);
   let result;
   try { result = await runGoalCompletionAuditor({ctx,goal,detailedSummary:'Fixture',completionSummary:'The file is complete.',signal:controller.signal}); }
   finally {clearTimeout(timer);}
   const requests = budget.requests.slice(start);
   const artifactUnchanged = fs.readFileSync(path.join(cwd,'final.txt'),'utf8') === content;
   results.push({phase,stage,expectedApproved:approved,approved:result.approved,disapproved:result.disapproved,error:result.error,passed:!result.error && result.approved===approved && artifactUnchanged,artifactUnchanged,report:result.output,elapsedMs:Date.now()-started,calls:requests.length,input:requests.reduce((n,r)=>n+r.input,0),output:requests.reduce((n,r)=>n+r.output,0),cacheRead:requests.reduce((n,r)=>n+r.cacheRead,0),cacheWrite:requests.reduce((n,r)=>n+r.cacheWrite,0),costUSD:requests.reduce((n,r)=>n+r.cost,0)});
   save(); console.log(JSON.stringify(results.at(-1)));
  }
 }
} finally {
 save();
 if (oldGlobal === undefined) delete process.env.PI_GOAL_GLOBAL_SETTINGS_FILE; else process.env.PI_GOAL_GLOBAL_SETTINGS_FILE=oldGlobal;
 if (oldProject === undefined) delete process.env.PI_GOAL_SETTINGS_FILE; else process.env.PI_GOAL_SETTINGS_FILE=oldProject;
 fs.rmSync(work,{recursive:true,force:true});
}
