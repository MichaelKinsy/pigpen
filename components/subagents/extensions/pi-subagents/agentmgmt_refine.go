package pi_subagents

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Agent refinements (src/agents/agent-refinements.ts): a project-local overlay of guidance, proposed by a read-only child from
// bounded recent evidence, appended to an agent's system prompt, with snapshots to roll back to.

const (
	refinementFormatVersion = 1
	refinementDir           = "refinements"
	refinementCurrentFence  = "pi-subagents-refinement-current"
	refinementSnapshotFence = "pi-subagents-refinement-snapshots-json"
	maxEvidenceItems        = 8
	maxEvidenceAgeDays      = 14
	maxEvidenceItemBytes    = 2048
	maxEvidencePacketBytes  = 16384
	refinementProposalAgent = "reviewer"
)

func projectSubagentsDir(cwd string) string { return filepath.Join(cwd, ".pi", "subagents") }

// formatUnknownAgentError renders local discovery evidence without exposing filesystem error details (agents.ts:338).
func formatUnknownAgentError(name string, d Discovery, prefix string) string {
	var dirs []string
	for _, dir := range d.Directories {
		state := dir.State
		switch dir.State {
		case "candidates":
			plural := "s"
			if dir.CandidateCount == 1 {
				plural = ""
			}
			state = fmt.Sprintf("%d candidate%s", dir.CandidateCount, plural)
		case "empty":
			state = "present but empty"
		case "not-directory":
			state = "not a directory"
		}
		dirs = append(dirs, fmt.Sprintf("- %s: %s (%s)", dir.Source, dir.Path, state))
	}
	agents := append([]AgentConfig(nil), d.Agents...)
	sort.SliceStable(agents, func(i, j int) bool {
		if agents[i].Name != agents[j].Name {
			return localeLess(agents[i].Name, agents[j].Name)
		}
		return localeLess(string(agents[i].Source), string(agents[j].Source))
	})
	var names []string
	for _, a := range agents {
		names = append(names, fmt.Sprintf("- %s (%s)", a.Name, a.Source))
	}
	if len(dirs) == 0 {
		dirs = []string{"- (none)"}
	}
	if len(names) == 0 {
		names = []string{"- (none)"}
	}
	cwd, _ := filepath.Abs(d.Cwd)
	lines := []string{prefix + ": " + name, "Effective cwd: " + cwd, "Consulted agent-definition directories:"}
	lines = append(lines, dirs...)
	lines = append(lines, "Discovered agents:")
	return strings.Join(append(lines, names...), "\n")
}

var refinementFileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func hashPrompt(systemPrompt string) string {
	sum := sha256.Sum256([]byte(systemPrompt))
	return hex.EncodeToString(sum[:])
}

func getAgentRefinementPath(cwd, agentName string) (string, error) {
	trimmed := jsTrim(agentName)
	if !refinementFileNameRe.MatchString(trimmed) || strings.Contains(trimmed, "..") {
		return "", fmt.Errorf("Agent name '%s' cannot be used as a refinement file name.", agentName)
	}
	root, _ := filepath.Abs(filepath.Join(projectSubagentsDir(cwd), refinementDir))
	return filepath.Join(root, trimmed+".md"), nil
}

// refinementSnapshot is RefinementSnapshot.
type refinementSnapshot struct {
	Revision      int
	At, Action    string
	Before, After string
	EvidenceIDs   []string
	ProposalAgent string
}

type refinementFile struct {
	Agent                           string
	Revision                        int
	UpdatedAt                       string
	BaseSource, BaseFile, BaseSHA   string
	MaxItems, MaxAgeDays, ItemBytes float64
	TotalBytes                      float64
	Current                         string
	Snapshots                       []refinementSnapshot
}

func trimmedText(v any) string {
	if s, ok := v.(string); ok {
		return jsTrim(s)
	}
	return ""
}

func textList(v any) []string {
	arr, _ := v.([]any)
	var out []string
	for _, e := range arr {
		if s := trimmedText(e); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func wholeInt(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok || f != float64(int(f)) {
		return 0, false
	}
	return int(f), true
}

func extractFence(markdown, fence string) (string, bool) {
	re := regexp.MustCompile("(?s)\\n```" + regexp.QuoteMeta(fence) + "\\n(.*?)\\n```")
	m := re.FindStringSubmatch(markdown)
	if m == nil {
		return "", false
	}
	return m[1], true
}

var refinementMetadataRe = regexp.MustCompile(`(?s)^<!-- pi-subagents-refinement:v1\n(.*?)\n-->\n`)

func parseRefinementFile(markdown, label string) (*refinementFile, error) {
	m := refinementMetadataRe.FindStringSubmatch(markdown)
	if m == nil || m[1] == "" {
		return nil, fmt.Errorf("%s is missing refinement metadata.", label)
	}
	value, err := parseJSON([]byte(m[1]))
	if err != nil {
		return nil, err
	}
	meta, ok := value.(*jsObject)
	if !ok {
		return nil, fmt.Errorf("%s metadata must be an object.", label)
	}
	agent, updatedAt := trimmedText(meta.vals["agent"]), trimmedText(meta.vals["updatedAt"])
	revision, revOK := wholeInt(meta.vals["revision"])
	base, evidence := meta.obj("base"), meta.obj("evidence")
	if agent == "" || !revOK || updatedAt == "" || base == nil || evidence == nil {
		return nil, fmt.Errorf("%s metadata is invalid.", label)
	}
	source, filePath, sha := trimmedText(base.vals["source"]), trimmedText(base.vals["filePath"]), trimmedText(base.vals["systemPromptSha256"])
	if source != "builtin" && source != "package" && source != "user" && source != "project" {
		return nil, fmt.Errorf("%s base source is invalid.", label)
	}
	if filePath == "" || sha == "" {
		return nil, fmt.Errorf("%s base metadata is invalid.", label)
	}
	current, ok := extractFence(markdown, refinementCurrentFence)
	if !ok {
		return nil, fmt.Errorf("%s is missing current refinement block.", label)
	}
	rawSnaps, ok := extractFence(markdown, refinementSnapshotFence)
	if !ok {
		rawSnaps = "[]"
	}
	sv, err := parseJSON([]byte(rawSnaps))
	if err != nil {
		return nil, err
	}
	arr, ok := sv.([]any)
	if !ok {
		return nil, fmt.Errorf("%s snapshots must be an array.", label)
	}
	file := &refinementFile{Agent: agent, Revision: revision, UpdatedAt: updatedAt, BaseSource: source, BaseFile: filePath, BaseSHA: sha, Current: current,
		MaxItems: maxEvidenceItems, MaxAgeDays: maxEvidenceAgeDays, ItemBytes: maxEvidenceItemBytes, TotalBytes: maxEvidencePacketBytes}
	for k, dst := range map[string]*float64{"maxItems": &file.MaxItems, "maxAgeDays": &file.MaxAgeDays, "itemBytes": &file.ItemBytes, "totalBytes": &file.TotalBytes} {
		if n, ok := evidence.num(k); ok {
			*dst = n
		}
	}
	for i, e := range arr {
		item, ok := e.(*jsObject)
		if !ok {
			return nil, fmt.Errorf("%s snapshot %d must be an object.", label, i)
		}
		action, at := trimmedText(item.vals["action"]), trimmedText(item.vals["at"])
		before, bok := item.vals["before"].(string)
		after, aok := item.vals["after"].(string)
		rev, rok := wholeInt(item.vals["revision"])
		if !rok || at == "" || (action != "refine" && action != "rollback") || !bok || !aok {
			return nil, fmt.Errorf("%s snapshot %d is invalid.", label, i)
		}
		file.Snapshots = append(file.Snapshots, refinementSnapshot{Revision: rev, At: at, Action: action, Before: before, After: after,
			EvidenceIDs: textList(item.vals["evidenceIds"]), ProposalAgent: trimmedText(item.vals["proposalAgent"])})
	}
	return file, nil
}

func (f *refinementFile) metadataObject() *jsObject {
	base := obj("source", f.BaseSource, "filePath", f.BaseFile, "systemPromptSha256", f.BaseSHA)
	evidence := obj("maxItems", f.MaxItems, "maxAgeDays", f.MaxAgeDays, "itemBytes", f.ItemBytes, "totalBytes", f.TotalBytes)
	return obj("agent", f.Agent, "revision", float64(f.Revision), "updatedAt", f.UpdatedAt, "base", base, "evidence", evidence)
}

func (f *refinementFile) serialize() string {
	snaps := []any{}
	for _, s := range f.Snapshots {
		ids := []any{}
		for _, id := range s.EvidenceIDs {
			ids = append(ids, id)
		}
		o := obj("revision", float64(s.Revision), "at", s.At, "action", s.Action, "before", s.Before, "after", s.After, "evidenceIds", ids)
		if s.ProposalAgent != "" {
			o.set("proposalAgent", s.ProposalAgent)
		}
		snaps = append(snaps, o)
	}
	return strings.Join([]string{
		fmt.Sprintf("<!-- pi-subagents-refinement:v%d", refinementFormatVersion),
		marshalJSON(f.metadataObject(), "  "),
		"-->",
		"",
		fmt.Sprintf("# Current refinement for `%s`", f.Agent),
		"",
		"```" + refinementCurrentFence,
		f.Current,
		"```",
		"",
		"# Snapshots",
		"",
		"```" + refinementSnapshotFence,
		marshalJSON(snaps, "  "),
		"```",
		"",
	}, "\n")
}

func writeRefinementFile(path string, f *refinementFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.%d.tmp", path, os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(tmp, []byte(f.serialize()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func newRefinementFile(a *AgentConfig, revision int, now string) *refinementFile {
	return &refinementFile{Agent: a.Name, Revision: revision, UpdatedAt: now, BaseSource: string(a.Source), BaseFile: a.FilePath, BaseSHA: hashPrompt(a.SystemPrompt),
		MaxItems: maxEvidenceItems, MaxAgeDays: maxEvidenceAgeDays, ItemBytes: maxEvidenceItemBytes, TotalBytes: maxEvidencePacketBytes}
}

// refinementEvidence is RefinementEvidenceItem.
type refinementEvidence struct {
	ID, Source, RunID, Agent, At, Status, Model, Thinking, AcceptanceStatus string
	ReviewFindings, ResidualRisks, Errors, ControlSignals                  []string
	OutputTail                                                             string
	Refs                                                                   []string
}

func (e refinementEvidence) object() *jsObject {
	o := obj("id", e.ID, "source", e.Source)
	str := func(k, v string) {
		if v != "" {
			o.set(k, v)
		}
	}
	list := func(k string, v []string) {
		if len(v) > 0 {
			arr := make([]any, len(v))
			for i, s := range v {
				arr[i] = s
			}
			o.set(k, arr)
		}
	}
	str("runId", e.RunID)
	o.set("agent", e.Agent)
	str("at", e.At)
	str("status", e.Status)
	str("model", e.Model)
	str("thinking", e.Thinking)
	str("acceptanceStatus", e.AcceptanceStatus)
	list("reviewFindings", e.ReviewFindings)
	list("residualRisks", e.ResidualRisks)
	list("errors", e.Errors)
	list("controlSignals", e.ControlSignals)
	str("outputTail", e.OutputTail)
	refs := make([]any, len(e.Refs))
	for i, r := range e.Refs {
		refs[i] = r
	}
	o.set("refs", refs)
	return o
}

func isoTimeOf(value any) string {
	switch v := value.(type) {
	case float64:
		return time.UnixMilli(int64(v)).UTC().Format("2006-01-02T15:04:05.000Z")
	case string:
		s := jsTrim(v)
		if s == "" {
			return ""
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000Z", "2006-01-02"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UTC().Format("2006-01-02T15:04:05.000Z")
			}
		}
	}
	return ""
}

func withinEvidenceAge(at string) bool {
	if at == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	return err == nil && time.Since(t) <= maxEvidenceAgeDays*24*time.Hour
}

func tailBytes(value string, max int) string {
	if len(value) <= max {
		return value
	}
	tail := []byte(value[len(value)-max:])
	for len(tail) > 0 && tail[0]&0xC0 == 0x80 {
		tail = tail[1:]
	}
	return string(tail)
}

func acceptanceEvidence(value any, item *refinementEvidence) {
	ledger, ok := value.(*jsObject)
	if !ok {
		return
	}
	var findings []string
	if report := ledger.obj("childReport"); report != nil {
		findings = append(findings, textList(report.vals["reviewFindings"])...)
		item.ResidualRisks = textList(report.vals["residualRisks"])
	}
	if review := ledger.obj("reviewResult"); review != nil {
		if arr, ok := review.vals["findings"].([]any); ok {
			for _, f := range arr {
				if o, ok := f.(*jsObject); ok {
					if s := trimmedText(o.vals["issue"]); s != "" {
						findings = append(findings, s)
					}
				}
			}
		}
	}
	item.AcceptanceStatus = trimmedText(ledger.vals["status"])
	item.ReviewFindings = findings
}

func controlSignalsOf(v any) []string {
	arr, _ := v.([]any)
	var out []string
	for _, e := range arr {
		o, ok := e.(*jsObject)
		if !ok {
			continue
		}
		message, reason := trimmedText(o.vals["message"]), trimmedText(o.vals["reason"])
		if message == "" {
			continue
		}
		if reason != "" {
			message = reason + ": " + message
		}
		out = append(out, message)
	}
	return out
}

// collectBoundedRefinementEvidence gathers the recent artifact metadata of one agent (at most 8 items, 14 days, bounded
// bytes). The live state of background runs is not part of this port (it has no background runs).
func collectBoundedRefinementEvidence(cwd, agentName string) []refinementEvidence {
	var items []refinementEvidence
	dir := filepath.Join(projectSubagentsDir(cwd), "artifacts")
	entries, err := os.ReadDir(dir)
	if err == nil {
		type fileTime struct {
			path string
			mod  time.Time
		}
		var files []fileTime
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), "_meta.json") {
				if info, err := e.Info(); err == nil {
					files = append(files, fileTime{filepath.Join(dir, e.Name()), info.ModTime()})
				}
			}
		}
		sort.SliceStable(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
		for _, f := range files {
			if len(items) >= maxEvidenceItems {
				break
			}
			b, err := os.ReadFile(f.path)
			if err != nil {
				continue
			}
			v, err := parseJSON(b)
			meta, ok := v.(*jsObject)
			if err != nil || !ok || trimmedText(meta.vals["agent"]) != agentName {
				continue
			}
			var stamp any = float64(f.mod.UnixMilli())
			if t, has := meta.vals["timestamp"]; has && t != nil {
				stamp = t
			}
			at := isoTimeOf(stamp)
			if !withinEvidenceAge(at) {
				continue
			}
			outputPath := strings.TrimSuffix(f.path, "_meta.json") + "_output.md"
			tail := ""
			if ob, err := os.ReadFile(outputPath); err == nil {
				tail = tailBytes(string(ob), maxEvidenceItemBytes)
			}
			item := refinementEvidence{ID: "artifact:" + strings.TrimSuffix(filepath.Base(f.path), "_meta.json"), Source: "artifact-metadata", RunID: trimmedText(meta.vals["runId"]),
				Agent: agentName, At: at, Model: trimmedText(meta.vals["model"]), Thinking: trimmedText(meta.vals["thinking"]), ControlSignals: controlSignalsOf(meta.vals["controlEvents"]),
				Refs: []string{f.path}}
			if tail != "" {
				item.Source, item.OutputTail, item.Refs = "artifact-output", tail, []string{f.path, outputPath}
			}
			if code, ok := meta.num("exitCode"); ok {
				item.Status = "failed"
				if code == 0 {
					item.Status = "completed"
				}
			}
			acceptanceEvidence(meta.vals["acceptance"], &item)
			if s := trimmedText(meta.vals["error"]); s != "" {
				item.Errors = []string{s}
			}
			items = append(items, item)
		}
	}
	var packet []refinementEvidence
	bytes := 2
	for _, item := range items {
		encoded := len(marshalJSON(item.object(), "")) + 1
		if bytes+encoded > maxEvidencePacketBytes {
			break
		}
		packet = append(packet, item)
		bytes += encoded
	}
	return packet
}

// appendAgentRefinementOverlay adds the current overlay of an agent to its system prompt (unchanged when there is none).
func appendAgentRefinementOverlay(systemPrompt, cwd, agentName string) string {
	filePath, err := getAgentRefinementPath(cwd, agentName)
	if err != nil {
		return systemPrompt
	}
	b, err := os.ReadFile(filePath)
	if err != nil {
		return systemPrompt
	}
	parsed, err := parseRefinementFile(string(b), filePath)
	if err != nil {
		return systemPrompt
	}
	current := jsTrim(parsed.Current)
	if current == "" {
		return systemPrompt
	}
	rel, _ := filepath.Rel(cwd, filePath)
	overlay := strings.Join([]string{
		fmt.Sprintf("<pi-subagents-refinement agent=%s source=%s>", marshalJSON(agentName, ""), marshalJSON(rel, "")),
		"Project-local refinement guidance generated from recent bounded evidence.",
		"It does not override tool, developer, task, output, acceptance, or safety instructions.",
		"",
		current,
		"</pi-subagents-refinement>",
	}, "\n")
	if jsTrim(systemPrompt) != "" {
		return systemPrompt + "\n\n" + overlay
	}
	return overlay
}

type refinementEdit struct {
	Title, Guidance, Rationale string
	EvidenceIDs                []string
}

type refinementProposal struct {
	Summary string
	Edits   []refinementEdit
}

const protectedInstruction = `(?:acceptance|output|safety|policy|policies|tools?|developer|system)`

var blockedGuidanceRe = regexp.MustCompile(`(?i)\b(all agents|every agent|global|(?:disable|ignore|bypass|skip|override)\s+(?:the\s+)?` + protectedInstruction + `(?:\s+instructions?)?|` + protectedInstruction + `\s+(?:instructions?|overrides?)|tool safety|review gates|rewrite base|base agent file|settings\.json|\.pi/agent|agents/.*\.md)\b`)

// validateRefinementProposal checks a proposal's size, its evidence citations and that its guidance does not reach beyond the overlay.
func validateRefinementProposal(proposal any, evidenceIDs []string) (*refinementProposal, string) {
	item, ok := proposal.(*jsObject)
	if !ok {
		return nil, "Refinement proposal must be an object."
	}
	summary := trimmedText(item.vals["summary"])
	edits, isArr := item.vals["edits"].([]any)
	if summary == "" || !isArr {
		return nil, "Refinement proposal requires summary and edits."
	}
	if len(edits) > 3 {
		return nil, "Refinement proposal may contain at most 3 edits."
	}
	allowed := map[string]bool{}
	for _, id := range evidenceIDs {
		allowed[id] = true
	}
	out := &refinementProposal{Summary: summary}
	for i, ev := range edits {
		edit, ok := ev.(*jsObject)
		if !ok {
			return nil, fmt.Sprintf("Refinement proposal edit %d must be an object.", i)
		}
		title, guidance, rationale := trimmedText(edit.vals["title"]), trimmedText(edit.vals["guidance"]), trimmedText(edit.vals["rationale"])
		cited := textList(edit.vals["evidenceIds"])
		if title == "" || guidance == "" || rationale == "" {
			return nil, fmt.Sprintf("Refinement proposal edit %d is missing title, guidance, or rationale.", i)
		}
		unknown := false
		for _, id := range cited {
			unknown = unknown || !allowed[id]
		}
		if len(cited) == 0 || unknown {
			return nil, fmt.Sprintf("Refinement proposal edit %d must cite known evidence ids.", i)
		}
		if strings.Contains(guidance, "```") || strings.Contains(guidance, "</pi-subagents-refinement>") || blockedGuidanceRe.MatchString(guidance) {
			return nil, fmt.Sprintf("Refinement proposal edit %d contains disallowed guidance.", i)
		}
		out.Edits = append(out.Edits, refinementEdit{Title: title, Guidance: guidance, Rationale: rationale, EvidenceIDs: cited})
	}
	return out, ""
}

// proposalChildResult is the part of a proposal child's result the action reads.
type proposalChildResult struct {
	IsError bool
	// Structured is the first structured output of the child (nil: none), FinalOutputs its texts.
	Structured   any
	FinalOutputs []string
	Content      []string
}

// proposalSchema is the JSON Schema a proposal child's output must match.
func proposalSchema() *jsObject {
	str := obj("type", "string")
	strList := obj("type", "array", "items", obj("type", "string"))
	edit := obj("type", "object", "additionalProperties", false,
		"required", []any{"title", "guidance", "evidenceIds", "rationale"},
		"properties", obj("title", str, "guidance", str, "evidenceIds", obj("type", "array", "items", obj("type", "string"), "minItems", float64(1)), "rationale", str))
	return obj("type", "object", "additionalProperties", false, "required", []any{"summary", "edits", "residualRisks"},
		"properties", obj("summary", str, "edits", obj("type", "array", "maxItems", float64(3), "items", edit), "rejectedIdeas", strList, "residualRisks", strList))
}

var (
	proposalFenceRe = regexp.MustCompile("(?s)```(?:json)?\\n(.*?)\\n```")
	proposalBraceRe = regexp.MustCompile(`(?s)(\{.*\})`)
)

func proposalFromChild(c proposalChildResult) any {
	if c.Structured != nil {
		return c.Structured
	}
	output := ""
	for _, s := range append(append([]string(nil), c.FinalOutputs...), c.Content...) {
		if jsTrim(s) != "" {
			output = s
			break
		}
	}
	m := proposalFenceRe.FindStringSubmatch(output)
	if m == nil {
		m = proposalBraceRe.FindStringSubmatch(output)
	}
	if m == nil || m[1] == "" {
		return nil
	}
	v, err := parseJSON([]byte(m[1]))
	if err != nil {
		return nil
	}
	return v
}

func proposalTask(a *AgentConfig, current string, evidence []refinementEvidence) string {
	arr := make([]any, len(evidence))
	for i, e := range evidence {
		arr[i] = e.object()
	}
	if jsTrim(current) == "" {
		current = "(none)"
	} else {
		current = jsTrim(current)
	}
	return strings.Join([]string{
		fmt.Sprintf("You are a fresh read-only proposal child for project-local refinement of one subagent: %s.", a.Name),
		"Do not read files. Do not use write, edit, or shell tools. Use only the evidence packet below.",
		"Propose at most 2-3 minimal overlay guidance edits.",
		"Each edit must cite one or more evidence ids from the packet.",
		"Do not propose base agent file, settings, global, automatic, tool, safety, output, acceptance, developer, or system instruction changes.",
		"Return only structured output that matches the schema.",
		"",
		"Agent metadata:",
		marshalJSON(obj("name", a.Name, "source", string(a.Source), "filePath", a.FilePath, "basePromptSha256", hashPrompt(a.SystemPrompt)), "  "),
		"",
		"Current overlay:",
		current,
		"",
		"Evidence packet:",
		marshalJSON(arr, "  "),
	}, "\n")
}

// refinementContext is RefinementActionContext: launchProposalChild runs the read-only proposal child.
type refinementContext struct {
	cwd                 string
	launchProposalChild func(task string, schema *jsObject) proposalChildResult
}

// handleRefinementAction runs refine, refine.show or refine.rollback for one agent.
func handleRefinementAction(action string, params map[string]any, ctx refinementContext) manageResult {
	res := func(text string, isErr bool) manageResult { return manageResult{text: text, isError: isErr} }
	requested := ""
	if s, ok := params["agent"].(string); ok {
		requested = jsTrim(s)
	}
	if requested == "" {
		return res(fmt.Sprintf("%s requires agent. Use /subagents-refine <agent> or subagent({ action: \"%s\", agent: \"<agent>\" }).", action, action), true)
	}
	discovered, err := discoverAgents(ctx.cwd, ScopeBoth, "", adv.options())
	if err != nil {
		return res(err.Error(), true)
	}
	agent, msg := ResolveAgentName(requested, discovered.Agents)
	if msg != "" {
		return res(msg, true)
	}
	if agent == nil {
		return res(formatUnknownAgentError(requested, discovered, "Unknown agent"), true)
	}
	path, err := getAgentRefinementPath(ctx.cwd, agent.Name)
	if err != nil {
		return res(err.Error(), true)
	}
	var existing *refinementFile
	if b, rerr := os.ReadFile(path); rerr == nil {
		if existing, err = parseRefinementFile(string(b), path); err != nil {
			return res(err.Error(), true)
		}
	}
	switch action {
	case "refine.show":
		if existing == nil {
			return res(fmt.Sprintf("No refinement overlay exists for '%s'.", agent.Name), false)
		}
		drift := "yes"
		if existing.BaseSHA == hashPrompt(agent.SystemPrompt) {
			drift = "no"
		}
		start := 0
		if len(existing.Snapshots) > 5 {
			start = len(existing.Snapshots) - 5
		}
		var history []string
		for _, s := range existing.Snapshots[start:] {
			plural := "s"
			if len(s.EvidenceIDs) == 1 {
				plural = ""
			}
			history = append(history, fmt.Sprintf("- r%d %s at %s (%d evidence id%s)", s.Revision, s.Action, s.At, len(s.EvidenceIDs), plural))
		}
		if len(history) == 0 {
			history = []string{"- none"}
		}
		current := jsTrim(existing.Current)
		if current == "" {
			current = "(empty)"
		}
		return res(strings.Join([]string{
			fmt.Sprintf("Refinement overlay for '%s'", agent.Name), "Path: " + path, fmt.Sprintf("Revision: %d", existing.Revision), "Updated: " + existing.UpdatedAt,
			fmt.Sprintf("Base: %s %s", existing.BaseSource, existing.BaseFile), "Base prompt changed since overlay: " + drift, "", "Current guidance:", current, "", "Recent history:",
			strings.Join(history, "\n"),
		}, "\n"), false)
	case "refine.rollback":
		if existing == nil {
			return res(fmt.Sprintf("No refinement overlay exists for '%s'.", agent.Name), true)
		}
		if len(existing.Snapshots) == 0 {
			return res(fmt.Sprintf("No refinement snapshot exists for '%s'.", agent.Name), true)
		}
		latest := existing.Snapshots[len(existing.Snapshots)-1]
		now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
		next := newRefinementFile(agent, existing.Revision+1, now)
		next.Current = latest.Before
		next.Snapshots = append(append([]refinementSnapshot(nil), existing.Snapshots...), refinementSnapshot{Revision: next.Revision, At: now, Action: "rollback", Before: existing.Current, After: latest.Before, EvidenceIDs: latest.EvidenceIDs})
		if err := writeRefinementFile(path, next); err != nil {
			return res(err.Error(), true)
		}
		return res(fmt.Sprintf("Rolled back refinement overlay for '%s' to revision %d.\nPath: %s", agent.Name, next.Revision, path), false)
	}
	evidence := collectBoundedRefinementEvidence(ctx.cwd, agent.Name)
	if len(evidence) == 0 {
		return res(fmt.Sprintf("No bounded recent evidence was found for '%s'. No proposal child was launched and no overlay was written.", agent.Name), false)
	}
	current := ""
	if existing != nil {
		current = existing.Current
	}
	child := ctx.launchProposalChild(proposalTask(agent, current, evidence), proposalSchema())
	if child.IsError {
		return res("Refinement proposal child failed. No overlay was written.", true)
	}
	ids := make([]string, len(evidence))
	for i, e := range evidence {
		ids[i] = e.ID
	}
	proposal, verr := validateRefinementProposal(proposalFromChild(child), ids)
	if proposal == nil {
		return res(verr+" No overlay was written.", true)
	}
	if len(proposal.Edits) == 0 {
		return res(fmt.Sprintf("The proposal child returned no edits for '%s'. No overlay was written.", agent.Name), false)
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	revision := 1
	var prior []refinementSnapshot
	if existing != nil {
		revision, prior = existing.Revision+1, existing.Snapshots
	}
	var lines []string
	var evIDs []string
	seen := map[string]bool{}
	for _, e := range proposal.Edits {
		lines = append(lines, "- "+jsTrim(e.Guidance))
		for _, id := range e.EvidenceIDs {
			if !seen[id] {
				seen[id] = true
				evIDs = append(evIDs, id)
			}
		}
	}
	after := strings.Join(lines, "\n")
	next := newRefinementFile(agent, revision, now)
	next.Current = after
	next.Snapshots = append(append([]refinementSnapshot(nil), prior...), refinementSnapshot{Revision: revision, At: now, Action: "refine", Before: current, After: after, EvidenceIDs: evIDs, ProposalAgent: refinementProposalAgent})
	if err := writeRefinementFile(path, next); err != nil {
		return res(err.Error(), true)
	}
	return res(strings.Join([]string{fmt.Sprintf("Wrote refinement overlay for '%s' at revision %d.", agent.Name, revision), "Path: " + path,
		fmt.Sprintf("Evidence items: %d", len(evidence)), fmt.Sprintf("Edits: %d", len(proposal.Edits))}, "\n"), false)
}

// launchReviewerProposal runs the read-only reviewer child on the proposal task; the schema is stated in the task and checked
// by validateRefinementProposal (this port runs children through pig and does not enforce output schemas).
func launchReviewerProposal(ctx sdk.Context) func(task string, schema *jsObject) proposalChildResult {
	return func(task string, schema *jsObject) proposalChildResult {
		d, err := discoverAgents(ctx.Cwd(), ScopeBoth, ctx.ModelProvider(), adv.options())
		if err != nil {
			return proposalChildResult{IsError: true}
		}
		reviewer, _ := ResolveAgentName(refinementProposalAgent, d.Agents)
		if reviewer == nil {
			return proposalChildResult{IsError: true}
		}
		done, stop := cancellation(ctx)
		defer stop()
		out, err := runAgent(done, ctx.Cwd(), reviewer, task+"\n\nOutput schema (JSON Schema):\n"+marshalJSON(schema, "  "), "")
		if err != nil {
			return proposalChildResult{IsError: true}
		}
		return proposalChildResult{FinalOutputs: []string{out}}
	}
}

func init() {
	for _, name := range []string{"refine", "refine.show", "refine.rollback"} {
		name := name
		registerAction(name, []string{"agent"}, func(ctx sdk.Context, params map[string]any) (any, error) {
			return manageOutcome(handleRefinementAction(name, params, refinementContext{cwd: ctx.Cwd(), launchProposalChild: launchReviewerProposal(ctx)}))
		})
	}
}
