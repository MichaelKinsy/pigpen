#!/usr/bin/env python3
"""Generates port/scenarios/*.json: the observable branches of the schedule_prompt tool, the store file, the settings files, the firing of
cron, interval and once jobs, the session lifecycle, each run against the original under Pi and the port under PiG.

The model is scripted: each entry of `turns` is one assistant turn (its tool calls), a final "done" turn follows. Job ids are random,
so a scenario that has to name a job seeds the store file (`files`) with known ids; scenarios that add jobs hide the random ids and
names with narrow N7 rules and read the store file back through the model's own `bash` tool."""
import json, os

out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "scenarios")
os.makedirs(out, exist_ok=True)

STORE = ".pi/schedule-prompts.json"
SETTINGS = ".pi/schedule-prompts-settings.json"

# N7: the random parts (the whole-string rule also matches a ten-letter object key such as "extensions"; both lanes are rewritten alike, so a
# scenario that has two such keys in one object must not use it: manage-seeded has known ids and does not) of a generated id and name, written in the shapes the original prints them in.
NORM = [
    {"pattern": r"\(([A-Za-z0-9_-]{10})\)", "replacement": "(<id>)"},
    {"pattern": r"\"id\": \"[A-Za-z0-9_-]{10}\"", "replacement": "\"id\": \"<id>\""},
    # the same id as a whole string value (details.jobId and details.jobs[].id): a string of exactly ten id characters
    {"pattern": r"^[A-Za-z0-9_-]{10}$", "replacement": "<id>"},
    {"pattern": r"job-[A-Za-z0-9_-]{6}", "replacement": "job-<n>"},
]


def call(_tool, **args):
    return {"name": _tool, "arguments": args}


def sp(action, **kw):
    return call("schedule_prompt", action=action, **kw)


def cat(path=STORE):
    return call("bash", command="cat " + path)


def prompt(message="go", **kw):
    return {"name": kw.pop("name", "prompt"), "rpc": {"type": "prompt", "message": message}, **kw}


def job(id, name, schedule, prompt_text="p", type="cron", enabled=True, **kw):
    j = {"id": id, "name": name, "schedule": schedule, "prompt": prompt_text, "enabled": enabled, "type": type, "createdAt": "2026-01-01T00:00:00.000Z", "runCount": 0}
    j.update(kw)
    return j


def store(*jobs, raw=None):
    if raw is not None:
        return raw
    return json.dumps({"jobs": list(jobs), "version": 1}, indent=2)


def scenario(name, description, turns, steps=None, after=(), **extra):
    llm = [{"toolCalls": t} if isinstance(t, list) else t for t in turns] + [{"text": "done"}] + [{"text": a} for a in after]
    s = {"name": name, "description": description, "llm": llm, "steps": steps or [prompt()]}
    s.update(extra)
    with open(os.path.join(out, name + ".json"), "w") as f:
        json.dump(s, f, indent=2, ensure_ascii=False)
        f.write("\n")


scenario("tool-definition", "The model request carries the schedule_prompt tool's full definition; a list on an empty store.", [
    [sp("list")],
])

scenario("add-variants", "Add a cron, an interval, relative one-shots and a described job; list them; read the store file back.", [
    [sp("add", name="five", schedule="0 */5 * * * *", prompt="check the build")],
    [sp("add", name="every-five", schedule="5m", type="interval", prompt="heartbeat", description="a heartbeat")],
    [sp("add", schedule="+20d", type="once", prompt="remind me in twenty days")],
    [sp("add", name="inline-notify", schedule="0 0 0 * * 1-5", prompt="weekday midnight", notify=True)],
    [sp("add", name="ext-skills", schedule="0 0 12 * * *", prompt="noon", extensions=["a", "b"], skills=True)],
    [sp("list")],
    [cat()],
], normalize=NORM)

scenario("add-errors", "Every rejection of add and the validation messages: missing parameters, a duplicate name, malformed cron, interval, once, past and unparseable timestamps.", [
    [sp("add")],
    [sp("add", schedule="+10s")],
    [sp("add", prompt="p")],
    [sp("add", name="taken", schedule="0 0 * * * *", prompt="p")],
    [sp("add", schedule="0 9 * * *", prompt="five fields")],
    [sp("add", schedule="61 * * * * *", prompt="second 61")],
    [sp("add", schedule="0 0 0 32 * *", prompt="day 32")],
    [sp("add", schedule="0 0 0 * 13 *", prompt="month 13")],
    [sp("add", schedule="0 0 0 * * 8", prompt="weekday 8")],
    [sp("add", schedule="0 */0 * * * *", prompt="step zero")],
    [sp("add", schedule="0 5/10 * * * *", prompt="numeric step prefix")],
    [sp("add", schedule="0 0 0 * * FRI#9", prompt="nth 9")],
    [sp("add", schedule="0 0 12:30 * * *", prompt="colon")],
    [sp("add", schedule="@daily x x x x", prompt="nickname")],
    [sp("add", schedule="every five minutes", type="interval", prompt="p")],
    [sp("add", schedule="0s", type="interval", prompt="p")],
    [sp("add", schedule="2000-01-01T00:00:00Z", type="once", prompt="p")],
    [sp("add", schedule="Feb 13 2000 3:30 PM GMT", type="once", prompt="legacy date format")],
    [sp("add", schedule="tomorrow", type="once", prompt="p")],
    [sp("add", schedule="+99999999999999d", type="once", prompt="p")],
    [sp("add", schedule="+5m", type="cron", prompt="relative as cron")],
    [sp("add", model="", schedule="+10s", type="once", prompt="p")],
    [sp("update", jobId="nope", prompt="x")],
    [sp("remove")],
    [sp("remove", jobId="nope")],
    [sp("enable")],
    [sp("disable", jobId="nope")],
    [sp("update")],
], files={STORE: store(job("seed000001", "taken", "0 0 * * * *"))}, normalize=NORM)

SEED = store(
    job("cron000001", "hourly", "0 0 * * * *", "hourly prompt", lastStatus="success", lastRun="2026-01-01T00:00:00.000Z", runCount=4, description="the hourly one"),
    job("cron000002", "off", "0 0 9 * * 1-5", "weekday nine", enabled=False),
    job("once000001", "later", "2999-01-01T00:00:00.000Z", "far", type="once", enabled=False),
    job("intv000001", "beat", "30m", "beat prompt", type="interval", intervalMs=1800000),
    job("fore000001", "foreign", "0 0 * * * *", "someone else's", session="another-session"),
    job("fore000002", "foreign-off", "0 0 * * * *", "someone else's, off", enabled=False, session="another-session"),
)

scenario("manage-seeded", "List, enable, disable, update (name, prompt, description, schedule), remove and cleanup on a seeded store with a foreign session's jobs.", [
    [sp("list")],
    [sp("enable", jobId="cron000002")],
    [sp("disable", jobId="cron000001")],
    [sp("update", jobId="cron000001", name="renamed", prompt="new prompt", description="new description")],
    [sp("update", jobId="cron000002", schedule="0 30 8 * * *")],
    [sp("update", jobId="intv000001", schedule="45m")],
    [sp("update", jobId="intv000001", schedule="soon")],
    [sp("update", jobId="once000001", schedule="+5d")],
    [sp("update", jobId="cron000001", notify=True, extensions=True, skills=["x"])],
    [sp("remove", jobId="intv000001")],
    [sp("cleanup")],
    [sp("cleanup")],
    [sp("list")],
    [cat()],
], files={STORE: SEED}, normalize=[{"pattern": r"20\d\d-\d\d-\d\dT\d\d:\d\d:\d\d\.\d\d\dZ", "replacement": "<iso>"}])

scenario("foreign-sessions", "A foreign session's jobs are invisible to list, and cleanup leaves its disabled jobs alone.", [
    [sp("list")],
    [sp("cleanup")],
    [cat()],
], files={STORE: SEED})

scenario("past-once-on-start", "A once job whose time has passed is disabled with an error status when the session starts.", [
    [sp("list")],
    [cat()],
], files={STORE: store(job("once000002", "missed", "2020-01-01T00:00:00.000Z", "was due", type="once"))})

scenario("stale-running-swept", "A lastStatus of running left by an interrupted run is cleared at start; a foreign session's is not.", [
    [sp("list")],
    [cat()],
], files={STORE: store(
    job("cron000003", "stuck", "0 0 * * * *", lastStatus="running"),
    job("fore000003", "theirs", "0 0 * * * *", lastStatus="running", session="another-session"),
)})

scenario("corrupt-store", "A store file that is not JSON reads as empty; the next add replaces it.", [
    [sp("list")],
    [sp("add", name="fresh", schedule="0 0 0 * * *", prompt="after corruption")],
    [cat()],
], files={STORE: "{not json"}, normalize=NORM)

scenario("settings-workdir", "defaultJobScope=workdir in the project settings file overrides the global one: a new job has no session.", [
    [sp("add", name="shared", schedule="0 0 0 * * *", prompt="for every pi here")],
    [cat()],
], files={SETTINGS: json.dumps({"defaultJobScope": "workdir"})}, agentFiles={"schedule-prompts-settings.json": json.dumps({"defaultJobScope": "session", "widgetVisible": False})}, normalize=NORM)

scenario("settings-global-workdir", "defaultJobScope=workdir in the global settings file; a malformed project file is ignored.", [
    [sp("add", name="shared", schedule="0 0 0 * * *", prompt="for every pi here")],
    [cat()],
], files={SETTINGS: "{broken"}, agentFiles={"schedule-prompts-settings.json": json.dumps({"defaultJobScope": "workdir"})}, normalize=NORM)

fire_prompt = "scheduled tick"


def raw_scenario(name, description, llm, steps, **extra):
    sc = {"name": name, "description": description, "llm": llm, "steps": steps}
    sc.update(extra)
    with open(os.path.join(out, name + ".json"), "w") as f:
        json.dump(sc, f, indent=2, ensure_ascii=False)
        f.write("\n")


# A job that fires within a second of the session start lands in the start-up quiet period of both lanes (settleMs 1500), so the
# order of events does not depend on where in a second the host happened to start.
raw_scenario("fire-cron", "An every-second cron job fires: the marker, then the prompt as a user message; the model removes the job.", [
    {"toolCalls": [sp("remove", jobId="cron000010")]}, {"text": "removed"}, {"text": "after"},
], [{"name": "wait", "rpc": {"type": "get_state"}}],
    files={STORE: store(job("cron000010", "tick", "* * * * * *", fire_prompt))}, settleMs=1500, tailMs=500)

raw_scenario("fire-interval", "An interval job fires after its interval; the model removes it.", [
    {"toolCalls": [sp("remove", jobId="intv000010")]}, {"text": "removed"}, {"text": "after"},
], [prompt("after", name="after")],
    files={STORE: store(job("intv000010", "beat", "1s", fire_prompt, type="interval", intervalMs=1000))}, settleMs=1500, tailMs=500)

raw_scenario("fire-once-tool", "A one-shot added by the tool (+2s) fires once and disables itself; the next list shows it.", [
    {"toolCalls": [sp("add", name="once-soon", schedule="+2s", type="once", prompt=fire_prompt)]}, {"text": "scheduled"},
    {"text": "fired"},
    {"toolCalls": [sp("list")]}, {"text": "listed"},
], [prompt("add", name="add"), {"name": "wait", "rpc": {"type": "get_state"}, "settleMs": 3000}, prompt("after", name="after")], tailMs=500, normalize=NORM)

scenario("session-new-cleanup", "A new session removes this session's and the unbound disabled jobs and keeps the foreign ones.", [
    [sp("list")],
], steps=[{"name": "new", "rpc": {"type": "prompt", "message": "/eq-new"}}, prompt("after", name="after")], files={STORE: SEED})

raw_scenario("recursion-after-fire", "A job added after a scheduled prompt ran is accepted: the original's guard looks for a `custom` entry, not the custom_message one a marker is.", [
    {"toolCalls": [sp("remove", jobId="cron000011")]},
    {"toolCalls": [sp("add", name="child", schedule="0 0 0 * * *", prompt="scheduled by a scheduled prompt")]},
    {"toolCalls": [sp("list")]}, {"text": "done"},
], [{"name": "wait", "rpc": {"type": "get_state"}}], files={STORE: store(job("cron000011", "parent", "* * * * * *", fire_prompt))}, normalize=NORM, settleMs=1500, tailMs=500)

# ---- the /schedule-prompt command: the menu and Settings (the Jobs overlay is compared frame by frame, port/frames) ----
def cmd(*answers, name="cmd"):
    ui = [{"cancelled": True} if a is None else {"value": a} for a in answers]
    return {"name": name, "rpc": {"type": "prompt", "message": "/schedule-prompt"}, "ui": ui}


def readback(path=SETTINGS):
    return [call("bash", command="cat " + path + " || echo missing")]


SEEDED = {STORE: store(job("cron000001", "alpha", "0 0 9 * * *"))}
scenario("cmd-menu-cancel", "The menu is cancelled: nothing happens.", [], [cmd(None)])
scenario("cmd-settings-back", "Settings, then Back.", [], [cmd("Settings", "Back")])
scenario("cmd-settings-cancel", "Settings, then the menu is cancelled.", [], [cmd("Settings", None)])
scenario("cmd-settings-widget-toggle", "The widget is hidden and shown again from Settings; the project settings file records the last choice.",
         [readback()], [cmd("Settings", "Widget visibility: shown", "Widget visibility: hidden", "Widget visibility: shown", "Widget visibility: hidden", "Back"),
                        prompt("show the file", name="read")], files=SEEDED)
scenario("cmd-settings-widget-no-job", "Hiding and showing the widget with no job to show.", [readback()],
         [cmd("Settings", "Widget visibility: shown", "Widget visibility: hidden", "Back"), prompt("show the file", name="read")])
scenario("cmd-settings-scope", "The default scope of new jobs is switched twice; a job added afterwards follows the last choice.",
         [readback(), [sp("add", name="after", schedule="0 0 0 * * *", prompt="p")], [cat()]],
         [cmd("Settings", "Bind new jobs to session: yes", "Bind new jobs to session: no", "Bind new jobs to session: yes", "Bind new jobs to session: no", "Back"),
          prompt("show the file", name="read")], normalize=NORM)
scenario("cmd-settings-scope-from-global", "The global setting says workdir: the menu starts at `no`.", [readback()],
         [cmd("Settings", "Bind new jobs to session: no", "Back"), prompt("show the file", name="read")],
         agentFiles={"schedule-prompts-settings.json": "{\"defaultJobScope\": \"workdir\", \"widgetVisible\": false}"})
scenario("cmd-settings-persist-fails", "`.pi` is a file, so the settings cannot be written: both toggles warn that the change is for this session only.", [],
         [cmd("Settings", "Widget visibility: shown", "Bind new jobs to session: yes", "Back")], files={".pi": "not a directory"})
scenario("cmd-jobs-in-rpc", "The Jobs overlay needs a terminal: in RPC mode the host answers the custom component with nothing and the command ends.", [], [cmd("Jobs")], files=SEEDED)
scenario("cmd-unknown-choice", "A choice that is neither Jobs nor Settings ends the command.", [], [cmd("Other")])
scenario("cmd-settings-empty-choice", "An empty answer leaves Settings like Back.", [], [cmd("Settings", "")])

print("wrote", len(os.listdir(out)), "scenarios")

# ---- evidence: behavior of the original that the port deliberately does not copy (PORT.md D1). Recorded under Pi only. ----
ev_out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "evidence", "scenarios")
os.makedirs(ev_out, exist_ok=True)


def evidence(name, description, turns, steps, **extra):
    llm = [{"toolCalls": t} if isinstance(t, list) else t for t in turns]
    s = {"name": name, "description": description, "llm": llm, "steps": steps}
    s.update(extra)
    with open(os.path.join(ev_out, name + ".json"), "w") as f:
        json.dump(s, f, indent=2, ensure_ascii=False)
        f.write("\n")


evidence("u1-long-once", "U1: a one-shot 30 days away. Node's setTimeout cannot hold 2592000000 ms and fires after 1 ms: the prompt arrives at once.", [
    [sp("add", name="in-a-month", schedule="+30d", type="once", prompt="renew the certificate")],
    {"text": "scheduled"},
    {"text": "reminded"},
    {"text": "again"},
    {"text": "and again"},
], [prompt("remind me in 30 days", name="add"), {"name": "wait", "rpc": {"type": "get_state"}, "settleMs": 1500}], tailMs=500, normalize=NORM)
