package ohmypig

// Differential of the approval gate against omp v19.1.1's own resolver.
//
// Default tests run automatically with `go test`:
//   - TestOmpApprovalDifferentialReduced runs the checked-in reduced corpus
//     (`testdata/approval_diff_reduced.jsonl`, derived deterministically via
//     `piglets/oh-my-pig/proof/diff/approval/` from `corpus.jsonl` sha256 `b64b61eb9a439973`)
//     covering every section, every tool, and every expectation class
//     (`port-stricter`, `port-ignores-untrusted-project`, `fail-closed`), and
//     FAILS on any case not equal and not as expected.
//   - TestOmpApprovalUnwrapRoute tests all 17 bracketed `proc://<job>/kill`
//     shapes end-to-end against the real `write` tool and FAILS on any violation.
//
// Env-driven full-corpus runs (skipped unless `OMP_APPROVAL_DIFF_CORPUS` is set):
//   - TestZZOmpApprovalDifferential
//   - TestZZOmpApprovalUnwrapRoute
//
// Environment variables for full runs:
//   OMP_APPROVAL_DIFF_CORPUS    corpus.jsonl (without it ZZ tests are skipped)
//   OMP_APPROVAL_DIFF_REPORT    file for the class counts and examples (also logged)
//   OMP_APPROVAL_DIFF_OUT       per-case JSONL: id, class, the port's verdict
//   OMP_APPROVAL_DIFF_EXAMPLES  examples per class and section (default 3)
//   OMP_APPROVAL_DIFF_SECTIONS  comma list, run only these sections
//   OMP_APPROVAL_DIFF_FAIL=1    fail when a class other than "equal" / "as expected" is not empty

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/agents"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/approval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
)

type ompApprDiffVerdict struct {
	Decision    string   `json:"decision"`
	Text        string   `json:"text,omitempty"`
	PromptTitle string   `json:"promptTitle,omitempty"`
	Choices     []string `json:"choices,omitempty"`
	Source      string   `json:"source,omitempty"`
	Tier        string   `json:"tier,omitempty"`
}

type ompApprDiffPrior struct {
	Tool   string `json:"tool"`
	Args   string `json:"args"`
	Answer string `json:"answer"`
	Choice string `json:"choice"`
}

type ompApprDiffSettings struct {
	Operator    map[string]any `json:"operator"`
	Project     map[string]any `json:"project"`
	Trusted     bool           `json:"trusted"`
	OperatorRaw *string        `json:"operatorRaw"`
}

type ompApprDiffCase struct {
	ID       int                 `json:"id"`
	Section  string              `json:"section"`
	Tool     string              `json:"tool"`
	Args     string              `json:"args"`
	Settings ompApprDiffSettings `json:"settings"`
	UI       string              `json:"ui"`
	Session  string              `json:"session"`
	State    *struct {
		NilSetting bool              `json:"nilSetting"`
		Prior      *ompApprDiffPrior `json:"prior"`
	} `json:"state"`
	Omp        ompApprDiffVerdict  `json:"omp"`
	Expect     string              `json:"expect"`
	ExpectPort *ompApprDiffVerdict `json:"expectPort"`
	Compare    *bool               `json:"compare"`
	Why        string              `json:"why"`
}

// ompApprDiffNest turns {"tools.approvalMode": v} into {"tools": {"approvalMode": v}}.
func ompApprDiffNest(flat map[string]any) map[string]any {
	out := map[string]any{}
	for id, v := range flat {
		parts := strings.Split(id, ".")
		at := out
		for _, p := range parts[:len(parts)-1] {
			next, ok := at[p].(map[string]any)
			if !ok {
				next = map[string]any{}
				at[p] = next
			}
			at = next
		}
		at[parts[len(parts)-1]] = v
	}
	return out
}

func ompApprDiffScalar(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ompApprDiffYAML writes block YAML (maps, lists, JSON-quoted scalars).
func ompApprDiffYAML(v any, indent int) []string {
	pad := strings.Repeat(" ", indent)
	var lines []string
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			switch c := t[k].(type) {
			case map[string]any:
				if len(c) == 0 {
					continue
				}
				lines = append(lines, pad+k+":")
				lines = append(lines, ompApprDiffYAML(c, indent+2)...)
			case []any:
				if len(c) == 0 {
					continue
				}
				lines = append(lines, pad+k+":")
				lines = append(lines, ompApprDiffYAML(c, indent+2)...)
			default:
				lines = append(lines, pad+k+": "+ompApprDiffScalar(c))
			}
		}
	case []any:
		for _, item := range t {
			switch c := item.(type) {
			case map[string]any:
				sub := ompApprDiffYAML(c, indent+2)
				if len(sub) > 0 {
					sub[0] = pad + "- " + strings.TrimPrefix(sub[0], pad+"  ")
				}
				lines = append(lines, sub...)
			default:
				lines = append(lines, pad+"- "+ompApprDiffScalar(c))
			}
		}
	}
	return lines
}

func ompApprDiffConfig(flat map[string]any) string {
	lines := ompApprDiffYAML(ompApprDiffNest(flat), 0)
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

type ompApprDiffHost struct {
	h       *fakeHost
	cwd     string
	cwdReal string
	mu      sync.Mutex
	asked   bool
	title   string
	choices []string
	want    []string // normalized labels the next prompt is answered with, first one offered wins
}

func (z *ompApprDiffHost) norm(s string) string {
	if z.cwdReal != "" && z.cwdReal != z.cwd {
		s = strings.ReplaceAll(s, z.cwdReal, "$CWD")
	}
	return strings.ReplaceAll(s, z.cwd, "$CWD")
}

var ompApprDiffSeq int

func ompApprDiffStart(t *testing.T, s ompApprDiffSettings, ui bool, sid string) *ompApprDiffHost {
	t.Helper()
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)
	t.Setenv("PIG_HOME", filepath.Join(fakeHome, ".pig"))
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(fakeHome, ".pig", "agent"))
	agentDir := filepath.Join(fakeHome, ".pig", "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	global := ompApprDiffConfig(s.Operator)
	if s.OperatorRaw != nil {
		global = *s.OperatorRaw
	}
	if global != "" {
		if err := os.WriteFile(filepath.Join(agentDir, "config.yml"), []byte(global), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cwd := t.TempDir()
	if project := ompApprDiffConfig(s.Project); project != "" {
		if err := os.MkdirAll(filepath.Join(cwd, ".pig"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cwd, ".pig", "config.yml"), []byte(project), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	z := &ompApprDiffHost{cwd: cwd}
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		z.cwdReal = real
	}
	mode := "tui"
	if !ui {
		mode = "print"
	}
	z.h = startFakeHost(t, Extension(), fakeHostOptions{
		Cwd: cwd, SessionID: sid, Mode: mode, HasUI: ui, FakeHome: fakeHome, ProjectTrusted: s.Trusted,
		SelectAnswer: func(title string, opts []string) (string, bool) {
			z.mu.Lock()
			defer z.mu.Unlock()
			z.asked, z.title, z.choices = true, title, append([]string(nil), opts...)
			for _, w := range z.want {
				for _, o := range opts {
					if z.norm(o) == w {
						return o, true
					}
				}
			}
			return "", false
		},
	})
	z.h.emitEvent(sdk.EventSessionStart, nil)
	_ = z.h.emitBeforeAgentStart()
	return z
}

// veto emits the tool_call event and reads the gate's answer. answer lists the labels a prompt is answered with.
func (z *ompApprDiffHost) veto(tool, rawArgs string, answer ...string) (ompApprDiffVerdict, bool) {
	z.mu.Lock()
	z.asked, z.title, z.choices, z.want = false, "", nil, answer
	z.mu.Unlock()
	ev := map[string]any{"toolName": tool, "toolCallId": "ompdiff"}
	var decoded any
	if json.Unmarshal([]byte(rawArgs), &decoded) == nil {
		if m, ok := decoded.(map[string]any); ok {
			ev["input"] = m
		}
	}
	blocked, reason := false, ""
	for _, res := range z.h.emitEventResults(sdk.EventToolCall, ev) {
		if inner, ok := res["_pigToolCallResult"].(map[string]any); ok {
			res = inner
		}
		if b, _ := res["block"].(bool); b {
			blocked = true
			reason, _ = res["reason"].(string)
		}
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	v := ompApprDiffVerdict{}
	switch {
	case z.asked:
		v.Decision, v.PromptTitle = "prompt", z.norm(z.title)
		for _, c := range z.choices {
			v.Choices = append(v.Choices, z.norm(c))
		}
	case blocked && strings.Contains(reason, "requires approval but no interactive UI"):
		v.Decision, v.Text = "prompt", z.norm(reason)
	case blocked:
		v.Decision, v.Text = "deny", z.norm(reason)
	default:
		v.Decision = "allow"
	}
	return v, z.asked && !blocked // second value: a prompt was shown and the answer let the call through
}

func ompApprDiffSameText(a, b ompApprDiffVerdict) bool {
	return a.Text == b.Text && a.PromptTitle == b.PromptTitle && strings.Join(a.Choices, "\x00") == strings.Join(b.Choices, "\x00")
}

// ompApprDiffClass names how the port's verdict relates to the target (omp's, or expectPort).
func ompApprDiffClass(target, port ompApprDiffVerdict) string {
	if target.Decision == port.Decision {
		if ompApprDiffSameText(target, port) {
			return "equal"
		}
		return "text differs"
	}
	switch {
	case port.Decision == "allow" && target.Decision == "prompt":
		return "PORT ALLOWS where omp prompts"
	case port.Decision == "allow" && target.Decision == "deny":
		return "PORT ALLOWS where omp denies"
	case port.Decision == "prompt" && target.Decision == "deny":
		return "port prompts where omp denies"
	case target.Decision == "allow":
		return "port denies or prompts where omp allows"
	default:
		return "port denies where omp prompts"
	}
}

func ompApprDiffShort(v ompApprDiffVerdict) string {
	s := v.Decision
	if v.Text != "" {
		s += " " + strconv.Quote(v.Text)
	}
	if v.PromptTitle != "" {
		s += " " + strconv.Quote(v.PromptTitle) + " " + fmt.Sprint(v.Choices)
	}
	if len(s) > 420 {
		s = s[:420] + "…"
	}
	return s
}

func runOmpApprovalDifferential(t *testing.T, corpus string, failOnBad bool, reportPath, outPath, sectionsCSV string, examples int) {
	t.Helper()
	only := map[string]bool{}
	for _, s := range strings.Split(sectionsCSV, ",") {
		if s = strings.TrimSpace(s); s != "" {
			only[s] = true
		}
	}
	f, err := os.Open(corpus)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	groups := map[string][]ompApprDiffCase{}
	var order []string
	total := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var c ompApprDiffCase
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatalf("corpus line: %v", err)
		}
		if len(only) > 0 && !only[c.Section] {
			continue
		}
		total++
		nilSetting := c.State != nil && c.State.NilSetting
		kb, _ := json.Marshal([]any{c.Settings, c.UI, c.Session, nilSetting})
		k := string(kb)
		if c.State != nil && c.State.Prior != nil {
			k += "#" + strconv.Itoa(c.ID) // grants must not leak between cases
		}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], c)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}

	var report strings.Builder
	say := func(format string, a ...any) { fmt.Fprintf(&report, format+"\n", a...) }
	say("approval differential: %d cases, %d settings groups, corpus %s", total, len(order), corpus)

	// Verify that fakeHost answers ui.select with {"ok": true, "selected": …}.
	canAnswer := false
	t.Run("probe", func(t *testing.T) {
		// A session id of its own: the fake host's stop does not end the
		// session, so a fixed id would carry state into the next probe.
		ompApprDiffSeq++
		z := ompApprDiffStart(t, ompApprDiffSettings{Operator: map[string]any{"tools.approvalMode": "always-ask"}}, true, fmt.Sprintf("ompdiff-probe-%d", ompApprDiffSeq))
		_, through := z.veto("write", `{"path":"a.txt","content":"x"}`, "Approve")
		canAnswer = through
		z.h.stop()
	})
	if !canAnswer {
		t.Fatalf("fakeHost failed to answer ui.select (cannot verify prior-answer / cached grant cases)")
	}

	var meta struct {
		Mounted []string `json:"mounted"`
	}
	if raw, err := os.ReadFile(filepath.Join(filepath.Dir(corpus), "meta.json")); err == nil {
		_ = json.Unmarshal(raw, &meta)
	}

	var outW *bufio.Writer
	if outPath != "" {
		of, err := os.Create(outPath)
		if err != nil {
			t.Fatal(err)
		}
		defer of.Close()
		outW = bufio.NewWriter(of)
		defer outW.Flush()
	}

	type key struct{ section, class string }
	counts := map[key]int{}
	exampleLines := map[key][]string{}
	sectionOrder := []string{}
	sectionSeen := map[string]bool{}
	classSeen := map[string]bool{}
	bad := 0
	mountedChecked := false

	for gi, k := range order {
		cases := groups[k]
		c0 := cases[0]
		t.Run(fmt.Sprintf("g%d", gi), func(t *testing.T) {
			ompApprDiffSeq++
			sid := fmt.Sprintf("ompdiff-%d", ompApprDiffSeq)
			sub := c0.Session == "subagent"
			if sub {
				// What the task executor does for a child (internal/agents/executor.go: RegisterChildSession).
				agents.DefaultRuntime().Registry.RegisterChildSession(sid, agents.ChildSessionMeta{ParentSessionID: sid + "-parent", Depth: 1})
				defer agents.DefaultRuntime().Registry.UnregisterChildSession(sid)
			}
			// A PiG child session has no UI and mode "print" (coding/cli/child_sessions.go).
			ui := c0.UI == "interactive" && !sub
			var mounted []string
			z := ompApprDiffStart(t, c0.Settings, ui, sid)
			defer z.h.stop()
			if !mountedChecked {
				mountedChecked = true
				ext := Extension()
				ext.RegisterCommand("ompdiff-mounted", sdk.CommandOptions{Description: "x", Handler: func(ctx sdk.Context, _ string) error {
					mounted = toolcmd.CommandNames(ctx)
					return nil
				}})
				h2 := startFakeHost(t, ext, fakeHostOptions{SessionID: sid + "-m", Mode: "tui", HasUI: true})
				h2.emitEvent(sdk.EventSessionStart, nil)
				_ = h2.emitBeforeAgentStart()
				_ = h2.callCommand("ompdiff-mounted", "")
				h2.stop()
				special := map[string]bool{"resolve": true, "reject": true, "propose": true}
				var got []string
				for _, m := range mounted {
					if !special[m] {
						got = append(got, m)
					}
				}
				sort.Strings(got)
				want := append([]string(nil), meta.Mounted...)
				sort.Strings(want)
				if len(want) > 0 && strings.Join(got, ",") != strings.Join(want, ",") {
					say("NOTE: the port mounts %v as bash commands; the corpus was built for %v (rebuild with OMP_APPROVAL_MOUNTED=%s): bash tool-command lines may differ for that reason only.", got, want, strings.Join(got, ","))
				}
			}
			if c0.State != nil && c0.State.NilSetting {
				prevS, prevSS := approval.Setting, approval.SessionSetting
				approval.Setting, approval.SessionSetting = nil, nil
				defer func() { approval.Setting, approval.SessionSetting = prevS, prevSS }()
			}
			for _, c := range cases {
				if !sectionSeen[c.Section] {
					sectionSeen[c.Section] = true
					sectionOrder = append(sectionOrder, c.Section)
				}
				var class string
				var port ompApprDiffVerdict
				target := c.Omp
				switch {
				case c.Compare != nil && !*c.Compare:
					port, _ = z.veto(c.Tool, c.Args)
					class = "not compared: " + c.Why
				default:
					if c.State != nil && c.State.Prior != nil {
						p := c.State.Prior
						var labels []string
						switch p.Answer {
						case "session":
							labels = []string{"Approve " + p.Tool + " for this session"}
						case "once":
							labels = []string{"Approve Once", "Approve"}
						default: // "target", "folder": omp's own label for that grant
							if p.Choice != "" {
								labels = []string{p.Choice}
							}
						}
						z.veto(p.Tool, p.Args, labels...)
					}
					port, _ = z.veto(c.Tool, c.Args)
					if c.Expect != "" && c.ExpectPort != nil {
						target = *c.ExpectPort
						if rel := ompApprDiffClass(target, port); rel == "equal" {
							class = "expected " + c.Expect + ": as expected"
						} else {
							class = "expected " + c.Expect + ": NOT met (" + rel + ")"
							bad++
						}
					} else {
						class = ompApprDiffClass(target, port)
						if class != "equal" {
							bad++
						}
					}
				}
				kk := key{c.Section, class}
				counts[kk]++
				classSeen[class] = true
				if class != "equal" && !strings.HasSuffix(class, ": as expected") && len(exampleLines[kk]) < examples {
					st := ""
					if c.State != nil && c.State.Prior != nil {
						st = fmt.Sprintf(" after %s %s answered %q", c.State.Prior.Tool, c.State.Prior.Args, c.State.Prior.Answer)
					}
					if c.State != nil && c.State.NilSetting {
						st = " with approval.Setting = nil"
					}
					sj, _ := json.Marshal(c.Settings)
					who := "omp "
					if c.Expect != "" && c.ExpectPort != nil {
						who = "want"
					}
					exampleLines[kk] = append(exampleLines[kk], fmt.Sprintf("    #%d %s %s  [%s, %s]%s\n      settings %s\n      %s: %s\n      port: %s",
						c.ID, c.Tool, c.Args, c.UI, c.Session, st, sj, who, ompApprDiffShort(target), ompApprDiffShort(port)))
				}
				if outW != nil {
					b, _ := json.Marshal(map[string]any{"id": c.ID, "section": c.Section, "class": class, "port": port})
					outW.Write(b)
					outW.WriteByte('\n')
				}
			}
		})
	}

	classes := make([]string, 0, len(classSeen))
	for c := range classSeen {
		classes = append(classes, c)
	}
	sort.Slice(classes, func(i, j int) bool {
		rank := func(c string) int {
			switch {
			case c == "equal":
				return 0
			case strings.HasSuffix(c, ": as expected"):
				return 1
			case strings.HasPrefix(c, "PORT ALLOWS"):
				return 2
			case strings.Contains(c, "NOT met"):
				return 3
			case strings.HasPrefix(c, "not "):
				return 9
			}
			return 5
		}
		if rank(classes[i]) != rank(classes[j]) {
			return rank(classes[i]) < rank(classes[j])
		}
		return classes[i] < classes[j]
	})
	say("== all sections")
	for _, c := range classes {
		n := 0
		for _, s := range sectionOrder {
			n += counts[key{s, c}]
		}
		say("%7d  %s", n, c)
	}
	for _, s := range sectionOrder {
		n := 0
		for _, c := range classes {
			n += counts[key{s, c}]
		}
		say("== section %s: %d cases", s, n)
		for _, c := range classes {
			if counts[key{s, c}] > 0 {
				say("%7d  %s", counts[key{s, c}], c)
			}
		}
		for _, c := range classes {
			for _, ex := range exampleLines[key{s, c}] {
				say("  %s", c)
				say("%s", ex)
			}
		}
	}
	say("cases not equal and not as expected: %d", bad)
	t.Log("\n" + report.String())
	if reportPath != "" {
		if err := os.WriteFile(reportPath, []byte(report.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if failOnBad && bad > 0 {
		t.Fatalf("%d cases differ from omp (or from the expected port answer)", bad)
	}
}

// TestOmpApprovalDifferentialReduced runs by default against the checked-in
// deterministic subset (`testdata/approval_diff_reduced.jsonl`, derived from
// omp v19.1.1 corpus sha256 `b64b61eb9a439973`) covering every section, every
// tool, and every expectation class (`port-stricter`,
// `port-ignores-untrusted-project`, `fail-closed`), failing on any mismatch.
func TestOmpApprovalDifferentialReduced(t *testing.T) {
	corpus := filepath.Join("testdata", "approval_diff_reduced.jsonl")
	runOmpApprovalDifferential(t, corpus, true, "", "", "", 5)
}

// TestZZOmpApprovalDifferential runs against an external full corpus when
// OMP_APPROVAL_DIFF_CORPUS is set; skipped otherwise.
func TestZZOmpApprovalDifferential(t *testing.T) {
	corpus := os.Getenv("OMP_APPROVAL_DIFF_CORPUS")
	if corpus == "" {
		t.Skip("OMP_APPROVAL_DIFF_CORPUS not set")
	}
	examples := 3
	if n, err := strconv.Atoi(os.Getenv("OMP_APPROVAL_DIFF_EXAMPLES")); err == nil {
		examples = n
	}
	runOmpApprovalDifferential(
		t,
		corpus,
		os.Getenv("OMP_APPROVAL_DIFF_FAIL") == "1",
		os.Getenv("OMP_APPROVAL_DIFF_REPORT"),
		os.Getenv("OMP_APPROVAL_DIFF_OUT"),
		os.Getenv("OMP_APPROVAL_DIFF_SECTIONS"),
		examples,
	)
}

var ompApprDiffJobRE = regexp.MustCompile(`job (bg_\d+)`)

func runOmpApprovalUnwrapRoute(t *testing.T, failOnViolation bool, reportPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sleep job test")
	}
	shapes := []string{
		"%s", "[%s]", "[%s#1A2B]", "[%s#1a2b]", "[%s#x]", "[%s#12345]",
		"[%s#]", "[%s#zzzz]", "[%s#1A2B#x]", "[%s##1A2B]", " [%s#x]",
		"[%s#x] ", "[%s #x]", "[ %s#x]", "[%s#x]]", "[[%s#x]", "[[%s#1A2B]#x]",
	}
	// bash runs unprompted here so that the job can be started without answering a dialog.
	s := ompApprDiffSettings{Operator: map[string]any{"tools.approvalMode": "write", "tools.approval": map[string]any{"bash": "allow"}}}
	ompApprDiffSeq++
	z := ompApprDiffStart(t, s, true, fmt.Sprintf("ompdiff-route-%d", ompApprDiffSeq))
	defer z.h.stop()
	defer z.h.emitEvent(sdk.EventSessionShutdown, nil) // reaps the jobs still running
	var report strings.Builder
	violations := 0
	job := ""
	for i, shape := range shapes {
		if job == "" { // one job serves every shape until a shape kills it
			out, err := z.h.callTool("bash", fmt.Sprintf("route-b%d", i), map[string]any{"i": "x", "command": "sleep 41", "async": true})
			m := ompApprDiffJobRE.FindStringSubmatch(string(out))
			if err != nil || m == nil {
				t.Fatalf("could not start a background job: err=%v out=%.200s", err, string(out))
			}
			job = m[1]
			time.Sleep(100 * time.Millisecond)
		}
		path := fmt.Sprintf(shape, "proc://"+job+"/kill")
		gate, _ := z.veto("write", string(mustJSONForOmpApprDiff(map[string]any{"path": path, "content": ""})))
		z.mu.Lock()
		z.asked, z.want = false, nil
		z.mu.Unlock()
		res, callErr := z.h.callTool("write", fmt.Sprintf("route-w%d", i), map[string]any{"i": "x", "path": path, "content": ""})
		z.mu.Lock()
		prompted := z.asked
		z.mu.Unlock()
		time.Sleep(150 * time.Millisecond)
		st, _ := z.h.callTool("read", fmt.Sprintf("route-r%d", i), map[string]any{"i": "x", "path": "proc://" + job})
		running := strings.Contains(string(st), "running")
		verdict := "ok"
		if !running && !prompted {
			verdict = "VIOLATION: job killed without a prompt"
			violations++
		}
		resText := string(res)
		if callErr != nil {
			resText = "error: " + callErr.Error()
		}
		if len(resText) > 110 {
			resText = resText[:110] + "…"
		}
		fmt.Fprintf(&report, "%-44q gate=%-6s prompted=%-5v job running after=%-5v %s\n      tool: %s\n", strings.ReplaceAll(path, job, "bg_N"), gate.Decision, prompted, running, verdict, strings.ReplaceAll(resText, "\n", "\\n"))
		if !running {
			job = ""
		}
	}
	fmt.Fprintf(&report, "unwrap route: %d shapes, %d violations\n", len(shapes), violations)
	t.Log("\n" + report.String())
	if reportPath != "" {
		_ = os.WriteFile(reportPath+".route", []byte(report.String()), 0o644)
	}
	if failOnViolation && violations > 0 {
		t.Fatalf("%d header shapes kill a job without a prompt", violations)
	}
}

// TestOmpApprovalUnwrapRoute runs by default and verifies all 17 bracketed
// `proc://<job>/kill` header shapes end-to-end against `write` under
// `tools.approvalMode: write` with all prompts dismissed.
func TestOmpApprovalUnwrapRoute(t *testing.T) {
	runOmpApprovalUnwrapRoute(t, true, "")
}

// TestZZOmpApprovalUnwrapRoute runs when OMP_APPROVAL_DIFF_CORPUS is set.
func TestZZOmpApprovalUnwrapRoute(t *testing.T) {
	if os.Getenv("OMP_APPROVAL_DIFF_CORPUS") == "" {
		t.Skip("OMP_APPROVAL_DIFF_CORPUS not set")
	}
	runOmpApprovalUnwrapRoute(t, os.Getenv("OMP_APPROVAL_DIFF_FAIL") == "1", os.Getenv("OMP_APPROVAL_DIFF_REPORT"))
}

func mustJSONForOmpApprDiff(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
