package warden

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The capability index (src/index-cmd.ts): a local file of entries, written by the session model in a fixed format
// and validated and sanitized here, that the conscience reads instead of bare names and descriptions.

// indexPackageDir is the directory the port keeps its stores in under the PiG config home (ConfigPath).
const indexPackageDir = "pigpen-warden"

// IndexEntry is one capability's entry. The field order is the order the original writes, which the 700-character
// cap measures.
type IndexEntry struct {
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	Scope         string   `json:"scope"`
	SourceHash    string   `json:"sourceHash"`
	Role          string   `json:"role"`
	Lead          string   `json:"lead"`
	UseWhen       []string `json:"useWhen"`
	NotWhen       []string `json:"notWhen"`
	Inputs        string   `json:"inputs"`
	Examples      []string `json:"examples"`
	Thin          bool     `json:"thin"`
	SourceQuality string   `json:"sourceQuality"`
	// Improve is one sentence, at most 20 words, saying what the description lacks (weak and thin entries).
	Improve string `json:"improve,omitempty"`
	// Location is the advertised location, kept local and never copied into judge state.
	Location string `json:"_location,omitempty"`
	// Truncated: the entry was cut at a bullet boundary.
	Truncated bool `json:"truncated,omitempty"`
}

// IndexFile is a whole index file.
type IndexFile struct {
	FormatVersion int          `json:"formatVersion"`
	BuiltAt       string       `json:"builtAt"`
	Model         string       `json:"model"`
	Entries       []IndexEntry `json:"entries"`
}

// indexDirs says where the index lives: PIGPEN_WARDEN_INDEX_DIR when set (the port's counterpart of
// PI_WARDEN_INDEX_DIR; a `~` prefix expands), else the warden data directory under the PiG config home.
type indexDirs struct {
	Getenv  func(string) string
	DataDir string
}

func (d indexDirs) env(key string) string {
	if d.Getenv != nil {
		return d.Getenv(key)
	}
	return os.Getenv(key)
}

// dir is the index directory.
func (d indexDirs) dir() string {
	if configured := strings.TrimSpace(d.env("PIGPEN_WARDEN_INDEX_DIR")); configured != "" {
		return filepath.Join(ExpandHome(configured), indexPackageDir, "index")
	}
	return filepath.Join(d.DataDir, "index")
}

// path is the global index file, or the project's (keyed by the first 12 hex digits of SHA-256 of its root).
func (d indexDirs) path(kind, projectRoot string) string {
	base := d.dir()
	if kind == "global" {
		return filepath.Join(base, "global.json")
	}
	sum := sha256.Sum256([]byte(projectRoot))
	return filepath.Join(base, "projects", hex.EncodeToString(sum[:])[:12]+".json")
}

// ensure creates the directory the file of kind lives in, owner-only like the files written into it.
func (d indexDirs) ensure(kind string) error {
	dir := d.dir()
	if kind != "global" {
		dir = filepath.Join(dir, "projects")
	}
	return os.MkdirAll(dir, 0o700)
}

// skillSourceHash is a skill's sourceHash: the hash of its file.
func skillSourceHash(s Skill) string { return fileContentHash(s.FilePath) }

// ---------------------------------------------------------------------------
// I/O.

// readIndexRaw reads a file and returns it decoded when it has the shape of an index file (formatVersion 1, string
// builtAt and model, an entries array); entries are not validated.
func readIndexRaw(path string) (map[string]any, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var parsed any
	if err := json.Unmarshal([]byte(nodeUTF8(raw)), &parsed); err != nil {
		return nil, false
	}
	obj, ok := parsed.(map[string]any)
	if !ok {
		return nil, false
	}
	if v, ok := obj["formatVersion"].(float64); !ok || v != 1 {
		return nil, false
	}
	if _, ok := obj["builtAt"].(string); !ok {
		return nil, false
	}
	if _, ok := obj["model"].(string); !ok {
		return nil, false
	}
	if _, ok := obj["entries"].([]any); !ok {
		return nil, false
	}
	return obj, true
}

// readIndex reads an index file; nil when it is missing, unreadable or not an index. An entry that does not fit the
// entry shape is dropped (it could never match a skill or a tool).
func readIndex(path string) *IndexFile {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	// The plain shape the port and the original write is read in one pass over the text (index_decode.go).
	if f, ok := decodeIndexFast(nodeUTF8(raw)); ok {
		return f
	}
	return readIndexGeneric(path, raw)
}

// readIndexGeneric is readIndex for any text, the final say on what a file means: the generic decoder reads it into
// the typed shape; a file with an entry of another type takes the route that drops the entries it cannot read.
func readIndexGeneric(path string, raw []byte) *IndexFile {
	text := []byte(nodeUTF8(raw))
	var wire struct {
		FormatVersion *float64      `json:"formatVersion"`
		BuiltAt       *string       `json:"builtAt"`
		Model         *string       `json:"model"`
		Entries       *[]IndexEntry `json:"entries"`
	}
	if json.Unmarshal(text, &wire) == nil {
		if wire.FormatVersion == nil || *wire.FormatVersion != 1 || wire.BuiltAt == nil || wire.Model == nil || wire.Entries == nil {
			return nil
		}
		return &IndexFile{FormatVersion: 1, BuiltAt: *wire.BuiltAt, Model: *wire.Model, Entries: *wire.Entries}
	}
	obj, ok := readIndexRaw(path)
	if !ok {
		return nil
	}
	return indexFromRaw(obj)
}

func indexFromRaw(obj map[string]any) *IndexFile {
	f := &IndexFile{FormatVersion: 1, Entries: []IndexEntry{}}
	f.BuiltAt, _ = obj["builtAt"].(string)
	f.Model, _ = obj["model"].(string)
	entries, _ := obj["entries"].([]any)
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			continue
		}
		var entry IndexEntry
		if json.Unmarshal(b, &entry) == nil {
			f.Entries = append(f.Entries, entry)
		}
	}
	return f
}

// writeIndex writes an index file owner-only (the folder 0700, the file 0600, replacing a more open earlier file).
func writeIndex(path string, index *IndexFile) error {
	for i := range index.Entries {
		e := &index.Entries[i]
		e.UseWhen, e.NotWhen, e.Examples = orEmpty(e.UseWhen), orEmpty(e.NotWhen), orEmpty(e.Examples)
	}
	if index.Entries == nil {
		index.Entries = []IndexEntry{}
	}
	return WriteFileAtomic(path, indexFileJSON(index))
}

// jsonIndent is JSON.stringify(value, null, 2).
func jsonIndent(v any) string {
	var sb strings.Builder
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
	return strings.TrimSuffix(sb.String(), "\n")
}

// ---------------------------------------------------------------------------
// Validation and sanitization.

const (
	maxEntryChars   = 700 // serialized UTF-16 units per entry
	maxImproveChars = 200
)

var (
	validRoles          = []string{"research", "evidence", "execution", "delegation", "review", "conversation"}
	validSourceQuality  = []string{"strong", "weak", "thin"}
	errNotAnObjectEntry = "not an object"
)

// EntryValidation is a sanitized entry or the reason it was rejected.
type EntryValidation struct {
	Entry  *IndexEntry
	Reject string
}

func rejectEntry(e map[string]any, field, msg string) EntryValidation {
	name := "position unknown"
	if s, ok := e["name"].(string); ok {
		name = `"` + s + `"`
	}
	return EntryValidation{Reject: name + ": " + field + " " + msg}
}

// jsString is String(value) for a decoded JSON value.
func jsString(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return jsNumToString(t)
	case []any:
		parts := make([]string, len(t))
		for i, x := range t {
			if x != nil {
				parts[i] = jsString(x)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

func jsNumToString(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func jsTruthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return t != ""
	case bool:
		return t
	case float64:
		return t != 0 && !math.IsNaN(t)
	}
	return true
}

func stringsOf(items []any) []string {
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = sanitizeDescription(jsString(s))
	}
	return out
}

// validateEntry validates one entry (decoded JSON) and returns it sanitized; position is its 1-based place in the
// file, 0 when unknown.
func validateEntry(entry any, position int) EntryValidation {
	e, ok := entry.(map[string]any)
	if !ok {
		pos := "unknown"
		if position > 0 {
			pos = itoaInt(position)
		}
		return EntryValidation{Reject: "position " + pos + ": " + errNotAnObjectEntry}
	}
	kind, _ := e["kind"].(string)
	if kind != "skill" && kind != "tool" {
		return rejectEntry(e, "kind", `must be "skill" or "tool"`)
	}
	name, ok := e["name"].(string)
	if !ok || name == "" {
		return rejectEntry(e, "name", "must be a non-empty string")
	}
	scope, _ := e["scope"].(string)
	if scope != "global" && scope != "project" {
		return rejectEntry(e, "scope", `must be "global" or "project"`)
	}
	sourceHash, ok := e["sourceHash"].(string)
	if !ok {
		return rejectEntry(e, "sourceHash", "must be a string")
	}
	role, _ := e["role"].(string)
	if !containsString(validRoles, role) {
		return rejectEntry(e, "role", "must be one of: "+strings.Join(validRoles, ", "))
	}
	lead, ok := e["lead"].(string)
	if !ok || lead == "" {
		return rejectEntry(e, "lead", "must be a non-empty string")
	}
	useWhen, ok := e["useWhen"].([]any)
	if !ok || len(useWhen) < 2 || len(useWhen) > 4 {
		return rejectEntry(e, "useWhen", "must have 2 to 4 items")
	}
	notWhen, ok := e["notWhen"].([]any)
	if !ok {
		return rejectEntry(e, "notWhen", "must be an array")
	}
	inputs, ok := e["inputs"].(string)
	if !ok {
		return rejectEntry(e, "inputs", "must be a string")
	}
	examples, ok := e["examples"].([]any)
	if !ok || len(examples) < 2 || len(examples) > 3 {
		return rejectEntry(e, "examples", "must have 2 to 3 items")
	}
	sq := "strong"
	if raw, present := e["sourceQuality"]; present {
		s, isString := raw.(string)
		if !isString || !containsString(validSourceQuality, s) {
			return rejectEntry(e, "sourceQuality", `must be "strong", "weak", or "thin"`)
		}
		sq = s
	}
	out := IndexEntry{
		Kind: kind, Name: name, Scope: scope, SourceHash: sourceHash, Role: role,
		Lead:    sanitizeDescription(lead),
		UseWhen: stringsOf(useWhen), NotWhen: stringsOf(notWhen),
		Inputs:        sanitizeDescription(inputs),
		Examples:      stringsOf(examples),
		Thin:          sq == "thin",
		SourceQuality: sq,
	}
	if sq == "weak" || sq == "thin" {
		improve := ""
		if s, ok := e["improve"].(string); ok {
			improve = utf16Slice(sanitizeDescription(s), 0, maxImproveChars)
		}
		out.Improve = improve
	}
	if loc, present := e["_location"]; present && jsTruthy(loc) {
		out.Location = jsString(loc)
	}
	if entryLength(&out) > maxEntryChars {
		if t := truncateEntry(out); t != nil {
			return EntryValidation{Entry: t}
		}
		return rejectEntry(e, "entries", "exceeds "+itoaInt(maxEntryChars)+" chars and cannot be truncated")
	}
	return EntryValidation{Entry: &out}
}

// truncateEntry cuts an entry at a bullet boundary (the end of a useWhen or notWhen item).
func truncateEntry(entry IndexEntry) *IndexEntry {
	base := entry
	base.NotWhen = []string{}
	base.UseWhen = append([]string{}, entry.UseWhen[:min(2, len(entry.UseWhen))]...)
	// The truncated flag the cut adds costs about 17 characters.
	if entryLength(&base)+18 <= maxEntryChars {
		base.Truncated = true
		return &base
	}
	// Last resort: drop examples from the reduced base.
	ex := append([]string{}, entry.Examples...)
	for len(ex) > 2 {
		ex = ex[:len(ex)-1]
		trial := base
		trial.Examples, trial.Truncated = ex, true
		if entryLength(&trial) <= maxEntryChars {
			return &trial
		}
	}
	return nil
}

// ValidationResult is a validated file, or the per-entry rejections that kept it from existing.
type ValidationResult struct {
	File       *IndexFile
	Rejections []string
}

// validateIndex validates a whole decoded index file.
func validateIndex(raw any) ValidationResult {
	obj, ok := raw.(map[string]any)
	if !ok {
		return ValidationResult{Rejections: []string{"not an object"}}
	}
	if v, ok := obj["formatVersion"].(float64); !ok || v != 1 {
		return ValidationResult{Rejections: []string{"formatVersion must be 1"}}
	}
	builtAt, ok := obj["builtAt"].(string)
	if !ok {
		return ValidationResult{Rejections: []string{"builtAt must be a string"}}
	}
	model, ok := obj["model"].(string)
	if !ok {
		return ValidationResult{Rejections: []string{"model must be a string"}}
	}
	raws, ok := obj["entries"].([]any)
	if !ok {
		return ValidationResult{Rejections: []string{"entries must be an array"}}
	}
	entries := []IndexEntry{}
	var rejections []string
	for i, r := range raws {
		v := validateEntry(r, i+1)
		if v.Entry != nil {
			entries = append(entries, *v.Entry)
		} else {
			rejections = append(rejections, v.Reject)
		}
	}
	if len(rejections) > 0 {
		return ValidationResult{Rejections: rejections}
	}
	return ValidationResult{File: &IndexFile{FormatVersion: 1, BuiltAt: builtAt, Model: model, Entries: entries}}
}

// SourceQualityReport is one weak or thin entry's line of the build report.
type SourceQualityReport struct {
	Name          string
	Kind          string
	SourceQuality string
	Improve       string
}

// IndexStats counts what an index holds.
type IndexStats struct {
	GlobalEntries, ProjectEntries int
	ThinSources                   []string
	TruncatedCount                int
	SourceQualityReport           []SourceQualityReport
}

func indexStats(index *IndexFile) IndexStats {
	s := IndexStats{ThinSources: []string{}, SourceQualityReport: []SourceQualityReport{}}
	for _, e := range index.Entries {
		if e.Scope == "global" {
			s.GlobalEntries++
		} else {
			s.ProjectEntries++
		}
		if e.Thin {
			s.ThinSources = append(s.ThinSources, e.Name)
		}
		if e.Truncated {
			s.TruncatedCount++
		}
		if e.SourceQuality == "weak" || e.SourceQuality == "thin" {
			s.SourceQualityReport = append(s.SourceQualityReport, SourceQualityReport{e.Name, e.Kind, e.SourceQuality, e.Improve})
		}
	}
	return s
}

// ---------------------------------------------------------------------------
// The prompt that asks the session model for the entries.

// indexFormatSpec is the format section of the prompt, from its heading to the end of the code block.
const indexFormatSpec = "## Format\n\n" +
	"Write each file as an object with only an `entries` array (the tool adds metadata):\n\n" +
	"```json\n{\n" +
	`  "entries": [` + "\n    {\n" +
	`      "kind": "skill" | "tool",` + "\n" +
	`      "name": "<skill or tool name>",` + "\n" +
	`      "scope": "global" | "project",` + "\n" +
	`      "sourceHash": "<first 16 hex chars of SHA-256 of the skill file content; for tools, SHA-256 of name:description>",` + "\n" +
	`      "role": "<one of: research, evidence, execution, delegation, review, conversation>",` + "\n" +
	`      "lead": "<at most 12 words; first word is the trigger>",` + "\n" +
	`      "useWhen": ["<situation 1>", "<situation 2>"] (2-4 bullets, each starting with the situation),` + "\n" +
	`      "notWhen": ["<situation> do <alternative>"] (0-2 bullets, each ending with what to do instead),` + "\n" +
	`      "inputs": "<one line describing what it needs>",` + "\n" +
	`      "examples": ["<prompt phrasing 1>", "<prompt phrasing 2>"] (2-3 phrasings a user would type),` + "\n" +
	`      "thin": true when the source description is under 80 characters or the skill body under 300 characters,` + "\n" +
	`      "sourceQuality": "strong" | "weak" | "thin",` + "\n" +
	`      "improve": "<one sentence, at most 20 words, saying what the description lacks>" (only for weak or thin entries)` + "\n" +
	"    }\n  ]\n}\n```\n"

// indexPromptTail1 is the rest of the scope section and the roles and output headings.
const indexPromptTail1 = "- Skills from the project directory: scope `project`\n" +
	"- All tools: scope `global`\n\n" +
	"## Roles\nChoose exactly one role per entry:\n" +
	"- `research`: finds information outside the repo (web search, docs fetch)\n" +
	"- `evidence`: inspects the repo or runtime (code graph, traces, reads)\n" +
	"- `execution`: changes or runs things\n" +
	"- `delegation`: spawns or manages agents\n" +
	"- `review`: judges or checks work\n" +
	"- `conversation`: thinking, planning, clarifying\n\n" +
	"## Output paths\n\n"

const indexWritingRules = "## Entry writing rules\n" +
	"- Front-load the leading word so the trigger is the first word of `lead`.\n" +
	"- One trigger per bullet; synonyms collapsed.\n" +
	"- Cut what the name already carries from descriptions.\n" +
	"- State the target behaviour positively; use a prohibition only as a guardrail paired with the positive alternative.\n" +
	"- Keep entries short: they are always-loaded material."

// IndexSkill is a skill as the build prompt lists it.
type IndexSkill struct {
	Name, Description, FilePath string
}

// buildIndexPrompt is the user message that asks the session model to write the index entries: the format, the
// writing rules and both absolute output paths. agentDir names where user-level skills live.
func buildIndexPrompt(_ string, skills []IndexSkill, tools []CapabilityTool, global, project, agentDir string) string {
	var b strings.Builder
	b.Grow(4096 + len(skills)*160 + len(tools)*120)
	b.WriteString("# Build a capability index\n\nYou are building a local capability index. Write valid JSON only.\n\n## Skills\n")
	if len(skills) == 0 {
		b.WriteString("(none)")
	}
	for i, s := range skills {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("- " + s.Name + ": " + s.Description + " (file: " + s.FilePath + ")")
	}
	b.WriteString("\n\n## Tools\n")
	if len(tools) == 0 {
		b.WriteString("(none)")
	}
	for i, t := range tools {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("- " + t.Name + ": " + t.Description)
	}
	b.WriteString("\n\n" + indexWritingRules + "\n\n" + indexFormatSpec + "\n## Scope\n")
	b.WriteString("- Skills from " + agentDir + "/skills/ and user-level directories: scope `global`\n")
	b.WriteString(indexPromptTail1)
	b.WriteString("Global index: " + global + "\nProject index: " + project + "\n\n")
	b.WriteString("Write one file per scope. The tool adds `formatVersion`, `builtAt`, and `model` metadata after validation.\nWrite valid JSON only. No markdown, no explanation, no code fences.")
	return b.String()
}

// ---------------------------------------------------------------------------
// Lookup for the conscience.

// findEntry finds an index entry by name and sourceHash, the project index before the global one.
func findEntry(name, sourceHash string, global, project *IndexFile) *IndexEntry {
	if e := findIndexEntry(project, name, sourceHash); e != nil {
		return e
	}
	return findIndexEntry(global, name, sourceHash)
}

// indexCoverage says which skills have a matching entry in the index and which do not.
func indexCoverage(skills []Skill, global, project *IndexFile) (covered, missing []string) {
	for _, s := range skills {
		if s.FilePath == "" {
			missing = append(missing, s.Name)
			continue
		}
		if findEntry(s.Name, fileContentHash(s.FilePath), global, project) != nil {
			covered = append(covered, s.Name)
		} else {
			missing = append(missing, s.Name)
		}
	}
	return
}
