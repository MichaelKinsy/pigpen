import json,os
out='scenarios'
CAPTURE=["response","extension_error","agent_start","agent_end","turn_start","turn_end","tool_execution_start","tool_execution_end","message_end"]
SKILL_FILE="skills/impeccable/SKILL.md"  # workspace file, loaded with --skill (the harness starts hosts with --no-skills)
SKILL="---\nname: impeccable\ndescription: Frontend interface design, polish, and UX\n---\n\nPolish the interface: align spacing, tighten type, check contrast.\n"
def judge_server(answers, model="jev-1.13.0"):
    return {"judge":{"routes":[
        {"method":"GET","path":"/v1/models","json":{"models":[{"name":"jev-latest","description":"","release_date":"2026-01-01"}]}},
        {"method":"POST","path":"/*","json":{"model":model,"answers":answers,"usage":{"input_tokens":50,"output_tokens":0}}}]}}
def choice(pick, other="unclear"):
    keys=["advance","awaiting_user","no_gap","unclear"]
    return {"type":"choice","choice":pick,"confidence":0.9,"probabilities":{k:(0.9 if k==pick else 0.0333) for k in keys}}
def score(level, probs):
    return {"type":"score","score":level,"confidence":0.8,"legend":{str(i):"level %d"%i for i in range(4)},"probabilities":{str(i):p for i,p in enumerate(probs)}}
def conscience_cfg(extra=None, tools_enabled=False):
    c={"enabled":True,"skills":{"mode":"recommend","exclude":[]},"tools":{"enabled":tools_enabled,"exclude":[]},"recommendThreshold":0.5}
    if extra: c.update(extra)
    return c
def scenario(name, description, answers, conscience, prompt="design a landing page", model="jev-1.13.0", files=None, judged=True, llm=None, extra_steps=None, env_extra=None, args=None):
    env={"PIGPEN_WARDEN_ENABLED":"1","PIGPEN_WARDEN_STEER_VISIBLE":"0"}
    orig={"notices":False,"rules":{"enabled":False},"slop":{"enabled":False},"security":{"enabled":False},"action":{"feedbackLog":False},"conscience":conscience}
    port={"conscience":conscience}
    d={"name":name,"description":description,"env":env,"commands":{"git":{"mode":"missing"}},"args":args or ["--tools","read,bash,edit,write","--skill","skills/impeccable"],"capture":CAPTURE,
       "llm":llm or [{"text":"ok"}],"steps":[{"name":"prompt","rpc":{"type":"prompt","message":prompt}}],"files":files if files is not None else {SKILL_FILE:SKILL}}
    if judged:
        env.update({"FAKE_JUDGE_KEY":"fake-judge-key","TYPESAFE_API_KEY":"fake-judge-key","TYPESAFE_BASE_URL":"{{server:judge}}","PIGPEN_WARDEN_BACKEND":"typesafe"})
        orig.update({"typesafe":True,"typesafeBackend":{"label":"Fake judge","host":"{{server:judge}}","keyEnv":"FAKE_JUDGE_KEY","defaultModel":"jev-latest"}})
        d["servers"]=judge_server(answers,model)
    else:
        orig["typesafe"]=False
    if env_extra: env.update(env_extra)
    d["agentFiles"]={"pi-warden/config.json":json.dumps(orig)}
    d["homeFiles"]={"pigpen-warden/config.json":json.dumps(port)}
    if extra_steps: d["steps"]=extra_steps
    return d
S=[]
adv=lambda lvl,probs:{"conscience_disposition":choice("advance"),"c1":score(lvl,probs)}
S.append(scenario("conscience-recommends-the-skill","The judge selects the one installed skill: before the model answers, the prompt carries one recommendation message (and the index nudge is shown once). The request body of the judge is compared field by field.",adv(3,[0.02,0.03,0.2,0.75]),conscience_cfg()))
S.append(scenario("conscience-below-threshold-delivers-nothing","The judge rates the skill at the related level only: nothing is recommended.",adv(1,[0.1,0.8,0.05,0.05]),conscience_cfg()))
S.append(scenario("conscience-awaiting-user-delivers-nothing","The request is a reply the assistant waits for: the disposition is awaiting_user and nothing is recommended.",{"conscience_disposition":choice("awaiting_user"),"c1":score(3,[0.02,0.03,0.2,0.75])},conscience_cfg()))
S.append(scenario("conscience-other-judge-model-holds-the-recommendation","The skill is selected, but the model that answered is not the one the measured policy names: the activation gate delivers nothing.",adv(3,[0.02,0.03,0.2,0.75]),conscience_cfg(),model="jev-other"))
S.append(scenario("conscience-load-mode-supplies-the-skill","Load mode: the selected skill's file is read and its body is supplied as the message (or the recommendation, when the model's context window is unknown).",adv(3,[0.02,0.03,0.2,0.75]),conscience_cfg({"skills":{"mode":"load","exclude":[]}})))
S.append(scenario("conscience-explicit-skill-invocation-asks-nothing","A prompt that names /skill:impeccable is the user's own choice: the conscience asks the judge nothing.",adv(3,[0.02,0.03,0.2,0.75]),conscience_cfg(),prompt="Use /skill:impeccable to polish the page"))
S.append(scenario("conscience-excluded-skill-leaves-no-candidate","The only skill is on the exclude list: no request is made and nothing is recommended.",adv(3,[0.02,0.03,0.2,0.75]),conscience_cfg({"skills":{"mode":"recommend","exclude":["impeccable"]}})))
S.append(scenario("conscience-user-only-skill-leaves-no-candidate","A skill that disables model invocation is never a candidate.",adv(3,[0.02,0.03,0.2,0.75]),conscience_cfg(),files={SKILL_FILE:SKILL.replace("description: Frontend","disable-model-invocation: true\ndescription: Frontend")}))
S.append(scenario("conscience-off-by-default-asks-nothing","The conscience is off unless enabled: the same skill and judge make no request.",adv(3,[0.02,0.03,0.2,0.75]),{"enabled":False}))
S.append(scenario("conscience-recommends-a-tool","No skill is installed and tools are on: the judge selects the first non-core tool, and the recommendation carries its real description (the three warden tools; the core tools are never offered).",{"conscience_disposition":choice("advance"),"c1":score(3,[0.02,0.03,0.2,0.75]),"c2":score(0,[0.9,0.05,0.03,0.02]),"c3":score(0,[0.9,0.05,0.03,0.02])},conscience_cfg(tools_enabled=True),files={},args=["--tools","read,bash,edit,write,warden_remember,warden_loops,warden_recall"],prompt="remember that we use pnpm in this repository"))
S.append(scenario("conscience-load-mode-credential-in-skill-falls-back-to-the-recommendation","Load mode, but the skill's body carries a credential: it is never supplied, and the plain recommendation is sent instead.",adv(3,[0.02,0.03,0.2,0.75]),conscience_cfg({"skills":{"mode":"load","exclude":[]}}),files={SKILL_FILE:SKILL+"\nToken: ghp_ABCDEFGHIJKLMNOPqrstuvwxyz1234567890\n"}))
S.append(scenario("conscience-load-mode-absolute-path-in-skill-falls-back-to-the-recommendation","Load mode, but the skill's body names an absolute path: it is not supplied, the recommendation is.",adv(3,[0.02,0.03,0.2,0.75]),conscience_cfg({"skills":{"mode":"load","exclude":[]}}),files={SKILL_FILE:SKILL+"\nRead /etc/hosts first.\n"}))
S.append(scenario("conscience-load-mode-oversized-skill-falls-back-to-the-recommendation","Load mode with maxSkillBytes 1024 (the smallest the original accepts) and a skill file of about 2 KB: it does not fit, so the recommendation is sent instead.",adv(3,[0.02,0.03,0.2,0.75]),conscience_cfg({"skills":{"mode":"load","exclude":[]},"maxSkillBytes":1024}),files={SKILL_FILE:SKILL+"Padding line to make the file larger than the limit.\\n"*40}))
for s in S:
    json.dump(s,open(os.path.join(out,s["name"]+".json"),"w"),indent=2)
    open(os.path.join(out,s["name"]+".json"),"a").write("\n")
print(len(S))
