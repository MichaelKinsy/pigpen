import json,os
here=os.path.dirname(os.path.abspath(__file__))
oracle=os.path.join(here,'oracle','instructions.md')
instr=open(oracle).read().strip()
name='@vanillagreen/pi-background-tasks'
block=f"<!-- kendex:append-system {name} begin -->\n{instr}\n<!-- kendex:append-system {name} end -->\n"
cmds={k:{"mode":m} for k,m in [("sh","real"),("ps","real"),("nice","real"),("ionice","real"),("systemctl","missing"),("systemd-run","missing"),("where","missing"),("which","real"),("taskkill","missing"),("sleep","real"),("tail","real"),("watch","real"),("printf","real"),("echo","real"),("cat","real")]}
cmds={k:v for k,v in cmds.items() if k in("sh","ps","nice","ionice","systemctl","systemd-run","where","which","taskkill")}
def settings(extra=None):
    cfg={"resourceControlEnabled":False,"outputSettleMs":200}
    cfg.update(extra or {})
    return json.dumps({"kendex":{"extensionManager":{"config":{name:cfg}}}})
# HOSTDOCS: PiG's system prompt names PiG's own documentation bundle where Pi names its source tree (documented additive difference D22).
# With APPEND_SYSTEM.md present the harness's system-prompt delta runs from the tool snippet to the end of the prompt, so the docs
# block falls inside it. The rule masks that block only (up to its closing tag, or the end of the removed half of the delta, which stops inside it): the <addendum> after it is compared byte for byte.
HOSTDOCS={"pattern":"(?s)<docs>\\n(?:PiG|Pi) documentation.*?(?:</docs>|$)","replacement":"<docs>"}
normalize=[
 HOSTDOCS,
 {"pattern":"\\(pid \\d+\\)","replacement":"(pid <pid>)"},
 {"pattern":"pid: \\d+","replacement":"pid <pid>".replace("pid <pid>","pid: <pid>")},
 {"pattern":"bg-(\\d+)-\\d{13}\\.log","replacement":"bg-$1-<ms>.log"},
 {"pattern":"pid \\d+","replacement":"pid <pid>"},
]
capture=["response","extension_ui_request","extension_error","agent_start","agent_end","turn_start","turn_end","tool_execution_start","tool_execution_end","message_end"]
vol=["result.details.task.pid","result.details.task.startedAt","result.details.task.updatedAt","result.details.task.expiresAt","result.details.task.lastOutputAt",
     "result.details.tasks.[].pid","result.details.tasks.[].startedAt","result.details.tasks.[].updatedAt","result.details.tasks.[].lastOutputAt",
     "message.details.task.pid","message.details.task.startedAt","message.details.task.updatedAt","message.details.task.lastOutputAt",
     "message.details.eventAt","message.details.timestamp"]
def scen(name_,desc,llm,steps,extra_settings=None,cap=None,**kw):
    d={"name":name_,"description":desc,"commands":cmds,
       "agentFiles":{"settings.json":settings(extra_settings),"APPEND_SYSTEM.md":block},
       "llm":llm,"steps":steps,"capture":cap or capture,"normalize":normalize,"volatileNumbers":vol}
    d.update(kw)
    return d
def tc(**a): return {"toolCalls":[{"name":"bg_task","arguments":a}]}
def P(msg,name="prompt"): return {"name":name,"rpc":{"type":"prompt","message":msg}}
out={}
out['spawn-exit-wake']=scen('spawn-exit-wake',"bg_task spawn of a short command, then the exit wake starts a turn that reaches the model.",
 [tc(action="spawn",command="printf 'hello\\n'",title="say hello"),{"text":"started"},{"text":"the task finished"}],[P("run it in the background")])
out['output-match-wake']=scen('output-match-wake',"A task with notifyOnOutput and a notifyPattern wakes the agent when the pattern appears; lines that do not match wake nothing.",
 [tc(action="spawn",command="echo boot; sleep 0.5; echo 'ready: db'; sleep 3",title="watch for ready",notifyOnOutput=True,notifyOnExit=False,notifyPattern="/ready: \\w+/"),{"text":"watching"},{"text":"saw the pattern"},{"toolCalls":[{"name":"bg_task","arguments":{"action":"stop","id":"bg-1"}}]},{"text":"stopped"}],
 [P("watch the log")])
out['list-log-stop']=scen('list-log-stop',"bg_task list, log and stop of a running task, and the stop of a task that already ended.",
 [tc(action="spawn",command="echo up; sleep 30",title="long",notifyOnExit=False),{"text":"1"},tc(action="list"),{"text":"2"},tc(action="log",id="bg-1"),{"text":"3"},tc(action="stop",id="bg-1"),{"text":"4"},tc(action="stop",id="bg-1"),{"text":"5"},tc(action="clear"),{"text":"6"}],
 [P("go")])
out['bg-commands']=scen('bg-commands',"The /bg commands: run, list, stop and clear, answered as notifications.",
 [{"text":"unused"}],
 [P("/bg:run echo from-the-command; sleep 30","run"),P("/bg:list","list"),P("/bg:stop bg-1","stop"),P("/bg:list","list-after-stop"),P("/bg:clear","clear")],
 extra_settings={"outputSettleMs":200,"resourceControlEnabled":True})
out['auto-background']=scen('auto-background',"A bash tool call that follows a log is moved into a background task and acknowledged; an ordinary command runs as usual; the user's own !command is moved too.",
 [{"toolCalls":[{"name":"bash","arguments":{"command":"tail -f /dev/null"}}]},{"toolCalls":[{"name":"bash","arguments":{"command":"echo ordinary"}}]},{"toolCalls":[{"name":"bg_task","arguments":{"action":"list"}}]},{"text":"done"}],
 [P("follow it"),{"name":"user-bash","rpc":{"type":"bash","command":"tail -f /dev/null"}},P("/bg:list","list")])
out['fork-restore']=scen('fork-restore',"A running task survives a forked session: the fork restores it from the branch and lists it.",
 [tc(action="spawn",command="sleep 60",title="survivor",notifyOnExit=False),{"text":"started"},{"toolCalls":[{"name":"bg_task","arguments":{"action":"list"}}]},{"text":"listed"}],
 [P("start it"),{"name":"fork","rpc":{"type":"prompt","message":"/eq-fork"}},P("list them","list-in-fork"),P("/bg:stop bg-1","stop")])
# The installed state of the original (what its postinstall leaves behind): the marked block in APPEND_SYSTEM.md, so the model request of the
# original under Pi carries the instructions and the port must add nothing of its own.
out['tool-definitions']={"name":"tool-definitions","description":"The model request carries bg_status and bg_task with the original's schemas, descriptions and guidelines, and the instructions block once (installed state: the block is in APPEND_SYSTEM.md). The <docs> block of the system prompt is masked (D22: PiG names its docs bundle where Pi names its source tree); the <addendum> after it is compared byte for byte.",
 "commands":cmds,"agentFiles":{"APPEND_SYSTEM.md":block},"llm":[{"text":"done"}],"steps":[P("hello")],"normalize":[HOSTDOCS]}
for n,d in out.items():
    f=os.path.join(here,'scenarios',n+'.json')
    with open(f,'w') as fh:
        json.dump(d,fh,indent=2); fh.write('\n')
print(list(out))
