package langsmith

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"
)

func TestRtParseJSON(t *testing.T) {
	valid := map[string]string{
		`[]`:                          `[]`,
		` [ 1 , "a" , null , true ] `: `[1,"a",null,true]`,
		"\t\r\n{}\n":                  `{}`,
		`{"b":1,"a":2,"b":3}`:         `{"b":3,"a":2}`,
		`{"2":"x","1":"y","z":0}`:     `{"1":"y","2":"x","z":0}`,
		`-0`:                          `0`,
		`1.5e3`:                       `1500`,
		`1E+2`:                        `100`,
		`"\u00e9\n\"\\\/"`:            `"é\n\"\\/"`,
		`1e999`:                       `null`, // Infinity stringifies as null
	}
	for in, want := range valid {
		v, err := rtParseJSON(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		b, _, _ := stringify(v)
		if string(b) != want {
			t.Errorf("%q: got %s, want %s", in, b, want)
		}
	}
	invalid := []string{
		``, ` `, `[`, `]`, `{`, `{"a"}`, `{"a":}`, `{a:1}`, `{'a':1}`, `[1,]`, `[,1]`, `{"a":1,}`, `01`, `1.`, `.5`, `+1`, `-`, `--1`, `1e`, `1e+`,
		`tru`, `nul`, `True`, `NaN`, `Infinity`, `"abc`, "\"a\nb\"", "\"a\tb\"", `"\x41"`, `"\u12"`, `"\u12g4"`, `[] x`, `[]]`, `{} {}`,
		"\ufeff[]", `[1 2]`, `{"a":1 "b":2}`, `undefined`,
	}
	for _, in := range invalid {
		if v, err := rtParseJSON(in); err == nil {
			t.Errorf("%q parsed to %v, want an error", in, v)
		}
	}
	if _, err := rtParseJSON(strings.Repeat("[", 600) + strings.Repeat("]", 600)); err == nil {
		t.Error("600 nested arrays parsed")
	}
}

func TestUUIDV5(t *testing.T) {
	// uuid.uuid5(uuid.NAMESPACE_DNS, "python.org"), the RFC 4122 example everyone checks against.
	if got := uuidV5("python.org", &uuidNamespaceDNS); got != "886313e1-3b8a-5372-9b90-0c9aee199e5d" {
		t.Errorf("uuidV5(python.org) = %s", got)
	}
	if got, want := getReplicaKey("p", nil, nil, nil), uuidV5("apiKey:|apiUrl:|projectName:p|workspaceId:", &uuidNamespaceDNS); got != want {
		t.Errorf("replica key = %s, want %s", got, want)
	}
	k, u, w := "K", "U", "W"
	if got, want := getReplicaKey("p", &u, &k, &w), uuidV5("apiKey:K|apiUrl:U|projectName:p|workspaceId:W", &uuidNamespaceDNS); got != want {
		t.Errorf("replica key = %s, want %s", got, want)
	}
}

type rtNopClient struct{}

func (rtNopClient) createRun(context.Context, *omap, *destOptions) error         { return nil }
func (rtNopClient) updateRun(context.Context, string, *omap, *destOptions) error { return nil }

// TestRunTreeGeneratedIDs: an absent id is uuid7FromTime(start_time) and the trace id and dotted order follow it.
func TestRunTreeGeneratedIDs(t *testing.T) {
	var clock int64 = 1700000000123
	var buf bytes.Buffer
	defer resetRtSeams(&clock, newOmap(), &buf)()
	t.Setenv("LANGSMITH_RUNS_ENDPOINTS", "")
	r, err := newRunTree(runTreeConfig{Name: "n", ProjectName: "p", Client: rtNopClient{}})
	if err != nil {
		t.Fatal(err)
	}
	// 48-bit millisecond prefix 0x018bcfe5687b = 1700000000123.
	re := regexp.MustCompile(`^018bcfe5-687b-7000-8000-[0-9a-f]{12}$`)
	if !re.MatchString(r.ID) {
		t.Errorf("id %s does not match %s", r.ID, re)
	}
	if r.traceID != r.ID || !strings.HasSuffix(r.dottedOrder, "Z"+r.ID) || !strings.HasPrefix(r.dottedOrder, "20231114T221320123001Z") {
		t.Errorf("trace %s dotted %s", r.traceID, r.dottedOrder)
	}
	c, err := r.createChild(runTreeConfig{Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if c.traceID != r.ID || c.executionOrder != 2 || !strings.HasPrefix(c.dottedOrder, r.dottedOrder+".20231114T221320123002Z") {
		t.Errorf("child trace %s order %d dotted %s", c.traceID, c.executionOrder, c.dottedOrder)
	}
	if c.projectName != "p" || c.client != (rtNopClient{}) {
		t.Errorf("child project %q client %v", c.projectName, c.client)
	}
}

func TestEnsureWriteReplicas(t *testing.T) {
	t.Setenv("LANGSMITH_RUNS_ENDPOINTS", `[{"api_url":"https://a/","api_key":"k"}]`)
	// explicit (even empty) beats the environment; nil reads it.
	got, err := ensureWriteReplicas([]replicaConfig{})
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("explicit empty: %v %v", got, err)
	}
	got, err = ensureWriteReplicas(nil)
	if err != nil || len(got) != 1 || *got[0].APIURL != "https://a" || *got[0].APIKey != "k" {
		t.Errorf("env: %+v %v", got, err)
	}
	yes := true
	_, err = ensureWriteReplicas([]replicaConfig{{Primary: &yes}, {Primary: &yes}})
	if err == nil || err.Error() != "Only one replica can be marked as primary." {
		t.Errorf("two primaries: %v", err)
	}
}

func TestStripReroot(t *testing.T) {
	yes := true
	in := []replicaConfig{{Reroot: &yes}, {}}
	out := stripReroot(in)
	if out[0].Reroot != nil || in[0].Reroot == nil {
		t.Errorf("reroot: out %v in %v", out[0].Reroot, in[0].Reroot)
	}
	plain := []replicaConfig{{}}
	if &stripReroot(plain)[0] != &plain[0] {
		t.Error("a list without reroot is shared, not copied")
	}
	if stripReroot(nil) != nil {
		t.Error("nil stays nil")
	}
}

func BenchmarkRunTreeLifecycle(b *testing.B) {
	var clock int64 = 1700000000000
	var buf bytes.Buffer
	defer resetRtSeams(&clock, obj("library", "langsmith", "runtime", "go", "sdk", "x", "sdk_version", "1"), &buf)()
	ctx := context.Background()
	meta := obj("a", 1, "b", "two")
	cl := rtNopClient{}
	b.ReportAllocs()
	for b.Loop() {
		root, _ := newRunTree(runTreeConfig{Name: "root", ProjectName: "p", Client: cl, Replicas: []replicaConfig{}, Metadata: meta, Inputs: obj("prompt", "x")})
		child, _ := root.createChild(runTreeConfig{Name: "child", RunType: "llm", Inputs: obj("x", 1)})
		child.postRun(ctx)
		child.end(obj("y", 2), nil, nil)
		_ = child.patchRun(ctx)
		root.end(obj("z", 3), nil, nil)
		_ = root.patchRun(ctx)
	}
}

func BenchmarkRunTreeReplicaPayload(b *testing.B) {
	var clock int64 = 1700000000000
	var buf bytes.Buffer
	defer resetRtSeams(&clock, obj("library", "langsmith"), &buf)()
	ctx := context.Background()
	name, url, key := "other", "https://a", "k"
	root, _ := newRunTree(runTreeConfig{Name: "root", ProjectName: "p", Client: rtNopClient{}, Replicas: []replicaConfig{{ProjectName: &name, APIURL: &url, APIKey: &key}}})
	b.ReportAllocs()
	for b.Loop() {
		root.postRun(ctx)
		_ = root.patchRun(ctx)
	}
}
