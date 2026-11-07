package lsdiff

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"lstools/internal/reqlog"
)

// Rule is one scenario-level rule (N7), the same shape as pigeq's `normalize` entries: a Go (RE2) pattern and a
// replacement that may use $1 / ${name}.
type Rule struct {
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
	re          *regexp.Regexp
}

// Rules documents every normalisation. Anything that is not here compares byte for byte.
var Rules = []struct{ ID, Text string }{
	{"N1", "ids: every UUID in any string (run ids, trace ids, dotted_order ids, parent ids, part names, URL paths, session ids, headers) is replaced by <id1>, <id2>, ... in order of first appearance in the log (path, query, headers, then body, request by request). Two logs agree when the same ids appear in the same places in the same order."},
	{"N2", "times: (a) the YYYYMMDDTHHMMSSffffffZ prefixes of dotted_order; (b) ISO-8601 timestamps inside any string; (c) numeric (epoch ms) start_time / end_time of a run, the `time` of an event, metadata.timestamp, and `timestamp` and `durationMs` (a measured time) numbers inside host-delivered objects: replaced by <ts>, <ts:num> for numbers. Number versus string stays visible."},
	{"N3", "multipart boundary: the boundary token of each request (Content-Type and body framing) becomes <boundary>. Its shape must be ----LangSmithFormBoundary plus base36, in both logs (the SDK's own format); a different shape is a difference."},
	{"N4", "divergence D1: in `extra.runtime` the values of runtime, sdk and sdk_version (the keys are compared), and the User-Agent request header (presence is compared); the `length=` parameter of an extra part's content type, which counts those values, is checked against the part's byte length instead of compared (the SDK declares the UTF-8 length of the serialised bytes)."},
	{"N5", "key order inside objects the host delivers (provider payloads under an llm run's inputs, tool args and tool results, message content under outputs, tool_update partialResult, context_usage): keys sorted recursively and the object re-emitted compactly; the keys the extension puts first (messages; role and content of a message; role of a tool result) must still lead. Objects the extension builds keep their order."},
	{"N6", "paths: a path through a lane scratch directory (<anything>/eqXXXXXXXX, eight hex digits) and any -path-prefix given become <lane>."},
	{"N7", "scenario rules: the `normalize` list of a scenario file (-scenario), applied in order after N1-N6, exactly as pigeq applies them."},
	{"N8", "opt-in (-host-prompt, only for a Pi-versus-PiG pair): host text the port cannot influence. Pi's and PiG's own system prompt (wording, doc paths, the host name): the content, sections and `_raw` of a system message and a provider payload's `systemInstruction` inside host-delivered objects become <host-prompt>. What the host derives from that text: `outputs.context_usage` and a compaction run's `outputs.tokensBefore` become <host-usage>. Pi serialises its AbortSignal into a Gemini request's config, PiG does not: an empty `abortSignal` member is dropped. pig-node versus pig-go (one host) never needs any of it."},
	{"N9", "opt-in (-unordered-drain): consecutive ingest requests (multipart, batch) are compared as a multiset, sorted by a key free of ids, times and boundary before N1 numbers the ids: the batches of one queue drain are sent concurrently and their wire order is scheduler order. The content of each request is still compared in full."},
	{"T1", "transport headers: host, keep-alive, accept-encoding, content-length, transfer-encoding are not compared (the framing mode, chunked or content-length, is compared instead; the length value is not, it moves with the boundary); accept, accept-language, sec-fetch-mode and connection are not compared when they carry the HTTP client's default (*/*, *, cors, keep-alive), so a value the SDK sets itself (Accept: application/json on /info) is."},
	{"T2", "a gzip body is compared after decompression (the compressor's bytes are the HTTP client's choice); whether the request was gzip-encoded is compared."},
}

var (
	reUUID    = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	reDotted  = regexp.MustCompile(`[0-9]{8}T[0-9]{12}Z`)
	reISO     = regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?(?:Z|[+-][0-9]{2}:[0-9]{2})`)
	reLanePat = regexp.MustCompile(`(?:/[^/\s"'\\]+)*/eq[0-9a-f]{8}`)
	reBound   = regexp.MustCompile(`^----LangSmithFormBoundary[0-9a-z]{1,13}$`)
	rePart    = regexp.MustCompile(`^(post|patch)\.([0-9a-fA-F-]{36})(?:\.(inputs|outputs|events|extra|error|serialized))?$`)
	reLength  = regexp.MustCompile(`length=[0-9]+`)
)

// transportHeaders is T1: headers the HTTP client adds on its own, never compared ...
var transportHeaders = map[string]bool{
	"host": true, "keep-alive": true, "accept-encoding": true, "content-length": true, "transfer-encoding": true,
}

// ... and headers compared unless they carry the client's default value (undici: accept */*, accept-language *,
// sec-fetch-mode cors, connection keep-alive). A value the SDK sets itself (Accept: application/json on /info) is compared.
var transportDefaults = map[string]string{
	"accept": "*/*", "accept-language": "*", "sec-fetch-mode": "cors", "connection": "keep-alive",
}

// Normalizer holds the per-log state of the normalisations: the id map, the counters, the run types.
type Normalizer struct {
	rules      []Rule
	prefixes   []string
	hostPrompt bool
	// lenEdits counts edits that change a part's text length (D1 values, replaced measured times).
	lenEdits int
	ids      map[string]string
	runTypes map[string]string // raw run id -> run_type, from this log
	Fired    map[string]int
}

// NewNormalizer prepares the normaliser of one log. rules are the scenario's N7 rules, prefixes extra N6 paths.
func NewNormalizer(rules []Rule, prefixes []string) (*Normalizer, error) {
	z := &Normalizer{prefixes: prefixes, ids: map[string]string{}, runTypes: map[string]string{}, Fired: map[string]int{}}
	for i, r := range rules {
		if r.Pattern == "" {
			return nil, fmt.Errorf("rule %d: empty pattern", i)
		}
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("rule %d: %w", i, err)
		}
		r.re = re
		z.rules = append(z.rules, r)
	}
	return z, nil
}

func (z *Normalizer) fire(rule string, n int) {
	if n > 0 {
		z.Fired[rule] += n
	}
}

// text applies the text-level rules N2 (a, b), N1, N6, N7 to one string.
func (z *Normalizer) text(s string) string {
	if n := len(reDotted.FindAllStringIndex(s, -1)); n > 0 {
		s = reDotted.ReplaceAllString(s, "<ts>")
		z.fire("N2", n)
	}
	if n := len(reISO.FindAllStringIndex(s, -1)); n > 0 {
		s = reISO.ReplaceAllString(s, "<ts>")
		z.fire("N2", n)
	}
	s = reUUID.ReplaceAllStringFunc(s, func(m string) string {
		z.fire("N1", 1)
		id, ok := z.ids[m]
		if !ok {
			id = fmt.Sprintf("<id%d>", len(z.ids)+1)
			z.ids[m] = id
		}
		return id
	})
	if n := len(reLanePat.FindAllStringIndex(s, -1)); n > 0 {
		s = reLanePat.ReplaceAllString(s, "<lane>")
		z.fire("N6", n)
	}
	for _, p := range z.prefixes {
		if n := strings.Count(s, p); n > 0 {
			s = strings.ReplaceAll(s, p, "<lane>")
			z.fire("N6", n)
		}
	}
	for _, r := range z.rules {
		if n := len(r.re.FindAllStringIndex(s, -1)); n > 0 {
			s = r.re.ReplaceAllString(s, r.Replacement)
			z.fire("N7", n)
		}
	}
	return s
}

// nPart is a normalised multipart part.
type nPart struct {
	Name, Header, Body string
}

// nEntry is a normalised request.
type nEntry struct {
	Src         *reqlog.Entry
	Route       string
	Method      string
	Path        string
	Query       string
	Headers     map[string]string
	Transfer    string
	Encoding    string
	Parts       []nPart
	Multipart   bool
	Body        string // non-multipart payload
	ParseError  string
	BoundaryBad string
	Problems    []string
}

// Prepare scans the log for run types (a patch without run_type still has the type of its post).
func (z *Normalizer) Prepare(es []*reqlog.Entry) {
	for _, e := range es {
		for _, p := range e.Parts {
			m := rePart.FindStringSubmatch(p.Name)
			if m == nil || m[3] != "" {
				continue
			}
			if d, err := parseJSON(p.Bytes); err == nil {
				if rt := d.get("run_type"); rt != nil && rt.kind == 's' {
					z.runTypes[m[2]] = strings.Trim(string(p.Bytes[rt.start:rt.end]), `"`)
				}
			}
		}
		if len(e.Parts) == 0 && (e.Route == "batch" || e.Route == "runs" || e.Route == "patch") {
			if d, err := parseJSON(e.Payload()); err == nil {
				for _, run := range runObjects(d) {
					z.noteRun(e.Payload(), run)
				}
			}
		}
	}
}

func (z *Normalizer) noteRun(src []byte, run *node) {
	id, rt := run.get("id"), run.get("run_type")
	if id != nil && rt != nil && id.kind == 's' && rt.kind == 's' {
		z.runTypes[strings.Trim(string(src[id.start:id.end]), `"`)] = strings.Trim(string(src[rt.start:rt.end]), `"`)
	}
}

// runObjects returns the run objects of a batch/POST/PATCH body: the elements of post[] and patch[], or the root.
func runObjects(root *node) []*node {
	if root.kind != 'o' {
		return nil
	}
	if p, q := root.get("post"), root.get("patch"); p != nil || q != nil {
		var out []*node
		for _, arr := range []*node{p, q} {
			if arr != nil && arr.kind == 'a' {
				out = append(out, arr.items...)
			}
		}
		return out
	}
	return []*node{root}
}

// Entry normalises one request. Requests must be passed in log order (N1 numbers ids by first appearance).
func (z *Normalizer) Entry(e *reqlog.Entry) *nEntry {
	n := &nEntry{Src: e, Route: e.Route, Method: e.Method, Transfer: e.Transfer, Encoding: e.Encoding, ParseError: e.ParseError}
	if e.Encoding == "gzip" {
		z.fire("T2", 1)
	}
	n.Path = z.text(e.Path)
	n.Query = z.query(e.Query)
	n.Headers = map[string]string{}
	names := make([]string, 0, len(e.Headers))
	for k := range e.Headers {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		v := e.Headers[k]
		switch {
		case transportHeaders[k]:
			continue
		case transportDefaults[k] != "" && v == transportDefaults[k]:
			continue
		case k == "user-agent":
			z.fire("N4", 1)
			v = "<d1>"
		case k == "content-type" && e.Boundary != "":
			if !reBound.MatchString(e.Boundary) {
				n.BoundaryBad = e.Boundary
			}
			if strings.Contains(v, e.Boundary) {
				v = strings.ReplaceAll(v, e.Boundary, "<boundary>")
				z.fire("N3", 1)
			}
		}
		n.Headers[k] = z.text(v)
	}
	payload := e.Payload()
	switch {
	case e.Boundary != "":
		n.Multipart = true
		z.parts(n, e, payload)
	case e.Form != nil:
		keys := make([]string, 0, len(e.Form))
		for k := range e.Form {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		for _, k := range keys {
			for _, v := range e.Form[k] {
				fmt.Fprintf(&b, "%s=%s\n", k, v)
			}
		}
		n.Body = z.text(b.String())
	case len(payload) > 0 && (e.Route == "batch" || e.Route == "runs" || e.Route == "patch"):
		n.Body = z.runBody(payload, n)
	default:
		n.Body = z.text(string(payload))
	}
	return n
}

func (z *Normalizer) query(q string) string {
	if q == "" {
		return ""
	}
	parts := strings.Split(q, "&")
	sort.Strings(parts)
	return z.text(strings.Join(parts, "&"))
}

func (z *Normalizer) runBody(payload []byte, n *nEntry) string {
	d, err := newDoc(payload)
	if err != nil {
		n.Problems = append(n.Problems, "body is not JSON: "+err.Error())
		return z.text(string(payload))
	}
	for _, run := range runObjects(d.root) {
		z.run(d, run)
	}
	return z.text(d.render())
}

// run applies the field rules to one run object (the whole JSON object of a batch element or POST /runs).
func (z *Normalizer) run(d *doc, run *node) {
	if run.kind != 'o' {
		return
	}
	rt := ""
	if t := run.get("run_type"); t != nil && t.kind == 's' {
		rt = strings.Trim(string(d.src[t.start:t.end]), `"`)
	} else if id := run.get("id"); id != nil && id.kind == 's' {
		rt = z.runTypes[strings.Trim(string(d.src[id.start:id.end]), `"`)]
	}
	for _, m := range run.members {
		switch m.key {
		case "start_time", "end_time":
			z.timeValue(d, m.val)
		case "inputs", "outputs", "events", "extra":
			z.field(d, m.key, rt, m.val)
		}
	}
}

// timeValue replaces an epoch-ms number by <ts:num>; an ISO string is left to the text pass (<ts>).
func (z *Normalizer) timeValue(d *doc, v *node) {
	if v != nil && v.kind == 'n' {
		d.repl[v] = `"<ts:num>"`
		z.lenEdits++
		z.fire("N2", 1)
	}
}

// field applies the rules of one run field (the value of inputs, outputs, events or extra) to its node.
func (z *Normalizer) field(d *doc, field, rt string, v *node) {
	if v == nil {
		return
	}
	switch field {
	case "extra":
		if rtm := v.get("runtime"); rtm != nil {
			for _, k := range []string{"runtime", "sdk", "sdk_version"} {
				if x := rtm.get(k); x != nil {
					d.repl[x] = `"<d1>"`
					z.lenEdits++
					z.fire("N4", 1)
				}
			}
		}
		if md := v.get("metadata"); md != nil {
			z.timeValue(d, md.get("timestamp"))
		}
	case "events":
		if v.kind == 'a' {
			for _, ev := range v.items {
				z.timeValue(d, ev.get("time"))
				if kw := ev.get("kwargs"); kw != nil {
					z.host(d, kw.get("partialResult"), nil)
				}
			}
		}
	case "inputs":
		switch rt {
		case "llm":
			z.host(d, v, []string{"messages"})
		case "tool":
			z.host(d, v.get("args"), nil)
		}
	case "outputs":
		if msgs := v.get("messages"); msgs != nil && msgs.kind == 'a' {
			for _, m := range msgs.items {
				z.host(d, m, []string{"role", "content"})
			}
		}
		z.host(d, v.get("toolResults"), nil)
		z.host(d, v.get("context_usage"), nil)
		if z.hostPrompt {
			// The host estimates the context size from its own prompt text, so tokens and percent follow the prompt; the
			// compaction run's tokensBefore is the same estimate.
			for _, k := range []string{"context_usage", "tokensBefore"} {
				if cu := v.get(k); cu != nil {
					d.repl[cu] = `"<host-usage>"`
					z.lenEdits++
					z.fire("N8", 1)
				}
			}
		}
		z.host(d, v.get("output"), []string{"role"})
	}
}

// host marks a host-delivered subtree (N5) and its timestamp numbers (N2).
func (z *Normalizer) host(d *doc, v *node, lead []string) {
	if v == nil {
		return
	}
	count := 0
	var walk func(*node)
	walk = func(n *node) {
		switch n.kind {
		case 'o':
			count++
			if z.hostPrompt {
				z.systemPrompt(d, n)
				// Pi puts its AbortSignal into a Gemini request's config (serialised as {}); PiG does not.
				for _, m := range n.members {
					if m.key == "abortSignal" && m.val.kind == 'o' && len(m.val.members) == 0 {
						d.drop[m.val] = true
						z.lenEdits++
						z.fire("N8", 1)
					}
				}
			}
			for _, m := range n.members {
				if (m.key == "timestamp" || m.key == "durationMs") && m.val.kind == 'n' {
					z.timeValue(d, m.val)
				}
				walk(m.val)
			}
		case 'a':
			for _, it := range n.items {
				walk(it)
			}
		}
	}
	walk(v)
	d.sortDeep(v, lead)
	z.fire("N5", count)
}

// systemPrompt is N8: the content and sections of a system message are the host's prompt text.
func (z *Normalizer) systemPrompt(d *doc, o *node) {
	// Gemini carries the prompt in config.systemInstruction, not in a message.
	if si := o.get("systemInstruction"); si != nil {
		d.repl[si] = `"<host-prompt>"`
		z.lenEdits++
		z.fire("N8", 1)
	}
	role := o.get("role")
	if role == nil || role.kind != 's' || string(d.src[role.start:role.end]) != `"system"` {
		return
	}
	// `_raw` is the Anthropic system block the extension copies into the system message it builds (index.ts convertProviderPayload).
	for _, k := range []string{"content", "sections", "_raw"} {
		if v := o.get(k); v != nil {
			d.repl[v] = `"<host-prompt>"`
			z.lenEdits++
			z.fire("N8", 1)
		}
	}
}

// parts normalises the parts of a multipart body and checks the framing.
func (z *Normalizer) parts(n *nEntry, e *reqlog.Entry, payload []byte) {
	// The parts must rebuild the payload exactly: nothing between them, nothing before or after.
	var rebuilt strings.Builder
	for _, p := range e.Parts {
		rebuilt.WriteString("--" + e.Boundary + "\r\n" + p.Header + "\r\n\r\n")
		rebuilt.Write(p.Bytes)
		rebuilt.WriteString("\r\n")
	}
	rebuilt.WriteString("--" + e.Boundary + "--\r\n")
	if rebuilt.String() != string(payload) {
		n.Problems = append(n.Problems, "body framing is not exactly the parts between boundary delimiters")
	}
	for _, p := range e.Parts {
		np := nPart{Name: z.text(p.Name)}
		header := p.Header
		d1 := false
		if p.ContentType != "" && strings.HasPrefix(p.ContentType, "application/json") {
			m := rePart.FindStringSubmatch(p.Name)
			text, fired := z.jsonPart(p, m)
			np.Body = text
			d1 = fired
			if want := len(p.Bytes); m != nil {
				if got := lengthParam(p.ContentType); got != want {
					n.Problems = append(n.Problems, fmt.Sprintf("part %s: content type says length=%d but the payload has %d bytes", np.Name, got, want))
				}
			}
		} else {
			np.Body = string(p.Bytes)
		}
		if d1 {
			header = reLength.ReplaceAllString(header, "length=<d1>")
		}
		header = strings.ReplaceAll(header, e.Boundary, "<boundary>")
		np.Header = z.text(header)
		n.Parts = append(n.Parts, np)
	}
}

func lengthParam(ct string) int {
	m := reLength.FindString(ct)
	if m == "" {
		return -1
	}
	var v int
	fmt.Sscanf(m, "length=%d", &v)
	return v
}

// jsonPart normalises a JSON part body; fired reports whether an edit changed its text length (D1 values, replaced
// times), so its length= is checked against the payload instead of compared.
func (z *Normalizer) jsonPart(p reqlog.Part, m []string) (text string, d1 bool) {
	d, err := newDoc(p.Bytes)
	if err != nil || m == nil {
		return z.text(string(p.Bytes)), false
	}
	before := z.lenEdits
	id, kind := m[2], m[3]
	rt := z.runTypes[id]
	switch kind {
	case "":
		z.run(d, d.root)
	case "inputs", "outputs", "events", "extra":
		z.field(d, kind, rt, d.root)
	}
	return z.text(d.render()), z.lenEdits > before
}
