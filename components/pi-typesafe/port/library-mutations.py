# Library mutation check: applies each (file, find, replace) to a copy of components/pi-typesafe-api and runs its tests; expects every mutant killed.
# Needs a go.work at /tmp/pts/go.work that uses the SDK, pi-typesafe-api and typesafe (see port/PORT.md). Mutant 5 is equivalent (removed); 13 and 18 no longer match the source.
import subprocess,shutil,os,sys
HERE=os.path.dirname(os.path.abspath(__file__))
API=os.path.normpath(os.path.join(HERE,'..','..','pi-typesafe-api'))
WORK=os.environ.get('LIBMUT_GOWORK','/tmp/pts/go.work')  # a go.work that uses the SDK dir, pi-typesafe-api and typesafe
M=[
("client.go","if c.usage.RequestsStarted >= c.maxRequests {\n\t\tc.mu.Unlock()","if c.usage.RequestsStarted > c.maxRequests {\n\t\tc.mu.Unlock()"),
("client.go","c.usage.RequestsStarted++\n\tc.ledger.RecordStart()","c.ledger.RecordStart()"),
("client.go","if ctx.Err() != nil {\n\t\treturn nil, newError(CodeAborted","if false {\n\t\treturn nil, newError(CodeAborted"),
("client.go","if c.backend.ModelsVerifyKey {","if true {"),
("client.go","transport = backendDoer{inner: opts.HTTPClient, backend: backend}","_ = backend"),
("backends.go","if strings.Contains(model, \"/\") || backend != BackendOpenRouter {","if backend != BackendOpenRouter {"),
("backends.go","strings.EqualFold(keyEnv, typesafeKeyEnv)","keyEnv == typesafeKeyEnv"),
("backends.go","return !b.Local && (b.KeyEnv == \"\" || b.KeyEnv == typesafeKeyEnv)","return !b.Local"),
("credentials.go","info.Mode().Perm()&0o077 != 0","info.Mode().Perm()&0o007 != 0"),
("credentials.go","len(key) >= 16","len(key) >= 4"),
("auth.go","(f.Status == 401 || f.Status == 403)","(f.Status == 401)"),
("auth.go","f.Code == CodeHTTP &&","true &&"),
("usage.go","if c.limit > 0 && c.used >= c.limit {","if c.limit > 0 && c.used > c.limit {"),
("usage.go","return min(a, b)\n\t}\n\treturn SpendCaps","return max(a, b)\n\t}\n\treturn SpendCaps"),
("schema.go","if qs.Len() < 1 || qs.Len() > DefaultMaxQuestions {","if qs.Len() < 1 || qs.Len() > DefaultMaxQuestions+1 {"),
("schema.go","if len(text) > maxInputBytes","if len(text) >= maxInputBytes"),
("schema.go",'item.Set("criteria", labels)','_ = labels'),
("errors.go",'advice += " Retry after "','advice += " Wait "'),
("errors.go",'if openrouter','if false'),
("batch.go","if ctx.Err() != nil {\n\t\t\t\tstopped = true","if false {\n\t\t\t\tstopped = true"),
("batch.go","return ok && (ie.Code == CodeBudget || ie.Code == CodeAborted)","return ok && ie.Code == CodeBudget"),
("calibrate.go","case p == n:\n\t\t\t\twins += 0.5","case p == n:\n\t\t\t\twins += 1"),
("ask.go","timeout = DefaultAskTimeout","timeout = 0"),
]
res=[]
for i,(f,a,b) in enumerate(M):
    d=f'/tmp/libmut/{i}'
    shutil.rmtree(d,ignore_errors=True); shutil.copytree(API,d,ignore=shutil.ignore_patterns('.git'))
    p=os.path.join(d,f); t=open(p).read()
    if t.count(a)!=1: print('BAD',i,f,t.count(a)); continue
    open(p,'w').write(t.replace(a,b))
    env=dict(os.environ); 
    work=open(WORK).read().replace(API,d)
    open(d+'/go.work.tmp','w').write(work); env['GOWORK']=d+'/go.work.tmp'
    r=subprocess.run(['go','test','-count=1','-timeout=120s','.'],cwd=d,env=env,capture_output=True,text=True)
    status='KILLED' if r.returncode!=0 and ('FAIL' in r.stdout) else ('INVALID' if r.returncode!=0 else 'SURVIVED')
    print(status,i,f,a[:40].replace('\n',' '))
