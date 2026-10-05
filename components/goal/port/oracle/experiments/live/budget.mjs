/** Conservative reservations use the entire model context window, never chars/4. */
export class EvaluationBudget {
 constructor({limit=5,maxTokens=8192}={}) {
  if(!Number.isFinite(limit)||limit<0||!Number.isInteger(maxTokens)||maxTokens<=0)throw new Error('Invalid evaluation allowance');
  this.limit=limit;this.maxTokens=maxTokens;this.spent=0;this.reserved=0;this.requests=[];this.caseRequests=0;this.caseLimit=12;
 }
 reservation(model) {
  const rates=['input','output','cacheRead','cacheWrite'].map(k=>model.cost?.[k]);
  if(rates.some(r=>!Number.isFinite(r)||r<0)||!Number.isFinite(model.contextWindow)||model.contextWindow<=0||!Number.isFinite(model.maxTokens)||model.maxTokens<=0) throw new Error('Cannot safely price this model');
  if(model.cost?.inputTiers?.length || model.cost?.tiers?.length) throw new Error('Tiered pricing requires explicit reservation support');
  return (model.contextWindow*Math.max(rates[0],rates[2],rates[3])+Math.min(model.maxTokens,this.maxTokens)*rates[1])/1e6;
 }
 canRunPair(model) {return this.spent+this.reserved+2*this.caseLimit*this.reservation(model)<=this.limit;}
 install(runtime) {
  const original=runtime.streamSimple.bind(runtime);
  runtime.streamSimple=(model,context,options={})=>{
   const reserve=this.reservation(model);
   if(this.caseRequests>=this.caseLimit || this.spent+this.reserved+reserve>this.limit) throw new Error('Evaluation request/spend ceiling reached before dispatch');
   this.caseRequests++;this.reserved+=reserve;
   const entry={provider:model.provider,model:model.id,reservation:reserve,input:0,output:0,cacheRead:0,cacheWrite:0,cost:0};this.requests.push(entry);
   this.onChange?.();
   let stream;
   try {stream=original(model,context,{...options,maxTokens:Math.min(model.maxTokens,this.maxTokens),maxRetries:0});}
   catch(error){this.reserved-=reserve;this.spent+=reserve;entry.cost=reserve;entry.uncertain=true;this.onChange?.();throw error;}
   stream.result().then(message=>{
    const u=message?.usage;
    const cost=u?.cost?.total;
    Object.assign(entry,{input:u?.input??0,output:u?.output??0,cacheRead:u?.cacheRead??0,cacheWrite:u?.cacheWrite??0,error:message?.errorMessage,stopReason:message?.stopReason});
    // Missing usage on a failed/aborted request is not proof of zero billing.
    entry.cost=Number.isFinite(cost)&&cost>=0&&(u.input+u.output+u.cacheRead+u.cacheWrite>0)?cost:reserve;
    if(entry.cost===reserve)entry.uncertain=true;
    this.reserved-=reserve;this.spent+=entry.cost;
    this.onChange?.();
   },()=>{this.reserved-=reserve;this.spent+=reserve;entry.cost=reserve;entry.uncertain=true;this.onChange?.();});
   return stream;
  };
 }
}
