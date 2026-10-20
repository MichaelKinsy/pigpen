package pi_permission_system

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// The differential for the path layer: testdata/paths-oracle.json holds what the original's own functions returned for a corpus
// of inputs (port/probe/paths-probe.test.ts, recorded by port/record-paths-probe.sh), and these tests replay the corpus through
// the Go port. A difference is a failure unless it is listed in testdata/paths-differences.json with its reason.

type pathsOracle struct {
	raw map[string]json.RawMessage
}

func loadPathsOracle(t *testing.T) *pathsOracle {
	t.Helper()
	data, err := os.ReadFile("testdata/paths-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	o := &pathsOracle{raw: map[string]json.RawMessage{}}
	if err := json.Unmarshal(data, &o.raw); err != nil {
		t.Fatal(err)
	}
	return o
}

// mockProcess gives the port the working directory and environment the probe ran with: relative inputs resolve against them.
func (o *pathsOracle) mockProcess(t *testing.T) {
	t.Helper()
	var cwd string
	o.get(t, "cwd", &cwd)
	oldCwd, oldEnv := processCwd, processEnv
	processCwd = func() string { return cwd }
	processEnv = func(string) string { return "" }
	t.Cleanup(func() { processCwd, processEnv = oldCwd, oldEnv })
}

func (o *pathsOracle) get(t *testing.T, key string, into any) {
	t.Helper()
	raw, ok := o.raw[key]
	if !ok {
		t.Fatalf("no %q in the oracle record", key)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("%s: %v", key, err)
	}
}

func loadJSONFixture[T any](t *testing.T, name string) T {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func replaceAll(s, old, repl string) string { return strings.ReplaceAll(s, old, repl) }

// nativeParity is testdata/native-target.json: Pi's own resolveReadPath and resolveToCwd on a real filesystem layout.
type nativeParity struct {
	Files       []string           `json:"files"`
	Spellings   []string           `json:"spellings"`
	FsDependent map[string]bool    `json:"fsDependent"`
	Read        map[string]*string `json:"read"`
	Resolve     map[string]*string `json:"resolve"`
}

// differ collects the differences of one corpus.
type differ struct {
	t     *testing.T
	label string
	n     int
	bad   int
	known map[string]string
}

func newDiffer(t *testing.T, label string) *differ {
	t.Helper()
	known := map[string]string{}
	if data, err := os.ReadFile("testdata/paths-differences.json"); err == nil {
		var all map[string]map[string]string
		if err := json.Unmarshal(data, &all); err != nil {
			t.Fatal(err)
		}
		known = all[label]
	}
	return &differ{t: t, label: label, known: known}
}

// eq compares got to want; key names the case so a documented difference can be listed.
func (d *differ) eq(key string, got, want any) {
	d.n++
	if reflect.DeepEqual(got, want) {
		return
	}
	if _, ok := d.known[key]; ok {
		return
	}
	d.bad++
	if d.bad <= 15 {
		d.t.Errorf("%s: %s\n  go:     %#v\n  oracle: %#v", d.label, key, got, want)
	}
}

func (d *differ) done() {
	d.t.Helper()
	if d.n == 0 {
		d.t.Errorf("%s: the corpus is empty", d.label)
	}
	if d.bad > 15 {
		d.t.Errorf("%s: %d more differences", d.label, d.bad-15)
	}
	d.t.Logf("%s: %d cases, %d differences", d.label, d.n, d.bad)
}

func flavorOf(name string) PathFlavor {
	if name == "win32" {
		return Win32PathFlavor
	}
	return PosixPathFlavor
}

func nilIfEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func TestPathsOracleNodePath(t *testing.T) {
	o := loadPathsOracle(t)
	var paths []string
	o.get(t, "paths", &paths)
	o.mockProcess(t)
	type rec struct {
		Normalize     []string `json:"normalize"`
		IsAbsolute    []bool   `json:"isAbsolute"`
		Dirname       []string `json:"dirname"`
		Basename      []string `json:"basename"`
		Root          []string `json:"root"`
		JoinPairs     []string `json:"joinPairs"`
		RelativePairs []string `json:"relativePairs"`
		ResolvePairs  []string `json:"resolvePairs"`
		AbsoluteBases []string `json:"absoluteBases"`
	}
	var all map[string]rec
	o.get(t, "nodepath", &all)
	for _, name := range []string{"posix", "win32"} {
		r, impl := all[name], map[string]*PlatformPath{"posix": PosixPath, "win32": Win32Path}[name]
		d := newDiffer(t, "nodepath."+name)
		for i, p := range paths {
			d.eq(fmt.Sprintf("normalize(%q)", p), impl.Normalize(p), r.Normalize[i])
			d.eq(fmt.Sprintf("isAbsolute(%q)", p), impl.IsAbsolute(p), r.IsAbsolute[i])
			d.eq(fmt.Sprintf("dirname(%q)", p), impl.Dirname(p), r.Dirname[i])
			d.eq(fmt.Sprintf("basename(%q)", p), impl.Basename(p), r.Basename[i])
			d.eq(fmt.Sprintf("root(%q)", p), impl.Root(p), r.Root[i])
		}
		k := 0
		for _, a := range paths {
			for _, b := range paths {
				d.eq(fmt.Sprintf("join(%q,%q)", a, b), impl.Join(a, b), r.JoinPairs[k])
				k++
			}
		}
		k = 0
		for _, a := range r.AbsoluteBases {
			for _, b := range r.AbsoluteBases {
				d.eq(fmt.Sprintf("relative(%q,%q)", a, b), impl.Relative(a, b), r.RelativePairs[k])
				k++
			}
		}
		k = 0
		for _, a := range r.AbsoluteBases {
			for _, b := range paths {
				d.eq(fmt.Sprintf("resolve(%q,%q)", a, b), impl.Resolve(a, b), r.ResolvePairs[k])
				k++
			}
		}
		d.done()
	}
}

func TestPathsOracleLowerCase(t *testing.T) {
	o := loadPathsOracle(t)
	var lowers [][]any
	o.get(t, "lowers", &lowers)
	d := newDiffer(t, "toLowerCase")
	changed := map[rune]bool{}
	for _, l := range lowers {
		c := rune(l[0].(float64))
		var want []rune
		for _, x := range l[1].([]any) {
			want = append(want, rune(x.(float64)))
		}
		changed[c] = true
		d.eq(fmt.Sprintf("U+%04X", c), jsToLowerCase(string(c)), string(want))
	}
	for c := rune(0); c <= 0x10ffff; c++ {
		if (c >= 0xd800 && c <= 0xdfff) || changed[c] {
			continue
		}
		d.eq(fmt.Sprintf("U+%04X unchanged", c), jsToLowerCase(string(c)), string(c))
	}
	var sigma []struct{ In, Out string }
	o.get(t, "sigma", &sigma)
	for _, s := range sigma {
		d.eq(fmt.Sprintf("%q", s.In), jsToLowerCase(s.In), s.Out)
	}
	d.done()
}

type optString struct {
	V      *string `json:"v"`
	Throws bool    `json:"throws"`
}

func TestPathsOracleFileURL(t *testing.T) {
	o := loadPathsOracle(t)
	var recs []struct {
		In    string    `json:"in"`
		Posix optString `json:"posix"`
		Win32 optString `json:"win32"`
	}
	o.get(t, "fileUrlResults", &recs)
	d := newDiffer(t, "fileUrl")
	for _, r := range recs {
		for _, c := range []struct {
			name string
			f    PathFlavor
			want optString
		}{{"posix", PosixPathFlavor, r.Posix}, {"win32", Win32PathFlavor, r.Win32}} {
			got, err := c.f.FileURLToPath(r.In)
			var gotRec optString
			if err != nil {
				gotRec.Throws = true
			} else {
				gotRec.V = &got
			}
			d.eq(fmt.Sprintf("%s %q", c.name, r.In), gotRec, c.want)
		}
	}
	d.done()
}

func TestPathsOracleFlavor(t *testing.T) {
	o := loadPathsOracle(t)
	var paths []string
	o.get(t, "paths", &paths)
	o.mockProcess(t)

	var tool []struct{ In, Flavor, Pi string }
	o.get(t, "toolShell", &tool)
	d := newDiffer(t, "toolShellPath")
	for _, r := range tool {
		d.eq(fmt.Sprintf("%q", r.In), Win32PathFlavor.ToolShellPath(r.In), r.Pi)
		d.eq(fmt.Sprintf("%q posix", r.In), PosixPathFlavor.ToolShellPath(r.In), r.In)
	}
	d.done()

	type shape struct {
		Kind        string `json:"kind"`
		WindowsPath string `json:"windowsPath"`
	}
	var shapes []struct {
		In    string
		Win32 shape
		Posix shape
	}
	o.get(t, "bashTokenShape", &shapes)
	d = newDiffer(t, "bashTokenShape")
	for _, r := range shapes {
		w, p := Win32PathFlavor.BashTokenShape(r.In), PosixPathFlavor.BashTokenShape(r.In)
		d.eq(fmt.Sprintf("win32 %q", r.In), shape{string(w.Kind), w.WindowsPath}, r.Win32)
		d.eq(fmt.Sprintf("posix %q", r.In), shape{string(p.Kind), p.WindowsPath}, r.Posix)
	}
	d.done()

	var seps []struct {
		In           string
		Posix, Win32 int
	}
	o.get(t, "separators", &seps)
	d = newDiffer(t, "lastSeparatorIndex")
	for _, r := range seps {
		// The original counts UTF-16 units, the port bytes: compare through the prefix, which is what callers slice.
		unit := func(s string, byteIdx int) int {
			if byteIdx < 0 {
				return -1
			}
			return jsLen(s[:byteIdx])
		}
		d.eq(fmt.Sprintf("posix %q", r.In), unit(r.In, PosixPathFlavor.LastSeparatorIndex(r.In)), r.Posix)
		d.eq(fmt.Sprintf("win32 %q", r.In), unit(r.In, Win32PathFlavor.LastSeparatorIndex(r.In)), r.Win32)
	}
	d.done()

	var pairs map[string]struct {
		Fold            []string `json:"fold"`
		WithinPairs     []bool   `json:"withinPairs"`
		OutsidePairs    []bool   `json:"outsidePairs"`
		ComparablePairs []string `json:"comparablePairs"`
	}
	o.get(t, "flavorPairs", &pairs)
	bases := []string{"/base", `C:\Base`, "/", `C:\`, `\\srv\sh\d`}
	for _, name := range []string{"posix", "win32"} {
		fl, r := flavorOf(name), pairs[name]
		d = newDiffer(t, "flavor."+name)
		k := 0
		for i, a := range paths {
			d.eq(fmt.Sprintf("fold(%q)", a), fl.Fold(a), r.Fold[i])
			for _, b := range paths {
				d.eq(fmt.Sprintf("isWithin(%q,%q)", a, b), fl.IsWithin(a, b), r.WithinPairs[k])
				d.eq(fmt.Sprintf("outside(%q,%q)", a, b), IsPathOutsideWorkingDirectory(a, b, fl), r.OutsidePairs[k])
				k++
			}
		}
		k = 0
		for _, a := range paths {
			for _, b := range bases {
				d.eq(fmt.Sprintf("comparable(%q,%q)", a, b), fl.Comparable(a, b), r.ComparablePairs[k])
				k++
			}
		}
		d.done()
	}

	var safe []bool
	o.get(t, "safe", &safe)
	d = newDiffer(t, "isSafeSystemPath")
	for i, p := range paths {
		d.eq(fmt.Sprintf("%q", p), IsSafeSystemPath(p), safe[i])
	}
	d.done()

	var approval map[string][][2][]string
	o.get(t, "approval", &approval)
	for _, name := range []string{"posix", "win32"} {
		d = newDiffer(t, "approval."+name)
		for i, p := range paths {
			d.eq(fmt.Sprintf("%q file", p), DeriveApprovalPatterns(p, flavorOf(name), false), approval[name][i][0])
			d.eq(fmt.Sprintf("%q dir", p), DeriveApprovalPatterns(p, flavorOf(name), true), approval[name][i][1])
		}
		d.done()
	}
}

type accessShown struct {
	Value    string   `json:"value"`
	Match    []string `json:"match"`
	Boundary string   `json:"boundary"`
	Alias    *string  `json:"alias"`
}

func showAccess(a *AccessPath) accessShown {
	s := accessShown{Value: a.Value(), Match: a.MatchValues(), Boundary: a.BoundaryValue()}
	if alias, ok := a.ResolvedAlias(); ok {
		s.Alias = &alias
	}
	return s
}

func TestPathsOracleNormalization(t *testing.T) {
	o := loadPathsOracle(t)
	var paths []string
	o.get(t, "paths", &paths)
	var home string
	o.get(t, "home", &home)
	mockHome(t, home)
	o.mockProcess(t)
	// The probe ran on a filesystem where none of these paths is a symlink; the port's own symlink resolution is covered by the
	// twins and by the real-filesystem tests, so the corpus replays with the identity.
	mockRealpath(t, identityRealpath)
	type rec struct {
		Cwd, Sub      string
		Literal       []string
		ForComparison []string         `json:"forComparison"`
		Canonical     []string         `json:"canonical"`
		PolicyValues  [][4][]string    `json:"policyValues"`
		CwdRelative   [][]string       `json:"cwdRelative"`
		AccessPath    [][2]accessShown `json:"accessPath"`
		BashToken     []struct {
			accessShown
			Cd       struct{ Kind, Value string }
			Approval []string
		} `json:"bashToken"`
	}
	var all map[string]rec
	o.get(t, "norm", &all)
	for _, name := range []string{"posix", "win32"} {
		fl, r := flavorOf(name), all[name]
		d := newDiffer(t, "normalization."+name)
		cwd, sub := r.Cwd, r.Sub
		n := NewPathNormalizer(fl, cwd)
		for i, p := range paths {
			k := fmt.Sprintf("%q", p)
			d.eq("literal "+k, NormalizePathPolicyLiteral(p), r.Literal[i])
			d.eq("forComparison "+k, NormalizePathForComparison(p, cwd, fl), r.ForComparison[i])
			d.eq("canonical "+k, CanonicalNormalizePathForComparison(p, cwd, fl), r.Canonical[i])
			pv := r.PolicyValues[i]
			for j, opts := range []PathPolicyValueOptions{{}, {Cwd: &cwd}, {Cwd: &cwd, ResolveBase: &sub}, {ResolveBase: &sub}} {
				d.eq(fmt.Sprintf("policyValues[%d] %s", j, k), GetPathPolicyValues(p, opts, fl), nilIfEmpty(pv[j]))
			}
			d.eq("cwdRelative "+k, CwdRelativePolicyValues(NormalizePathForComparison(p, cwd, fl), cwd, fl), nilIfEmpty(r.CwdRelative[i]))
			ap := showAccess(AccessPathForPath(p, AccessPathForPathOptions{Cwd: cwd, Flavor: fl}))
			ap2 := showAccess(AccessPathForPath(p, AccessPathForPathOptions{Cwd: cwd, ResolveBase: &sub, Flavor: fl}))
			for _, c := range [][2]accessShown{{ap, r.AccessPath[i][0]}, {ap2, r.AccessPath[i][1]}} {
				c[1].Match = nilIfEmpty(c[1].Match)
				d.eq("accessPath "+k, c[0], c[1])
			}
			b := n.ForBashToken(p, nil)
			want := r.BashToken[i]
			got := showAccess(b)
			want.Match = nilIfEmpty(want.Match)
			d.eq("bashToken "+k, got, want.accessShown)
			cd := n.InterpretBashCdTarget(p)
			d.eq("cd "+k, [2]string{cd.Kind, cd.Value}, [2]string{want.Cd.Kind, want.Cd.Value})
		}
		d.done()
	}
}

func TestPathsOracleNativeTargets(t *testing.T) {
	o := loadPathsOracle(t)
	var home string
	o.get(t, "home", &home)
	mockHome(t, home)
	o.mockProcess(t)
	// The probe ran on a filesystem where none of these paths is a symlink; the port's own symlink resolution is covered by the
	// twins and by the real-filesystem tests, so the corpus replays with the identity.
	mockRealpath(t, identityRealpath)
	var natives []struct {
		Flavor           string
		Cwd, Raw         string
		Existing         []string
		ReadFallbacks    bool
		Target           string
		Rewritten        bool
		RelativeSpelling *string
	}
	o.get(t, "natives", &natives)
	d := newDiffer(t, "nativeTarget")
	for _, c := range natives {
		set := map[string]bool{}
		for _, e := range c.Existing {
			set[e] = true
		}
		got := ResolveNativeToolTarget(c.Raw, NativeToolTargetOptions{Cwd: c.Cwd, Flavor: flavorOf(c.Flavor), ReadFallbacks: c.ReadFallbacks, Exists: func(p string) bool { return set[p] }})
		d.eq(fmt.Sprintf("%s %q existing=%q fallbacks=%v", c.Flavor, c.Raw, c.Existing, c.ReadFallbacks),
			[3]any{got.Target, got.Rewritten, derefOr(got.RelativeSpelling, "\x00absent")},
			[3]any{c.Target, c.Rewritten, derefOr(c.RelativeSpelling, "\x00absent")})
	}
	d.done()
}

func TestPathsOracleInfrastructureRead(t *testing.T) {
	o := loadPathsOracle(t)
	var home string
	o.get(t, "home", &home)
	mockHome(t, home)
	o.mockProcess(t)
	// The probe ran on a filesystem where none of these paths is a symlink; the port's own symlink resolution is covered by the
	// twins and by the real-filesystem tests, so the corpus replays with the identity.
	mockRealpath(t, identityRealpath)
	var infra []struct {
		Flavor, Cwd, Tool, Path string
		Excluded, Dirs          []string
		R                       bool
	}
	o.get(t, "infra", &infra)
	d := newDiffer(t, "infrastructureRead")
	for _, c := range infra {
		d.eq(fmt.Sprintf("%s %s %q excluded=%q", c.Flavor, c.Tool, c.Path, c.Excluded),
			IsPiInfrastructureRead(c.Tool, c.Path, c.Dirs, c.Cwd, flavorOf(c.Flavor), c.Excluded), c.R)
	}
	var eh []struct{ In, Out string }
	o.get(t, "expandHome", &eh)
	for _, c := range eh {
		d.eq(fmt.Sprintf("expandHome %q", c.In), expandHomePath(c.In), c.Out)
	}
	d.done()
}

func TestPathsOracleSurfacesAndKinds(t *testing.T) {
	o := loadPathsOracle(t)
	var toolNames []string
	o.get(t, "toolNames", &toolNames)
	var kinds []string
	o.get(t, "toolKinds", &kinds)
	d := newDiffer(t, "toolKind")
	for i, n := range toolNames {
		d.eq(fmt.Sprintf("%q", n), string(ClassifyToolKind(n)), kinds[i])
	}
	var mcpChecks []bool
	o.get(t, "isMcpCheck", &mcpChecks)
	k := 0
	for _, n := range toolNames {
		for _, s := range []string{"tool", "mcp", "bash", "default"} {
			d.eq(fmt.Sprintf("isMcpCheck %q %s", n, s), IsMcpCheck(McpKindFields{n, s}), mcpChecks[k])
			k++
		}
	}
	var shell struct {
		Inputs  []json.RawMessage
		Results [][2]*struct {
			Command string
			Workdir *string
		}
	}
	o.get(t, "shell", &shell)
	aliases := ShellToolsConfig{"exec_command": {CommandArgument: "cmd", WorkdirArgument: "workdir"}, "bare": {CommandArgument: "c"}}
	k = 0
	for _, n := range toolNames {
		for _, raw := range shell.Inputs {
			in, err := parseJSON(raw)
			if err != nil {
				t.Fatal(err)
			}
			for j, a := range []ShellToolsConfig{aliases, nil} {
				got := ResolveShellInvocation(n, in, a)
				want := shell.Results[k][j]
				key := fmt.Sprintf("shell %q %s aliases=%v", n, raw, j == 0)
				if (got == nil) != (want == nil) {
					d.eq(key, got, want)
					continue
				}
				if got != nil {
					d.eq(key, [2]string{got.Command, derefOr(got.Workdir, "\x00")}, [2]string{want.Command, derefOr(want.Workdir, "\x00")})
				}
			}
			k++
		}
	}
	var surfaces []struct {
		In        string
		Family    string
		Direction *string
		Members   *[2]string
		IsPath    bool     `json:"isPath"`
		ForTool   []string `json:"forTool"`
		ForEffect []string `json:"forEffect"`
	}
	o.get(t, "surfaces", &surfaces)
	for _, s := range surfaces {
		d.eq("family "+s.In, SurfaceFamilyOf(s.In), s.Family)
		dir, ok := CapabilityDirectionOf(s.In)
		var gotDir *string
		if ok {
			ds := string(dir)
			gotDir = &ds
		}
		d.eq("direction "+s.In, gotDir, s.Direction)
		m, ok := SurfaceFamilyMembers(s.In)
		var gotM *[2]string
		if ok {
			gotM = &m
		}
		d.eq("members "+s.In, gotM, s.Members)
		d.eq("isPath "+s.In, PathSurfaces[s.In], s.IsPath)
		for i, n := range toolNames {
			d.eq(fmt.Sprintf("forTool %q %q", s.In, n), CapabilitySurfaceForTool(s.In, n), s.ForTool[i])
		}
		for i, e := range []AttributedEffect{EffectRead, EffectWrite, EffectUnproven} {
			d.eq(fmt.Sprintf("forEffect %q %s", s.In, e), CapabilitySurfaceForEffect(s.In, e), s.ForEffect[i])
		}
	}
	var effects struct {
		List   [][2]string
		Merged []struct{ Effect, Source string }
	}
	o.get(t, "effects", &effects)
	k = 0
	for _, a := range effects.List {
		for _, b := range effects.List {
			got := MergeTokenEffects(TokenEffect{Effect: AttributedEffect(a[0]), Source: EffectSource(a[1])}, TokenEffect{Effect: AttributedEffect(b[0]), Source: EffectSource(b[1])})
			d.eq(fmt.Sprintf("merge %v %v", a, b), [2]string{string(got.Effect), string(got.Source)}, [2]string{effects.Merged[k].Effect, effects.Merged[k].Source})
			k++
		}
	}
	d.done()
}

func TestPathsOracleMcpTargets(t *testing.T) {
	o := loadPathsOracle(t)
	var inputs []json.RawMessage
	o.get(t, "mcpInputs", &inputs)
	var servers [][]string
	o.get(t, "serverLists", &servers)
	var m struct {
		Targets   [][]string
		Pi        [][]string
		PiNames   []string `json:"piNames"`
		Qualified []struct {
			In  string
			Out *struct{ Server, Tool string }
		}
	}
	o.get(t, "mcp", &m)
	d := newDiffer(t, "mcpTargets")
	k := 0
	for _, raw := range inputs {
		in, err := parseJSON(raw)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range servers {
			d.eq(fmt.Sprintf("targets %s %q", raw, s), CreateMcpPermissionTargets(in, s), m.Targets[k])
			k++
		}
	}
	k = 0
	for _, n := range m.PiNames {
		for _, s := range servers {
			d.eq(fmt.Sprintf("pi %q %q", n, s), CreatePiMcpToolTargets(n, s), m.Pi[k])
			k++
		}
	}
	for _, q := range m.Qualified {
		got, ok := ParseQualifiedMcpToolName(q.In)
		var gotP *struct{ Server, Tool string }
		if ok {
			gotP = &struct{ Server, Tool string }{got.Server, got.Tool}
		}
		d.eq(fmt.Sprintf("qualified %q", q.In), gotP, q.Out)
	}
	d.done()
}

type normalizedJSON struct {
	Surface      string
	Values       []string
	ResultExtras json.RawMessage `json:"resultExtras"`
}

func TestPathsOracleNormalizeInput(t *testing.T) {
	o := loadPathsOracle(t)
	var toolNames []string
	o.get(t, "toolNames", &toolNames)
	var ni struct {
		Inputs       []json.RawMessage
		Results      []normalizedJSON
		Bash         []normalizedJSON
		BashCommands []string `json:"bashCommands"`
	}
	o.get(t, "normalizeInput", &ni)
	d := newDiffer(t, "normalizeInput")
	canon := func(raw json.RawMessage) string {
		v, err := parseJSON(raw)
		if err != nil {
			t.Fatal(err)
		}
		return marshalJSON(v, "")
	}
	k := 0
	for _, n := range toolNames {
		for _, raw := range ni.Inputs {
			in, err := parseJSON(raw)
			if err != nil {
				t.Fatal(err)
			}
			got := NormalizeInput(n, in, []string{"exa", "github", "danger-srv"})
			want := ni.Results[k]
			d.eq(fmt.Sprintf("%q %s", n, raw), [3]any{got.Surface, got.Values, marshalJSON(got.ResultExtras, "")}, [3]any{want.Surface, want.Values, canon(want.ResultExtras)})
			k++
		}
	}
	for i, c := range ni.BashCommands {
		got := NormalizeBashCommand(c, []string{"x", c})
		want := ni.Bash[i]
		d.eq(fmt.Sprintf("bash %q", c), [3]any{got.Surface, got.Values, marshalJSON(got.ResultExtras, "")}, [3]any{want.Surface, want.Values, canon(want.ResultExtras)})
	}
	d.done()
}

func TestPathsOracleIntents(t *testing.T) {
	o := loadPathsOracle(t)
	var home string
	o.get(t, "home", &home)
	mockHome(t, home)
	o.mockProcess(t)
	// The probe ran on a filesystem where none of these paths is a symlink; the port's own symlink resolution is covered by the
	// twins and by the real-filesystem tests, so the corpus replays with the identity.
	mockRealpath(t, identityRealpath)
	var intents []struct {
		Flavor, Cwd, Surface string
		Value                *string
		Agent                *string
		Kind                 string
		Input                json.RawMessage
		Values               []string
		Path                 *accessShown
		ToolPath             []accessShown `json:"toolPath"`
	}
	o.get(t, "intents", &intents)
	d := newDiffer(t, "intents")
	norms := map[string]*PathNormalizer{}
	for _, c := range intents {
		key := c.Flavor
		n := norms[key]
		if n == nil {
			n = NewPathNormalizer(flavorOf(c.Flavor), c.Cwd)
			norms[key] = n
		}
		label := fmt.Sprintf("%s %q %v %v", c.Flavor, c.Surface, derefOr(c.Value, "<undef>"), c.Agent != nil)
		got := BuildAccessIntentForSurface(c.Surface, c.Value, n, c.Agent)
		switch g := got.(type) {
		case ToolAccessIntent:
			d.eq("kind "+label, "tool", c.Kind)
			if c.Kind == "tool" {
				want, _ := parseJSON(c.Input)
				d.eq("input "+label, marshalJSON(g.Input, ""), marshalJSON(want, ""))
			}
			d.eq("agent "+label, g.AgentName, c.Agent)
		case PathValuesAccessIntent:
			d.eq("kind "+label, "path-values", c.Kind)
			d.eq("values "+label, g.Values, c.Values)
			d.eq("agent "+label, g.AgentName, c.Agent)
		case AccessPathAccessIntent:
			d.eq("kind "+label, "access-path", c.Kind)
			if c.Path != nil {
				want := *c.Path
				want.Match = nilIfEmpty(want.Match)
				d.eq("path "+label, showAccess(g.Path), want)
			}
			d.eq("agent "+label, g.AgentName, c.Agent)
		}
		for i, tool := range []string{"read", "write", "bash", "task"} {
			a := showAccess(n.ForToolPath(tool, derefOr(c.Value, "")))
			want := c.ToolPath[i]
			want.Match = nilIfEmpty(want.Match)
			d.eq(fmt.Sprintf("toolPath %s %s", tool, label), a, want)
		}
	}
	d.done()

	var resolved []struct {
		Surface string
		Vals    []string
		R       struct {
			Kind      string
			Surface   string
			Values    []string
			Input     json.RawMessage
			AgentName string `json:"agentName"`
		}
	}
	o.get(t, "resolved", &resolved)
	d = newDiffer(t, "resolvedIntent")
	for _, c := range resolved {
		got := BuildResolvedIntentFromMatchValues(c.Surface, c.Vals, "Agent")
		label := fmt.Sprintf("%q %q", c.Surface, c.Vals)
		switch g := got.(type) {
		case PathValuesAccessIntent:
			d.eq("kind "+label, "path-values", c.R.Kind)
			d.eq("values "+label, g.Values, nilIfEmpty(c.R.Values))
		case ToolAccessIntent:
			d.eq("kind "+label, "tool", c.R.Kind)
			want, _ := parseJSON(c.R.Input)
			d.eq("input "+label, marshalJSON(g.Input, ""), marshalJSON(want, ""))
		}
	}
	d.done()

	var names []string
	o.get(t, "toolNames", &names)
	var tipInputs []json.RawMessage
	o.get(t, "toolInputPathInputs", &tipInputs)
	var tip []struct {
		Path   *string
		Source string
	}
	o.get(t, "toolInputPath", &tip)
	d = newDiffer(t, "toolInputPath")
	k := 0
	for _, n := range names {
		for _, raw := range tipInputs {
			in, _ := parseJSON(raw)
			got := GetToolInputPath(n, in, nil)
			var gp *string
			if got.HasPath {
				gp = &got.Path
			}
			d.eq(fmt.Sprintf("%q %s", n, raw), [2]any{gp, string(got.Source)}, [2]any{tip[k].Path, tip[k].Source})
			k++
		}
	}
	d.done()
	_ = runtime.GOOS
}
