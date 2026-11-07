package lsdiff

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"lstools/internal/reqlog"
)

const (
	idRoot  = "0198a000-0000-7000-8000-000000000001"
	idTurn  = "0198a000-0000-7000-8000-000000000002"
	idLLM   = "0198a000-0000-7000-8000-000000000003"
	idRoot2 = "01ffb111-1111-7111-8111-00000000000a"
	idTurn2 = "01ffb111-1111-7111-8111-00000000000b"
	idLLM2  = "01ffb111-1111-7111-8111-00000000000c"
)

type partSpec struct{ name, body string }

// multipartEntry builds an Entry like mockls records one for a JS SDK request.
func multipartEntry(boundary string, parts []partSpec) *reqlog.Entry {
	e := &reqlog.Entry{
		Route: "multipart", Method: "POST", Path: "/runs/multipart", Transfer: "chunked", ContentLength: -1,
		Headers:  map[string]string{"content-type": "multipart/form-data; boundary=" + boundary, "x-api-key": "k", "user-agent": "node", "host": "127.0.0.1:1", "accept": "*/*"},
		Boundary: boundary,
	}
	var body bytes.Buffer
	for _, p := range parts {
		ct := fmt.Sprintf("application/json; length=%d", len(p.body))
		hdr := fmt.Sprintf("Content-Disposition: form-data; name=\"%s\"\r\nContent-Type: %s", p.name, ct)
		e.Parts = append(e.Parts, reqlog.Part{Name: p.name, ContentType: ct, Header: hdr, Bytes: []byte(p.body)})
		fmt.Fprintf(&body, "--%s\r\n%s\r\n\r\n%s\r\n", boundary, hdr, p.body)
	}
	fmt.Fprintf(&body, "--%s--\r\n", boundary)
	e.Body = body.Bytes()
	return e
}

type variant struct {
	root, turn, llm string
	boundary        string
	runtime         string // value of extra.runtime.runtime
	sdk             string
	startTime       string // JSON of start_time
	endTime         string
	prompt          string
	inputsOrder     string // llm inputs payload: "a" or "b" key order
	toolTimestamp   string
	extraKey        string // replaces "library"
	ua              string
}

func base() variant {
	return variant{
		root: idRoot, turn: idTurn, llm: idLLM, boundary: "----LangSmithFormBoundaryfxs6learuge", runtime: "node", sdk: "langsmith-js",
		startTime: `"2026-11-06T22:56:12.673Z"`, endTime: "1794005772771", prompt: "hi", inputsOrder: "a", toolTimestamp: "1794005772001",
		extraKey: "library", ua: "node",
	}
}

func (v variant) entry() *reqlog.Entry {
	do := func(id string, ts string) string { return ts + id }
	rootDO := do(idRoot, "20261106T225612673001Z")
	_ = rootDO
	var llmInputs string
	if v.inputsOrder == "a" {
		llmInputs = `{"messages":[{"role":"user","content":"hi","timestamp":` + v.toolTimestamp + `}],"model":"m","stream":true}`
	} else {
		llmInputs = `{"messages":[{"timestamp":` + v.toolTimestamp + `,"content":"hi","role":"user"}],"stream":true,"model":"m"}`
	}
	extra := fmt.Sprintf(`{"metadata":{"thread_id":"%s","cwd":"/tmp/eq1a2b3c4d/work","timestamp":1794005772002},"runtime":{"%s":"langsmith","runtime":"%s","sdk":"%s","sdk_version":"0.10.5"}}`, v.root, v.extraKey, v.runtime, v.sdk)
	parts := []partSpec{
		{"post." + v.root, fmt.Sprintf(`{"id":"%s","name":"Pi agent run","start_time":%s,"end_time":%s,"run_type":"chain","trace_id":"%s","dotted_order":"20261106T225612673001Z%s"}`, v.root, v.startTime, v.endTime, v.root, v.root)},
		{"post." + v.root + ".inputs", fmt.Sprintf(`{"prompt":"%s","imageCount":0}`, v.prompt)},
		{"post." + v.root + ".extra", extra},
		{"post." + v.llm, fmt.Sprintf(`{"id":"%s","name":"openai","start_time":%s,"run_type":"llm","parent_run_id":"%s","trace_id":"%s","dotted_order":"20261106T225612673001Z%s.20261106T225612680002Z%s"}`, v.llm, v.startTime, v.root, v.root, v.root, v.llm)},
		{"post." + v.llm + ".inputs", llmInputs},
		{"post." + v.llm + ".events", fmt.Sprintf(`[{"name":"tool_update","time":"2026-11-06T22:56:12.700Z","kwargs":{"partialResult":{"b":1,"a":2}}}]`)},
	}
	e := multipartEntry(v.boundary, parts)
	e.Headers["user-agent"] = v.ua
	return e
}

// reframe rebuilds the raw body after a test changed the parts.
func reframe(e *reqlog.Entry) *reqlog.Entry {
	var body bytes.Buffer
	for _, p := range e.Parts {
		fmt.Fprintf(&body, "--%s\r\n%s\r\n\r\n%s\r\n", e.Boundary, p.Header, p.Bytes)
	}
	fmt.Fprintf(&body, "--%s--\r\n", e.Boundary)
	e.Body = body.Bytes()
	return e
}

func logOf(es ...*reqlog.Entry) []*reqlog.Entry {
	for i, e := range es {
		e.Seq = i + 1
	}
	return es
}

func mustCompare(t *testing.T, a, b []*reqlog.Entry, o Options) *Result {
	t.Helper()
	r, err := Compare(a, b, o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestIdenticalModuloListedNormalisations(t *testing.T) {
	a := base()
	b := base()
	b.root, b.turn, b.llm = idRoot2, idTurn2, idLLM2 // N1
	b.boundary = "----LangSmithFormBoundary9k2mm"    // N3
	b.startTime = `"2026-11-06T23:10:00.001Z"`       // N2 (string)
	b.endTime = "1794009999999"                      // N2 (number)
	b.runtime, b.sdk = "go", "pigpen-langsmith-go"   // N4
	b.ua = "pigpen-langsmith-go/0.1.0"               // N4
	b.inputsOrder = "b"                              // N5
	b.toolTimestamp = "1794000000000"                // N2 inside a host object
	r := mustCompare(t, logOf(a.entry()), logOf(b.entry()), Options{})
	if !r.Same() {
		var buf bytes.Buffer
		r.Report(&buf, "A", "B")
		t.Fatal(buf.String())
	}
	for _, rule := range []string{"N1", "N2", "N3", "N4", "N5", "N6"} {
		if r.FiredA[rule] == 0 || r.FiredB[rule] == 0 {
			t.Errorf("%s did not fire in both logs: %d/%d", rule, r.FiredA[rule], r.FiredB[rule])
		}
	}
	var buf bytes.Buffer
	r.Report(&buf, "A", "B")
	if !strings.Contains(buf.String(), "IDENTICAL") || !strings.Contains(buf.String(), "normalisations fired") {
		t.Fatal(buf.String())
	}
}

// Every mutation below changes one thing that is NOT on the normalisation list; each must be reported.
func TestMutationsAreReported(t *testing.T) {
	// One changed value.
	a, b := base(), base()
	b.prompt = "ho"
	r := mustCompare(t, logOf(a.entry()), logOf(b.entry()), Options{})
	if r.Same() || r.Diffs[0].Request != 1 || !strings.Contains(r.Diffs[0].Where, `"post.<id1>.inputs" bytes`) {
		t.Fatalf("changed value: %+v", r.Diffs)
	}
	if !strings.Contains(r.Diffs[0].Note, "offset") || !strings.Contains(r.Diffs[0].A, `"hi"`) || !strings.Contains(r.Diffs[0].B, `"ho"`) {
		t.Fatalf("report does not show the bytes: %+v", r.Diffs[0])
	}

	// A key order change in an object the extension builds ({prompt, imageCount}) is a difference.
	b = base()
	be := b.entry()
	be.Parts[1].Bytes = []byte(`{"imageCount":0,"prompt":"hi"}`)
	reframe(be)
	r = mustCompare(t, logOf(a.entry()), logOf(be), Options{})
	if r.Same() || !strings.Contains(r.Diffs[0].Where, ".inputs") {
		t.Fatalf("extension-built key order not reported: %+v", r.Diffs)
	}

	// extra.runtime.library is not D1.
	b = base()
	b.extraKey = "libraryx"
	r = mustCompare(t, logOf(a.entry()), logOf(b.entry()), Options{})
	if r.Same() || !strings.Contains(r.Diffs[0].Where, ".extra") {
		t.Fatalf("runtime key change not reported: %+v", r.Diffs)
	}

	// A time written as a string where the original wrote a number keeps its type.
	b = base()
	b.endTime = `"2026-11-06T22:56:12.771Z"`
	r = mustCompare(t, logOf(a.entry()), logOf(b.entry()), Options{})
	if r.Same() {
		t.Fatal("number versus ISO string end_time not reported")
	}

	// Header changes.
	be = base().entry()
	be.Headers["x-api-key"] = "other"
	r = mustCompare(t, logOf(a.entry()), logOf(be), Options{})
	if r.Same() || r.Diffs[0].Where != "header x-api-key" {
		t.Fatalf("%+v", r.Diffs)
	}
	be = base().entry()
	be.Headers["x-extra"] = "1"
	if r = mustCompare(t, logOf(a.entry()), logOf(be), Options{}); r.Same() || r.Diffs[0].B != "1" || r.Diffs[0].A != "<absent>" {
		t.Fatalf("%+v", r.Diffs)
	}
	// Transport headers are ignored (T1).
	be = base().entry()
	be.Headers["accept-encoding"] = "gzip"
	be.Headers["connection"] = "keep-alive"
	if r = mustCompare(t, logOf(a.entry()), logOf(be), Options{}); !r.Same() {
		t.Fatalf("%+v", r.Diffs)
	}
	// ... but an Accept the SDK sets itself is compared, and a changed connection is not a client default.
	be = base().entry()
	be.Headers["accept"] = "application/json"
	if r = mustCompare(t, logOf(a.entry()), logOf(be), Options{}); r.Same() || r.Diffs[0].Where != "header accept" {
		t.Fatalf("%+v", r.Diffs)
	}

	// Framing mode.
	be = base().entry()
	be.Transfer, be.ContentLength = "content-length", int64(len(be.Body))
	if r = mustCompare(t, logOf(a.entry()), logOf(be), Options{}); r.Same() || r.Diffs[0].Where != "transfer framing" {
		t.Fatalf("%+v", r.Diffs)
	}

	// A part dropped, a part reordered.
	be = base().entry()
	be.Parts = be.Parts[:5]
	reframe(be)
	r = mustCompare(t, logOf(a.entry()), logOf(be), Options{})
	if r.Same() || !strings.Contains(r.Diffs[0].Where, "part count") {
		t.Fatalf("%+v", r.Diffs)
	}
	be = base().entry()
	be.Parts[1], be.Parts[2] = be.Parts[2], be.Parts[1]
	reframe(be)
	r = mustCompare(t, logOf(a.entry()), logOf(be), Options{})
	if r.Same() || !strings.Contains(r.Diffs[0].Where, "name") {
		t.Fatalf("%+v", r.Diffs)
	}

	// Request count.
	r = mustCompare(t, logOf(a.entry(), a.entry()), logOf(a.entry()), Options{})
	if r.Same() || r.Diffs[0].Where != "request count" || !strings.Contains(r.Diffs[0].Note, "log A has request 2") {
		t.Fatalf("%+v", r.Diffs)
	}
}

func TestIDsMapByOrderOfFirstAppearance(t *testing.T) {
	a, b := base(), base()
	// B sends the root id where A sent the turn id as the llm's parent: a structural difference.
	be := b.entry()
	be.Parts[3].Bytes = bytes.ReplaceAll(be.Parts[3].Bytes, []byte(idRoot), []byte(idTurn))
	reframe(be)
	r := mustCompare(t, logOf(a.entry()), logOf(be), Options{})
	if r.Same() {
		t.Fatal("a different id relationship was normalised away")
	}
}

func TestKeyOrderCanonicalisedOnlyInHostObjects(t *testing.T) {
	a, b := base(), base()
	// Reorder a key of the llm inputs ROOT after messages: host-delivered (N5).
	b.inputsOrder = "b"
	be := b.entry()
	be.Parts[4].Bytes = []byte(strings.Replace(string(be.Parts[4].Bytes), `{"messages":`, `{"model":"m","stream":true,"messages":`, 1))
	be.Parts[4].Bytes = []byte(strings.Replace(string(be.Parts[4].Bytes), `],"stream":true,"model":"m"}`, `]}`, 1))
	reframe(be)
	r := mustCompare(t, logOf(a.entry()), logOf(be), Options{})
	if r.Same() {
		t.Fatal("`messages` must keep leading in an llm run's inputs")
	}
	// Whitespace is not on the list outside host objects.
	be = base().entry()
	be.Parts[1].Bytes = []byte(`{"prompt": "hi","imageCount":0}`)
	reframe(be)
	if r := mustCompare(t, logOf(a.entry()), logOf(be), Options{}); r.Same() {
		t.Fatal("whitespace in an extension-built object was normalised away")
	}
}

func TestBoundaryShapeAndLengthChecks(t *testing.T) {
	a := base()
	b := base()
	b.boundary = "----SomethingElse123"
	r := mustCompare(t, logOf(a.entry()), logOf(b.entry()), Options{})
	if r.Same() || r.Diffs[0].Where != "boundary shape" {
		t.Fatalf("%+v", r.Diffs)
	}
	// length= is the byte length of the serialised part (the SDK serialises to bytes before it declares the length).
	be := base().entry()
	be.Parts[1].Bytes = []byte(`{"prompt":"é","imageCount":0}`)
	be.Parts[1].ContentType = fmt.Sprintf("application/json; length=%d", len(string(be.Parts[1].Bytes))-1) // one short: wrong
	be.Parts[1].Header = "Content-Disposition: form-data; name=\"post." + idRoot + ".inputs\"\r\nContent-Type: " + be.Parts[1].ContentType
	reframe(be)
	r = mustCompare(t, logOf(base().entry()), logOf(be), Options{Max: 10})
	found := false
	for _, d := range r.Diffs {
		found = found || (d.Where == "body problems" && strings.Contains(d.B, "bytes"))
	}
	if !found {
		t.Fatalf("%+v", r.Diffs)
	}
}

func TestGzipAndJSONBodiesAndScenarioRules(t *testing.T) {
	js := func(body string, gz bool) *reqlog.Entry {
		e := &reqlog.Entry{Route: "batch", Method: "POST", Path: "/runs/batch", Transfer: "content-length", ContentLength: int64(len(body)),
			Headers: map[string]string{"content-type": "application/json", "x-api-key": "k"}, Body: []byte(body)}
		if gz {
			e.Encoding = "gzip"
			e.Body = []byte("compressed-differently")
			e.Decoded = []byte(body)
		}
		return e
	}
	body := `{"post":[{"id":"` + idRoot + `","run_type":"chain","start_time":"2026-11-06T22:00:00.000Z","extra":{"runtime":{"runtime":"node","sdk":"langsmith-js","sdk_version":"0.10.5","library":"langsmith"}},"session_id":"resp_abc123"}],"patch":[]}`
	body2 := strings.NewReplacer(idRoot, idRoot2, "node", "go", "resp_abc123", "resp_zzz999", "22:00:00", "23:59:59").Replace(body)
	r := mustCompare(t, logOf(js(body, false)), logOf(js(body2, false)), Options{})
	if r.Same() {
		t.Fatal("resp id difference must show without a scenario rule")
	}
	r = mustCompare(t, logOf(js(body, false)), logOf(js(body2, false)), Options{Rules: []Rule{{Pattern: `resp_[a-z0-9]+`, Replacement: "<resp>"}}})
	if !r.Same() || r.FiredA["N7"] != 1 || r.FiredB["N7"] != 1 || r.FiredA["N4"] == 0 {
		t.Fatalf("%+v %+v", r.Diffs, r.FiredA)
	}
	// A gzip body is compared decoded; whether it was gzip is compared.
	r = mustCompare(t, logOf(js(body, true)), logOf(js(body, true)), Options{})
	if !r.Same() || r.FiredA["T2"] != 1 {
		t.Fatalf("%+v", r.Diffs)
	}
	if r = mustCompare(t, logOf(js(body, true)), logOf(js(body, false)), Options{}); r.Same() {
		t.Fatal("gzip versus plain not reported")
	}
}

func TestLaneScratchPathsAreN6(t *testing.T) {
	mk := func(p string) *reqlog.Entry {
		return &reqlog.Entry{Route: "runs", Method: "POST", Path: "/runs", Transfer: "content-length", Headers: map[string]string{},
			Body: []byte(`{"extra":{"metadata":{"cwd":"` + p + `/ws"}}}`)}
	}
	r := mustCompare(t, logOf(mk("/tmp/eq0a1b2c3d")), logOf(mk("/var/folders/x/T/eqffeeddcc")), Options{})
	if !r.Same() || r.FiredA["N6"] != 1 {
		t.Fatalf("%+v", r.Diffs)
	}
	if r = mustCompare(t, logOf(mk("/tmp/eq0a1b2c3d")), logOf(mk("/tmp/other")), Options{}); r.Same() {
		t.Fatal("a different cwd must differ")
	}
}

func TestRenderIsByteExactWithoutEdits(t *testing.T) {
	src := []byte(` {"a": [1, 2.50, "x\u00e9\"y"],  "b":{"c":null,"d":true}} `)
	d, err := newDoc(bytes.TrimSpace(src))
	if err != nil {
		t.Fatal(err)
	}
	if got := d.render(); got != string(bytes.TrimSpace(src)) {
		t.Fatalf("render changed the text: %q", got)
	}
	d.sortDeep(d.root, nil)
	if got := d.render(); got != `{"a":[1,2.50,"x\u00e9\"y"],"b":{"c":null,"d":true}}` {
		t.Fatalf("sorted render: %q", got)
	}
	for _, bad := range []string{`{`, `{"a":}`, `[1,]`, `{"a":1} x`, `"abc`} {
		if _, err := parseJSON([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestHostPromptIsOptInAndMeasuredTimesKeepLengthChecked(t *testing.T) {
	mk := func(content string, dur string) *reqlog.Entry {
		out := `{"messages":[{"role":"system","content":"` + content + `","sections":{"a":"` + content + `"}},{"role":"assistant","content":"x","durationMs":` + dur + `}]}`
		return multipartEntry("----LangSmithFormBoundaryabc", []partSpec{
			{"post." + idRoot, `{"id":"` + idRoot + `","run_type":"chain"}`},
			{"post." + idRoot + ".outputs", out},
		})
	}
	a, b := mk("You run inside pi", "35"), mk("You run inside pig, longer", "4")
	r := mustCompare(t, logOf(a), logOf(b), Options{})
	if r.Same() {
		t.Fatal("system prompt text differs and N8 is off")
	}
	r = mustCompare(t, logOf(mk("You run inside pi", "35")), logOf(mk("You run inside pig, longer", "4")), Options{HostPrompt: true})
	if !r.Same() || r.FiredA["N8"] != 2 {
		var buf bytes.Buffer
		r.Report(&buf, "A", "B")
		t.Fatal(buf.String())
	}
	// A measured time of another width changes length=, which is checked and not compared.
	if r = mustCompare(t, logOf(mk("p", "35")), logOf(mk("p", "4")), Options{}); !r.Same() {
		t.Fatalf("%+v", r.Diffs)
	}
	// ... but a wrong declared length is still caught.
	be := mk("p", "4")
	be.Parts[1].ContentType = "application/json; length=1"
	be.Parts[1].Header = "Content-Disposition: form-data; name=\"post." + idRoot + ".outputs\"\r\nContent-Type: " + be.Parts[1].ContentType
	reframe(be)
	if r = mustCompare(t, logOf(mk("p", "35")), logOf(be), Options{}); r.Same() {
		t.Fatal("wrong length= not caught")
	}
}

func TestUnorderedDrainComparesConcurrentBatchesAsAMultiset(t *testing.T) {
	mk := func(root, a string, prompt string) *reqlog.Entry {
		return multipartEntry("----LangSmithFormBoundaryabc", []partSpec{
			{"post." + root, `{"id":"` + root + `","name":"` + a + `","start_time":1790000000000,"run_type":"chain"}`},
			{"post." + root + ".inputs", `{"prompt":"` + prompt + `"}`},
		})
	}
	info := &reqlog.Entry{Route: "info", Method: "GET", Path: "/info", Transfer: "none", Headers: map[string]string{}}
	A := logOf(info, mk(idRoot, "root", "x"), mk(idTurn, "turn", "y"), mk(idLLM, "llm", "z"))
	// Same three batches, other arrival order, other ids, other times.
	B := logOf(&reqlog.Entry{Route: "info", Method: "GET", Path: "/info", Transfer: "none", Headers: map[string]string{}},
		mk(idLLM2, "llm", "z"), mk(idRoot2, "root", "x"), mk(idTurn2, "turn", "y"))
	if r := mustCompare(t, A, B, Options{}); r.Same() {
		t.Fatal("ordered comparison must see the different order")
	}
	if r := mustCompare(t, A, B, Options{UnorderedDrain: true}); !r.Same() || r.FiredA["N9"] != 1 || r.FiredB["N9"] != 1 {
		var buf bytes.Buffer
		r.Report(&buf, "A", "B")
		t.Fatal(buf.String())
	}
	// The multiset still has to match: one batch changed, one missing.
	C := logOf(&reqlog.Entry{Route: "info", Method: "GET", Path: "/info", Transfer: "none", Headers: map[string]string{}},
		mk(idLLM2, "llm", "CHANGED"), mk(idRoot2, "root", "x"), mk(idTurn2, "turn", "y"))
	if r := mustCompare(t, A, C, Options{UnorderedDrain: true}); r.Same() {
		t.Fatal("a changed batch was not reported")
	}
	// A request that is not ingest is a barrier: info moved across a drain is a difference.
	D := logOf(mk(idRoot2, "root", "x"), &reqlog.Entry{Route: "info", Method: "GET", Path: "/info", Transfer: "none", Headers: map[string]string{}},
		mk(idTurn2, "turn", "y"), mk(idLLM2, "llm", "z"))
	if r := mustCompare(t, A, D, Options{UnorderedDrain: true}); r.Same() {
		t.Fatal("a non-ingest request must stay in place")
	}
}

func TestHostUsageFollowsThePromptUnderN8Only(t *testing.T) {
	mk := func(tokens string) *reqlog.Entry {
		return multipartEntry("----LangSmithFormBoundaryabc", []partSpec{
			{"post." + idRoot, `{"id":"` + idRoot + `","run_type":"chain"}`},
			{"post." + idRoot + ".outputs", `{"messages":[],"tokensBefore":` + tokens + `,"context_usage":{"tokens":` + tokens + `,"percent":1.3}}`},
		})
	}
	if r := mustCompare(t, logOf(mk("1319")), logOf(mk("1300")), Options{}); r.Same() {
		t.Fatal("context_usage must be compared without N8")
	}
	if r := mustCompare(t, logOf(mk("1319")), logOf(mk("1300")), Options{HostPrompt: true}); !r.Same() {
		t.Fatalf("%+v", r.Diffs)
	}
}

func TestHostPromptRulesCoverGeminiAndAnthropicShapes(t *testing.T) {
	mk := func(sys, abort string) *reqlog.Entry {
		return multipartEntry("----LangSmithFormBoundaryabc", []partSpec{
			{"post." + idRoot, `{"id":"` + idRoot + `","run_type":"llm"}`},
			{"post." + idRoot + ".inputs", `{"messages":[{"role":"system","content":"` + sys + `","_raw":[{"type":"text","text":"` + sys + `"}]}],"model":"m","config":{` + abort + `"maxOutputTokens":4096,"systemInstruction":"` + sys + `"}}`},
		})
	}
	if r := mustCompare(t, logOf(mk("inside pi", `"abortSignal":{},`)), logOf(mk("inside pig", "")), Options{}); r.Same() {
		t.Fatal("differs without N8")
	}
	if r := mustCompare(t, logOf(mk("inside pi", `"abortSignal":{},`)), logOf(mk("inside pig", "")), Options{HostPrompt: true}); !r.Same() {
		t.Fatalf("%+v", r.Diffs)
	}
	// A non-empty abortSignal, or other config differences, still show.
	if r := mustCompare(t, logOf(mk("x", `"abortSignal":{"a":1},`)), logOf(mk("x", "")), Options{HostPrompt: true}); r.Same() {
		t.Fatal("a non-empty abortSignal must stay")
	}
}
