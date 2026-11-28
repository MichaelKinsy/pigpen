package agents

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// upstream: packages/coding-agent/src/internal-urls/agent-protocol.ts,
// packages/coding-agent/src/internal-urls/history-protocol.ts,
// packages/coding-agent/src/session/session-history-format.ts

const (
	blankProbeBytes     = 4096
	maxIDSuggestions    = 5
	maxHistoryScanDepth = 8
	primaryArgMaxChars  = 120
)

// AgentURLHandler implements `urls.Handler`, `urls.Writer`, `urls.Locator`,
// `urls.Completer`, and `urls.PromptDocer` for `agent://`.
// upstream: packages/coding-agent/src/internal-urls/agent-protocol.ts
type AgentURLHandler struct {
	Runtime *Runtime
}

func (h *AgentURLHandler) registry() *Registry {
	if h.Runtime != nil {
		return h.Runtime.Registry
	}
	return defaultRuntime.Registry
}

func (h *AgentURLHandler) Scheme() string { return "agent" }

func (h *AgentURLHandler) Spec() urls.Spec {
	return urls.Spec{
		Backing:   urls.BackingFile,
		Selectors: urls.SelectorsLines,
		Immutable: true,
		Linkable:  true,
		Write: &urls.WritePolicy{
			Via:     "handler",
			Payload: "verbatim",
			Scope:   "coordination",
		},
	}
}

// PromptDoc renders prompts/internal-urls/agent.md.
func (h *AgentURLHandler) PromptDoc(host urls.SchemeHost) string {
	var b strings.Builder
	b.WriteString("agent://<id>: latest completed output (status + progress while unpublished); nested IDs dotted, /key/index JSON path; write = message")
	if !host.Subagent {
		b.WriteString(", agent://all broadcast only")
	}
	b.WriteByte('.')
	if host.CrossInstanceAgents {
		b.WriteString(" Other omp instances in this folder: agent://peers lists them; agent://<pid> is one's main agent, agent://<pid>/<id> its subagent (read + write).")
	}
	return b.String()
}

func (h *AgentURLHandler) preferredDir(c urls.Context) string {
	var zero sdk.Context
	if c.SDK == zero {
		return ""
	}
	if sf, err := c.SDK.SessionManager().GetSessionFile(); err == nil && sf != nil && *sf != "" {
		return artifactsDirForSession(*sf)
	}
	return ""
}

// callerTopSession resolves the caller's top-level session ID (walking any child
// lineage to the root parent) so every agent:// and history:// read, write,
// locate, and completion is scoped to that session tree.
func callerTopSession(reg *Registry, c urls.Context) (sessScope string, senderID string) {
	senderID = MainAgentID
	var zero sdk.Context
	if c.SDK == zero {
		return "", senderID
	}
	sid, err := c.SDK.SessionManager().GetSessionID()
	if err != nil || sid == "" {
		return "", senderID
	}
	if meta, ok := reg.ChildMeta(sid); ok {
		if meta.AgentID != "" {
			senderID = meta.AgentID
		}
		if top, isSub := reg.ResolveTopParent(sid); isSub && top != "" {
			return top, senderID
		}
		return meta.ParentSessionID, senderID
	}
	return sid, senderID
}

func (h *AgentURLHandler) Locate(c urls.Context, u urls.URL, _ urls.LocateOptions) (string, bool, error) {
	outputID := u.Host
	if outputID == "" {
		return "", false, errors.New("agent:// URL requires an output ID: agent://<id>")
	}
	if outputID == "all" || hasPathExtraction(u) {
		return "", false, nil
	}
	reg := h.registry()
	sessScope, _ := callerTopSession(reg, c)
	if isSupersededInSession(reg, sessScope, outputID) {
		return "", false, nil
	}
	dirs := reg.ArtifactsDirsForSession(sessScope, h.preferredDir(c))
	if len(dirs) == 0 {
		return "", false, nil
	}
	scan := findAgentOutput(dirs, outputID)
	if scan.foundPath == "" {
		return "", false, nil
	}
	if isBlankFile(scan.foundPath) {
		return "", false, nil
	}
	return scan.foundPath, true, nil
}

func (h *AgentURLHandler) Write(c urls.Context, u urls.URL, content string) (*urls.WriteResult, error) {
	target := u.Host
	if target == "" {
		return nil, errors.New("agent:// URL requires a recipient: agent://<id>")
	}
	if hasPathExtraction(u) {
		return nil, errors.New("agent:// message target cannot have a JSON-path suffix.")
	}
	progress := hasQueryKey(u, "progress")
	if progress && target == "all" {
		return nil, errors.New("agent://all broadcasts cannot be ?progress updates.")
	}
	if strings.TrimSpace(content) == "" {
		return nil, errors.New("agent:// messages require non-empty content.")
	}

	reg := h.registry()
	sessScope, senderID := callerTopSession(reg, c)
	text, details, isErr := reg.SendMessageInSession(sessScope, senderID, target, content, progress, "", false)
	return &urls.WriteResult{
		Text:    text,
		Details: map[string]any{"message": details},
		IsError: isErr,
	}, nil
}

func (h *AgentURLHandler) Resolve(c urls.Context, u urls.URL) (*urls.Resource, error) {
	outputID := u.Host
	if outputID == "all" {
		return nil, errors.New("agent://all is write-only; use it to broadcast a message.")
	}
	if outputID == "" {
		return nil, errors.New("agent:// URL requires an output ID: agent://<id>")
	}

	segments := decodePathSegments(u.Path)
	extraction := len(segments) > 0
	reg := h.registry()
	sessScope, _ := callerTopSession(reg, c)
	dirs := reg.ArtifactsDirsForSession(sessScope, h.preferredDir(c))
	scan := findAgentOutput(dirs, outputID)

	if scan.foundPath == "" {
		if ref, ok := reg.GetInSession(sessScope, outputID); ok {
			return resolveAgentProgress(u, ref, extraction, false)
		}
		if len(dirs) == 0 {
			return nil, errors.New("No session - agent outputs unavailable")
		}
		if !scan.anyDirExists {
			return nil, errors.New("No artifacts directory found")
		}
		candidates := make([]string, 0, len(scan.availableIDs))
		for id := range scan.availableIDs {
			candidates = append(candidates, id)
		}
		for _, ref := range reg.ListInSession(sessScope) {
			candidates = append(candidates, ref.ID)
		}
		return nil, notFoundAgentError(outputID, candidates)
	}

	rawBytes, err := os.ReadFile(scan.foundPath)
	if err != nil {
		return nil, err
	}
	rawContent := string(rawBytes)
	if !extraction && strings.TrimSpace(rawContent) == "" {
		if ref, ok := reg.GetInSession(sessScope, outputID); ok {
			return resolveAgentProgress(u, ref, false, true)
		}
	}

	var notes []string
	content := rawContent
	if strings.TrimSpace(content) == "" {
		content = fmt.Sprintf("%s published an empty output. Transcript: history://%s\n", outputID, outputID)
	}
	if !extraction && isSupersededInSession(reg, sessScope, outputID) {
		ageStr := "0ms"
		if info, err := os.Stat(scan.foundPath); err == nil {
			ageStr = formatDurationMs(max(int64(0), time.Since(info.ModTime()).Milliseconds()))
		}
		banner := fmt.Sprintf("> `%s` is running a newer turn. The output below is from its PREVIOUS run (published %s ago), not the current one. Live progress: `history://%s`; its next result auto-delivers to its owner.\n", outputID, ageStr, outputID)
		content = banner + rawContent
		notes = append(notes, fmt.Sprintf("Superseded: %s is running a newer turn", outputID))
	}
	contentType := "text/markdown"
	extractedFrom := scan.foundPath
	isValue := extraction
	absent := false

	if extraction {
		var jsonVal any
		parsed := false
		if scan.jsonPath != "" {
			if sidecarBytes, err := os.ReadFile(scan.jsonPath); err == nil {
				if err := json.Unmarshal(sidecarBytes, &jsonVal); err == nil {
					extractedFrom = scan.jsonPath
					parsed = true
				}
			}
		}
		if !parsed {
			if err := json.Unmarshal([]byte(rawContent), &jsonVal); err != nil {
				return nil, fmt.Errorf("Output %s is not valid JSON: %v", outputID, err)
			}
		}
		extracted, found := ExtractJSONPath(jsonVal, segments)
		if !found {
			absent = true
			content = "null"
			contentType = "application/json"
		} else if s, ok := extracted.(string); ok {
			// A string leaf (e.g. markdown report) reads as prose, not a JSON-escaped single line.
			content = s
			contentType = "text/markdown"
		} else {
			if b, err := json.MarshalIndent(extracted, "", "  "); err == nil {
				content = string(b)
			} else {
				content = fmt.Sprint(extracted)
			}
			contentType = "application/json"
		}
		notes = append(notes, "Extracted: /"+strings.Join(segments, "/"))
		if parsed {
			notes = append(notes, "Source: "+filepath.Base(extractedFrom))
		}
		if absent {
			notes = append(notes, "JSON path not found")
		}
	}

	return &urls.Resource{
		URL:         u.Href,
		Content:     content,
		ContentType: contentType,
		SourcePath:  extractedFrom,
		Notes:       notes,
		Value:       isValue,
		Absent:      absent,
	}, nil
}

func (h *AgentURLHandler) Complete(c urls.Context, _ string) ([]urls.Completion, error) {
	reg := h.registry()
	sessScope, _ := callerTopSession(reg, c)
	ids := map[string]bool{}
	for _, dir := range reg.ArtifactsDirsForSession(sessScope, h.preferredDir(c)) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".md") {
				ids[strings.TrimSuffix(e.Name(), ".md")] = true
			}
		}
	}
	for _, ref := range reg.ListInSession(sessScope) {
		ids[ref.ID] = true
	}
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	out := make([]urls.Completion, len(sorted))
	for i, id := range sorted {
		out[i] = urls.Completion{Value: id}
	}
	return out, nil
}

func hasPathExtraction(u urls.URL) bool {
	return u.Path != "" && u.Path != "/"
}

func hasQueryKey(u urls.URL, key string) bool {
	if u.Query != nil {
		if _, ok := u.Query[key]; ok {
			return true
		}
	}
	if u.RawQuery == "" {
		return false
	}
	for _, part := range strings.Split(u.RawQuery, "&") {
		k, _, _ := strings.Cut(part, "=")
		if k == key {
			return true
		}
	}
	return false
}

func decodePathSegments(rawPath string) []string {
	var out []string
	for _, seg := range strings.Split(rawPath, "/") {
		if seg == "" {
			continue
		}
		if dec, err := url.PathUnescape(seg); err == nil {
			out = append(out, dec)
		} else {
			out = append(out, seg)
		}
	}
	return out
}

// ExtractJSONPath walks segments into data: map keys for objects, numeric
// indices for slices.
// upstream: packages/coding-agent/src/internal-urls/agent-protocol.ts:124
func ExtractJSONPath(data any, segments []string) (any, bool) {
	cur := data
	for _, seg := range segments {
		if cur == nil {
			return nil, false
		}
		switch node := cur.(type) {
		case []any:
			if !digitsOnlyRE.MatchString(seg) {
				return nil, false
			}
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, false
			}
			cur = node[idx]
		case map[string]any:
			v, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = v
		default:
			return nil, false
		}
	}
	return cur, true
}

func isSupersededInSession(reg *Registry, sessScope, outputID string) bool {
	ref, ok := reg.GetInSession(sessScope, outputID)
	if !ok {
		return false
	}
	ref.mu.Lock()
	defer ref.mu.Unlock()
	return ref.Status == "running"
}

func isBlankFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if info.Size() > blankProbeBytes {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == ""
}

type outputScan struct {
	foundPath    string
	jsonPath     string
	anyDirExists bool
	availableIDs map[string]bool
}

func findAgentOutput(dirs []string, id string) outputScan {
	scan := outputScan{availableIDs: map[string]bool{}}
	byID := map[string]string{}
	jsonByID := map[string]string{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		scan.anyDirExists = true
		for _, e := range entries {
			name := e.Name()
			if strings.HasSuffix(name, ".json") {
				jid := strings.TrimSuffix(name, ".json")
				if _, exists := jsonByID[jid]; !exists {
					jsonByID[jid] = filepath.Join(dir, name)
				}
				continue
			}
			if !strings.HasSuffix(name, ".md") {
				continue
			}
			mid := strings.TrimSuffix(name, ".md")
			if _, exists := byID[mid]; !exists {
				byID[mid] = filepath.Join(dir, name)
			}
			scan.availableIDs[mid] = true
		}
	}
	scan.foundPath = byID[id]
	if scan.foundPath != "" {
		if sidecar, ok := jsonByID[id]; ok && filepath.Dir(sidecar) == filepath.Dir(scan.foundPath) {
			scan.jsonPath = sidecar
		}
	}
	return scan
}

func resolveAgentProgress(u urls.URL, ref *AgentRef, extraction, publishedEmpty bool) (*urls.Resource, error) {
	ref.mu.Lock()
	status := ref.Status
	id := ref.ID
	ref.mu.Unlock()
	if extraction {
		return nil, fmt.Errorf("Output %s is not published yet (status: %s); read agent://%s for its progress.", id, status, id)
	}
	msgs, source := ref.SnapshotMessages()
	sections := extractYieldSectionsFromMessages(msgs)
	lastText := lastAssistantTextFromMessages(msgs)
	empty := len(sections) == 0 && lastText == ""

	var b strings.Builder
	fmt.Fprintf(&b, "# %s (%s)\n\n", id, status)
	if publishedEmpty {
		fmt.Fprintf(&b, "Published output is empty. Concise transcript: `history://%s`.\n", id)
	} else {
		fmt.Fprintf(&b, "No final output yet: `agent://%s` shows progress until the run publishes its output. Concise transcript: `history://%s`.\n", id, id)
	}
	for _, sec := range sections {
		b.WriteString("\n## Yield")
		if sec.labels != "" {
			b.WriteString(" [")
			b.WriteString(sec.labels)
			b.WriteByte(']')
		}
		b.WriteString("\n\n```json\n")
		b.WriteString(sec.data)
		b.WriteString("\n```\n")
	}
	if lastText != "" {
		b.WriteString("\n## Latest assistant text\n\n")
		b.WriteString(lastText)
		b.WriteByte('\n')
	}
	if empty {
		b.WriteString("\nNothing produced yet.\n")
	}

	prefix := "No"
	if publishedEmpty {
		prefix = "Empty"
	}
	return &urls.Resource{
		URL:         u.Href,
		Content:     b.String(),
		ContentType: "text/markdown",
		Notes:       []string{fmt.Sprintf("%s published output; progress from %s (%s)", prefix, source, status)},
	}, nil
}

type yieldSectionView struct {
	labels string
	data   string
}

func extractYieldSectionsFromMessages(msgs []map[string]any) []yieldSectionView {
	var out []yieldSectionView
	for _, m := range msgs {
		role, _ := m["role"].(string)
		toolName, _ := m["toolName"].(string)
		isErr, _ := m["isError"].(bool)
		if role != "toolResult" || toolName != "yield" || isErr {
			continue
		}
		details, ok := m["details"].(map[string]any)
		if !ok {
			continue
		}
		if st, _ := details["status"].(string); st != "success" {
			continue
		}
		data, hasData := details["data"]
		if !hasData || data == nil {
			continue
		}
		var labels string
		switch t := details["type"].(type) {
		case string:
			labels = t
		case []any:
			var parts []string
			for _, item := range t {
				if s, ok := item.(string); ok && s != "" {
					parts = append(parts, s)
				}
			}
			labels = strings.Join(parts, ", ")
		case []string:
			labels = strings.Join(t, ", ")
		}
		var dataStr string
		if b, err := json.MarshalIndent(data, "", "  "); err == nil {
			dataStr = string(b)
		} else {
			dataStr = fmt.Sprint(data)
		}
		out = append(out, yieldSectionView{labels: labels, data: dataStr})
	}
	return out
}

func lastAssistantTextFromMessages(msgs []map[string]any) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if role, _ := m["role"].(string); role != "assistant" {
			continue
		}
		text := strings.TrimSpace(messageContentToText(m["content"]))
		if text != "" {
			return text
		}
	}
	return ""
}

func notFoundAgentError(outputID string, candidates []string) error {
	unique := map[string]bool{}
	var filtered []string
	for _, c := range candidates {
		if c == "" || c == outputID || unique[c] {
			continue
		}
		unique[c] = true
		filtered = append(filtered, c)
	}
	suggestions := fuzzyTopIDs(outputID, filtered, maxIDSuggestions)
	hint := "List agents with history://"
	if len(suggestions) > 0 {
		hint = "Did you mean: " + strings.Join(suggestions, ", ")
	}
	return fmt.Errorf("Not found: %s\n%s", outputID, hint)
}

func fuzzyTopIDs(query string, candidates []string, limit int) []string {
	type scored struct {
		id    string
		score int
	}
	qLower := strings.ToLower(query)
	var matches []scored
	for _, c := range candidates {
		cLower := strings.ToLower(c)
		if score, ok := subseqScore(qLower, cLower); ok {
			matches = append(matches, scored{id: c, score: score})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].id < matches[j].id
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	out := make([]string, len(matches))
	for i, m := range matches {
		out[i] = m.id
	}
	return out
}

func subseqScore(needle, hay string) (int, bool) {
	if needle == "" {
		return 0, false
	}
	if strings.Contains(hay, needle) {
		return 1000 - (len(hay) - len(needle)), true
	}
	ni := 0
	score := 0
	streak := 0
	for hi := range len(hay) {
		if ni < len(needle) && hay[hi] == needle[ni] {
			ni++
			streak++
			score += 10 + streak*2
		} else {
			streak = 0
		}
	}
	if ni < len(needle) {
		return 0, false
	}
	return score - (len(hay) - len(needle)), true
}

// ---------------------------------------------------------------------------
// history:// protocol handler
// ---------------------------------------------------------------------------

// HistoryURLHandler implements `urls.Handler`, `urls.Locator`, `urls.Completer`,
// and `urls.PromptDocer` for `history://`.
// upstream: packages/coding-agent/src/internal-urls/history-protocol.ts
type HistoryURLHandler struct {
	Runtime *Runtime
}

func (h *HistoryURLHandler) registry() *Registry {
	if h.Runtime != nil {
		return h.Runtime.Registry
	}
	return defaultRuntime.Registry
}

func (h *HistoryURLHandler) Scheme() string { return "history" }

func (h *HistoryURLHandler) Spec() urls.Spec {
	return urls.Spec{
		Backing:   urls.BackingVirtual,
		Selectors: urls.SelectorsLines,
		Immutable: false,
		Linkable:  true,
	}
}

func (h *HistoryURLHandler) PromptDoc(_ urls.SchemeHost) string {
	return "history://<id>: read-only concise transcript (tool calls/results summarized); bare lists registered and retained on-disk agents, not persisted unregistered top-level sessions."
}

func (h *HistoryURLHandler) preferredDir(c urls.Context) string {
	var zero sdk.Context
	if c.SDK == zero {
		return ""
	}
	if sf, err := c.SDK.SessionManager().GetSessionFile(); err == nil && sf != nil && *sf != "" {
		return artifactsDirForSession(*sf)
	}
	return ""
}

func historyCurrentRoute(u urls.URL) (route string, err error) {
	if !strings.EqualFold(u.Host, "current") {
		return "", nil
	}
	if u.Path == "" || u.Path == "/" {
		if u.RawQuery != "" || strings.Contains(u.RawHref, "#") {
			return "", errors.New("Invalid history://current route; use exactly history://current/full (selectors may follow it)")
		}
		return "bare", nil
	}
	if u.Path != "/full" || u.RawQuery != "" || strings.Contains(u.RawHref, "#") {
		return "", errors.New("Invalid history://current route; use exactly history://current/full (selectors may follow it)")
	}
	return "full", nil
}

func (h *HistoryURLHandler) Locate(c urls.Context, u urls.URL, _ urls.LocateOptions) (string, bool, error) {
	route, err := historyCurrentRoute(u)
	if err != nil {
		return "", false, err
	}
	if route == "full" {
		return "", false, nil
	}
	agentID := u.Host
	if agentID == "" {
		return "", false, nil
	}
	reg := h.registry()
	sessScope, _ := callerTopSession(reg, c)
	if ref, ok := reg.LookupInSession(sessScope, agentID); ok {
		ref.mu.Lock()
		sf := ref.SessionFile
		hasLive := ref.Session != nil
		ref.mu.Unlock()
		if sf != "" {
			if info, err := os.Stat(sf); err == nil && !info.IsDir() {
				return sf, true, nil
			}
		}
		if hasLive {
			return "", false, nil
		}
	}
	if route == "bare" {
		return "", false, nil
	}
	disk := scanSessionFiles(reg.ArtifactsDirsForSession(sessScope, h.preferredDir(c)))
	if p, ok := matchDiskSession(disk, agentID); ok {
		return p, true, nil
	}
	return "", false, nil
}

func (h *HistoryURLHandler) resolveCurrentFull(c urls.Context, u urls.URL) (*urls.Resource, error) {
	var zero sdk.Context
	if c.SDK == zero {
		return nil, errors.New("history://current/full requires a bound live session branch")
	}
	entries, ok, err := activeBranchRawEntries(c.SDK)
	if err != nil {
		return nil, fmt.Errorf("history://current/full requires a bound live session branch: %w", err)
	}
	if !ok {
		return nil, errors.New("history://current/full requires a bound live session branch")
	}
	content := FormatCurrentBranchFullHistory(entries)
	return &urls.Resource{
		URL:         u.Href,
		Content:     content,
		ContentType: "text/markdown",
		Notes:       []string{"Source: caller-bound live session branch (full, uncompacted)"},
	}, nil
}

func (h *HistoryURLHandler) Resolve(c urls.Context, u urls.URL) (*urls.Resource, error) {
	route, err := historyCurrentRoute(u)
	if err != nil {
		return nil, err
	}
	if route == "full" {
		return h.resolveCurrentFull(c, u)
	}

	reg := h.registry()
	sessScope, _ := callerTopSession(reg, c)
	prefDir := h.preferredDir(c)
	agentID := u.Host

	if agentID == "" {
		content := renderHistoryIndex(reg, sessScope, prefDir)
		return &urls.Resource{
			URL:         u.Href,
			Content:     content,
			ContentType: "text/markdown",
		}, nil
	}

	ref, ok := reg.LookupInSession(sessScope, agentID)
	if !ok && route == "bare" {
		return h.resolveCurrentFull(c, u)
	}
	if !ok {
		disk := scanSessionFiles(reg.ArtifactsDirsForSession(sessScope, prefDir))
		if idMatch, filePath, found := findDiskSession(disk, agentID); found {
			msgs, err := LoadSessionMessagesReadOnly(filePath)
			if err != nil {
				return nil, err
			}
			return &urls.Resource{
				URL:         u.Href,
				Content:     FormatSessionHistoryMarkdown(msgs, idMatch+" (on disk)"),
				ContentType: "text/markdown",
				SourcePath:  filePath,
				Notes:       []string{"Source: session file (read-only, unregistered)"},
			}, nil
		}
		refs := reg.ListInSession(sessScope)
		known := make([]string, len(refs))
		for i, r := range refs {
			known[i] = r.ID
		}
		knownStr := "none"
		if len(known) > 0 {
			knownStr = strings.Join(known, ", ")
		}
		return nil, fmt.Errorf("Unknown agent: %s\nKnown agents: %s\nList all with history://", agentID, knownStr)
	}
	msgs, source := ref.SnapshotMessages()
	ref.mu.Lock()
	status := ref.Status
	createdAt := ref.CreatedAt
	sessFile := ref.SessionFile
	refID := ref.ID
	ref.mu.Unlock()

	if len(msgs) == 0 && source == "no transcript" {
		if status == "running" {
			content := fmt.Sprintf("# %s (%s)\n\nNo transcript has been written yet.\nStarted %s.\n", refID, status, formatAgo(createdAt))
			return &urls.Resource{
				URL:         u.Href,
				Content:     content,
				ContentType: "text/markdown",
				Notes:       []string{"Source: agent registry (no transcript yet)"},
			}, nil
		}
		return nil, fmt.Errorf("Agent %s has no transcript: session is gone and no session file was retained", refID)
	}

	return &urls.Resource{
		URL:         u.Href,
		Content:     FormatSessionHistoryMarkdown(msgs, fmt.Sprintf("%s (%s)", refID, status)),
		ContentType: "text/markdown",
		SourcePath:  sessFile,
		Notes:       []string{"Source: " + source},
	}, nil
}

func (h *HistoryURLHandler) Complete(c urls.Context, _ string) ([]urls.Completion, error) {
	reg := h.registry()
	sessScope, _ := callerTopSession(reg, c)
	var out []urls.Completion
	seen := map[string]bool{}
	for _, ref := range reg.ListInSession(sessScope) {
		seen[ref.ID] = true
		ref.mu.Lock()
		desc := fmt.Sprintf("%s · %s", ref.Status, ref.Kind)
		if ref.ParentID != "" {
			desc += " · parent " + ref.ParentID
		}
		ref.mu.Unlock()
		out = append(out, urls.Completion{Value: ref.ID, Description: desc})
	}
	disk := scanSessionFiles(reg.ArtifactsDirsForSession(sessScope, h.preferredDir(c)))
	for _, id := range sortedKeys(disk) {
		if seen[id] {
			continue
		}
		out = append(out, urls.Completion{Value: id, Description: "on disk"})
	}
	return out, nil
}

func renderHistoryIndex(reg *Registry, sessScope, prefDir string) string {
	type row struct {
		id, status, kind, parent, lastActivity string
	}
	refs := reg.ListInSession(sessScope)
	registered := map[string]bool{}
	rows := make([]row, 0, len(refs))
	for _, ref := range refs {
		registered[ref.ID] = true
		ref.mu.Lock()
		parent := ref.ParentID
		if parent == "" {
			parent = "—"
		}
		rows = append(rows, row{
			id:           ref.ID,
			status:       ref.Status,
			kind:         ref.Kind,
			parent:       parent,
			lastActivity: formatAgo(ref.LastActive),
		})
		ref.mu.Unlock()
	}
	disk := scanSessionFiles(reg.ArtifactsDirsForSession(sessScope, prefDir))
	for _, id := range sortedKeys(disk) {
		if registered[id] {
			continue
		}
		rows = append(rows, row{id: id, status: "on disk", kind: "—", parent: "—", lastActivity: "—"})
	}

	var b strings.Builder
	b.WriteString("# Agents\n\n")
	if len(rows) == 0 {
		b.WriteString("No agents registered.\n")
		return b.String()
	}
	b.WriteString("| id | status | kind | parent | last activity |\n|---|---|---|---|---|\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", r.id, r.status, r.kind, r.parent, r.lastActivity)
	}
	b.WriteString("\nRead a transcript with `read history://<id>`.\n")
	return b.String()
}

func scanSessionFiles(dirs []string) map[string]string {
	found := map[string]string{}
	seenDirs := map[string]bool{}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > maxHistoryScanDepth || seenDirs[dir] {
			return
		}
		seenDirs[dir] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() {
				walk(filepath.Join(dir, e.Name()), depth+1)
				continue
			}
			if e.Type()&fs.ModeType != 0 && !e.Type().IsRegular() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(name, ".jsonl") || strings.HasPrefix(name, "__advisor") {
				continue
			}
			id := strings.TrimSuffix(name, ".jsonl")
			if _, exists := found[id]; !exists {
				found[id] = filepath.Join(dir, name)
			}
		}
	}
	for _, d := range dirs {
		walk(d, 0)
	}
	return found
}

func findDiskSession(disk map[string]string, agentID string) (string, string, bool) {
	if p, ok := disk[agentID]; ok {
		return agentID, p, true
	}
	lower := strings.ToLower(agentID)
	for _, id := range sortedKeys(disk) {
		if strings.ToLower(id) == lower {
			return id, disk[id], true
		}
	}
	return "", "", false
}

func matchDiskSession(disk map[string]string, agentID string) (string, bool) {
	_, p, ok := findDiskSession(disk, agentID)
	return p, ok
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func formatAgo(t time.Time) string {
	if t.IsZero() {
		return "0s ago"
	}
	secs := max(int64(0), int64(time.Since(t).Seconds()))
	if secs < 60 {
		return itoa(secs) + "s ago"
	}
	mins := secs / 60
	if mins < 60 {
		return itoa(mins) + "m ago"
	}
	hours := mins / 60
	if hours < 24 {
		return itoa(hours) + "h ago"
	}
	return itoa(hours/24) + "d ago"
}

// FormatSessionHistoryMarkdown formats a session's message slice as omp's concise
// markdown transcript for `history://`.
// upstream: packages/coding-agent/src/session/session-history-format.ts:507
func FormatSessionHistoryMarkdown(messages []map[string]any, title string) string {
	var lines []string
	if title != "" {
		lines = append(lines, "# "+title, "")
	}

	resultsByCallID := map[string]map[string]any{}
	for _, m := range messages {
		if role, _ := m["role"].(string); role == "toolResult" {
			if id, _ := m["toolCallId"].(string); id != "" {
				resultsByCallID[id] = m
			}
		}
	}
	consumed := map[string]bool{}

	for _, m := range messages {
		role, _ := m["role"].(string)
		switch role {
		case "user", "developer":
			txt := messageContentToText(m["content"])
			if strings.TrimSpace(txt) == "" {
				continue
			}
			lines = append(lines, "## "+role, "", txt, "")

		case "assistant":
			var body []string
			switch content := m["content"].(type) {
			case string:
				if strings.TrimSpace(content) != "" {
					body = append(body, content)
				}
			case []any:
				for _, raw := range content {
					block, ok := raw.(map[string]any)
					if !ok {
						continue
					}
					bType, _ := block["type"].(string)
					switch bType {
					case "text":
						if t, _ := block["text"].(string); strings.TrimSpace(t) != "" {
							body = append(body, t)
						}
					case "toolCall", "tool_use":
						id, _ := block["id"].(string)
						name, _ := block["name"].(string)
						args, _ := block["arguments"].(map[string]any)
						if args == nil {
							args, _ = block["input"].(map[string]any)
						}
						res := resultsByCallID[id]
						if res != nil {
							consumed[id] = true
						}
						body = append(body, formatConciseToolCall(name, args, res))
					}
				}
			}
			if len(body) > 0 {
				lines = append(lines, "## assistant", "")
				lines = append(lines, body...)
				lines = append(lines, "")
			}

		case "toolResult":
			id, _ := m["toolCallId"].(string)
			if consumed[id] {
				continue
			}
			name, _ := m["toolName"].(string)
			lines = append(lines, formatConciseToolCall(name, nil, m), "")

		case "custom", "hookMessage":
			ct, _ := m["customType"].(string)
			txt := messageContentToText(m["content"])
			lines = append(lines, fmt.Sprintf("[%s] %s", ct, oneLineMax(txt, primaryArgMaxChars)), "")

		case "compactionSummary":
			summary, _ := m["summary"].(string)
			lines = append(lines, "[compaction] "+oneLineMax(summary, primaryArgMaxChars), "")

		case "branchSummary":
			fromID, _ := m["fromId"].(string)
			summary, _ := m["summary"].(string)
			lines = append(lines, fmt.Sprintf("[branch] from %s: %s", fromID, oneLineMax(summary, primaryArgMaxChars)), "")
		}
	}

	return strings.TrimSpace(strings.Join(lines, "\n")) + "\n"
}

func formatConciseToolCall(name string, args map[string]any, result map[string]any) string {
	head := fmt.Sprintf("→ %s(%s)", name, oneLineMax(summarizePrimaryArg(name, args), primaryArgMaxChars))
	if result == nil {
		return head + " ⇒ pending"
	}
	raw := messageContentToText(result["content"])
	nLines := 0
	if raw != "" {
		nLines = strings.Count(raw, "\n") + 1
	}
	unit := "lines"
	if nLines == 1 {
		unit = "line"
	}
	if isErr, _ := result["isError"].(bool); isErr {
		firstLine, _, _ := strings.Cut(raw, "\n")
		firstLine = oneLineMax(firstLine, primaryArgMaxChars)
		if firstLine != "" {
			return fmt.Sprintf("%s ⇒ error · %d %s — %s", head, nLines, unit, firstLine)
		}
		return fmt.Sprintf("%s ⇒ error · %d %s", head, nLines, unit)
	}
	return fmt.Sprintf("%s ⇒ ok · %d %s", head, nLines, unit)
}

func messageContentToText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, raw := range c {
			block, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			bType, _ := block["type"].(string)
			switch bType {
			case "text":
				if t, ok := block["text"].(string); ok {
					parts = append(parts, t)
				}
			case "image":
				parts = append(parts, "[image]")
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func oneLineMax(s string, maxChars int) string {
	flat := strings.Join(strings.Fields(s), " ")
	runes := []rune(flat)
	if len(runes) > maxChars {
		return string(runes[:maxChars-1]) + "…"
	}
	return flat
}

// activeBranchRawEntries collects every persisted session entry on the caller's
// active branch in root-to-leaf order (including pre-compaction entries), never
// leaking abandoned sibling branches.
// Intersects ctx.GetEntries() with ctx.GetBranch() IDs so full JSON blocks
// (thinking, toolCall arguments, compaction markers, reset boundaries, etc.)
// survive intact; falls back to synthesizing entries from BranchEntry when
// GetEntries() returns no raw match for an ID.
func activeBranchRawEntries(ctx sdk.Context) ([]map[string]any, bool, error) {
	branch, err := ctx.GetBranch()
	if err != nil {
		return nil, false, err
	}
	if len(branch) == 0 {
		return []map[string]any{}, true, nil
	}
	wanted := make(map[string]bool, len(branch))
	for _, be := range branch {
		if be.ID != "" {
			wanted[be.ID] = true
		}
	}
	rawEntries, _ := ctx.GetEntries()
	byID := make(map[string]map[string]any, len(wanted))
	for _, raw := range rawEntries {
		var hdr struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &hdr) != nil || !wanted[hdr.ID] {
			continue
		}
		var entry map[string]any
		if json.Unmarshal(raw, &entry) == nil {
			byID[hdr.ID] = entry
		}
	}
	ordered := make([]map[string]any, 0, len(branch))
	for _, be := range branch {
		if be.ID != "" {
			if raw, ok := byID[be.ID]; ok {
				ordered = append(ordered, raw)
				continue
			}
		}
		ordered = append(ordered, branchEntryToRawMap(be))
	}
	return ordered, true, nil
}

func branchEntryToRawMap(be sdk.BranchEntry) map[string]any {
	entryType := be.Type
	if entryType == "" {
		entryType = "message"
	}
	m := map[string]any{
		"id":   be.ID,
		"type": entryType,
	}
	switch entryType {
	case "compaction":
		m["summary"] = be.Content
		if be.FirstKeptEntryID != "" {
			m["firstKeptEntryId"] = be.FirstKeptEntryID
		}
	case "branch_summary":
		m["summary"] = be.Content
	case "message":
		msg := map[string]any{"role": be.Role}
		switch be.Role {
		case "assistant":
			var blocks []any
			if be.Thinking != "" {
				blocks = append(blocks, map[string]any{"type": "thinking", "thinking": be.Thinking})
			}
			if be.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": be.Content})
			}
			for _, tc := range be.ToolCalls {
				var argVal any = tc.Args
				if tc.Args != "" {
					var decoded any
					if json.Unmarshal([]byte(tc.Args), &decoded) == nil {
						argVal = decoded
					}
				}
				blocks = append(blocks, map[string]any{
					"type":      "toolCall",
					"id":        tc.ID,
					"name":      tc.Name,
					"arguments": argVal,
				})
			}
			msg["content"] = blocks
		case "toolResult":
			msg["toolName"] = be.ToolName
			msg["toolCallId"] = be.ToolCallID
			msg["isError"] = be.IsError
			msg["content"] = be.Content
		default:
			msg["content"] = be.Content
		}
		m["message"] = msg
	default:
		if be.Content != "" {
			m["content"] = be.Content
		}
	}
	return m
}

// FormatCurrentBranchFullHistory renders every persisted entry on the caller's
// live branch without the compaction filters that shape model context.
// upstream: packages/coding-agent/src/internal-urls/history-protocol.ts:114-305
func FormatCurrentBranchFullHistory(entries []map[string]any) string {
	lines := []string{
		"# Current branch — full history",
		"",
		"Source: caller-bound live session branch. Context-window boundaries are retained below; this view never uses a registry or on-disk fallback.",
		"",
	}
	for _, entry := range entries {
		renderRawBranchEntry(&lines, entry)
		lines = append(lines, "")
	}
	return strings.TrimSpace(strings.Join(lines, "\n")) + "\n"
}

func historyJSONFence(text string) string {
	longest := 0
	cur := 0
	for i := range len(text) {
		if text[i] == '`' {
			cur++
			if cur > longest {
				longest = cur
			}
		} else {
			cur = 0
		}
	}
	fence := strings.Repeat("`", max(3, longest+1))
	return fence + "json\n" + text + "\n" + fence
}

func marshalPrettyJSON(v any) string {
	if v == nil {
		return "null"
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "null"
	}
	return string(b)
}

func appendHistoryTextContent(lines *[]string, content any) {
	switch c := content.(type) {
	case string:
		*lines = append(*lines, c)
	case []any:
		for _, raw := range c {
			block, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			bType, _ := block["type"].(string)
			switch bType {
			case "text":
				if t, ok := block["text"].(string); ok {
					*lines = append(*lines, t)
				}
			case "image":
				*lines = append(*lines, "[image]")
			}
		}
	}
}

func renderRawBranchEntry(lines *[]string, entry map[string]any) {
	id, _ := entry["id"].(string)
	entryType, _ := entry["type"].(string)
	parentID := "root"
	if p, ok := entry["parentId"].(string); ok && p != "" {
		parentID = p
	}
	timestamp := formatAnyScalar(entry["timestamp"])
	*lines = append(*lines,
		fmt.Sprintf("## Entry %s · %s", id, entryType),
		"",
		fmt.Sprintf("Parent: %s", parentID),
		fmt.Sprintf("Timestamp: %s", timestamp),
		"",
	)
	switch entryType {
	case "message":
		if msg, ok := entry["message"].(map[string]any); ok {
			appendRawBranchMessage(lines, msg)
			return
		}
	case "compaction":
		firstKept, _ := entry["firstKeptEntryId"].(string)
		tokensBefore := formatAnyScalar(entry["tokensBefore"])
		summary, _ := entry["summary"].(string)
		*lines = append(*lines,
			"> Context window boundary: compaction",
			"",
			fmt.Sprintf("First kept entry: %s", firstKept),
			fmt.Sprintf("Tokens before: %s", tokensBefore),
			"",
			summary,
		)
		return
	case "reset_boundary":
		*lines = append(*lines, "> Context window boundary: reset")
		return
	case "branch_summary":
		fromID, _ := entry["fromId"].(string)
		summary, _ := entry["summary"].(string)
		*lines = append(*lines, fmt.Sprintf("From entry: %s", fromID), "", summary)
		return
	}
	*lines = append(*lines, historyJSONFence(marshalPrettyJSON(entry)))
}

func appendRawBranchMessage(lines *[]string, msg map[string]any) {
	role, _ := msg["role"].(string)
	switch role {
	case "user", "developer":
		*lines = append(*lines, "### "+role)
		appendHistoryTextContent(lines, msg["content"])
	case "assistant":
		*lines = append(*lines, "### assistant")
		switch content := msg["content"].(type) {
		case string:
			*lines = append(*lines, content)
		case []any:
			for _, raw := range content {
				block, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				bType, _ := block["type"].(string)
				switch bType {
				case "text":
					if t, ok := block["text"].(string); ok {
						*lines = append(*lines, t)
					}
				case "thinking":
					thinking, _ := block["thinking"].(string)
					*lines = append(*lines, "<thinking>", thinking, "</thinking>")
				case "toolCall", "tool_use":
					name, _ := block["name"].(string)
					id, _ := block["id"].(string)
					argVal := block["arguments"]
					if argVal == nil {
						argVal = block["input"]
					}
					*lines = append(*lines, fmt.Sprintf("#### tool call: %s (%s)", name, id))
					*lines = append(*lines, historyJSONFence(marshalPrettyJSON(argVal)))
				case "redactedThinking":
					data, _ := block["data"].(string)
					*lines = append(*lines, fmt.Sprintf("[redacted thinking: %d chars omitted]", len(data)))
				case "anthropicServerTool":
					srvBlock, _ := block["block"].(map[string]any)
					srvType, _ := srvBlock["type"].(string)
					*lines = append(*lines, fmt.Sprintf("#### server tool: %s", srvType))
					*lines = append(*lines, historyJSONFence(marshalPrettyJSON(srvBlock)))
				case "image":
					mimeType, _ := block["mimeType"].(string)
					data, _ := block["data"].(string)
					*lines = append(*lines, fmt.Sprintf("[image: %s, %d base64 chars omitted]", mimeType, len(data)))
				case "fallback":
					fromModel, _ := mapString(block["from"], "model")
					toModel, _ := mapString(block["to"], "model")
					*lines = append(*lines, fmt.Sprintf("[provider fallback: %s to %s]", fromModel, toModel))
				}
			}
		}
	case "toolResult":
		toolName, _ := msg["toolName"].(string)
		toolCallID, _ := msg["toolCallId"].(string)
		errSuffix := ""
		if isErr, _ := msg["isError"].(bool); isErr {
			errSuffix = " [error]"
		}
		*lines = append(*lines, fmt.Sprintf("### tool result: %s (%s)%s", toolName, toolCallID, errSuffix))
		appendHistoryTextContent(lines, msg["content"])
	case "bashExecution":
		*lines = append(*lines,
			"### bash execution",
			formatBashExecutionText(msg),
			historyJSONFence(marshalPrettyJSON(map[string]any{
				"exitCode":   msg["exitCode"],
				"cancelled":  boolOrFalse(msg["cancelled"]),
				"truncated":  boolOrFalse(msg["truncated"]),
				"meta":       msg["meta"],
				"imageCount": sliceLen(msg["images"]),
			})),
		)
	case "pythonExecution":
		*lines = append(*lines,
			"### python execution",
			formatPythonExecutionText(msg),
			historyJSONFence(marshalPrettyJSON(map[string]any{
				"exitCode":  msg["exitCode"],
				"cancelled": boolOrFalse(msg["cancelled"]),
				"truncated": boolOrFalse(msg["truncated"]),
				"meta":      msg["meta"],
			})),
		)
	case "custom", "hookMessage":
		customType, _ := msg["customType"].(string)
		*lines = append(*lines, fmt.Sprintf("### %s (%s)", role, customType))
		appendHistoryTextContent(lines, msg["content"])
	case "branchSummary":
		fromID, _ := msg["fromId"].(string)
		summary, _ := msg["summary"].(string)
		*lines = append(*lines, fmt.Sprintf("### branch summary (from %s)", fromID), summary)
	case "compactionSummary":
		tokensBefore := formatAnyScalar(msg["tokensBefore"])
		summary, _ := msg["summary"].(string)
		*lines = append(*lines, fmt.Sprintf("### compaction summary (%s tokens before)", tokensBefore), summary)
	case "fileMention":
		*lines = append(*lines, "### file mention")
		if files, ok := msg["files"].([]any); ok {
			for _, raw := range files {
				f, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				path, _ := f["path"].(string)
				*lines = append(*lines, fmt.Sprintf(`<file path="%s">`, path))
				if content, ok := f["content"].(string); ok && content != "" {
					*lines = append(*lines, content)
				}
				if f["image"] != nil {
					*lines = append(*lines, "[image]")
				}
				*lines = append(*lines, "</file>")
			}
		}
	default:
		*lines = append(*lines, historyJSONFence(marshalPrettyJSON(msg)))
	}
}

func formatBashExecutionText(msg map[string]any) string {
	cmd, _ := msg["command"].(string)
	out, _ := msg["output"].(string)
	var b strings.Builder
	fmt.Fprintf(&b, "Ran `%s`\n", cmd)
	if out != "" {
		fmt.Fprintf(&b, "```\n%s\n```", out)
	} else {
		b.WriteString("(no output)")
	}
	if boolOrFalse(msg["cancelled"]) {
		b.WriteString("\n\n(command cancelled)")
	} else if code, ok := intFromAny(msg["exitCode"]); ok && code != 0 {
		fmt.Fprintf(&b, "\n\nCommand exited with code %d", code)
	}
	return b.String()
}

func formatPythonExecutionText(msg map[string]any) string {
	code, _ := msg["code"].(string)
	out, _ := msg["output"].(string)
	var b strings.Builder
	fmt.Fprintf(&b, "Ran Python:\n```python\n%s\n```\n", code)
	if out != "" {
		fmt.Fprintf(&b, "Output:\n```\n%s\n```", out)
	} else {
		b.WriteString("(no output)")
	}
	if boolOrFalse(msg["cancelled"]) {
		b.WriteString("\n\n(execution cancelled)")
	} else if exitCode, ok := intFromAny(msg["exitCode"]); ok && exitCode != 0 {
		fmt.Fprintf(&b, "\n\nExecution failed with code %d", exitCode)
	}
	return b.String()
}

func formatAnyScalar(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	default:
		return fmt.Sprint(x)
	}
}

func mapString(v any, key string) (string, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return "", false
	}
	s, ok := m[key].(string)
	return s, ok
}

func boolOrFalse(v any) bool {
	b, _ := v.(bool)
	return b
}

func sliceLen(v any) int {
	s, ok := v.([]any)
	if !ok {
		return 0
	}
	return len(s)
}

func intFromAny(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}
