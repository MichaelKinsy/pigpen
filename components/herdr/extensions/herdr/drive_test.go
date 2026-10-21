package herdr_test

// The differential check against the original at handler level: testdata/drive-cases.json holds event sequences
// (session_start, agent_start, agent_settled, session_shutdown and herdr:blocked, with a ctx per event, an environment
// and a fake herdr that can close, hang or lag), port/drive/drive.mjs replays them against the unmodified
// herdr-agent-state.ts in Node, behind the stub pi of the original's own test, and records the effects in
// testdata/drive-golden.json. This test replays the same cases through the fake PiG host against the Go port and
// requires the same registered handlers, handler outcomes, connections and request lines, byte for byte (the id keeps
// its shape, each seq becomes its rank, see drive.mjs).

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"testing"
	"time"

	herdr "github.com/MichaelKinsy/pigpen/components/herdr/extensions/herdr"
)

type driveCase struct {
	Name   string                     `json:"name"`
	Env    map[string]*string         `json:"env"`
	Mode   string                     `json:"mode"`
	Ctx    map[string]json.RawMessage `json:"ctx"`
	Server struct {
		Behaviors []string `json:"behaviors"`
		DelayMs   int      `json:"delayMs"`
	} `json:"server"`
	Steps []struct {
		Op     string                     `json:"op"`
		Reason *string                    `json:"reason"`
		Data   json.RawMessage            `json:"data"`
		Ms     int                        `json:"ms"`
		WaitMs *int                       `json:"waitMs"`
		Ctx    map[string]json.RawMessage `json:"ctx"`
	} `json:"steps"`
	FinalMs *int `json:"finalMs"`
}

type driveOutcome struct {
	Name     string   `json:"name"`
	Handlers []string `json:"handlers"`
	Bus      []string `json:"bus"`
	Steps    []string `json:"steps"`
	Requests []struct {
		Conn int    `json:"conn"`
		Line string `json:"line"`
	} `json:"requests"`
	SeqFromClock bool `json:"seqFromClock"`
}

// driveState is the host state of one event: the defaults of the original's ctx, the case's, then the event's.
func driveState(t *testing.T, layers ...map[string]json.RawMessage) hostState {
	t.Helper()
	st := hostState{sessionFile: "/home/user/.pig/agent/sessions/one.jsonl", sessionID: "session-one", idle: true}
	for _, layer := range layers {
		for key, raw := range layer {
			var s *string
			var b bool
			isNull := string(raw) == "null"
			if err := json.Unmarshal(raw, &s); err != nil && key != "idle" {
				t.Fatalf("ctx %s: %v", key, err)
			}
			switch key {
			case "idle":
				st.idleFailure = false
				if s != nil && *s == "throws" {
					st.idleFailure = true
				} else if err := json.Unmarshal(raw, &b); err == nil {
					st.idle = b
				} else {
					t.Fatalf("ctx idle: %s", raw)
				}
			case "file":
				st.noFile, st.fileFailure = isNull, s != nil && *s == "throws"
				if s != nil && !st.fileFailure {
					st.sessionFile = *s
				}
			case "id":
				st.noID, st.idFailure = isNull, s != nil && *s == "throws"
				if s != nil && !st.idFailure {
					st.sessionID = *s
				}
			default:
				t.Fatalf("unknown ctx member %s", key)
			}
		}
	}
	return st
}

var (
	driveIDRE  = regexp.MustCompile(`"id":"pig:(state|release):\d+:[0-9a-z]+"`)
	driveSeqRE = regexp.MustCompile(`"seq":(\d+)`)
)

// slowHerdr is a case whose herdr is slow to answer (server.delayMs): the original's reports stay in flight while the
// next events arrive. The port's fake herdr holds the reports' answers until the case's events are over, which is what
// the delay stands for, instead of a time that a loaded host can outrun between two events.
func slowHerdr(c driveCase) bool { return c.Server.DelayMs > 0 }

// hungHerdr is a case whose herdr leaves a request unanswered, so the extension's own timers end the attempt.
func hungHerdr(c driveCase) bool { return slices.Contains(c.Server.Behaviors, "silent") }

func runDriveCase(t *testing.T, c driveCase) driveOutcome {
	t.Helper()
	h := newFakeHerdr(t)
	answers := make(chan struct{})
	h.setBehave(func(n int) behavior {
		var b behavior
		if n < len(c.Server.Behaviors) {
			switch c.Server.Behaviors[n] {
			case "close":
				b.Close = true
			case "silent":
				b.Silent = true
			}
		}
		return b
	})
	if slowHerdr(c) {
		h.setBehaveRequest(func(req herdrRequest) behavior {
			if req.Method == "pane.report_agent" {
				return behavior{Hold: answers}
			}
			return behavior{}
		})
	}
	env := map[string]string{"HERDR_ENV": "1", "HERDR_SOCKET_PATH": h.path, "HERDR_PANE_ID": "w1:p1", "HERDR_BIN_PATH": "/unused"}
	for k, v := range c.Env {
		if v == nil {
			delete(env, k)
		} else {
			env[k] = *v
		}
	}
	startMicro := time.Now().UnixMicro()
	mode := c.Mode
	if mode == "" {
		mode = "tui"
	}
	host := startHost(t, herdr.ExtensionFor(func(k string) string { return env[k] }), mode)

	out := driveOutcome{Name: c.Name, Handlers: []string{}, Bus: []string{}, Steps: []string{}}
	for _, handler := range host.register.Handlers {
		out.Handlers = append(out.Handlers, handler.Event)
	}
	for channel := range host.listeners {
		out.Bus = append(out.Bus, channel)
	}
	slices.Sort(out.Handlers)
	slices.Sort(out.Bus)

	for _, step := range c.Steps {
		host.setState(func(s *hostState) { *s = driveState(t, c.Ctx, step.Ctx) })
		result := "ok"
		switch step.Op {
		case "start", "agent_start", "agent_settled":
			event := map[string]string{"start": "session_start", "agent_start": "agent_start", "agent_settled": "agent_settled"}[step.Op]
			if _, failed := host.fireResult(event, nil); failed {
				result = "throws"
			}
		case "shutdown":
			data := map[string]any{}
			if step.Reason != nil {
				data["reason"] = *step.Reason
			}
			host.fire("session_shutdown", data)
		case "bus":
			if _, ok := host.listeners["herdr:blocked"]; ok {
				var data any
				if err := json.Unmarshal(step.Data, &data); err != nil {
					t.Fatal(err)
				}
				host.emit("herdr:blocked", data)
			}
		case "sleep":
			time.Sleep(time.Duration(step.Ms) * time.Millisecond)
		default:
			t.Fatalf("unknown op %q", step.Op)
		}
		out.Steps = append(out.Steps, result)
		wait := 60
		if step.WaitMs != nil {
			wait = *step.WaitMs
		}
		if wait == 0 {
			continue // the next event follows at once, as in the original
		}
		// The original waits so that the effects of the event have happened before the next one. Here that is no
		// time: it is every request the extension took to send being on herdr's wire (so connections open in the
		// order the events came), then, unless herdr is deliberately slow or hung, the extension having nothing left
		// to send.
		h.waitSent(host)
		if hungHerdr(c) {
			// The case is about the extension's timers: the events are spaced by real time, as the original's are.
			time.Sleep(time.Duration(wait) * time.Millisecond)
		} else if !slowHerdr(c) {
			host.settle()
		}
	}
	close(answers)
	host.settle() // the extension has sent everything it will send, behind a hung herdr after its own attempt bounds

	reqs := h.requests()
	var seqs []int64
	for _, r := range reqs {
		if m := driveSeqRE.FindStringSubmatch(r.Raw); m != nil {
			n, _ := strconv.ParseInt(m[1], 10, 64)
			seqs = append(seqs, n)
		}
	}
	distinct := slices.Clone(seqs)
	slices.Sort(distinct)
	distinct = slices.Compact(distinct)
	out.SeqFromClock = true
	for _, n := range seqs {
		if n < startMicro {
			out.SeqFromClock = false
		}
	}
	out.Requests = []struct {
		Conn int    `json:"conn"`
		Line string `json:"line"`
	}{}
	for _, r := range reqs {
		line := driveIDRE.ReplaceAllString(r.Raw, `"id":"pig:$1:<ms>:<rand>"`)
		line = driveSeqRE.ReplaceAllStringFunc(line, func(m string) string {
			n, _ := strconv.ParseInt(driveSeqRE.FindStringSubmatch(m)[1], 10, 64)
			return fmt.Sprintf(`"seq":%d`, slices.Index(distinct, n))
		})
		out.Requests = append(out.Requests, struct {
			Conn int    `json:"conn"`
			Line string `json:"line"`
		}{r.Conn, line})
	}
	return out
}

func TestDriveAgainstTheOriginal(t *testing.T) {
	var cases []driveCase
	var golden []driveOutcome
	for file, into := range map[string]any{"testdata/drive-cases.json": &cases, "testdata/drive-golden.json": &golden} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, into); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	if len(cases) != len(golden) || len(cases) == 0 {
		t.Fatalf("%d cases, %d golden entries", len(cases), len(golden))
	}
	// The cases that test a timeout or a retry need the original's real bounds, and run first. A slow herdr's reports are
	// held until the case's events are over (slowHerdr), so no bound may end them before that: those cases run after,
	// with bounds no loaded host reaches. A group's subtests run in parallel and all of them finish before the next
	// group's bounds are set, because t.Run returns when they have.
	pf, pr := *herdr.FirstAttempt, *herdr.RetryAttempt
	defer func() { *herdr.FirstAttempt, *herdr.RetryAttempt = pf, pr }()
	for _, group := range []struct {
		name         string
		first, retry time.Duration
		slow         bool
	}{
		{"cases", herdr.OriginalFirstAttempt, herdr.OriginalRetryAttempt, false},
		{"cases with a slow herdr", 10 * time.Minute, 10 * time.Minute, true},
	} {
		*herdr.FirstAttempt, *herdr.RetryAttempt = group.first, group.retry
		t.Run(group.name, func(t *testing.T) {
			for i, c := range cases {
				if slowHerdr(c) != group.slow {
					continue
				}
				want := golden[i]
				t.Run(c.Name, func(t *testing.T) {
					t.Parallel()
					if want.Name != c.Name {
						t.Fatalf("golden entry %d is %q", i, want.Name)
					}
					got := runDriveCase(t, c)
					a, _ := json.Marshal(got)
					b, _ := json.Marshal(want)
					var ga, gb any
					_ = json.Unmarshal(a, &ga)
					_ = json.Unmarshal(b, &gb)
					if !reflect.DeepEqual(ga, gb) {
						t.Fatalf("port differs from the original\n port: %s\n orig: %s", a, b)
					}
				})
			}
		})
	}
}
