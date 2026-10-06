package cedar

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/hardening/policy"
	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

func mustParse(t *testing.T, text string) policy.Decider {
	t.Helper()
	d, err := Parse([]byte(text))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return d
}

func decide(t *testing.T, d policy.Decider, r policy.Request) policy.Decision {
	t.Helper()
	dec, err := policy.FailClosed(d).Decide(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return dec
}

func req(action string, args map[string]any) policy.Request {
	return policy.Request{Principal: "session-1", Action: action, Context: args}
}

const allowBash = `permit (principal, action == Action::"bash", resource);`

func TestAPermitAllows(t *testing.T) {
	dec := decide(t, mustParse(t, allowBash), req("bash", nil))
	if !dec.Allow || dec.Reason != "" || len(dec.Matched) != 1 || dec.Matched[0] != "policy0" {
		t.Fatalf("%+v", dec)
	}
}

func TestDefaultDeny(t *testing.T) {
	for name, text := range map[string]string{
		"other tool": allowBash, "empty": "", "whitespace": " \n\t ", "comment only": "// nothing here\n",
	} {
		action := "read"
		dec := decide(t, mustParse(t, text), req(action, nil))
		if dec.Allow || dec.Reason != profile.PolicyDenied {
			t.Errorf("%s: %+v", name, dec)
		}
	}
}

func TestAForbidWinsWhateverPermitsMatch(t *testing.T) {
	text := `
permit (principal, action, resource);
permit (principal, action == Action::"bash", resource);
forbid (principal, action == Action::"bash", resource) when { context.command like "*rm -rf*" };`
	d := mustParse(t, text)
	dec := decide(t, d, req("bash", map[string]any{"command": "rm -rf /"}))
	if dec.Allow || dec.Reason != profile.PolicyForbidden || len(dec.Matched) != 1 || dec.Matched[0] != "policy2" {
		t.Fatalf("%+v", dec)
	}
	if dec := decide(t, d, req("bash", map[string]any{"command": "ls"})); !dec.Allow {
		t.Fatalf("%+v", dec)
	}
}

func TestAnErroringForbidDeniesEvenThoughCedarSkipsIt(t *testing.T) {
	text := `
permit (principal, action, resource);
forbid (principal, action == Action::"bash", resource) when { context.command like "*rm -rf*" };`
	d := mustParse(t, text)
	// context.command is absent: the forbid errors, Cedar skips it, the permit matches. The Decider denies.
	dec := decide(t, d, req("bash", map[string]any{"other": "x"}))
	if dec.Allow || dec.Reason != profile.PolicyError {
		t.Fatalf("%+v", dec)
	}
	// With the attribute present the same policy allows.
	if dec := decide(t, d, req("bash", map[string]any{"command": "ls"})); !dec.Allow {
		t.Fatalf("%+v", dec)
	}
	// A request to a tool the forbid does not name never evaluates it.
	if dec := decide(t, d, req("read", nil)); !dec.Allow {
		t.Fatalf("%+v", dec)
	}
	// An erroring permit and no other: deny with policy_error, not a silent "no match".
	dec = decide(t, mustParse(t, `permit (principal, action, resource) when { context.missing == "x" };`), req("bash", nil))
	if dec.Allow || dec.Reason != profile.PolicyError {
		t.Fatalf("%+v", dec)
	}
	// The raw Decider also reports a deny (FailClosed is not what makes it one).
	raw, err := d.Decide(context.Background(), req("bash", map[string]any{"other": "x"}))
	if err != nil || raw.Allow {
		t.Fatalf("%+v %v", raw, err)
	}
}

func TestArgumentsCannotChangeThePolicy(t *testing.T) {
	d := mustParse(t, `permit (principal, action == Action::"bash", resource) when { context.command == "ls" };`)
	for _, cmd := range []string{`x" || true || "`, "ls\n", "ls ", "LS", `ls" }; permit (principal, action, resource); //`, ""} {
		if dec := decide(t, d, req("bash", map[string]any{"command": cmd})); dec.Allow {
			t.Errorf("%q allowed", cmd)
		}
	}
	if dec := decide(t, d, req("bash", map[string]any{"command": "ls"})); !dec.Allow {
		t.Fatalf("%+v", dec)
	}
}

func TestNumbersAndOptionalArguments(t *testing.T) {
	d := mustParse(t, `
permit (principal, action == Action::"a", resource) when { context.count == 3 };
permit (principal, action == Action::"b", resource) when { context.timeout == "1.5" };
permit (principal, action == Action::"c", resource) when { context.big == "1e+20" };
permit (principal, action == Action::"d", resource) when { !(context has x) };
permit (principal, action == Action::"e", resource) when { context.list.contains("x") && context.nested.deep == true };
permit (principal, action == Action::"f", resource) when { context.u == "18446744073709551615" };`)
	cases := []struct {
		action string
		args   map[string]any
		want   bool
	}{
		{"a", map[string]any{"count": 3.0}, true},
		{"a", map[string]any{"count": float64(3)}, true},
		{"a", map[string]any{"count": 3}, true},
		{"a", map[string]any{"count": int64(3)}, true},
		{"a", map[string]any{"count": uint8(3)}, true},
		{"a", map[string]any{"count": 3.5}, false}, // a string "3.5": no integer match
		{"b", map[string]any{"timeout": 1.5}, true},
		{"c", map[string]any{"big": 1e20}, true},
		{"d", map[string]any{"x": nil}, true},
		{"d", map[string]any{"x": 1}, false},
		{"e", map[string]any{"list": []any{"x", nil}, "nested": map[string]any{"deep": true}}, true},
		{"e", map[string]any{"list": []string{"x"}, "nested": map[string]any{"deep": true}}, true},
		{"e", map[string]any{"list": []any{"y"}, "nested": map[string]any{"deep": true}}, false},
		{"f", map[string]any{"u": uint64(18446744073709551615)}, true},
	}
	for i, c := range cases {
		if dec := decide(t, d, req(c.action, c.args)); dec.Allow != c.want {
			t.Errorf("case %d (%s %v): %+v", i, c.action, c.args, dec)
		}
	}
}

func TestActionGroupsAndPrincipalAttributes(t *testing.T) {
	d := mustParse(t, `
permit (principal, action in Action::"Shell", resource) when { principal.tenant == "t1" && principal.subject == "alice" };`)
	r := policy.Request{Principal: "s", Action: "bash", ActionGroups: []string{"Shell"}, Subject: "alice", Tenant: "t1"}
	if dec := decide(t, d, r); !dec.Allow {
		t.Fatalf("%+v", dec)
	}
	r.Tenant = "t2"
	if dec := decide(t, d, r); dec.Allow {
		t.Fatalf("%+v", dec)
	}
	r.Tenant = ""
	if dec := decide(t, d, r); dec.Allow || dec.Reason != profile.PolicyError {
		t.Fatalf("a missing tenant attribute: %+v", dec)
	}
	r = policy.Request{Principal: "s", Action: "bash", Subject: "alice", Tenant: "t1"} // not in the group
	if dec := decide(t, d, r); dec.Allow {
		t.Fatalf("%+v", dec)
	}
	r.ActionGroups = []string{"Shell\" ; permit"}
	if _, err := d.Decide(context.Background(), r); !profile.IsCode(err, profile.PolicyError) {
		t.Fatalf("a malformed group: %v", err)
	}
}

func TestResourceTargets(t *testing.T) {
	d := mustParse(t, `
permit (principal, action == Action::"read", resource == Target::"/etc/hosts");
permit (principal, action == Action::"fetch", resource == Target::"api.example.com");
permit (principal, action == Action::"ping", resource == Target::"none");`)
	cases := []struct {
		action, resource string
		want             bool
	}{
		{"read", "/etc/hosts", true},
		{"read", "/etc/../etc/hosts", true},
		{"read", "/etc//hosts", true},
		{"read", "/etc/./hosts", true},
		{"read", "/etc/hosts/", true},
		{"read", "/etc/hosts2", false},
		{"read", "/tmp/../etc/hosts", true},
		{"read", "/etc/hosts/../shadow", false},
		{"fetch", "https://API.example.com:8443/path?q=1", true},
		{"fetch", "https://api.example.com.evil.net/", false},
		{"fetch", "https://evil.net/api.example.com", false},
		{"ping", "", true},
		{"ping", "x", false},
		{"fetch", "://", false},
		// spellings of the same host match the same target
		{"fetch", "https://API.example.com./", true},
		{"fetch", "https://\uff41\uff50\uff49.example.com/", true}, // full-width "api"
		{"fetch", "https://api.example\u3002com/", true},           // ideographic full stop
		{"fetch", "https://api..example.com/", false},
	}
	for _, c := range cases {
		r := policy.Request{Principal: "s", Action: c.action, Resource: c.resource}
		if runtime.GOOS == "windows" && strings.HasPrefix(c.resource, "/") {
			continue
		}
		if dec := decide(t, d, r); dec.Allow != c.want {
			t.Errorf("%s %q: %+v", c.action, c.resource, dec)
		}
	}
	if got, err := Target(""); err != nil || got != "none" {
		t.Fatalf("%q %v", got, err)
	}
	if got, err := Target("https://Example.COM/x"); err != nil || got != "example.com" {
		t.Fatalf("%q %v", got, err)
	}
	if got, err := Target("http://[::ffff:10.0.0.1]:80/"); err != nil || got != "10.0.0.1" {
		t.Fatalf("%q %v", got, err)
	}
}

// A forbid on a host or a path must not be escaped by another spelling of the same target: a spelling that Target
// cannot reduce to the canonical form is denied, never judged as a different target.
func TestAForbiddenTargetCannotBeRespelled(t *testing.T) {
	d := mustParse(t, `
permit (principal, action, resource);
forbid (principal, action == Action::"fetch", resource == Target::"evil.example");
forbid (principal, action == Action::"read", resource == Target::"/etc/shadow");`)
	fetches := []string{"https://evil.example/", "https://EVIL.example./", "https://\uff45\uff56\uff49\uff4c.example/",
		"https://evil.example\u3002/", "https://evil..example/", "https://-evil.example/"}
	reads := []string{"/etc/shadow", "/etc/./shadow", "../../../../etc/shadow", "etc/shadow", "./shadow", "shadow"}
	if runtime.GOOS == "windows" {
		reads = []string{"../../../../etc/shadow", "etc/shadow", "./shadow", "shadow"}
	}
	for _, r := range []struct {
		action string
		list   []string
	}{{"fetch", fetches}, {"read", reads}} {
		for _, res := range r.list {
			if dec := decide(t, d, policy.Request{Principal: "s", Action: r.action, Resource: res}); dec.Allow {
				t.Errorf("%s %q was allowed", r.action, res)
			}
		}
	}
	if dec := decide(t, d, policy.Request{Principal: "s", Action: "fetch", Resource: "https://good.example/"}); !dec.Allow {
		t.Fatalf("an unrelated host was denied: %+v", dec)
	}
}

// A resource that looks like a URL but has no host is an error, not "none": a permit for a call with no target must
// not match it.
func TestAURLWithoutAHostIsNotNone(t *testing.T) {
	for _, resource := range []string{"://", "https:///etc/passwd", "http://", "file:///etc/passwd", "http://[::1", "ht tp://x"} {
		if got, err := Target(resource); !profile.IsCode(err, profile.PolicyError) || got != "" {
			t.Errorf("%q: %q %v", resource, got, err)
		}
	}
	d := mustParse(t, `permit (principal, action, resource == Target::"none");`)
	if dec := decide(t, d, policy.Request{Principal: "s", Action: "ping", Resource: "https:///etc/passwd"}); dec.Allow || dec.Reason != profile.PolicyError {
		t.Fatalf("%+v", dec)
	}
}

func TestPolicyFileProblemsDenyEverything(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	paths := map[string]string{
		"missing":         filepath.Join(dir, "missing.cedar"),
		"directory":       dir,
		"syntax error":    write("bad.cedar", `permit (principal, action, resource`),
		"unknown keyword": write("kw.cedar", `allow (principal, action, resource);`),
		"template":        write("tpl.cedar", `permit (principal == ?principal, action, resource);`),
		"binary":          write("bin.cedar", "\x00\x01\x02"),
		"oversize":        write("big.cedar", strings.Repeat("// pad\n", (1<<20)/7+10)),
		"truncated":       write("trunc.cedar", `permit (principal, action == Action::"bash", resource) when { context.x == `),
	}
	for name, p := range paths {
		d, err := Load(p)
		if !profile.IsCode(err, profile.PolicyUnavailable) {
			t.Errorf("%s: err = %v", name, err)
		}
		if d == nil {
			t.Fatalf("%s: a nil Decider", name)
		}
		dec, derr := d.Decide(context.Background(), req("bash", nil))
		if derr != nil || dec.Allow || dec.Reason != profile.PolicyUnavailable {
			t.Errorf("%s: %+v %v", name, dec, derr)
		}
		if strings.Contains(err.Error(), dir) || strings.Contains(err.Error(), "bad.cedar") {
			t.Errorf("%s: the error names the path: %v", name, err)
		}
	}
	d, err := Load(write("ok.cedar", allowBash))
	if err != nil || !decide(t, d, req("bash", nil)).Allow {
		t.Fatalf("a valid file: %v", err)
	}
}

func TestNoPolicyFileIsSyntaxFreeButDeniesEverything(t *testing.T) {
	d, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if dec := decide(t, d, req("bash", nil)); dec.Allow || dec.Reason != profile.PolicyDenied {
		t.Fatalf("%+v", dec)
	}
}

func TestUnmappableRequestsAreDenied(t *testing.T) {
	d := mustParse(t, `permit (principal, action, resource);`)
	type s struct{ A int }
	deep := map[string]any{}
	cur := deep
	for i := 0; i < 20; i++ {
		next := map[string]any{}
		cur["n"] = next
		cur = next
	}
	wide := make([]any, 25000)
	for i := range wide {
		wide[i] = i
	}
	bad := map[string]policy.Request{
		"no action":       {Principal: "s"},
		"huge action":     {Principal: "s", Action: strings.Repeat("a", 2000)},
		"huge principal":  {Principal: strings.Repeat("a", 2000), Action: "bash"},
		"huge resource":   {Principal: "s", Action: "bash", Resource: "/" + strings.Repeat("a", 9000)},
		"struct arg":      req("bash", map[string]any{"x": s{1}}),
		"chan arg":        req("bash", map[string]any{"x": make(chan int)}),
		"func arg":        req("bash", map[string]any{"x": func() {}}),
		"complex arg":     req("bash", map[string]any{"x": complex(1, 2)}),
		"nested struct":   req("bash", map[string]any{"x": []any{map[string]any{"y": s{}}}}),
		"int-keyed map":   req("bash", map[string]any{"x": map[int]string{1: "a"}}),
		"too deep":        req("bash", deep),
		"too many values": req("bash", map[string]any{"x": wide}),
		"huge string":     req("bash", map[string]any{"x": strings.Repeat("a", maxStrings+1)}),
	}
	for name, r := range bad {
		dec, err := d.Decide(context.Background(), r)
		if !profile.IsCode(err, profile.PolicyError) || dec.Allow {
			t.Errorf("%s: %+v %v", name, dec, err)
		}
		if dec := decide(t, d, r); dec.Allow || dec.Reason != profile.PolicyError {
			t.Errorf("%s through FailClosed: %+v", name, dec)
		}
	}
	// at the limits: fine
	ok := map[string]any{"x": strings.Repeat("a", maxStrings)}
	if dec := decide(t, d, req("bash", ok)); !dec.Allow {
		t.Fatalf("%+v", dec)
	}
}

func TestACancelledContextIsNotDecided(t *testing.T) {
	d := mustParse(t, allowBash)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := d.Decide(ctx, req("bash", nil))
	if !profile.IsCode(err, profile.Cancelled) || !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}

func TestAForbidIsReportedEvenWhenAnotherPolicyErrors(t *testing.T) {
	d := mustParse(t, `
forbid (principal, action == Action::"bash", resource);
permit (principal, action, resource) when { context.missing == "x" };`)
	dec := decide(t, d, req("bash", nil))
	if dec.Allow || dec.Reason != profile.PolicyForbidden || len(dec.Matched) != 1 || dec.Matched[0] != "policy0" {
		t.Fatalf("%+v", dec)
	}
}

func TestANilDecider(t *testing.T) {
	if _, err := (&Decider{}).Decide(context.Background(), req("bash", nil)); !profile.IsCode(err, profile.PolicyUnavailable) {
		t.Fatalf("a Decider with no policy set: %v", err)
	}
	var d *Decider
	if _, err := d.Decide(context.Background(), req("bash", nil)); !profile.IsCode(err, profile.PolicyUnavailable) {
		t.Fatal(err)
	}
	if dec := decide(t, d, req("bash", nil)); dec.Allow {
		t.Fatal("allowed")
	}
	if dec := decide(t, &Decider{}, req("bash", nil)); dec.Allow {
		t.Fatal("allowed")
	}
}

func TestConcurrentDecisions(t *testing.T) {
	d := mustParse(t, `permit (principal, action == Action::"bash", resource) when { context.n == 1 };`)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			n := 1 + i%2
			if dec := decide(t, d, req("bash", map[string]any{"n": n})); dec.Allow != (n == 1) {
				t.Errorf("n=%d: %+v", n, dec)
			}
		}(i)
	}
	wg.Wait()
}

// The plan: no run-time dependency on cedar-go's x/exp packages.
func TestNoExperimentalCedarImports(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pkgs {
		for name, f := range p.Files {
			for _, imp := range f.Imports {
				if strings.Contains(imp.Path.Value, "cedar-go/x/") {
					t.Errorf("%s imports %s", name, imp.Path.Value)
				}
			}
		}
	}
}
