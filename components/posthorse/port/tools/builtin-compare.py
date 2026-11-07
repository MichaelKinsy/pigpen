# Compares goldens recorded from the original under Pi with a fused Piglet Binary's self-recorded traces (pigeq record --self --builtin).
# Normalisations: llm.system is absent in builtin mode (no baseline prompt), the Piglet adds an `addendum` prompt section, tool lists are order-insensitive (fused extension loads before helper -e extensions).
# usage: builtin-compare.py <pi-golden-dir> <binary-self-golden-dir> <scenarios-dir>
import json,sys,os
a,b=sys.argv[1],sys.argv[2]
bad=0;n=0
def load(p):
    out=[]
    for l in open(p):
        d=json.loads(l)
        if d.get('kind')=='header': continue
        if d.get('ch')=='llm' and isinstance(d.get('data'),dict): d['data'].pop('system',None)
        def srt(o):
            if isinstance(o,dict):
                for k,v in o.items():
                    if k in('tools','toolsAdded') and isinstance(v,list) and all(isinstance(t,dict) and 'name' in t for t in v): o[k]=sorted(v,key=lambda t:t['name'])
                    srt(o[k])
            elif isinstance(o,list):
                for v in o: srt(v)
        def drop(o):
            if isinstance(o,dict):
                o.pop('addendum',None)
                for v in o.values(): drop(v)
            elif isinstance(o,list):
                for v in o: drop(v)
        drop(d)
        srt(d)
        out.append(d)
    return out
for f in sorted(os.listdir(a)):
    if not f.endswith('.jsonl'): continue
    n+=1
    x=load(os.path.join(a,f)); y=load(os.path.join(b,f))
    sc=json.load(open(os.path.join(sys.argv[3],f[:-6]+'.json')))
    if x!=y:
        bad+=1
        print('HELPER-EXT' if sc.get('extensions') else 'NO-HELPER',end=' ')
        i=next((i for i,(p,q) in enumerate(zip(x,y)) if p!=q),min(len(x),len(y)))
        print('DIFF',f,'event',i,len(x),len(y))
        if i<len(x) and i<len(y): print('  pi :',json.dumps(x[i])[:400]); print('  bin:',json.dumps(y[i])[:400])
print(n,'compared',bad,'differ')
