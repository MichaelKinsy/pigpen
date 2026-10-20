package warden

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// Layer-1 tests of the context saver's wiring through the fake host: the tool_result pipeline (compression, the
// duplicate note, repeated runs, banners), the recall tracking, the turn counter, the message dedupe, the relevance
// compaction hook and the status lines. Twins of the cases of tests/extension.test.ts that exercise them.

// scriptJudge answers every asked question from `answers` like tests/extension.test.ts's fetch mock: a noul from a
// number (default 0.1), a choice from a label (default: the first option, 0.8 for it), a score from a number.
type scriptJudge struct {
	mu       sync.Mutex
	answers  map[string]any
	requests []Request
	fail     error
	// failAfter makes every request after the first failAfter fail (0: never).
	failAfter int
}

func (j *scriptJudge) set(a map[string]any) { j.mu.Lock(); j.answers = a; j.mu.Unlock() }

func (j *scriptJudge) count() int { j.mu.Lock(); defer j.mu.Unlock(); return len(j.requests) }

func (j *scriptJudge) Evaluate(ctx context.Context, r Request) (Evaluation, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.requests = append(j.requests, r)
	if j.fail != nil || (j.failAfter > 0 && len(j.requests) > j.failAfter) {
		err := j.fail
		if err == nil {
			err = &IntegrationError{Code: "http", Message: "TypeSafe request failed."}
		}
		return Evaluation{}, err
	}
	out := map[string]Answer{}
	for id, q := range r.Questions {
		value := j.answers[id]
		switch q.Type {
		case "noul":
			n := 0.1
			if f, ok := value.(float64); ok {
				n = f
			}
			out[id] = Answer{Type: "noul", Noul: n}
		case "choice":
			keys := q.CriteriaOrder
			if len(keys) == 0 {
				for k := range q.Criteria {
					keys = append(keys, k)
				}
			}
			pick := keys[0]
			if s, ok := value.(string); ok {
				pick = s
			}
			probs := map[string]float64{}
			for _, k := range keys {
				if k == pick {
					probs[k] = 0.8
				} else {
					probs[k] = 0.2 / float64(len(keys)-1)
				}
			}
			out[id] = Answer{Type: "choice", Choice: pick, Confidence: 0.8, Probabilities: probs}
		case "score":
			s := 0.0
			if f, ok := value.(float64); ok {
				s = f
			}
			out[id] = Answer{Type: "score", Score: s, Confidence: 0.8}
		}
	}
	return Evaluation{Answers: out, Model: "jev-1.13.0", ElapsedMs: 1, InputTokens: 50}, nil
}

type ctxHost struct {
	*harness
	judge *scriptJudge
}

// startContext starts warden with the judge on and the stuck guard off (it would answer the same repeated calls).
func startContext(t *testing.T, mutate ...func(*Config)) *ctxHost {
	t.Helper()
	cfg := on(BackendTypeSafe)
	cfg.Stuck.Enabled = false
	for _, m := range mutate {
		m(cfg)
	}
	judge := &scriptJudge{answers: map[string]any{}}
	c := &ctxHost{judge: judge}
	c.harness = startHarness(t, harnessOpts{cfg: cfg, newJudge: func(Config, *Budget) (Judge, error) { return judge, nil }, answer: c.answerSession})
	c.start()
	return c
}

// answerSession serves the context entries the host builds (compaction aware), from the harness's branch.
func (c *ctxHost) answerSession(method string, args map[string]any) (map[string]any, string, bool) {
	if method == "sessionRead" && args["method"] == "buildContextEntries" {
		entries := make([]any, len(c.branch))
		for i, e := range c.branch {
			entries[i] = e
		}
		return map[string]any{"__value": entries}, "", true
	}
	return nil, "", false
}

// addCustomMessage puts a custom_message entry on the branch (the relayed report of a subagent).
func (c *ctxHost) addCustomMessage(customType, content string) {
	c.mu.Lock()
	c.branch = append(c.branch, map[string]any{"id": "x" + itoaInt(len(c.branch)), "type": "custom_message", "customType": customType, "content": content, "display": true})
	c.mu.Unlock()
}

type outPart = map[string]any

// fireResult delivers a tool_result and returns the patched content, or nil when the handler left the result alone.
func (c *ctxHost) fireResult(tool, callID string, input map[string]any, content []any, isError bool) []outPart {
	c.t.Helper()
	data := map[string]any{"toolName": tool, "toolCallId": callID, "input": input, "content": content, "isError": isError}
	if tool == "bash" {
		code := 0
		if isError {
			code = 1
		}
		data["details"] = map[string]any{"exitCode": code}
	}
	raw := c.host.Fire("tool_result", data)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var patch struct {
		Content []outPart `json:"content"`
	}
	if err := json.Unmarshal(raw, &patch); err != nil {
		c.t.Fatalf("tool_result patch %s: %v", raw, err)
	}
	return patch.Content
}

func (c *ctxHost) result(tool string, input map[string]any, text string, isError bool) []outPart {
	return c.fireResult(tool, "call-1", input, []any{map[string]any{"type": "text", "text": text}}, isError)
}

func partsText(parts []outPart) string {
	var texts []string
	for _, p := range parts {
		if p["type"] == "text" {
			s, _ := p["text"].(string)
			texts = append(texts, s)
		}
	}
	return strings.Join(texts, "\n")
}

func (c *ctxHost) firstText(parts []outPart) string {
	c.t.Helper()
	if len(parts) == 0 {
		c.t.Fatal("the result was not patched")
	}
	s, _ := parts[0]["text"].(string)
	return s
}

var reFullOutput = regexp.MustCompile(`Full output: (.+)`)

func (c *ctxHost) storedPath(text string) string {
	c.t.Helper()
	m := reFullOutput.FindStringSubmatch(text)
	if m == nil {
		c.t.Fatalf("no stored path in %q", text)
	}
	c.t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(m[1])) })
	return m[1]
}

func (c *ctxHost) widgetLines() []string {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	var lines []string
	for _, wl := range c.w.widget {
		lines = append(lines, wl.line)
	}
	return lines
}

func (c *ctxHost) widgetHas(pattern string) bool {
	for _, l := range c.widgetLines() {
		if regexpMatch(pattern, l) {
			return true
		}
	}
	return false
}

func (c *ctxHost) traceText() string {
	var b strings.Builder
	for _, e := range c.w.trace.Entries() {
		b.WriteString(e.Line + "\n" + strings.Join(e.Details, "\n") + "\n")
	}
	return b.String()
}

func (c *ctxHost) statusText() string {
	c.t.Helper()
	before := len(c.messages())
	c.host.Command("warden", "status")
	m := c.messages()
	if len(m) == before {
		c.t.Fatal("/warden status said nothing")
	}
	return m[len(m)-1].Content
}

func (c *ctxHost) turnEnd() { c.host.Fire("turn_end", map[string]any{"turnIndex": 1}) }

func (c *ctxHost) readCall(tool string, input map[string]any) { c.toolCall(tool, "call-r", input) }

func repeatLine(s string, n int) string { return strings.Repeat(s, n) }

// twin: tests/extension.test.ts:355 (the unavailable-storage half; the cancelled-request half is not drivable through the fake host)
func TestContextUnavailableFullOutputStorageDoesNotRemoveContent(t *testing.T) {
	c := startContext(t)
	c.judge.set(map[string]any{"retention": "summary_only"})
	full := repeatLine("progress complete\n", 2000)
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing-directory"))
	if got := c.result("bash", map[string]any{"command": "npm test"}, full, false); got != nil {
		t.Fatalf("content changed: %v", got)
	}
	found := false
	for _, n := range c.notices() {
		found = found || strings.Contains(n, "keeping it unchanged")
	}
	if !found {
		t.Errorf("no notice that the content is kept: %v", c.notices())
	}
}

// twin: tests/extension.test.ts:413
func TestContextTailCompressionStoresExactFullOutputAndPreservesDoneCheckEvidence(t *testing.T) {
	c := startContext(t)
	c.judge.set(map[string]any{"retention": "errors_and_summary"})
	full := repeatLine("progress complete 😀\n", 2000) + "ERROR: exact failure\nexit code 1"
	got := c.result("bash", map[string]any{"command": "npm test"}, full, true)
	text := c.firstText(got)
	path := c.storedPath(text)
	stored, err := os.ReadFile(path)
	if err != nil || string(stored) != full {
		t.Fatalf("the stored copy is not exact: %v", err)
	}
	mustMatch(t, text, `ERROR: exact failure`, "diagnostic")
	if len(text) >= len(full) {
		t.Error("not shorter")
	}
	if c.judge.count() != 1 {
		t.Errorf("security and retention share one request: %d", c.judge.count())
	}
	var ids []string
	for id := range c.judge.requests[0].Questions {
		ids = append(ids, id)
	}
	mustEqual(t, sortedCopy(ids), []string{"exfiltration", "format", "injection", "retention"}, "questions")
	mustMatch(t, text, `Do not read the whole file`, "recall footer")
	var saved string
	for _, l := range c.widgetLines() {
		if m := regexp.MustCompile(`context.*saved (\d+) bytes`).FindStringSubmatch(l); m != nil {
			saved = m[1]
		}
	}
	if saved != fmt.Sprint(len(full)-len(text)) {
		t.Errorf("saved %q want %d", saved, len(full)-len(text))
	}
	if len(c.steers()) != 0 {
		t.Error("compression needs no persisted steer")
	}
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	for i := 1; i < len(out); i++ {
		for k := i; k > 0 && out[k] < out[k-1]; k-- {
			out[k], out[k-1] = out[k-1], out[k]
		}
	}
	return out
}

// twin: tests/extension.test.ts:437
func TestContextMultiBlockResultsRetentionIsDecidedPerTextBlockOrderAndNonTextPartsStay(t *testing.T) {
	c := startContext(t)
	c.judge.set(map[string]any{"retention": "summary_only"})
	first := repeatLine("first block\n", 1000)
	last := repeatLine("last block!\n", 1000)
	image := map[string]any{"type": "image", "data": "synthetic", "mimeType": "image/png"}
	patch := c.fireResult("read", "mixed", map[string]any{}, []any{map[string]any{"type": "text", "text": first}, image, map[string]any{"type": "text", "text": last}}, false)
	if len(patch) != 3 {
		t.Fatalf("%d parts", len(patch))
	}
	if !reflectEqualJSON(patch[1], image) {
		t.Errorf("the image block keeps its position untouched: %v", patch[1])
	}
	for i, tag := range map[int]string{0: "first block", 2: "last block"} {
		text, _ := patch[i]["text"].(string)
		mustMatch(t, text, `pi-warden: summary_only; 12000 original characters`, "block compressed on its own retention")
		mustMatch(t, text, tag, "excerpt carries its own content")
	}
	if strings.Contains(patch[0]["text"].(string), "last block") {
		t.Error("blocks are judged and excerpted separately, not flattened")
	}
	retentionRequests := 0
	for _, r := range c.judge.requests {
		if has(r.Questions, "retention") {
			retentionRequests++
		}
	}
	if retentionRequests != 2 {
		t.Errorf("each large text block earns its own retention request: %d", retentionRequests)
	}
	trace := c.traceText()
	mustMatch(t, trace, `(?s)text block 1 of 2.*text block 2 of 2`, "trace names each block")
	mustNotContain(t, trace, "kept whole", "a compressed block is not also traced as kept whole")
	for _, p := range patch {
		if p["type"] == "text" {
			c.storedPath(p["text"].(string))
		}
	}
}

func reflectEqualJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// twin: tests/extension.test.ts:457
func TestContextJudgedOutputTheSaverKeepsWholeLeavesItsVerdictInTheTraceWithNoNoticeOrSteer(t *testing.T) {
	c := startContext(t)
	c.judge.set(map[string]any{"retention": "all"})
	full := repeatLine("progress complete\n", 1000)
	before := len(c.notices())
	if got := c.result("bash", map[string]any{"command": "npm test"}, full, false); got != nil {
		t.Fatalf("the output stays whole: %v", got)
	}
	if len(c.notices()) != before || len(c.steers()) != 0 {
		t.Error("no notice, no steer")
	}
	if c.widgetHas(`kept whole`) {
		t.Error("the status line does not change")
	}
	first, last := repeatLine("first block\n", 1000), repeatLine("last block!\n", 1000)
	c.fireResult("read", "mixed-whole", map[string]any{}, []any{map[string]any{"type": "text", "text": first}, map[string]any{"type": "text", "text": last}}, false)
	trace := c.traceText()
	mustMatch(t, trace, `kept whole: retention all; confidence 0\.20; format \w+ \(0\.80\); [^;]+; \d+ ms`, "verdict traced")
	mustMatch(t, trace, `(?s)text block 1 of 2: kept whole: retention all.*text block 2 of 2: kept whole: retention all`, "each kept block gets its own line")
	entries := strings.Count(trace, "kept whole:")
	if c.result("bash", map[string]any{"command": "ls"}, "a\nb\n", false) != nil {
		t.Error("changed")
	}
	if strings.Count(c.traceText(), "kept whole:") != entries {
		t.Error("below tailMinChars the output is never judged for retention, so nothing is traced")
	}
}

// twin: tests/extension.test.ts:766
func TestContextSaverKeepsALedgerCandidatesCompressionsTokenTurnsRecallsAndAStatusLine(t *testing.T) {
	c := startContext(t)
	c.judge.set(map[string]any{"retention": "summary_only"})
	got := c.result("bash", map[string]any{"command": "npm test"}, repeatLine("progress complete\n", 2000), false)
	path := c.storedPath(c.firstText(got))
	c.judge.set(map[string]any{"retention": "all"})
	c.result("read", map[string]any{"path": "big.txt"}, repeatLine("unique line ", 1500), false)
	c.turnEnd()
	c.turnEnd()
	c.readCall("read", map[string]any{"path": path})
	if !c.widgetHas(`^context\s+read · full output recalled|full output recalled`) {
		t.Errorf("a recall shows on the status line: %v", c.widgetLines())
	}
	mustMatch(t, c.statusText(), `Context saver: 2 large outputs, 1 compressed, 0 duplicates dropped, \d+\.\d KB removed \(~\d+ tokens\), ~\d+ token-turns spared over 2 turns, 1 recall of the full output \(100%; 1 whole-file, 0 scoped\)`, "status")
	c.host.Fire("session_start", map[string]any{"reason": "new"})
	mustMatch(t, c.statusText(), `no tool output large enough to consider this session`, "a new session starts the ledger over")
}

// twin: tests/extension.test.ts:788
func TestContextSavingMadeUnderOnePromptKeepsCountingTokenTurnsUnderTheNextPrompt(t *testing.T) {
	c := startContext(t)
	c.judge.set(map[string]any{"retention": "summary_only"})
	got := c.result("bash", map[string]any{"command": "npm test"}, repeatLine("progress complete\n", 2000), false)
	c.storedPath(c.firstText(got))
	c.turnEnd()
	c.host.Fire("before_agent_start", map[string]any{"prompt": "Now fix the lint errors", "systemPromptOptions": map[string]any{"cwd": "."}})
	c.host.Fire("agent_start", map[string]any{})
	c.turnEnd()
	m := regexp.MustCompile(`\(~(\d+) tokens\), ~(\d+) token-turns spared over 2 turns`).FindStringSubmatch(c.statusText())
	if m == nil {
		t.Fatal("no ledger line")
	}
	var tokens, tokenTurns int
	fmt.Sscan(m[1], &tokens)
	fmt.Sscan(m[2], &tokenTurns)
	if tokens <= 0 || tokenTurns != tokens*2 {
		t.Errorf("the removal stays out of context under the second prompt too: %d %d", tokens, tokenTurns)
	}
}

// twin: tests/extension.test.ts:804
func TestContextTraceRecordsANonZeroTokenTurnsLedgerLineAfterACompressionAndAFinishedTurn(t *testing.T) {
	c := startContext(t)
	c.judge.set(map[string]any{"retention": "summary_only"})
	got := c.result("bash", map[string]any{"command": "npm test"}, repeatLine("progress complete\n", 2000), false)
	c.storedPath(c.firstText(got))
	c.turnEnd()
	all := regexp.MustCompile(`~(\d+) token-turns spared over (\d+) turns`).FindAllStringSubmatch(c.traceText(), -1)
	if len(all) == 0 {
		t.Fatal("the trace carries a ledger line")
	}
	last := all[len(all)-1]
	if last[2] != "1" || last[1] == "0" {
		t.Errorf("the latest ledger line counts the finished turn: %v", last)
	}
}

// twin: tests/extension.test.ts:820
func TestContextIdenticalRepeatedResultBecomesADuplicateNoteWithAStoredCopyWithoutAJudgeRequest(t *testing.T) {
	c := startContext(t)
	c.judge.set(map[string]any{"retention": "all"})
	full := "unique line " + strings.Repeat("x", 3000) + "\nERROR: kept once\n"
	if got := c.result("bash", map[string]any{"command": "npm test"}, full, true); got != nil {
		t.Fatal("the first result stays")
	}
	if c.judge.count() != 1 {
		t.Fatalf("judged %d", c.judge.count())
	}
	got := c.result("bash", map[string]any{"command": "npm test"}, "\u001b[31m"+full+"\u001b[0m  ", true)
	if c.judge.count() != 1 {
		t.Errorf("a duplicate is decided by code: %d", c.judge.count())
	}
	text := c.firstText(got)
	mustMatch(t, text, `duplicate; this \d+-character, 3-line output is identical to an earlier bash result`, "note")
	path := c.storedPath(text)
	if stored, _ := os.ReadFile(path); !strings.Contains(string(stored), "ERROR: kept once") {
		t.Error("the stored copy holds the output")
	}
	mustMatch(t, text, `Do not read the whole file`, "footer")
	if !c.widgetHas(`^warden · context · bash · duplicate`) {
		t.Errorf("widget %v", c.widgetLines())
	}
	// A third copy reuses the stored file instead of writing another.
	again := c.result("read", map[string]any{"path": "log.txt"}, full, false)
	if reFullOutput.FindStringSubmatch(c.firstText(again))[1] != path {
		t.Error("a third copy reuses the stored file")
	}
	// Reading the stored copy back is a recall, never a duplicate or a compression.
	c.readCall("read", map[string]any{"path": path})
	if c.result("read", map[string]any{"path": path}, full, false) != nil {
		t.Error("a recall is left alone")
	}
	status := c.statusText()
	mustMatch(t, status, `2 duplicates dropped`, "status")
	mustMatch(t, status, `0 recalls of the full output`, "duplicate copies are not compression recalls")
	// Below duplicateMinChars nothing is replaced.
	for range 2 {
		if c.result("bash", map[string]any{"command": "ls"}, "a\nb\n", false) != nil {
			t.Error("short output replaced")
		}
	}
}

var (
	relayReport = func() string {
		l := make([]string, 40)
		for i := range l {
			l[i] = fmt.Sprintf("report line %d: module %d built and every check passed cleanly", i, i)
		}
		return strings.Join(l, "\n")
	}()
	relayTail = func() string {
		l := make([]string, 50)
		for i := range l {
			l[i] = fmt.Sprintf("turn 4 line %d: new progress since the last relay", i)
		}
		return strings.Join(l, "\n")
	}()
)

// twin: tests/extension.test.ts:856
func TestContextReportRepeatedInANewMessageOrToolResultBecomesOnePointerLine(t *testing.T) {
	c := startContext(t, func(cfg *Config) { cfg.Context.DedupeMessages = true })
	c.judge.set(map[string]any{"retention": "all"})
	c.addCustomMessage("subagent-report", "Turn 3\n"+relayReport)
	incoming := "Turn 4, with earlier turns:\n" + relayReport + "\n" + relayTail
	raw := c.host.Fire("message_end", map[string]any{"message": map[string]any{"role": "custom", "customType": "subagent-report", "content": incoming, "display": true, "timestamp": 1}})
	var res struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.Message.Role != "custom" {
		t.Fatalf("message_end result %s: %v", raw, err)
	}
	text := res.Message.Content
	m := regexp.MustCompile(`^Turn 4, with earlier turns:\n\[pi-warden: the next 40 lines repeat an earlier subagent-report message — omitted; full text: (.+)\]\nturn 4 line 0:`).FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("%.300s", text)
	}
	path := m[1]
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(path)) })
	if stored, _ := os.ReadFile(path); string(stored) != incoming {
		t.Error("the stored copy is the full original")
	}
	if !strings.HasSuffix(text, relayTail) {
		t.Error("the new part and the tail stay")
	}
	// A user message with an image: the text part is cut, the image keeps its place.
	image := map[string]any{"type": "image", "data": "AAAA", "mimeType": "image/png"}
	raw = c.host.Fire("message_end", map[string]any{"message": map[string]any{"role": "user", "content": []any{image, map[string]any{"type": "text", "text": incoming}}, "timestamp": 2}})
	var user struct {
		Message struct {
			Content []outPart `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &user); err != nil || len(user.Message.Content) != 2 {
		t.Fatalf("user result %s: %v", raw, err)
	}
	if !reflectEqualJSON(user.Message.Content[0], image) {
		t.Error("the image keeps its place")
	}
	mustMatch(t, user.Message.Content[1]["text"].(string), `the next 40 lines repeat an earlier subagent-report message`, "user text cut")
	// The same repeat in a tool result.
	tool := c.result("bash", map[string]any{"command": "cat relay.txt"}, incoming, false)
	mustMatch(t, c.firstText(tool), `the next 40 lines repeat an earlier subagent-report message — omitted; full text: `, "tool result cut")
	for _, body := range []string{user.Message.Content[1]["text"].(string), c.firstText(tool)} {
		for _, mm := range regexp.MustCompile(`full text: (.+)\]`).FindAllStringSubmatch(body, -1) {
			p := mm[1]
			t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(p)) })
		}
	}
	// Reading a stored copy back is a recall and returns the full text unchanged.
	c.readCall("read", map[string]any{"path": path})
	if c.result("read", map[string]any{"path": path}, incoming, false) != nil {
		t.Error("a recall is left alone")
	}
	// An assistant reply is never rewritten.
	raw = c.host.Fire("message_end", map[string]any{"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": incoming}}}})
	if len(raw) != 0 && string(raw) != "null" {
		t.Errorf("assistant message rewritten: %s", raw)
	}
	status := c.statusText()
	mustMatch(t, status, `3 repeats cut`, "status")
	mustMatch(t, status, `1 recall of the full output`, "status recall")
}

// twin: tests/extension.test.ts:894
func TestContextMessagesStayWholeByDefaultWhileToolResultsAreCut(t *testing.T) {
	c := startContext(t)
	c.judge.set(map[string]any{"retention": "all"})
	c.addCustomMessage("subagent-report", "Turn 3\n"+relayReport)
	incoming := "Turn 4\n" + relayReport + "\n" + relayTail
	for _, msg := range []map[string]any{{"role": "custom", "customType": "subagent-report", "content": incoming, "display": true, "timestamp": 1}, {"role": "user", "content": incoming, "timestamp": 2}} {
		if raw := c.host.Fire("message_end", map[string]any{"message": msg}); len(raw) != 0 && string(raw) != "null" {
			t.Errorf("message rewritten by default: %s", raw)
		}
	}
	tool := c.result("bash", map[string]any{"command": "cat relay.txt"}, incoming, false)
	mustMatch(t, c.firstText(tool), `the next 40 lines repeat an earlier subagent-report message — omitted; full text: `, "tool result cut")
	c.storedPath(c.firstText(tool) + "\nFull output: " + regexp.MustCompile(`full text: (.+)\]`).FindStringSubmatch(c.firstText(tool))[1])
}

// twin: tests/extension.test.ts:906
func TestContextDedupeRunsFalseKeepsRepeatedRunsInMessagesAndToolResultsEvenWithDedupeMessagesOn(t *testing.T) {
	c := startContext(t, func(cfg *Config) { cfg.Context.DedupeRuns, cfg.Context.DedupeMessages = false, true })
	c.judge.set(map[string]any{"retention": "all"})
	c.addCustomMessage("subagent-report", "Turn 3\n"+relayReport)
	incoming := "Turn 4\n" + relayReport + "\n" + relayTail
	if raw := c.host.Fire("message_end", map[string]any{"message": map[string]any{"role": "custom", "customType": "subagent-report", "content": incoming, "display": true, "timestamp": 1}}); len(raw) != 0 && string(raw) != "null" {
		t.Errorf("message rewritten: %s", raw)
	}
	if c.result("bash", map[string]any{"command": "cat relay.txt"}, incoming, false) != nil {
		t.Error("tool result rewritten")
	}
}

// twin: tests/extension.test.ts:915
func TestContextRecallKindsAScopedSearchKeepsTheSavingAWholeFileReadIsCountedAsSuch(t *testing.T) {
	c := startContext(t, func(cfg *Config) { cfg.Context.RecallTool = "grep" })
	c.judge.set(map[string]any{"retention": "summary_only"})
	first := c.result("bash", map[string]any{"command": "npm test"}, repeatLine("progress complete\n", 2000), false)
	text := c.firstText(first)
	path := c.storedPath(text)
	mustMatch(t, text, `grep -n -C 3 -E '<pattern>'`, "the configured tool is named without probing")
	c.toolCall("bash", "call-s", map[string]any{"command": fmt.Sprintf("grep -n -C 3 -E 'error' '%s'", path)})
	if !c.widgetHas(`full output recalled \(scoped\)`) {
		t.Errorf("widget %v", c.widgetLines())
	}
	mustMatch(t, c.statusText(), `1 recall of the full output \(100%; 0 whole-file, 1 scoped\)`, "status")
}

// beforeCompact is a compaction event with a two-call span; the host's cut point and token count ride through unchanged.
func beforeCompact() map[string]any {
	return map[string]any{
		"preparation": map[string]any{
			"firstKeptEntryId": "entry-42", "tokensBefore": 123456, "isSplitTurn": false, "turnPrefixMessages": []any{},
			"fileOps": map[string]any{"read": []any{"src/a.ts"}, "written": []any{}, "edited": []any{"src/b.ts"}},
			"messagesToSummarize": []any{
				map[string]any{"role": "user", "content": "Fix the build"},
				map[string]any{"role": "assistant", "content": []any{
					map[string]any{"type": "toolCall", "id": "c1", "name": "read", "arguments": map[string]any{"path": "src/a.ts"}},
					map[string]any{"type": "toolCall", "id": "c2", "name": "bash", "arguments": map[string]any{"command": "npm run build"}},
				}},
				map[string]any{"role": "toolResult", "toolCallId": "c1", "toolName": "read", "content": []any{map[string]any{"type": "text", "text": "export const a = 1;"}}, "isError": false},
				map[string]any{"role": "toolResult", "toolCallId": "c2", "toolName": "bash", "content": []any{map[string]any{"type": "text", "text": "build log noise"}}, "isError": false},
			},
		},
		"branchEntries": []any{}, "reason": "threshold", "willRetry": false,
	}
}

func (c *ctxHost) setProvider(provider string) {
	c.host.Notify("state_update", map[string]any{"state": map[string]any{"model": map[string]any{"id": "m", "provider": provider}}})
	time.Sleep(30 * time.Millisecond)
}

func (c *ctxHost) compact() map[string]any {
	c.t.Helper()
	raw := c.host.Fire("session_before_compact", beforeCompact())
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		c.t.Fatalf("%s: %v", raw, err)
	}
	return out
}

// twin: tests/extension.test.ts:3815
func TestContextBeforeCompactOffByDefaultPisSummaryRunsAndNothingIsSent(t *testing.T) {
	c := startContext(t)
	if c.compact() != nil || c.judge.count() != 0 {
		t.Fatal("a compaction ran with compaction.enabled off")
	}
}

// twin: tests/extension.test.ts:3821
func TestContextBeforeCompactEnabledKeptOutputReplacesPisSummaryWithPisCutPointUnchanged(t *testing.T) {
	c := startContext(t, func(cfg *Config) { cfg.Compaction.Enabled = true })
	c.judge.set(map[string]any{"u1": 0.9, "u2": 0.2})
	got := c.compact()
	if got == nil {
		t.Fatal("Pi's summary ran")
	}
	compaction := got["compaction"].(map[string]any)
	if compaction["firstKeptEntryId"] != "entry-42" || compaction["tokensBefore"] != float64(123456) {
		t.Errorf("%v", compaction)
	}
	summary := compaction["summary"].(string)
	mustMatch(t, summary, `^pi-warden relevance compaction`, "header")
	mustMatch(t, summary, `export const a = 1;`, "kept")
	mustNotContain(t, summary, "build log noise", "dropped")
	mustMatch(t, summary, `Files read:\n- src/a\.ts\n\nFiles modified:\n- src/b\.ts`, "files")
	if c.judge.count() != 1 {
		t.Fatalf("%d requests", c.judge.count())
	}
	var ids []string
	for id := range c.judge.requests[0].Questions {
		ids = append(ids, id)
	}
	mustEqual(t, sortedCopy(ids), []string{"u1", "u2"}, "questions")
	mustMatch(t, c.statusText(), `Relevance compaction: 1 compaction, 1 replaced Pi's summary\. Last: kept 1 of 2 scored units, 1 request`, "status")
}

// twin: tests/extension.test.ts:3837 (the aborted-request leg is not drivable through the fake host; relevance_test.go covers it)
func TestContextBeforeCompactNoConsentASkippedProviderOrAJudgeFailureReturnNothingAndNeverCancel(t *testing.T) {
	c := startContext(t, func(cfg *Config) { cfg.Compaction.Enabled = true })
	c.w.mu.Lock()
	c.w.cfg.Consent = BackendNone
	c.w.mu.Unlock()
	if c.compact() != nil || c.judge.count() != 0 {
		t.Fatal("no consent: nothing is sent")
	}
	c.w.mu.Lock()
	c.w.cfg.Consent = BackendTypeSafe
	c.w.mu.Unlock()
	c.setProvider("claude-bridge")
	if c.compact() != nil || c.judge.count() != 0 {
		t.Fatal("a provider in skipProviders keeps its own compaction")
	}
	c.setProvider("prov")
	c.judge.fail = &IntegrationError{Code: "http", Message: "TypeSafe request failed."}
	if c.compact() != nil {
		t.Fatal("a judge failure returns nothing")
	}
	mustMatch(t, c.statusText(), `Relevance compaction: 3 compactions, 0 replaced Pi's summary, Pi's summary ran instead \(judgments off 1, skipped 1, judge error 1\)`, "status")
	found := false
	for _, n := range c.notices() {
		found = found || strings.Contains(n, "Relevance compaction failed")
	}
	if !found {
		t.Error("a judge error is announced like other TypeSafe errors")
	}
}

// twin: tests/extension.test.ts:3855
func TestContextBeforeCompactInsideTheRequestReservePisSummaryRunsNothingIsSentAndJudgmentsStayOn(t *testing.T) {
	c := startContext(t, func(cfg *Config) { cfg.MaxRequests, cfg.Compaction.Enabled = 50, true })
	if c.compact() != nil || c.judge.count() != 0 {
		t.Fatal("one request would leave 49 of 50")
	}
	for _, n := range c.notices() {
		if strings.Contains(n, "Pattern checks continue without TypeSafe") {
			t.Error("the budget is not marked as spent")
		}
	}
	c.toolCall("bash", "call-1", map[string]any{"command": "npm publish"})
	if c.judge.count() == 0 {
		t.Error("the action guard still asks the judge")
	}
	mustMatch(t, c.statusText(), `Pi's summary ran instead \(budget 1\)`, "status")
}

// ── Context filter (beta) ──

func filterLogText() string {
	lines := make([]string, 1000)
	for i := range lines {
		lines[i] = fmt.Sprintf("progress %d complete", i)
		if i == 250 {
			lines[i] = "FAIL parser.test.ts > keeps the header: expected 3 to equal 4"
		}
	}
	return strings.Join(lines, "\n") + "\nsummary: 1 failed\nexit code 1"
}

func (c *ctxHost) filterQuestions() []string {
	var ids []string
	c.judge.mu.Lock()
	defer c.judge.mu.Unlock()
	for _, r := range c.judge.requests {
		for id := range r.Questions {
			if regexp.MustCompile(`^c\d+$`).MatchString(id) {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// twin: tests/extension.test.ts:5100
func TestContextFilterOffByDefaultTheGenericExcerptIsUsedAndNoFilterQuestionIsAsked(t *testing.T) {
	c := startContext(t)
	c.judge.set(map[string]any{"retention": "errors_and_summary", "format": "other"})
	got := c.result("bash", map[string]any{"command": "npm test"}, filterLogText(), true)
	text := c.firstText(got)
	c.storedPath(text)
	mustMatch(t, text, `^\[pi-warden: errors_and_summary; .*Excerpts only;`, "excerpt")
	if c.judge.count() != 1 || len(c.filterQuestions()) != 0 {
		t.Errorf("only the output check: %d", c.judge.count())
	}
	mustNotContain(t, c.statusText(), "Context filter", "status adds no filter line while it is off and unused")
}

// twin: tests/extension.test.ts:5114
func TestContextFilterOnThePassagesThatPassReplaceTheExcerptWithTheFooterLedgerTraceAndRecallCounts(t *testing.T) {
	c := startContext(t, func(cfg *Config) { cfg.Context.Filter.Enabled = true })
	full := filterLogText()
	// The failing line sits in the 3rd 2000-character chunk; the fake judge rates only that one as useful.
	c.judge.set(map[string]any{"retention": "errors_and_summary", "format": "other", "c3": 3.0})
	got := c.result("bash", map[string]any{"command": "npm test"}, full, true)
	text := c.firstText(got)
	path := c.storedPath(text)
	mustMatch(t, text, fmt.Sprintf(`^\[pi-warden: filtered; %d original characters, %d lines\. Passages selected for the current task; omitted text is in the full-output file\.\]\n\[… \d+ lines omitted …\]\n`, utf16Len(full), strings.Count(full, "\n")+1), "header")
	mustMatch(t, text, `FAIL parser\.test\.ts > keeps the header: expected 3 to equal 4`, "failing line")
	if !strings.Contains(text, utf16Slice(full, utf16Len(full)-1000, -1)) {
		t.Error("the final status is kept")
	}
	mustNotContain(t, text, "progress 5 complete", "an unrelated chunk is omitted")
	mustMatch(t, text, `Do not read the whole file`, "the same footer as the excerpt")
	if stored, _ := os.ReadFile(path); string(stored) != full {
		t.Error("stored copy")
	}
	var filterRequests []Request
	for _, r := range c.judge.requests {
		if has(r.Questions, "c1") {
			filterRequests = append(filterRequests, r)
		}
	}
	if len(filterRequests) != 1 {
		t.Fatalf("a 12k output fits one request: %d", len(filterRequests))
	}
	if filterRequests[0].State["task"] != "Run the test suite" && filterRequests[0].State["task"] != "(no user request)" {
		t.Errorf("task %v", filterRequests[0].State["task"])
	}
	if filterRequests[0].State["command"] != "npm test" || filterRequests[0].Questions["c1"].Type != "score" {
		t.Errorf("request %v", filterRequests[0].State["command"])
	}
	c.toolCall("bash", "call-s", map[string]any{"command": "rg FAIL " + path})
	mustMatch(t, c.statusText(), `Context filter \(beta\): 1 filtered \(\d+ characters kept\), 1 recalled \(100%; 0 whole-file, 1 scoped\); 0 excerpts, 0 recalled \(n/a; 0 whole-file, 0 scoped\); 1 request, \d+ ms; fallbacks: none\.`, "status")
	mustMatch(t, c.traceText(), `context filter: kept 1 of \d+ chunks, \d+ of \d+ characters; 1 request; \d+ ms`, "trace")
}

// twin: tests/extension.test.ts:5143
func TestContextFilterNoPassingChunkFallsBackToTheExcerptRetentionAllAndDuplicatesNeverReachIt(t *testing.T) {
	c := startContext(t, func(cfg *Config) { cfg.Context.Filter.Enabled = true })
	c.judge.set(map[string]any{"retention": "errors_and_summary", "format": "other"})
	full := filterLogText()
	got := c.result("bash", map[string]any{"command": "npm test"}, full, true)
	text := c.firstText(got)
	c.storedPath(text)
	mustMatch(t, text, `^\[pi-warden: errors_and_summary; .*Excerpts only;`, "today's excerpt, unchanged")
	mustMatch(t, c.traceText(), `context filter fell back to the excerpt: none_passed; \d+ chunks; 1 request; \d+ ms`, "fallback")
	asked := len(c.filterQuestions())
	c.judge.mu.Lock()
	c.judge.requests = nil
	c.judge.mu.Unlock()
	c.result("bash", map[string]any{"command": "npm test"}, full, true)
	if len(c.filterQuestions()) != 0 {
		t.Error("a duplicate is dropped by code, never filtered")
	}
	c.judge.set(map[string]any{"retention": "all", "format": "other", "c3": 3.0})
	if c.result("bash", map[string]any{"command": "npm test -- --verbose"}, strings.Replace(full, "summary", "totals", 1), true) != nil {
		t.Error("retention all keeps the output whole")
	}
	if len(c.filterQuestions()) != 0 {
		t.Error("retention all never reaches the filter")
	}
	mustMatch(t, c.statusText(), `Context filter \(beta\): 0 filtered .*; 1 excerpts, .*; 1 request, \d+ ms; fallbacks: none_passed 1\.`, "status")
	if asked == 0 {
		t.Error("no filter question asked")
	}
}

// twin: tests/extension.test.ts:5167
func TestContextFilterAFailedJudgeKeepsTheExcerptAndCountsTheFallback(t *testing.T) {
	c := startContext(t, func(cfg *Config) {
		cfg.Context.Filter.Enabled, cfg.Context.Filter.TimeoutMs, cfg.Security.Enabled = true, 200, false
	})
	c.judge.set(map[string]any{"retention": "errors_and_summary", "format": "other", "c3": 3.0})
	// The output check answers; every later request fails upstream.
	c.judge.failAfter = 1
	got := c.result("bash", map[string]any{"command": "npm test"}, filterLogText(), true)
	text := c.firstText(got)
	c.storedPath(text)
	mustMatch(t, text, `^\[pi-warden: errors_and_summary; .*Excerpts only;`, "excerpt")
	mustMatch(t, c.statusText(), `fallbacks: (?:error|timeout) 1\.`, "status")
}
