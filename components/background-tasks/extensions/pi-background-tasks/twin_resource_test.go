package pi_background_tasks

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Twins of the original's resource-control, probe and sidecar-root tests.

const planCommand = "printf 'a b'; cat <<'EOF'\n$HOME && *\nEOF"

func planSettings(mods ...func(*resourceControlSettings)) *resourceControlSettings {
	s := &resourceControlSettings{enabled: true, mode: "auto", applyToBgTask: true, applyToAutoBackground: true, cpuWeight: 100, ioWeight: 100, nice: 10, ioniceClass: "best-effort", ioniceLevel: 7, warnOnFallback: true}
	for _, m := range mods {
		m(s)
	}
	return s
}

func planProbes(systemd bool, commands ...string) *resourceControlProbes {
	if len(commands) == 0 {
		commands = []string{"nice", "ionice", "systemd-run", "systemctl"}
	}
	available := map[string]bool{}
	for _, c := range commands {
		available[c] = true
	}
	return &resourceControlProbes{platform: "linux", commandExists: func(c string) bool { return available[c] }, userSystemdAvailable: func() bool { return systemd }}
}

func planInput(mods ...func(*resourceControlSpawnInput)) resourceControlSpawnInput {
	in := resourceControlSpawnInput{command: planCommand, cwd: "/tmp/work tree", shell: "/bin/bash", shellArgs: []string{"-lc"}, taskID: "bg-7", now: 123456}
	for _, m := range mods {
		m(&in)
	}
	return in
}

func resetSystemdCache() {
	systemdCacheMu.Lock()
	systemdRunnableFor = map[string]bool{}
	systemdCacheMu.Unlock()
}

func TestTwinResourceControl(t *testing.T) {
	tw(t, "resource-settings", "resource settings parse configured fields and clamp numeric values", func(t *testing.T) {
		defaults := resourceControlSettings{enabled: true, mode: "nice-ionice", applyToBgTask: true, applyToAutoBackground: true, cpuWeight: 100, ioWeight: 100, nice: 10, ioniceClass: "best-effort", ioniceLevel: 7, warnOnFallback: true}
		rows := []struct {
			name   string
			config map[string]any
			mod    func(*resourceControlSettings)
		}{
			{"absent configuration uses complete defaults", nil, func(*resourceControlSettings) {}},
			{"enabled setting is read", map[string]any{"resourceControlEnabled": false}, func(s *resourceControlSettings) { s.enabled = false }},
			{"mode setting is read", map[string]any{"resourceControlMode": "auto"}, func(s *resourceControlSettings) { s.mode = "auto" }},
			{"explicit task opt out is read", map[string]any{"resourceControlApplyToBgTask": false}, func(s *resourceControlSettings) { s.applyToBgTask = false }},
			{"auto background opt out is read", map[string]any{"resourceControlApplyToAutoBackground": false}, func(s *resourceControlSettings) { s.applyToAutoBackground = false }},
			{"CPU weight clamps at upper bound", map[string]any{"resourceControlCpuWeight": 50_000}, func(s *resourceControlSettings) { s.cpuWeight = 10_000 }},
			{"string IO weight parses and clamps at lower bound", map[string]any{"resourceControlIoWeight": "0"}, func(s *resourceControlSettings) { s.ioWeight = 1 }},
			{"nice clamps at upper bound", map[string]any{"resourceControlNice": 42}, func(s *resourceControlSettings) { s.nice = 19 }},
			{"unknown ionice class uses default", map[string]any{"resourceControlIoniceClass": "not-a-class"}, func(s *resourceControlSettings) { s.ioniceClass = "best-effort" }},
			{"ionice level clamps at lower bound", map[string]any{"resourceControlIoniceLevel": -5}, func(s *resourceControlSettings) { s.ioniceLevel = 0 }},
			{"fallback warning setting is read", map[string]any{"resourceControlWarnOnFallback": false}, func(s *resourceControlSettings) { s.warnOnFallback = false }},
			{"combined extension manager configuration preserves every parsed value", map[string]any{
				"resourceControlEnabled": true, "resourceControlMode": "nice-ionice", "resourceControlApplyToBgTask": false, "resourceControlApplyToAutoBackground": false,
				"resourceControlCpuWeight": 50_000, "resourceControlIoWeight": "0", "resourceControlNice": 42, "resourceControlIoniceClass": "not-a-class",
				"resourceControlIoniceLevel": -5, "resourceControlWarnOnFallback": false}, func(s *resourceControlSettings) {
				s.applyToBgTask, s.applyToAutoBackground, s.cpuWeight, s.ioWeight, s.nice, s.ioniceLevel, s.warnOnFallback = false, false, 10_000, 1, 19, 0, false
			}},
		}
		for _, row := range rows {
			agent := testAgent(t)
			root := t.TempDir()
			if row.config != nil {
				writeSettings(t, filepath.Join(agent, "settings.json"), row.config)
			}
			want := defaults
			row.mod(&want)
			eq(t, readResourceControlSettings(root), want, row.name)
		}
	})

	tw(t, "resource-spawn-plan", "resource spawn plans retain complete argv, metadata and warnings", func(t *testing.T) {
		fallback := regexp.MustCompile(`^resourceControlMode=auto(?:\s|$)`)
		rows := []struct {
			name            string
			input           func(*resourceControlSpawnInput)
			file            string
			args            []string
			metadata        *resourceControlMeta
			warnings        []*regexp.Regexp
			warnLiteral     []string
			systemdProbes   int
			metaWarnMatches bool
		}{
			{"disabled controls retain original shell argv", func(in *resourceControlSpawnInput) {
				in.settings, in.probes = planSettings(func(s *resourceControlSettings) { s.enabled = false }), planProbes(true)
			}, "/bin/bash", []string{"-lc", planCommand}, nil, nil, nil, 0, true},
			{"systemd service preserves properties cwd and multiline shell command", func(in *resourceControlSpawnInput) {
				in.settings = planSettings(func(s *resourceControlSettings) { s.cpuWeight, s.ioWeight, s.nice, s.ioniceLevel = 25, 50, 12, 6 })
				in.probes = planProbes(true)
			}, "systemd-run", []string{"--user", "--quiet", "--wait", "--pipe", "--collect", "--unit=kendex-pi-bg-bg-7-123456.service",
				"--working-directory=/tmp/work tree", "--property=CPUWeight=25", "--property=IOWeight=50", "--property=Nice=12", "--property=IOSchedulingClass=best-effort",
				"--property=IOSchedulingPriority=6", "--", "/bin/bash", "-lc", planCommand},
				&resourceControlMeta{Mode: "systemd-run", RequestedMode: "auto", UnitName: "kendex-pi-bg-bg-7-123456.service"}, nil, nil, 1, true},
			{"auto mode falls back to nice and ionice", func(in *resourceControlSpawnInput) { in.settings, in.probes = planSettings(), planProbes(false) },
				"nice", []string{"-n", "10", "ionice", "-c", "2", "-n", "7", "/bin/bash", "-lc", planCommand},
				&resourceControlMeta{Mode: "nice-ionice", RequestedMode: "auto"}, []*regexp.Regexp{fallback}, nil, 1, true},
			{"auto background opt out retains original shell argv", func(in *resourceControlSpawnInput) {
				in.origin, in.settings, in.probes = "auto-background", planSettings(func(s *resourceControlSettings) { s.applyToAutoBackground = false }), planProbes(true)
			}, "/bin/bash", []string{"-lc", planCommand}, nil, nil, nil, 0, true},
			{"unavailable explicit systemd mode retains shell with a warning", func(in *resourceControlSpawnInput) {
				in.settings, in.probes = planSettings(func(s *resourceControlSettings) { s.mode = "systemd-run" }), planProbes(false)
			}, "/bin/bash", []string{"-lc", planCommand}, nil, []*regexp.Regexp{regexp.MustCompile(`^resourceControlMode=systemd-run(?:\s|$)`)}, nil, 1, false},
			{"nice ionice mode never asks the systemd probe", func(in *resourceControlSpawnInput) {
				in.settings, in.probes = planSettings(func(s *resourceControlSettings) { s.mode = "nice-ionice" }), planProbes(true)
			}, "nice", []string{"-n", "10", "ionice", "-c", "2", "-n", "7", "/bin/bash", "-lc", planCommand}, &resourceControlMeta{Mode: "nice-ionice", RequestedMode: "nice-ionice"}, nil, nil, 0, true},
			{"nice ionice mode uses nice alone when ionice is missing", func(in *resourceControlSpawnInput) {
				in.settings, in.probes = planSettings(func(s *resourceControlSettings) { s.mode, s.ioniceClass = "nice-ionice", "idle" }), planProbes(false, "nice")
			}, "nice", []string{"-n", "10", "/bin/bash", "-lc", planCommand}, &resourceControlMeta{Mode: "nice-ionice", RequestedMode: "nice-ionice"}, nil, nil, 0, true},
			{"nice ionice mode on Windows retains shell with a warning", func(in *resourceControlSpawnInput) {
				p := planProbes(false)
				p.platform = "windows"
				in.settings, in.probes = planSettings(func(s *resourceControlSettings) { s.mode = "nice-ionice" }), p
			}, "/bin/bash", []string{"-lc", planCommand}, nil, []*regexp.Regexp{regexp.MustCompile(`^resourceControlMode=nice-ionice(?:\s|$)`)}, nil, 0, false},
		}
		for _, row := range rows {
			input := planInput(row.input)
			systemd := 0
			inner := input.probes.userSystemdAvailable
			probes := *input.probes
			probes.userSystemdAvailable = func() bool { systemd++; return inner() }
			input.probes = &probes
			plan := planResourceControlledSpawn(input)
			eq(t, plan.file, row.file, row.name+": file")
			eq(t, plan.args, row.args, row.name+": args")
			eq(t, plan.metadata == nil, row.metadata == nil, row.name+": metadata presence")
			if row.metadata != nil && plan.metadata != nil {
				got := *plan.metadata
				got.Warning = ""
				eq(t, got, *row.metadata, row.name+": metadata")
			}
			eq(t, len(plan.warnings), len(row.warnings), row.name+": warning count")
			for i, re := range row.warnings {
				mustTrue(t, i < len(plan.warnings) && re.MatchString(plan.warnings[i]), row.name+": warning "+re.String())
				if plan.metadata != nil && i == 0 {
					eq(t, plan.metadata.Warning, plan.warnings[0], row.name+": metadata warning is the first warning")
				}
			}
			eq(t, systemd, row.systemdProbes, row.name+": systemd probes")
		}
	})

	tw(t, "resource-spawn-plan", "helper lookups run once per settled command across spawns", func(t *testing.T) {
		niceIonice := []string{"-n", "10", "ionice", "-c", "2", "-n", "7", "/bin/bash", "-lc", planCommand}
		rows := []struct {
			name, mode string
			present    []string
			unsettled  string
			lookups    []string
			file       string
			args       []string
			warn       *regexp.Regexp
		}{
			{"nice ionice mode looks up each helper once", "nice-ionice", []string{"nice", "ionice"}, "", []string{"nice", "ionice"}, "nice", niceIonice, nil},
			{"auto mode settles a missing systemd-run once and falls back", "auto", []string{"nice", "ionice"}, "", []string{"systemd-run", "nice", "ionice"}, "nice", niceIonice, regexp.MustCompile(`^resourceControlMode=auto(?:\s|$)`)},
			{"a killed lookup is asked again on the next spawn", "nice-ionice", []string{"nice", "ionice"}, "ionice", []string{"nice", "ionice", "ionice", "ionice"}, "nice", []string{"-n", "10", "/bin/bash", "-lc", planCommand}, nil},
		}
		for _, row := range rows {
			resetSystemdCache()
			var lookups [][]string
			lookup := newCommandLookup(func(file string, args []string) probeResult {
				lookups = append(lookups, append([]string{file}, args...))
				name := args[len(args)-1]
				if name == row.unsettled {
					return probeSignalled("SIGKILL")
				}
				status := 1
				for _, p := range row.present {
					if p == name {
						status = 0
					}
				}
				return exitedProbe(status, "")
			})
			for i := range 3 {
				plan := planResourceControlledSpawn(planInput(func(in *resourceControlSpawnInput) {
					in.taskID = "bg-" + itoa(i)
					in.settings = planSettings(func(s *resourceControlSettings) { s.mode = row.mode })
					in.probes = &resourceControlProbes{platform: "linux", commandLookup: lookup}
				}))
				eq(t, [2]any{plan.file, plan.args}, [2]any{row.file, row.args}, row.name+": plan")
				if row.warn != nil {
					mustTrue(t, len(plan.warnings) == 1 && row.warn.MatchString(plan.warnings[0]), row.name+": fallback warning")
				} else {
					eq(t, len(plan.warnings), 0, row.name+": no warning")
				}
			}
			var want [][]string
			for _, name := range row.lookups {
				want = append(want, []string{"sh", "-c", `command -v "$1" >/dev/null 2>&1`, "sh", name})
			}
			eq(t, lookups, want, row.name+": lookups")
		}
	})

	tw(t, "resource-stop", "resource stop selects the unit command and returns its result", func(t *testing.T) {
		const unit = "kendex-pi-bg-bg-7.service"
		systemd := &resourceControlMeta{Mode: "systemd-run", RequestedMode: "auto", UnitName: unit}
		zero, one := 0, 1
		rows := []struct {
			name   string
			meta   *resourceControlMeta
			signal string
			run    stopRunResult
			want   resourceStopResult
			calls  [][]string
		}{
			{"SIGTERM stops the persisted unit without blocking", systemd, "SIGTERM", stopRunResult{status: &zero},
				resourceStopResult{attempted: true, ok: true, command: "systemctl", args: []string{"--user", "stop", "--no-block", unit}}, [][]string{{"systemctl", "--user", "stop", "--no-block", unit}}},
			{"SIGKILL kills the persisted unit", systemd, "SIGKILL", stopRunResult{status: &zero},
				resourceStopResult{attempted: true, ok: true, command: "systemctl", args: []string{"--user", "kill", "--signal=SIGKILL", unit}}, [][]string{{"systemctl", "--user", "kill", "--signal=SIGKILL", unit}}},
			{"non systemd metadata does not run a unit command", &resourceControlMeta{Mode: "nice-ionice", RequestedMode: "nice-ionice"}, "SIGTERM", stopRunResult{status: &zero}, resourceStopResult{}, nil},
			{"failed unit command retains stderr", systemd, "SIGTERM", stopRunResult{status: &one, stderr: "unit stop refused", hasStderr: true},
				resourceStopResult{attempted: true, command: "systemctl", args: []string{"--user", "stop", "--no-block", unit}, err: "unit stop refused"}, [][]string{{"systemctl", "--user", "stop", "--no-block", unit}}},
			{"runner error takes precedence over stderr", systemd, "SIGKILL", stopRunResult{errText: "unit command could not start", stderr: "less specific stderr", hasStderr: true},
				resourceStopResult{attempted: true, command: "systemctl", args: []string{"--user", "kill", "--signal=SIGKILL", unit}, err: "unit command could not start"}, [][]string{{"systemctl", "--user", "kill", "--signal=SIGKILL", unit}}},
		}
		for _, row := range rows {
			var calls [][]string
			got := stopResourceControlledTask(row.meta, row.signal, func(command string, args []string) stopRunResult {
				calls = append(calls, append([]string{command}, args...))
				return row.run
			})
			eq(t, got, row.want, row.name)
			eq(t, calls, row.calls, row.name+": calls")
		}
	})

	// resource-systemd-probe.test.ts is a skipIf case that the ledger does not list, so this is a subtest, not a counted twin.
	t.Run("default systemd availability probe executes the required service arguments", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("the original runs this row on Linux only")
		}
		rows := []struct {
			name                       string
			systemctlStatus, runStatus int
			probeRuns, available       bool
		}{
			{"successful service probe includes properties and actual cwd", 0, 0, true, true},
			{"failed user manager probe prevents service probe", 1, 0, false, false},
			{"failed service probe rejects systemd plan", 0, 1, true, false},
		}
		// The stand-ins are shell scripts, as light as the original's one-line Node stubs: a Go test binary built with the race
		// detector can take longer than the one-second probe timeout to start on a loaded host.
		const stub = `#!/bin/sh
cmd=${0##*/}
case $cmd in systemctl) status=$RESOURCE_SYSTEMCTL_STATUS ;; *) status=$RESOURCE_SYSTEMD_RUN_STATUS ;; esac
args=
for a in "$@"; do args="$args${args:+,}\"$a\""; done
printf '{"command":"%s","args":[%s]}\n' "$cmd" "$args" >> "$RESOURCE_PROBE_LOG"
exit "$status"
`
		for _, row := range rows {
			resetSystemdCache()
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			log := filepath.Join(root, "commands.jsonl")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, log, "")
			for _, name := range []string{"systemctl", "systemd-run"} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(stub), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			t.Setenv("RESOURCE_PROBE_LOG", log)
			t.Setenv("RESOURCE_SYSTEMCTL_STATUS", itoa(row.systemctlStatus))
			t.Setenv("RESOURCE_SYSTEMD_RUN_STATUS", itoa(row.runStatus))
			t.Chdir(root)
			defer func(prev clockFunc) { clockNow = prev }(clockNow)
			clockNow = func() int64 { return 123456 }
			manager := newUserManagerReachability(nil, nil)
			plan := planResourceControlledSpawn(planInput(func(in *resourceControlSpawnInput) {
				in.settings = planSettings(func(s *resourceControlSettings) {
					s.mode, s.cpuWeight, s.ioWeight, s.nice, s.ioniceLevel = "systemd-run", 25, 50, 12, 6
				})
				in.probes = &resourceControlProbes{platform: "linux", commandExists: func(c string) bool { return c == "systemctl" || c == "systemd-run" }, userManager: manager}
			}))
			unitProbe := createSystemdUnitActiveProbe(func() string { return "linux" }, nil, manager)
			active := unitProbe("kendex-pi-bg-bg-7-123456.service")
			type call struct {
				Command string   `json:"command"`
				Args    []string `json:"args"`
			}
			var calls []call
			raw, _ := os.ReadFile(log)
			for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				if line == "" {
					continue
				}
				var c call
				if err := json.Unmarshal([]byte(line), &c); err != nil {
					t.Fatal(err)
				}
				calls = append(calls, c)
			}
			wantCalls := []call{{"systemctl", []string{"--user", "show-environment"}}}
			if row.probeRuns {
				wantCalls = append(wantCalls, call{"systemd-run", []string{"--user", "--wait", "--pipe", "--quiet", "--collect", "--unit=kendex-pi-bg-probe-" + itoa(os.Getpid()) + "-123456.service",
					"--working-directory=" + mustGetwd(t), "--property=CPUWeight=25", "--property=IOWeight=50", "--property=Nice=12", "--property=IOSchedulingClass=best-effort",
					"--property=IOSchedulingPriority=6", "--", "/usr/bin/true"}})
			}
			// A reachable manager is not asked again; the unit query follows planning.
			if row.systemctlStatus == 0 {
				wantCalls = append(wantCalls, call{"systemctl", []string{"--user", "is-active", "--quiet", "kendex-pi-bg-bg-7-123456.service"}})
			}
			eq(t, calls, wantCalls, row.name+": calls")
			if row.available {
				eq(t, plan.file, "systemd-run", row.name+": file")
				eq(t, plan.args, []string{"--user", "--quiet", "--wait", "--pipe", "--collect", "--unit=kendex-pi-bg-bg-7-123456.service", "--working-directory=/tmp/work tree",
					"--property=CPUWeight=25", "--property=IOWeight=50", "--property=Nice=12", "--property=IOSchedulingClass=best-effort", "--property=IOSchedulingPriority=6",
					"--", "/bin/bash", "-lc", planCommand}, row.name+": args")
				eq(t, *plan.metadata, resourceControlMeta{Mode: "systemd-run", RequestedMode: "systemd-run", UnitName: "kendex-pi-bg-bg-7-123456.service"}, row.name+": metadata")
				eq(t, len(plan.warnings), 0, row.name+": no warnings")
			} else {
				eq(t, [2]any{plan.file, plan.args}, [2]any{"/bin/bash", []string{"-lc", planCommand}}, row.name+": shell kept")
				mustTrue(t, len(plan.warnings) == 1 && regexp.MustCompile(`^resourceControlMode=systemd-run(?:\s|$)`).MatchString(plan.warnings[0]), row.name+": warning")
			}
			wantActive := triNull
			if row.systemctlStatus == 0 {
				wantActive = triTrue
			}
			// status 0 of the stub for is-active reads as active
			eq(t, active, wantActive, row.name+": unit active")
		}
	})
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	d, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestTwinSystemdUnitProbe(t *testing.T) {
	exited := func(n int) probeResult { return exitedProbe(n, "") }
	manager := "systemctl --user show-environment"
	isActive := func(u string) string { return "systemctl --user is-active --quiet " + u }
	timedOut := probeTimedOut
	tw(t, "systemd-unit-probe", "systemd unit liveness probe caches the user manager's reachability", func(t *testing.T) {
		rows := []struct {
			name     string
			platform string
			units    []string
			answers  map[string][]probeResult
			verdicts []tri
			calls    []string
		}{
			{"a reachable manager is probed once for many units", "linux", []string{"a.service", "b.service", "c.service"},
				map[string][]probeResult{manager: {exited(0)}, isActive("a.service"): {exited(0)}, isActive("b.service"): {exited(3)}, isActive("c.service"): {exited(4)}},
				[]tri{triTrue, triFalse, triNull}, []string{manager, isActive("a.service"), isActive("b.service"), isActive("c.service")}},
			{"an unreachable manager is cached and no unit is queried", "linux", []string{"a.service", "b.service"}, map[string][]probeResult{manager: {exited(1)}}, []tri{triNull, triNull}, []string{manager}},
			{"a missing systemctl is cached as unreachable", "linux", []string{"a.service", "b.service"}, map[string][]probeResult{manager: {probeMissing}}, []tri{triNull, triNull}, []string{manager}},
			{"a spawn failure other than a missing systemctl is retried by the next unit", "linux", []string{"a.service", "b.service"},
				map[string][]probeResult{manager: {probeSpawnFailed("EAGAIN"), exited(0)}, isActive("b.service"): {exited(0)}}, []tri{triNull, triTrue}, []string{manager, manager, isActive("b.service")}},
			{"a timed-out manager probe is retried by the next unit", "linux", []string{"a.service", "b.service"},
				map[string][]probeResult{manager: {timedOut, exited(0)}, isActive("b.service"): {exited(0)}}, []tri{triNull, triTrue}, []string{manager, manager, isActive("b.service")}},
			{"a signalled manager probe is retried by the next unit", "linux", []string{"a.service", "b.service"},
				map[string][]probeResult{manager: {probeSignalled("SIGTERM"), exited(1)}}, []tri{triNull, triNull}, []string{manager, manager}},
			{"a timed-out unit query is unknown", "linux", []string{"a.service"}, map[string][]probeResult{manager: {exited(0)}, isActive("a.service"): {timedOut}}, []tri{triNull}, []string{manager, isActive("a.service")}},
			{"no probe runs off Linux", "darwin", []string{"a.service"}, map[string][]probeResult{}, []tri{triNull}, nil},
		}
		for _, row := range rows {
			var calls []string
			run := func(file string, args []string) probeResult {
				call := strings.Join(append([]string{file}, args...), " ")
				calls = append(calls, call)
				queue := row.answers[call]
				if len(queue) == 0 {
					t.Errorf("%s: unexpected call %s", row.name, call)
					return probeResult{}
				}
				row.answers[call] = queue[1:]
				return queue[0]
			}
			probe := createSystemdUnitActiveProbe(func() string { return row.platform }, run, newUserManagerReachability(run, run))
			var verdicts []tri
			for _, u := range row.units {
				verdicts = append(verdicts, probe(u))
			}
			eq(t, verdicts, row.verdicts, row.name+": verdicts")
			eq(t, calls, row.calls, row.name+": calls")
		}
	})

	tw(t, "systemd-unit-probe", "concurrent unit probes share one manager probe", func(t *testing.T) {
		var mu sync.Mutex
		var calls []string
		release := make(chan struct{})
		run := func(file string, args []string) probeResult {
			call := strings.Join(append([]string{file}, args...), " ")
			mu.Lock()
			calls = append(calls, call)
			mu.Unlock()
			if call == manager {
				<-release
			}
			return exited(0)
		}
		probe := createSystemdUnitActiveProbe(func() string { return "linux" }, run, newUserManagerReachability(run, run))
		results := make([]tri, 2)
		var wg sync.WaitGroup
		for i, u := range []string{"a.service", "b.service"} {
			wg.Add(1)
			go func() { defer wg.Done(); results[i] = probe(u) }()
		}
		waitUntil(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(calls) == 1 })
		time.Sleep(10 * time.Millisecond) // the second caller has joined the probe in flight
		close(release)
		wg.Wait()
		eq(t, results, []tri{triTrue, triTrue}, "verdicts")
		mu.Lock()
		defer mu.Unlock()
		mustTrue(t, len(calls) == 3 && calls[0] == manager, "one manager probe, then one unit query each")
		got := []string{calls[1], calls[2]}
		if got[0] > got[1] {
			got[0], got[1] = got[1], got[0]
		}
		eq(t, got, []string{isActive("a.service"), isActive("b.service")}, "unit queries (either order)")
	})

	tw(t, "systemd-unit-probe", "spawn planning and unit probes read one user manager answer", func(t *testing.T) {
		rows := []struct {
			name    string
			first   string
			answers []probeResult
			answer  [2]tri
			calls   []string
		}{
			{"a synchronous answer serves the unit probe", "sync", []probeResult{exited(0)}, [2]tri{triTrue, triTrue}, []string{"sync " + manager}},
			{"an asynchronous answer serves spawn planning", "async", []probeResult{exited(1)}, [2]tri{triFalse, triFalse}, []string{"async " + manager}},
			{"a synchronous timeout settles nothing", "sync", []probeResult{timedOut, exited(0)}, [2]tri{triNull, triTrue}, []string{"sync " + manager, "async " + manager}},
			{"a missing systemctl settles unreachable", "sync", []probeResult{probeMissing}, [2]tri{triFalse, triFalse}, []string{"sync " + manager}},
		}
		for _, row := range rows {
			var calls []string
			answers := append([]probeResult{}, row.answers...)
			answer := func(mode string) probeRunner {
				return func(file string, args []string) probeResult {
					calls = append(calls, mode+" "+strings.Join(append([]string{file}, args...), " "))
					if len(answers) == 0 {
						t.Errorf("%s: unexpected call", row.name)
						return probeResult{}
					}
					r := answers[0]
					answers = answers[1:]
					return r
				}
			}
			reach := newUserManagerReachability(answer("async"), answer("sync"))
			checkTri := func() tri {
				if reach.check() {
					return triTrue
				}
				reach.mu.Lock()
				defer reach.mu.Unlock()
				return reach.answer
			}
			var got [2]tri
			if row.first == "sync" {
				got[0] = reach.sync()
				got[1] = checkTri()
			} else {
				got[0] = checkTri()
				got[1] = reach.sync()
			}
			// The original's check() answers a boolean, so a null answer reads false there: compare through the memo.
			eq(t, got, row.answer, row.name+": answers")
			eq(t, calls, row.calls, row.name+": calls")
		}
	})

	tw(t, "systemd-unit-probe", "spawn planning asks an unanswering user manager once per settings", func(t *testing.T) {
		resetSystemdCache()
		syncCalls := 0
		reach := newUserManagerReachability(func(string, []string) probeResult { t.Error("unexpected asynchronous probe"); return probeResult{} },
			func(string, []string) probeResult { syncCalls++; return probeTimedOut })
		// A settings shape no other suite plans with, so the per-settings cache starts empty.
		input := planInput(func(in *resourceControlSpawnInput) {
			in.settings = planSettings(func(s *resourceControlSettings) { s.mode, s.cpuWeight = "systemd-run", 4321 })
			in.probes = &resourceControlProbes{platform: "linux", commandExists: func(string) bool { return true }, userManager: reach}
		})
		plans := []string{planResourceControlledSpawn(input).file, planResourceControlledSpawn(input).file}
		eq(t, plans, []string{"/bin/bash", "/bin/bash"}, "plans")
		eq(t, syncCalls, 1, "sync calls")
	})
}

func TestTwinProbes(t *testing.T) {
	tw(t, "probes", "each way a probe ends maps to one tagged result from either runner", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the original skips this table on Windows")
		}
		notExec := filepath.Join(t.TempDir(), "not-executable")
		writeFile(t, notExec, "#!/bin/sh\n")
		rows := []struct {
			name, file string
			args       []string
			want       probeResult
		}{
			{"zero exit keeps stdout", "/bin/sh", []string{"-c", "printf ok"}, exitedProbe(0, "ok")},
			{"non-zero exit keeps its status", "/bin/sh", []string{"-c", "exit 3"}, exitedProbe(3, "")},
			{"a signal the probe did not send", "/bin/sh", []string{"-c", "kill -TERM $$"}, probeSignalled("SIGTERM")},
			{"a missing command", "/nonexistent/kendex-probe", nil, probeMissing},
			{"a command that exists but cannot start", notExec, nil, probeSpawnFailed("EACCES")},
			// The one real wait: a child that outlives the probe timeout.
			{"a child past the timeout", "/bin/sh", []string{"-c", "sleep 5"}, probeTimedOut},
		}
		for _, row := range rows {
			// Both of the original's runners are the one blocking function here.
			eq(t, [2]probeResult{runProbe(row.file, row.args), runProbe(row.file, row.args)}, [2]probeResult{row.want, row.want}, row.name)
		}
	})

	tw(t, "probes", "mapWithConcurrency bounds calls in flight and keeps input order", func(t *testing.T) {
		rows := []struct {
			name        string
			items       []int
			limit       int
			maxInFlight int
		}{
			{"more items than the limit", []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 4, 4},
			{"fewer items than the limit", []int{1, 2}, 4, 2},
			{"no items", nil, 4, 0},
		}
		for _, row := range rows {
			var mu sync.Mutex
			var waiting []chan struct{}
			inFlight, maxInFlight := 0, 0
			done := make(chan []int, 1)
			go func() {
				done <- mapWithConcurrency(row.items, row.limit, func(item int) int {
					ch := make(chan struct{})
					mu.Lock()
					inFlight++
					maxInFlight = max(maxInFlight, inFlight)
					waiting = append(waiting, ch)
					mu.Unlock()
					<-ch
					mu.Lock()
					inFlight--
					mu.Unlock()
					return item * 10
				})
			}()
			var results []int
			for results == nil {
				// Release the calls newest first so completion order differs from input order.
				mu.Lock()
				if n := len(waiting); n > 0 {
					ch := waiting[n-1]
					waiting = waiting[:n-1]
					close(ch)
				}
				mu.Unlock()
				select {
				case results = <-done:
				case <-time.After(time.Millisecond):
				}
			}
			want := []int{}
			for _, i := range row.items {
				want = append(want, i*10)
			}
			eq(t, results, want, row.name+": results")
			eq(t, maxInFlight, row.maxInFlight, row.name+": max in flight")
		}
	})

	tw(t, "sidecar-root", "resolves each override spelling to its exact sidecar root", func(t *testing.T) {
		home := t.TempDir()
		absolute := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		t.Setenv("PIG_EXT_HOST_INFO", "")
		// The original reads PI_CODING_AGENT_DIR; the port's agent directory is PiG's, which is Pi's under PIG_USE_PI_DIRS=1.
		t.Setenv("PIG_USE_PI_DIRS", "1")
		fallback := filepath.Join(home, ".pi", "agent")
		rows := []struct {
			name, override string
			set            bool
			expected       string
		}{
			{"missing override", "", false, fallback},
			{"empty override", "", true, fallback},
			{"whitespace override", "   ", true, fallback},
			{"relative override", "relative/agent", true, fallback},
			{"home shorthand", "~", true, home},
			{"home-relative override", "~/elsewhere", true, filepath.Join(home, "elsewhere")},
			{"absolute override", absolute, true, absolute},
		}
		for _, row := range rows {
			if row.set {
				t.Setenv("PI_CODING_AGENT_DIR", row.override)
			} else {
				os.Unsetenv("PI_CODING_AGENT_DIR")
			}
			eq(t, sidecarStatePath("sidecar-root"), filepath.Join(row.expected, "kendex", "sessions", "sidecar-root", "pi-background-tasks", "state.json"), row.name)
		}
	})

	tw(t, "identity-probe-live", "observes each owned process transition", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("the original runs this suite on Linux only")
		}
		script := "\nprintf 'bash-ready\\n'\nIFS= read -r release || exit 64\ncase \"$release\" in\n  exec) exec /bin/sh -c 'printf \"exec-ready\\n\"; IFS= read -r release; test \"$release\" = exit' ;;\n  exit) exit 0 ;;\n  *) exit 64 ;;\nesac\n"
		for _, row := range []struct {
			name, release string
			matches       bool
		}{
			{"exec changes the executable but retains the process identity", "exec", true},
			{"reaped process no longer matches its recorded identity", "exit", false},
		} {
			cmd := newCommand("/bin/bash", "--noprofile", "--norc", "-c", script)
			cmd.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
			stdin, _ := cmd.StdinPipe()
			stdout, _ := cmd.StdoutPipe()
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			readLine := func(want string) {
				buf := make([]byte, len(want)+1)
				if _, err := ioReadFull(stdout, buf); err != nil || string(buf) != want+"\n" {
					t.Fatalf("%s: expected %s, got %q (%v)", row.name, want, buf, err)
				}
			}
			readLine("bash-ready")
			pid := cmd.Process.Pid
			initialReading := defaultReadProcessIdentity(pid, identityReaderDeps{})
			if initialReading.kind != "identity" {
				t.Fatalf("%s: initial live identity is %s", row.name, initialReading.kind)
			}
			initial := initialReading.identity
			mustTrue(t, regexp.MustCompile(`^\d+$`).MatchString(initial.StartToken) && initial.Comm == "bash" && initial.Pid == pid, row.name+": initial identity")
			stdin.Write([]byte(row.release + "\n"))
			if row.release == "exec" {
				readLine("exec-ready")
			} else if err := cmd.Wait(); err != nil {
				t.Fatal(err)
			}
			current := defaultReadProcessIdentity(pid, identityReaderDeps{})
			matches := current.kind == "identity" && identityMatches(&initial, current.identity)
			eq(t, matches, row.matches, row.name+": matches")
			if row.release == "exec" {
				eq(t, current, identityReading{kind: "identity", identity: processIdentity{Pid: pid, StartToken: initial.StartToken, Comm: "sh"}}, row.name+": current")
				stdin.Write([]byte("exit\n"))
				if err := cmd.Wait(); err != nil {
					t.Fatal(err)
				}
			} else {
				mustTrue(t, current.kind == "gone" || (current.kind == "identity" && current.identity.StartToken != initial.StartToken), row.name+": gone or another start")
			}
		}
	})
}

var _ = errors.New
