/** Capture a real SDK provider payload before dispatch; no HTTP request is sent. */
import assert from 'node:assert/strict';
import * as piAI from '@earendil-works/pi-ai';
import {stream as streamResponses} from '../../node_modules/@earendil-works/pi-ai/dist/api/openai-responses.js';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {createAgentSession, DefaultResourceLoader, ModelRuntime, SessionManager, SettingsManager} from '@earendil-works/pi-coding-agent';
import goalExtension from '../../extensions/goal.ts';
import {captureOne} from './capture-context.mjs';
import {FIXTURES} from './fixtures.mjs';
import {runGoalCompletionAuditor} from '../../extensions/goal-auditor.ts';
import {runBlockerOracle} from '../../extensions/goal-oracle.ts';

const work=fs.mkdtempSync(path.join(os.tmpdir(),'goal-wire-check-'));
// Exercise Responses caller compatibility resolution, not merely its converter.
const registered=[];
goalExtension({registerTool:tool=>registered.push(tool),registerCommand(){},on(){},getActiveTools:()=>[],setActiveTools(){},appendEntry(){},registerMessageRenderer(){}});
const responseTools=registered.filter(tool=>['create_goal','get_goal','update_goal_task','propose_goal_draft'].includes(tool.name)).map(({name,description,parameters})=>({name,description,parameters}));
assert.equal(responseTools.length,4);
for(const supportsStrictMode of [undefined,false,true]) {
 let payload;
 const model={id:'fixture',name:'fixture',api:'openai-responses',provider:'opencode',baseUrl:'http://127.0.0.1:1/v1',reasoning:false,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:200000,maxTokens:128,...(supportsStrictMode === undefined ? {} : {compat:{supportsStrictMode}})};
 await streamResponses(model,(piAI.normalizeContext ?? (context => context))({messages:[{role:'user',content:'Inspect optional schemas only',timestamp:0}],tools:responseTools}),{apiKey:'fixture-only',onPayload:value=>{payload=value;throw new Error('Intentional capture before transmission');}}).result();
 assert.ok(payload);
 for(const original of responseTools){
  const wire=payload.tools.find(tool=>tool.name===original.name);
  assert.deepEqual(wire.parameters,JSON.parse(JSON.stringify(original.parameters)));
  assert.equal(wire.strict,supportsStrictMode === true ? false : undefined);
  assert.equal(Object.hasOwn(wire,'strict'),supportsStrictMode === true);
 }
 const byName=Object.fromEntries(payload.tools.map(tool=>[tool.name,tool]));
 assert.deepEqual(byName.create_goal.parameters.required,['objective']);
 assert.equal(byName.create_goal.parameters.properties.token_budget.minimum,1);
 assert.ok(!byName.get_goal.parameters.required?.includes('task_id'));
 assert.ok(!byName.update_goal_task.parameters.required?.includes('updates'));
 assert.ok(!byName.update_goal_task.parameters.required?.includes('task_id'));
 assert.ok(!byName.propose_goal_draft.parameters.required?.includes('token_budget'));
 assert.ok(byName.propose_goal_draft.parameters.properties.token_budget.anyOf.some(type=>type.type==='null'));
}
console.log('Responses compatibility: 3 real adapter payloads; no network requests sent (omitted strict remains an upstream concern).');
let count=0;
try {
 for (const id of ['active-regular-no-tasks','current-contracted-task','tasks-disabled','guided-drafting-question']) {
  const expected=await captureOne(id);
  const scenario=FIXTURES[id]();
  const cwd='/tmp/goal-context-capture';
  const manager=SessionManager.inMemory(cwd);
  if(scenario.goal) manager.appendCustomEntry('pi-goal-focus',{version:1,focusedGoalId:scenario.goal.id,reason:'created'});
  if(scenario.draftPrompt) manager.appendCustomEntry('pi-goal-draft',{version:1,mode:'goal',seed:'build the thing',startedAt:'2026-08-23T12:00:00.000Z',auditorEnabled:true});
  for(const message of expected.messages)manager.appendMessage(message);
  const runtime=await ModelRuntime.create({authPath:path.join(work,'auth.json'),modelsPath:null,allowModelNetwork:false,refreshOnCreate:false});
  runtime.registerProvider('capture',{baseUrl:'http://127.0.0.1:1/v1',api:'openai-completions',apiKey:'fixture-only',models:[{id:'fixture',name:'fixture',reasoning:false,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:200000,maxTokens:128}]});
  let payload;
  const loader=new DefaultResourceLoader({cwd,agentDir:work,noExtensions:true,noSkills:true,noThemes:true,noPromptTemplates:true,noContextFiles:true,extensionFactories:[pi=>{goalExtension(pi);pi.on('before_provider_request',event=>{payload=event.payload;throw new Error('Intentional capture: no network dispatch');});}]});
  await loader.reload();
  const {session}=await createAgentSession({cwd,agentDir:work,modelRuntime:runtime,model:runtime.getModel('capture','fixture'),thinkingLevel:'off',resourceLoader:loader,sessionManager:manager,settingsManager:SettingsManager.inMemory({retry:{enabled:false},compaction:{enabled:false}})});
  await session.bindExtensions({});
  try {await session.prompt(scenario.draftPrompt ? 'Continue drafting' : scenario.trigger??'continue');} finally {await session.abort();session.dispose();}
  assert.ok(payload,`${id}: provider payload captured`);
  const defs=payload.tools.map(t=>t.function);
  assert.deepEqual(defs.filter(t=>expected.tools.some(x=>x.name===t.name)).map(t=>t.name).sort(),expected.tools.map(t=>t.name).sort());
  for(const tool of expected.tools){const wire=defs.find(t=>t.name===tool.name);assert.equal(wire.description,tool.description);assert.deepEqual(wire.parameters,JSON.parse(JSON.stringify(tool.schema)));}
  const sdkRoot=fs.realpathSync(path.resolve('node_modules/@earendil-works/pi-coding-agent'));
  const actualSystem=payload.messages.filter(m=>m.role==='system'||m.role==='developer').map(m=>m.content).join('\n').replaceAll(sdkRoot,'/sdk').replaceAll(cwd,'/fixture');
  const expectedSystem=expected.baseSystem+expected.extensionSystem;
  if(actualSystem!==expectedSystem){fs.writeFileSync(path.join(work,'actual.txt'),actualSystem);fs.writeFileSync(path.join(work,'expected.txt'),expectedSystem);}
  assert.equal(actualSystem,expectedSystem,`${id}: SDK system prompt matches capture (diff in ${work})`);
  assert.ok(payload.messages.some(m=>m.role==='user'));
  const wireText=JSON.stringify(payload.messages);
  for(const message of expected.messages) {
   const texts=typeof message.content==='string'?[message.content]:message.content.filter(c=>c.type==='text').map(c=>c.text);
   for(const text of texts)assert.ok(wireText.includes(JSON.stringify(text).slice(1,-1)),`${id}: conversation content omitted`);
  }
  count++;
 }
 for(const id of ['completion-audit','oracle-consultation']) {
  const expected=(await captureOne(id)).childRequests[0];
  const scenario=FIXTURES[id]();
  const cwd='/tmp/goal-context-capture';
  const runtime=await ModelRuntime.create({authPath:path.join(work,'auth.json'),modelsPath:null,allowModelNetwork:false,refreshOnCreate:false});
  runtime.registerProvider('capture',{baseUrl:'http://127.0.0.1:1/v1',api:'openai-completions',apiKey:'fixture-only',models:[{id:'fixture',name:'fixture',reasoning:false,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:200000,maxTokens:128}]});
  const model=runtime.getModel('capture','fixture');
  let payload;
  const stream=runtime.streamSimple.bind(runtime);
  runtime.streamSimple=(model,context,options)=>stream(model,context,{...options,onPayload:actual=>{payload=actual;throw new Error('Intentional capture: no network dispatch');}});
  const ctx={cwd,model,modelRegistry:{runtime,getAvailable:()=>[],find:()=>model}};
  const createSession=options=>createAgentSession({...options,model,modelRuntime:runtime});
  if(id==='oracle-consultation')await runBlockerOracle({ctx,goal:scenario.goal,reason:'Missing dependency',attemptedActions:['Inspect configuration'],recentEvidence:'Dependency is absent',settings:{enabled:true,provider:'capture',model:'fixture',maxFailedAttemptsPerBlocker:2},createSession});
  else await runGoalCompletionAuditor({ctx,goal:scenario.goal,detailedSummary:'fixture',completionSummary:'Implemented and tested',warmContext:'Recent test evidence',createSession});
  assert.ok(payload,`${id}: isolated child provider payload captured`);
  const actualSystem=payload.messages.filter(m=>m.role==='system'||m.role==='developer').map(m=>m.content).join('\n').replaceAll(cwd,'/fixture');
  assert.equal(actualSystem,expected.system,`${id}: isolated system matches`);
  assert.deepEqual(payload.tools.map(t=>t.function.name).sort(),expected.tools.map(t=>t.name).sort());
  assert.ok(JSON.stringify(payload.messages).includes(JSON.stringify(expected.messages[0].content).slice(1,-1)),`${id}: full child request matches`);
  count++;
 }
 console.log(`[provider-crosscheck] PASS: ${count} real SDK payloads; no network requests sent`);
} finally {fs.rmSync(work,{recursive:true,force:true});}
