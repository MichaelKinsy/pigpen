package pi_subagents

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctor(t *testing.T) {
	const f = "doctor"
	agent := func(name string, source AgentSource) AgentConfig {
		return AgentConfig{Name: name, Description: name + " agent", SystemPrompt: "Prompt", SystemPromptMode: "replace", Source: source, FilePath: "/tmp/" + name + ".md"}
	}
	emptyAll := func(root string) DiscoveryAll {
		return DiscoveryAll{UserDir: root, ProjectDir: root, UserChainDir: root, ProjectChainDir: root, UserSettingsPath: filepath.Join(root, "user.json"), ProjectSettingsPath: filepath.Join(root, "project.json")}
	}
	plainDeps := func(root string) doctorDeps {
		return doctorDeps{
			IsAsyncAvailable:        func() bool { return true },
			DiscoverAgentsAll:       func(string) (DiscoveryAll, error) { return emptyAll(root), nil },
			DiscoverAvailableSkills: func(string) ([]AvailableSkill, error) { return nil, nil },
			DiagnoseIntercomBridge:  func() intercomDiagnostic { return intercomDiagnostic{Mode: "off", ExtensionDir: "none"} },
		}
	}
	tw(t, f, "formats a bounded successful environment summary", func(t *testing.T) {
		root := tmp(t)
		state := &subagentState{Spawns: &spawnCounters{SessionID: "session-abc123", Count: 3, ConfiguredLimit: new(int), ConfiguredResolved: true, Granted: 1,
			GrantHistory: []grantRecord{{SessionID: "session-abc123", Amount: 1, GrantedAt: 0, PreviousLimit: 4, Limit: 5}}}}
		*state.Spawns.ConfiguredLimit = 4
		paths := doctorPaths{TempRoot: filepath.Join(root, "temp-root"), Async: filepath.Join(root, "async"), Results: filepath.Join(root, "results"), ChainRuns: filepath.Join(root, "chains")}
		for _, d := range []string{paths.TempRoot, paths.Async, paths.Results, paths.ChainRuns} {
			mkdirAll(t, d)
		}
		t.Setenv("PI_SUBAGENT_MAX_SPAWNS_PER_SESSION", "")
		unsetenv(t, "PI_SUBAGENT_MAX_SPAWNS_PER_SESSION")
		t.Setenv("PI_SUBAGENT_MAX_SPAWNS_PER_RUN", "")
		unsetenv(t, "PI_SUBAGENT_MAX_SPAWNS_PER_RUN")
		report := buildDoctorReport(doctorInput{
			Cwd: root, Config: doctorConfig{DefaultSessionDir: "~/subagent-sessions", MaxActiveAsyncRunsPerSession: float64(2), Spawn: extensionConfig{MaxSubagentSpawnsPerSession: float64(4)}},
			State: state, CurrentSessionFile: filepath.Join(root, "sessions", "parent.jsonl"), CurrentSessionID: "session-abc123", OrchestratorTarget: "subagent-chat-abc123",
			ExpandTilde: func(v string) string { return strings.Replace(v, "~/", root+"/home/", 1) }, Paths: &paths,
			Deps: doctorDeps{
				IsAsyncAvailable: func() bool { return true },
				DiscoverAgentsAll: func(string) (DiscoveryAll, error) {
					all := emptyAll(root)
					all.Builtin = []AgentConfig{agent("builtin-a", SourceBuiltin)}
					all.User = []AgentConfig{agent("user-a", SourceUser)}
					all.Project = []AgentConfig{agent("project-a", SourceProject), agent("project-b", SourceProject)}
					all.Diagnostics = []Diagnostic{{Source: SourceProject, FilePath: filepath.Join(root, ".pi", "agents", "broken.md"), Name: "broken", Error: "invalid runner"}}
					return all, nil
				},
				DiscoverAvailableSkills: func(string) ([]AvailableSkill, error) {
					return []AvailableSkill{{Name: "project-skill", Source: skillProject}, {Name: "package-skill", Source: skillUserPackage}}, nil
				},
				DiagnoseIntercomBridge: func() intercomDiagnostic {
					return intercomDiagnostic{Active: true, Mode: "always", WantsIntercom: true, SupervisorChannelAvailable: true, ExtensionDir: "native:pi-subagents-supervisor-channel", OrchestratorTarget: "subagent-chat-abc123"}
				},
			},
		})
		polMatches(t, report, `^Subagents doctor report`)
		eq(t, strings.Contains(report, "- cwd: "+root), true, "cwd")
		polMatches(t, report, `- async support: available`)
		polMatches(t, report, `- configured session dir: .*subagent-sessions`)
		polMatches(t, report, `- current session file: .*parent\.jsonl`)
		polMatches(t, report, `- temp root: ok `)
		polMatches(t, report, `- agents: total 4 \(builtin 1, package 0, user 1, project 2\)`)
		polMatches(t, report, `- invalid agent broken \(project\): invalid runner`)
		polMatches(t, report, `Spawn budget\n- usage: 3/5 used, 2 remaining \(configured 4; granted 1; grant allowance 3\)`)
		polMatches(t, report, `- recent grants: \+1 at 1970-01-01T00:00:00\.000Z \(4 → 5\)`)
		polMatches(t, report, `new parent session resets usage and grants; compaction does not`)
		polMatches(t, report, `Run fan-out budget\n- configured limit: 64 \(default\)`)
		polMatches(t, report, `cumulative claims are never released; a new top-level run creates a new budget`)
		polMatches(t, report, `Active async capacity\n- usage: 0/2 used`)
		polMatches(t, report, `terminal state plus matching observed process-terminal proof, or abandoned-timeout for failed runs with a dead runner PID and stale activity when enabled; false keeps unknown-proof slots`)
		polMatches(t, report, `Workflow script\n- helpers: runs\.run, runs\.all, runs\.steer, runs\.status, runs\.ref/refs, emit, console`)
		polMatches(t, report, `if runs\.all is missing, reload or update pi-subagents; await Promise\.all\(\[runs\.run\(\.\.\.\)\]\) is also supported`)
		polMatches(t, report, `- skills: total 2 \(project 1, user-package 1\)`)
		polMatches(t, report, `- bridge: active`)
		polMatches(t, report, `- supervisor channel: available \(native:pi-subagents-supervisor-channel\)`)
		polNotMatches(t, report, `Companion packages`)
	})
	tw(t, f, "reports the effective source when the run fan-out environment value is invalid", func(t *testing.T) {
		root := tmp(t)
		t.Setenv("PI_SUBAGENT_MAX_SPAWNS_PER_RUN", "invalid")
		report := buildDoctorReport(doctorInput{Cwd: root, Config: doctorConfig{Spawn: extensionConfig{MaxSubagentSpawnsPerRun: float64(12)}}, Deps: plainDeps(root)})
		polMatches(t, report, `Run fan-out budget\n- configured limit: 12 \(config\)`)
	})
	tw(t, f, "distinguishes dedicated runner forwarding from root state", func(t *testing.T) {
		root := tmp(t)
		report := func() string { return buildDoctorReport(doctorInput{Cwd: root, Deps: plainDeps(root)}) }
		t.Setenv("PI_SUBAGENT_CHILD", "1")
		t.Setenv("PI_SUBAGENT_PARENT_SESSION", "launch-parent")
		runner := report()
		polMatches(t, runner, `runner parent session: set \(launch-parent\) — explicit target for this dedicated child process`)
		polNotMatches(t, runner, `foreground external ask forwarding`)
		unsetenv(t, "PI_SUBAGENT_CHILD")
		t.Setenv("PI_SUBAGENT_PARENT_SESSION", "legacy-root")
		legacy := report()
		polMatches(t, legacy, `root parent session: ignored legacy process-global value — root sessions do not use it for routing`)
		polMatches(t, legacy, `foreground external ask forwarding: unavailable until the permission extension supports a session-scoped target`)
		unsetenv(t, "PI_SUBAGENT_PARENT_SESSION")
		polMatches(t, report(), `root parent session: not set \(healthy\) — detached forwarding is attached at runner launch`)
	})
	tw(t, f, "keeps reporting when a directory or discovery check fails", func(t *testing.T) {
		root := tmp(t)
		asyncPath := filepath.Join(root, "async-file")
		writeFile(t, asyncPath, "not a directory")
		report := buildDoctorReport(doctorInput{Cwd: root, Paths: &doctorPaths{TempRoot: root, Async: asyncPath, Results: filepath.Join(root, "missing-results"), ChainRuns: filepath.Join(root, "missing-chains")},
			Deps: doctorDeps{
				IsAsyncAvailable:        func() bool { return false },
				DiscoverAgentsAll:       func(string) (DiscoveryAll, error) { return DiscoveryAll{}, errors.New("discovery exploded") },
				DiscoverAvailableSkills: func(string) ([]AvailableSkill, error) { return nil, nil },
				DiagnoseIntercomBridge: func() intercomDiagnostic {
					return intercomDiagnostic{Mode: "fork-only", SupervisorChannelAvailable: true, ExtensionDir: "native:pi-subagents-supervisor-channel", Reason: "bridge mode is fork-only and context is not fork"}
				},
			}})
		polMatches(t, report, `- async support: unavailable`)
		polMatches(t, report, `- async runs: failed .*Error: not a directory:`)
		polMatches(t, report, `- results: missing `)
		polMatches(t, report, `- agents: failed — Error: discovery exploded`)
		polMatches(t, report, `- skills: total 0 \(none\)`)
		polMatches(t, report, `- bridge: inactive \(bridge mode is fork-only and context is not fork\)`)
	})
}
