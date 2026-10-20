#!/usr/bin/env python3
"""Generates testdata/manager-cases.json: the worlds (config files, agent files, session rules, yolo, MCP servers) and intents the manager probe
(manager-probe.test.ts) runs through the original's PermissionManager, recorded to extensions/pi-permission-system/testdata/
manager-oracle.json for manager_oracle_test.go. Run from this directory; the output is deterministic (fixed seed)."""
import json
import random

rnd = random.Random(20261020)

STATES = ["allow", "deny", "ask"]
BASH = ["*", "git *", "git push*", "rm -rf *", "rm *", "npm *", "ls", "echo *", "sudo *", "~/bin/x", "~/bin/*", "$HOME/bin/x",
        "?/bin/x", "git status", "# *", "node -e *", "git status*"]
MCP = ["*", "exa_*", "exa_search", "exa:*", "github", "github_*", "danger", "mcp_status", "mcp_list", "mcp_describe", "mcp_call",
       "mcp_search", "myServer:*", "exa_web_search_exa", "research_query-*", "danger_srv", "danger-srv", "mcp__danger_srv__*"]
SKILL = ["*", "librarian", "web-*", "pi-*", "requesting-code-review"]
PATHS = ["*", "*.env", "src/*", "/tmp/*", "/workspace/p/*", "~/.ssh/*", "$HOME/trusted/*", "/dev/null", "/secret/*", "?rc/*", "/other/*"]
TOOLS = ["read", "write", "edit", "grep", "find", "ls", "task", "third_party_tool", "custom", "mcp", "bash", "skill",
         "mcp__danger_srv__wipe", "mcp__danger_srv__*", "mcp__other__x", "mcp__foo", "mcp__*"]
SURFACE_KEYS = ["bash", "mcp", "skill", "path", "path_read", "path_write", "external_directory", "external_directory_read",
                "external_directory_write", "read", "write", "edit", "find", "task", "third_party_tool", "*"]
POOLS = {"bash": BASH, "mcp": MCP, "skill": SKILL}
SERVERS = ["exa", "github", "danger", "danger-srv", "myServer", "research"]
AGENTS = ["reviewer", "coder"]


def pool_for(key):
    if key in POOLS:
        return POOLS[key]
    if key.startswith(("path", "external_directory", "read", "write", "edit", "find")):
        return PATHS
    return ["*", "x*", "y"]


def random_permission(yaml_safe=False):
    perm = {}
    for key in rnd.sample(SURFACE_KEYS, rnd.randint(1, 6)):
        if key == "*" or rnd.random() < 0.3:
            perm[key] = rnd.choice(STATES)
            continue
        patterns = {}
        for p in rnd.sample(pool_for(key), min(len(pool_for(key)), rnd.randint(1, 4))):
            roll = rnd.random()
            if not yaml_safe and roll < 0.1:
                patterns[p] = {"action": "deny", "reason": "no " + p}
            else:
                patterns[p] = rnd.choice(STATES)
        perm[key] = patterns
    # a relocated Pi MCP tool key now and then
    if rnd.random() < 0.12:
        perm[rnd.choice(["mcp__danger_srv__wipe", "mcp__danger_srv__*", "mcp__other__x", "mcp__foo"])] = rnd.choice(STATES)
    return perm


def yaml_of(perm):
    lines = []
    for k, v in perm.items():
        if isinstance(v, str):
            lines.append("  %s: %s" % (json.dumps(k), v))
        else:
            lines.append("  %s:" % json.dumps(k))
            for p, a in v.items():
                lines.append("    %s: %s" % (json.dumps(p), a))
    return "\n".join(lines)


def agent_file(name, perm):
    return "---\nname: %s\ndescription: test agent\npermission:\n%s\n---\n# body\n" % (name, yaml_of(perm))


def config_text(perm):
    return json.dumps({"permission": perm}, indent=2) + "\n"


INVALID = json.dumps({"permission": {"bash": "maybe"}}) + "\n"

BASH_CMDS = ["git status", "git push origin main", "rm -rf /", "rm -rf build", "npm install", "ls", "echo hi", "~/bin/x",
             "~/bin/x --now", "sudo ls -la", "", "# note", "# c\ngit status", "node -e \"\nx\n\"", "  git status  ", "git status\nls",
             "/home/kinsy/bin/x", "$HOME/bin/x"]
BASH_INPUTS = [{"command": c} for c in BASH_CMDS] + [{}, {"command": 5}, {"command": None}]
PATH_INPUTS = [{"path": "/tmp/x"}, {"path": "src/a.ts"}, {}, {"path": ".env"}]
MCP_INPUTS = [{"tool": "exa:search"}, {"tool": "search"}, {"tool": "exa_search"}, {"server": "exa"}, {"describe": "exa:foo"},
              {"describe": "github_list_issues"}, {"connect": "exa"}, {}, {"search": "q"}, {"tool": "wipe", "server": "danger"},
              {"tool": "search_code", "server": "github"}, {"tool": "github:search_code"}, {"tool": "web_search_exa"},
              {"tool": "other_exa"}, {"describe": "unconfigured_server_tool"}, {"tool": "research:query-docs"}, {"status": True},
              {"list": "x"}, {"tool": 5}, {"tool": "danger-srv:wipe"}]
SKILL_INPUTS = [{"name": "librarian"}, {}, {"name": "web-design"}, {"name": "pi-code-review"}, {"name": 5}, {"name": "requesting-code-review"}]
VALUES = [["/tmp/x"], ["/workspace/p/src/a.ts", "src/a.ts"], ["~/.ssh/config"], ["/home/kinsy/.ssh/config"], [], ["*"], ["/secret/x"],
          [".env"], ["/dev/null"], ["/other/y", "/real/other/y"], ["src/a.ts"], ["/home/kinsy/trusted/z"], ["exa"], ["danger"], ["mcp_status"]]
PV_SURFACES = ["path_read", "path_write", "external_directory_read", "external_directory_write", "path", "external_directory", "read",
               "write", "mcp", "bash", "skill", "my_tool"]
TOOL_SURFACES = ["bash", "bash", "bash", " bash ", "read", "write", "edit", "grep", "find", "ls", "task", "third_party_tool", "custom",
                 "mcp", "mcp", "mcp", "skill", "skill", "mcp__danger_srv__wipe", "mcp__other__x", "mcp__foo",
                 "external_directory_read", "path_read", " read "]
SPELLINGS = [[], ["/home/kinsy/bin/x"], ["/home/kinsy/bin/x", "~/bin/x"], ["git status"], [""], ["/home/kinsy/bin/danger --now"]]


def tool_intent(agent):
    s = rnd.choice(TOOL_SURFACES)
    t = s.strip()
    if t == "bash":
        inp = rnd.choice(BASH_INPUTS)
    elif t == "mcp":
        inp = rnd.choice(MCP_INPUTS)
    elif t == "skill":
        inp = rnd.choice(SKILL_INPUTS)
    elif t.startswith("mcp__"):
        inp = {"target": "prod"}
    else:
        inp = rnd.choice(PATH_INPUTS)
    out = {"kind": "tool", "surface": s, "input": inp}
    if agent:
        out["agentName"] = agent
    return out


def values_intent(agent):
    out = {"kind": "path-values", "surface": rnd.choice(PV_SURFACES), "values": rnd.choice(VALUES)}
    if agent:
        out["agentName"] = agent
    return out


def bash_intent(agent):
    out = {"kind": "bash-command", "surface": "bash", "command": rnd.choice(BASH_CMDS), "spellings": rnd.choice(SPELLINGS)}
    if agent:
        out["agentName"] = agent
    return out


def random_session_rules():
    if rnd.random() < 0.45:
        return []
    rules = []
    for _ in range(rnd.randint(1, 3)):
        surface = rnd.choice(["bash", "mcp", "skill", "read", "path_read", "path_write", "external_directory_read",
                              "external_directory_write", "third_party_tool", "path"])
        rules.append({"surface": surface, "pattern": rnd.choice(pool_for(surface)), "action": rnd.choices(STATES, [8, 1, 1])[0],
                      "layer": "session", "origin": "session"})
    return rules


def intents_for(agents):
    out = []
    for _ in range(46):
        agent = rnd.choice([None] + agents + agents)
        roll = rnd.random()
        out.append(tool_intent(agent) if roll < 0.55 else values_intent(agent) if roll < 0.85 else bash_intent(agent))
    return out


def world(wid, global_perm, project=None, agents=None, project_agents=None, mcp=None, yolo=False, session=None, **over):
    agents = agents or {}
    project_agents = project_agents or {}
    names = sorted(set(agents) | set(project_agents) | set(over.get("unreadableAgents", [])) | set(over.get("unreadableProjectAgents", [])))
    w = {
        "id": wid,
        "global": None if global_perm is None else (global_perm if isinstance(global_perm, str) else config_text(global_perm)),
        "project": None if project is None else (project if isinstance(project, str) else config_text(project)),
        "agents": {n: agent_file(n, p) for n, p in agents.items()},
        "projectAgents": {n: agent_file(n, p) for n, p in project_agents.items()},
        "unreadableAgents": [], "unreadableProjectAgents": [],
        "mcp": mcp if mcp is not None else [],
        "yolo": yolo,
        "sessionRules": session if session is not None else [],
        "agentNames": [None] + names + (["ghost"] if names else []),
        "tools": TOOLS + [" bash "],
    }
    w.update(over)
    w["intents"] = intents_for(names or AGENTS[:1])
    return w


worlds = []

# hand-picked worlds: the configurations the original's own tests exercise, plus the failure modes
worlds.append(world("documented-mcp", {"mcp": {"*": "ask", "mcp_status": "allow", "mcp_list": "allow", "myServer:*": "ask", "dangerousServer": "deny"}},
                    mcp=["myServer", "dangerousServer"]))
worlds.append(world("no-config", None))
worlds.append(world("empty-config", {}))
worlds.append(world("universal-deny", {"*": "deny", "read": "allow"}))
worlds.append(world("universal-allow", {"*": "allow", "bash": {"*": "ask", "rm *": "deny"}}))
worlds.append(world("pi-mcp-tool-keys", {"*": "allow", "mcp": {"*": "allow", "danger-srv": "deny"}, "mcp__other__x": "deny"}, mcp=["danger-srv"]))
worlds.append(world("pi-mcp-wildcard-key", {"*": "ask", "mcp__danger_srv__*": "deny", "mcp__foo": "allow"}, mcp=["danger-srv"]))
worlds.append(world("directional-sugar", {"*": "ask", "path": {"*": "allow", "*.env": "deny"}, "external_directory": {"/tmp/*": "allow"},
                                          "external_directory_read": {"/other/*": "allow"}}))
worlds.append(world("invalid-project-floors", {"*": "allow", "bash": "allow", "read": "allow", "mcp": {"exa_*": "allow"}}, project=INVALID, mcp=["exa"]))
worlds.append(world("invalid-project-deny-kept", {"bash": "deny", "read": "allow"}, project=INVALID))
worlds.append(world("invalid-project-yolo", {"bash": "allow", "mcp": {"*": "deny", "exa_*": "ask"}}, project=INVALID, yolo=True, mcp=["exa"]))
worlds.append(world("invalid-global-ignored", INVALID, project={"permission": {"read": "allow"}}))
worlds.append(world("yolo-asks", {"bash": "ask", "read": {"*": "ask", "*.env": "deny"}}, yolo=True))
worlds.append(world("agent-overrides", {"*": "ask", "bash": "deny", "mcp": {"*": "deny"}}, agents={"reviewer": {"bash": {"*": "deny", "git *": "ask"}, "mcp": "allow"}}))
worlds.append(world("agent-unreadable-floors", {"*": "allow", "bash": "allow"}, unreadableAgents=["reviewer"], agents={"coder": {"read": "deny"}}))
worlds.append(world("project-agent-unreadable-floors", {"*": "allow", "bash": "allow"}, unreadableProjectAgents=["reviewer"], agents={"reviewer": {"read": "deny"}}))
worlds.append(world("session-over-config", {"*": "deny", "bash": {"*": "deny"}},
                    session=[{"surface": "bash", "pattern": "git *", "action": "allow", "layer": "session", "origin": "session"},
                             {"surface": "mcp", "pattern": "mcp_call", "action": "allow", "layer": "session", "origin": "session"},
                             {"surface": "path_read", "pattern": "*.env", "action": "deny", "layer": "session", "origin": "session"}]))
worlds.append(world("home-patterns", {"bash": {"*": "ask", "~/bin/x": "allow", "$HOME/bin/*": "deny"}, "external_directory": {"~/trusted/*": "allow", "$HOME/trusted/*": "deny"},
                                      "path": {"~/.ssh/*": "deny", "*": "allow"}}))
worlds.append(world("deny-with-reason", {"bash": {"npm *": {"action": "deny", "reason": "Use pnpm instead"}, "*": "ask"}, "read": {"*.env": {"action": "deny", "reason": "secrets"}}}))
worlds.append(world("malformed-entries", {"bash": {"npm *": {"action": "deny", "reason": 42}, "ls": "allow"}, "read": ["allow", "deny"], "skill": 7}))
worlds.append(world("all-scopes", {"*": "deny", "bash": {"*": "ask", "git *": "allow"}}, project={"bash": {"rm *": "deny"}, "read": "allow"},
                    agents={"reviewer": {"*": "ask", "bash": {"git log *": "allow"}}}, project_agents={"reviewer": {"*": "deny", "read": "deny"}}))

# random worlds
for i in range(170):
    ag = {}
    pag = {}
    for name in AGENTS:
        if rnd.random() < 0.4:
            ag[name] = random_permission(yaml_safe=True)
        if rnd.random() < 0.25:
            pag[name] = random_permission(yaml_safe=True)
    project = None
    roll = rnd.random()
    if roll < 0.45:
        project = random_permission()
    elif roll < 0.57:
        project = INVALID
    gp = random_permission() if rnd.random() < 0.92 else (INVALID if rnd.random() < 0.5 else None)
    over = {}
    if rnd.random() < 0.05:
        over["unreadableAgents"] = [rnd.choice(AGENTS)]
        ag.pop(over["unreadableAgents"][0], None)
    if rnd.random() < 0.04:
        over["unreadableProjectAgents"] = [rnd.choice(AGENTS)]
        pag.pop(over["unreadableProjectAgents"][0], None)
    worlds.append(world("random-%03d" % i, gp, project=project, agents=ag, project_agents=pag,
                        mcp=rnd.sample(SERVERS, rnd.randint(0, 3)), yolo=rnd.random() < 0.25, session=random_session_rules(), **over))

with open("../../extensions/pi-permission-system/testdata/manager-cases.json", "w") as f:
    json.dump(worlds, f, indent=1, sort_keys=True)
    f.write("\n")
print(len(worlds), "worlds", sum(len(w["intents"]) for w in worlds), "intents")
