package modes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/slash-commands/builtin-lifecycle.ts:452-464
// upstream: packages/coding-agent/src/modes/controllers/omfg-controller.ts
// upstream: packages/coding-agent/src/modes/controllers/omfg-rule.ts
// upstream: packages/coding-agent/src/prompts/system/omfg-user.md

const (
	omfgMaxAttempts   = 3
	omfgProjectOption = "This project (.omp/rules)"
	omfgGlobalOption  = "Global — all projects (<agentDir>/rules)"
	omfgAmendOption   = "Amend with feedback..."
	omfgCancelOption  = "Cancel"
)

var (
	omfgJSONFenceRE   = regexp.MustCompile("(?is)```(?:json)?\\s*([\\s\\S]*?)```")
	omfgValidNameRE   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	omfgInvalidNameRE = regexp.MustCompile(`[^a-z0-9-]+`)
	omfgMultiDashRE   = regexp.MustCompile(`-+`)

	omfgWindowsReservedNames = map[string]bool{
		"con": true, "prn": true, "aux": true, "nul": true,
		"com1": true, "com2": true, "com3": true, "com4": true,
		"com5": true, "com6": true, "com7": true, "com8": true, "com9": true,
		"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true,
		"lpt5": true, "lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
	}
)

// OmfgRulePayload is the structured rule JSON returned by the `/omfg` LLM call.
// upstream: packages/coding-agent/src/modes/controllers/omfg-rule.ts:148-156
type OmfgRulePayload struct {
	Name         string
	Description  string
	Condition    []string
	ASTCondition []string
	Question     string
	Scope        []string
	Body         string
}

// OmfgParsedRule bundles a validated rule payload with its assembled markdown
// file content (YAML frontmatter + guidance body).
type OmfgParsedRule struct {
	Payload     OmfgRulePayload
	FileContent string
}

// omfgHistoryOutput is a single assistant prose or tool-call output extracted
// from the session branch for validating rule regexes against prior turns.
type omfgHistoryOutput struct {
	Kind     string // "text" | "tool"
	ToolName string // e.g. "edit", "write", "bash"
	FilePath string // target file path when tool operates on a file
	Content  string // text or serialized args / written content
}

// completeOmfg performs the LLM completion for `/omfg` rule generation.
// Overridable in unit tests so tests never make network calls.
var completeOmfg = func(ctx sdk.Context, modelObj, req, auth map[string]any) (json.RawMessage, error) {
	return ctx.Complete(modelObj, req, auth)
}

// RenderOmfgUserPrompt renders upstream `omfg-user.md` for a complaint and
// optional retry feedback / previous candidate.
// upstream: packages/coding-agent/src/modes/controllers/omfg-controller.ts:155-159
func RenderOmfgUserPrompt(complaint, feedback, previousRule string) string {
	vars := map[string]string{
		"complaint": strings.TrimSpace(complaint),
	}
	if fb := strings.TrimSpace(feedback); fb != "" {
		vars["feedback"] = fb
	}
	if prev := strings.TrimSpace(previousRule); prev != "" {
		vars["previousRule"] = prev
	}
	return renderTemplate(omfgUserTemplate, templateData{Vars: vars})
}

// ExtractBalancedJSONObject extracts the first balanced `{...}` JSON object
// from text (respecting string literals and escape sequences), checking fenced
// JSON blocks first.
// upstream: packages/coding-agent/src/modes/controllers/omfg-rule.ts:34-42,158-199
func ExtractBalancedJSONObject(text string) string {
	trimmed := strings.TrimSpace(text)
	if m := omfgJSONFenceRE.FindStringSubmatch(trimmed); len(m) > 1 {
		if obj := scanBalancedJSONObject(m[1]); obj != "" {
			return obj
		}
	}
	return scanBalancedJSONObject(trimmed)
}

func scanBalancedJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// ValidateOmfgRuleName strictly validates that name matches
// `^[a-z0-9][a-z0-9-]{0,63}$` and contains no path traversal or slashes.
func ValidateOmfgRuleName(name string) error {
	if name == "" {
		return fmt.Errorf("rule name must not be empty")
	}
	if strings.Contains(name, "/") || strings.Contains(name, `\`) || strings.Contains(name, "..") {
		return fmt.Errorf("rule name %q must not contain path separators or '..'", name)
	}
	if !omfgValidNameRE.MatchString(name) {
		return fmt.Errorf("rule name %q must match ^[a-z0-9][a-z0-9-]{0,63}$", name)
	}
	if omfgWindowsReservedNames[strings.ToLower(name)] {
		return fmt.Errorf("rule name %q is a reserved device name", name)
	}
	return nil
}

// SanitizeOmfgRuleName normalizes a candidate rule name into kebab-case, then
// rejects traversal attempts or invalid names.
// upstream: packages/coding-agent/src/modes/controllers/omfg-rule.ts:44-52
func SanitizeOmfgRuleName(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("rule name must not be empty")
	}
	// Reject explicit path traversal or path separators before sanitizing so
	// malicious names like "../escape" or "a/b" are never silently rewritten.
	if strings.Contains(trimmed, "/") || strings.Contains(trimmed, `\`) || strings.Contains(trimmed, "..") {
		return "", fmt.Errorf("rule name %q must not contain path separators or '..'", raw)
	}
	lower := strings.ToLower(trimmed)
	lower = strings.NewReplacer(`"`, "", `'`, "", "`", "", "_", "-").Replace(lower)
	lower = omfgInvalidNameRE.ReplaceAllString(lower, "-")
	lower = omfgMultiDashRE.ReplaceAllString(lower, "-")
	lower = strings.Trim(lower, "-")
	if len(lower) > 64 {
		lower = strings.TrimRight(lower[:64], "-")
	}
	if err := ValidateOmfgRuleName(lower); err != nil {
		return "", err
	}
	return lower, nil
}

// ParseGeneratedOmfgRule parses the model's JSON response into a validated
// OmfgParsedRule with compiled Go RE2 condition regexes and YAML frontmatter.
// upstream: packages/coding-agent/src/modes/controllers/omfg-rule.ts:105-289
func ParseGeneratedOmfgRule(text string) (*OmfgParsedRule, error) {
	jsonText := ExtractBalancedJSONObject(text)
	if jsonText == "" {
		return nil, fmt.Errorf("Missing generated rule JSON object")
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(jsonText), &obj); err != nil {
		return nil, fmt.Errorf("Generated rule JSON is invalid: %v", err)
	}
	if obj == nil {
		return nil, fmt.Errorf("Generated rule JSON must be an object")
	}

	rawName := omfgStringField(obj, "name")
	if rawName == "" {
		return nil, fmt.Errorf("Generated rule JSON must include a non-empty name")
	}
	ruleName, err := SanitizeOmfgRuleName(rawName)
	if err != nil {
		return nil, err
	}

	desc := omfgStringField(obj, "description")
	if desc == "" {
		desc = omfgStringField(obj, "desc")
	}
	if desc == "" {
		return nil, fmt.Errorf("Generated rule JSON must include a non-empty description")
	}

	conds := omfgStringArrayField(obj, "condition")
	if len(conds) == 0 {
		conds = omfgStringArrayField(obj, "cond")
	}
	astConds := omfgStringArrayField(obj, "astCondition")
	question := omfgStringField(obj, "question")
	if len(conds) == 0 && len(astConds) == 0 && question == "" {
		return nil, fmt.Errorf("Generated rule JSON must include a `condition`, `astCondition`, or `question`")
	}

	scope := omfgStringArrayField(obj, "scope")
	if len(scope) == 0 {
		return nil, fmt.Errorf("Generated rule JSON must include at least one scope")
	}

	body := omfgStringField(obj, "body")
	if body == "" {
		return nil, fmt.Errorf("Generated rule JSON must include a non-empty body")
	}

	var normalizedConds []string
	if len(conds) > 0 {
		normalizedConds, err = normalizeOmfgConditions(conds)
		if err != nil {
			return nil, err
		}
	}

	payload := OmfgRulePayload{
		Name:         ruleName,
		Description:  desc,
		Condition:    normalizedConds,
		ASTCondition: astConds,
		Question:     question,
		Scope:        scope,
		Body:         body,
	}
	return &OmfgParsedRule{
		Payload:     payload,
		FileContent: AssembleOmfgRuleMarkdown(payload),
	}, nil
}

func normalizeOmfgConditions(conds []string) ([]string, error) {
	out := make([]string, 0, len(conds))
	seen := make(map[string]bool, len(conds))
	for _, c := range conds {
		norm := c
		if _, err := regexp.Compile(norm); err != nil {
			repaired := strings.ReplaceAll(norm, `\\`, `\`)
			if repaired != norm {
				if _, err2 := regexp.Compile(repaired); err2 == nil {
					norm = repaired
				} else {
					return nil, fmt.Errorf("Invalid condition regex %q: %v", c, err)
				}
			} else {
				return nil, fmt.Errorf("Invalid condition regex %q: %v", c, err)
			}
		}
		if !seen[norm] {
			seen[norm] = true
			out = append(out, norm)
		}
	}
	return out, nil
}

// AssembleOmfgRuleMarkdown formats YAML frontmatter + markdown body matching
// upstream `omfg-rule.ts:270-289`.
func AssembleOmfgRuleMarkdown(p OmfgRulePayload) string {
	var lines []string
	lines = append(lines,
		"---",
		"name: "+p.Name,
		"description: "+omfgJSONString(p.Description),
	)
	if len(p.Condition) > 0 {
		lines = append(lines, "condition: "+omfgFormatFrontmatterArray(p.Condition))
	}
	if len(p.ASTCondition) > 0 {
		lines = append(lines, "astCondition: "+omfgFormatFrontmatterArray(p.ASTCondition))
	}
	if p.Question != "" {
		lines = append(lines, "question: "+omfgJSONString(p.Question))
	}
	body := strings.ReplaceAll(strings.TrimSpace(p.Body), "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	lines = append(lines,
		"scope: "+omfgFormatFrontmatterArray(p.Scope),
		"---",
		"",
		body,
	)
	return strings.Join(lines, "\n") + "\n"
}

func omfgJSONString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(buf.String(), "\n")
}

func omfgFormatFrontmatterArray(vals []string) string {
	if len(vals) == 1 {
		return omfgJSONString(vals[0])
	}
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = omfgJSONString(v)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func omfgStringField(obj map[string]any, key string) string {
	v, ok := obj[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

func omfgStringArrayField(obj map[string]any, key string) []string {
	raw, exists := obj[key]
	if !exists || raw == nil {
		return nil
	}
	if s, ok := raw.(string); ok {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil
		}
		// Support comma-separated scopes when a single string is returned.
		if key == "scope" && strings.Contains(s, ",") {
			var parts []string
			seen := map[string]bool{}
			for _, p := range strings.Split(s, ",") {
				t := strings.TrimSpace(p)
				if t != "" && !seen[t] {
					seen[t] = true
					parts = append(parts, t)
				}
			}
			return parts
		}
		return []string{s}
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, item := range arr {
		s, ok := item.(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// collectOmfgHistoryOutputs extracts assistant prose and tool-call payloads
// from the active session branch.
func collectOmfgHistoryOutputs(branch []sdk.BranchEntry) []omfgHistoryOutput {
	var out []omfgHistoryOutput
	for _, entry := range branch {
		if entry.Type != "message" || entry.Role != "assistant" {
			continue
		}
		if text := strings.TrimSpace(entry.Content); text != "" {
			out = append(out, omfgHistoryOutput{
				Kind:    "text",
				Content: text,
			})
		}
		for _, tc := range entry.ToolCalls {
			fp, content := extractToolCallTextForOmfg(tc.Name, tc.Args)
			if content == "" {
				continue
			}
			out = append(out, omfgHistoryOutput{
				Kind:     "tool",
				ToolName: tc.Name,
				FilePath: fp,
				Content:  content,
			})
		}
	}
	return out
}

func extractToolCallTextForOmfg(toolName, rawArgs string) (filePath, content string) {
	trimmed := strings.TrimSpace(rawArgs)
	if trimmed == "" {
		return "", ""
	}
	var parsed map[string]any
	if json.Unmarshal([]byte(trimmed), &parsed) == nil && parsed != nil {
		for _, k := range []string{"path", "file_path", "filePath"} {
			if p, ok := parsed[k].(string); ok && strings.TrimSpace(p) != "" {
				filePath = strings.TrimSpace(p)
				break
			}
		}
		switch toolName {
		case "write":
			if c, ok := parsed["content"].(string); ok && c != "" {
				return filePath, c
			}
		case "edit":
			for _, k := range []string{"input", "newText", "content", "replacement"} {
				if c, ok := parsed[k].(string); ok && c != "" {
					if filePath == "" {
						filePath = extractHashlineHeaderPath(c)
					}
					return filePath, c
				}
			}
		}
	}
	return filePath, trimmed
}

func extractHashlineHeaderPath(editInput string) string {
	for _, line := range strings.Split(editInput, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inner := line[1 : len(line)-1]
			if hash := strings.LastIndexByte(inner, '#'); hash > 0 {
				return strings.TrimSpace(inner[:hash])
			}
		}
	}
	return ""
}

// omfgScopeMatchesOutput reports whether an assistant output falls within at
// least one scope entry (`text`, `thinking`, `tool`, `tool:<name>`,
// `tool:<name>(<glob>)`).
func omfgScopeMatchesOutput(scopes []string, out omfgHistoryOutput) bool {
	for _, raw := range scopes {
		sc := strings.TrimSpace(raw)
		if sc == "" {
			continue
		}
		if sc == "text" {
			if out.Kind == "text" {
				return true
			}
			continue
		}
		if sc == "thinking" {
			continue
		}
		if sc == "tool" {
			if out.Kind == "tool" {
				return true
			}
			continue
		}
		if strings.HasPrefix(sc, "tool:") && out.Kind == "tool" {
			spec := strings.TrimPrefix(sc, "tool:")
			toolPart := spec
			globPart := ""
			if open := strings.IndexByte(spec, '('); open >= 0 && strings.HasSuffix(spec, ")") {
				toolPart = spec[:open]
				globPart = spec[open+1 : len(spec)-1]
			}
			if toolPart != "*" && !strings.EqualFold(toolPart, out.ToolName) {
				continue
			}
			if globPart == "" {
				return true
			}
			if out.FilePath != "" && omfgMatchPathGlob(globPart, out.FilePath) {
				return true
			}
		}
	}
	return false
}

func omfgMatchPathGlob(pattern, filePath string) bool {
	normPath := filepath.ToSlash(filePath)
	normPat := filepath.ToSlash(strings.TrimSpace(pattern))
	if ok, _ := filepath.Match(normPat, normPath); ok {
		return true
	}
	if ok, _ := filepath.Match(normPat, filepath.Base(normPath)); ok {
		return true
	}
	if strings.HasPrefix(normPat, "**/") {
		sub := strings.TrimPrefix(normPat, "**/")
		if ok, _ := filepath.Match(sub, normPath); ok {
			return true
		}
		if ok, _ := filepath.Match(sub, filepath.Base(normPath)); ok {
			return true
		}
	}
	return false
}

// validateOmfgRuleAgainstHistory checks a parsed rule's `condition` regexes
// against prior assistant history outputs, returning `(matched, repaired, feedback)`.
// Attempts single unescape repair matching upstream `omfg-rule.ts:383-439`.
func validateOmfgRuleAgainstHistory(candidate *OmfgParsedRule, outputs []omfgHistoryOutput) (matched bool, finalRule *OmfgParsedRule, feedback string) {
	finalRule = candidate
	if len(candidate.Payload.Condition) == 0 {
		// AST-only or question-only rules cannot be regex-validated locally without
		// an ast-grep/judge backend.
		return false, finalRule, "Rule has no `condition` regex to verify against assistant history; add a precise `condition` regex matching the offending output."
	}
	if matchOmfgConditions(candidate.Payload.Condition, candidate.Payload.Scope, outputs) {
		return true, finalRule, ""
	}
	// Try unescaping double-escaped backslashes once (e.g. `\\b` -> `\b`).
	repairedConds := make([]string, len(candidate.Payload.Condition))
	changed := false
	for i, c := range candidate.Payload.Condition {
		r := strings.ReplaceAll(c, `\\`, `\`)
		if r != c {
			changed = true
		}
		repairedConds[i] = r
	}
	if changed {
		if norm, err := normalizeOmfgConditions(repairedConds); err == nil && matchOmfgConditions(norm, candidate.Payload.Scope, outputs) {
			repairedPayload := candidate.Payload
			repairedPayload.Condition = norm
			return true, &OmfgParsedRule{
				Payload:     repairedPayload,
				FileContent: AssembleOmfgRuleMarkdown(repairedPayload),
			}, ""
		}
	}
	return false, finalRule, fmt.Sprintf(
		"No earlier assistant output within scope %s matched condition %s.",
		omfgFormatFrontmatterArray(candidate.Payload.Scope),
		omfgFormatFrontmatterArray(candidate.Payload.Condition),
	)
}

func matchOmfgConditions(conds, scopes []string, outputs []omfgHistoryOutput) bool {
	compiled := make([]*regexp.Regexp, 0, len(conds))
	for _, c := range conds {
		re, err := regexp.Compile(c)
		if err != nil {
			return false
		}
		compiled = append(compiled, re)
	}
	for _, out := range outputs {
		if out.Content == "" || !omfgScopeMatchesOutput(scopes, out) {
			continue
		}
		for _, re := range compiled {
			if re.MatchString(out.Content) {
				return true
			}
		}
	}
	return false
}

func buildOmfgMessages(branch []sdk.BranchEntry, promptText string) []map[string]any {
	var messages []map[string]any
	for _, entry := range branch {
		switch entry.Type {
		case "compaction", "branch_summary":
			if strings.TrimSpace(entry.Content) != "" {
				messages = append(messages, map[string]any{
					"role":    "user",
					"content": []map[string]any{{"type": "text", "text": entry.Content}},
				})
			}
		case "message":
			switch entry.Role {
			case "user":
				if strings.TrimSpace(entry.Content) != "" {
					messages = append(messages, map[string]any{
						"role":    "user",
						"content": []map[string]any{{"type": "text", "text": entry.Content}},
					})
				}
			case "assistant":
				var parts []string
				if strings.TrimSpace(entry.Content) != "" {
					parts = append(parts, entry.Content)
				}
				for _, tc := range entry.ToolCalls {
					parts = append(parts, fmt.Sprintf("[tool_call %s(%s)]", tc.Name, sanitizeSingleLine(tc.Args, 400)))
				}
				if len(parts) > 0 {
					messages = append(messages, map[string]any{
						"role":    "assistant",
						"content": []map[string]any{{"type": "text", "text": strings.Join(parts, "\n")}},
					})
				}
			case "toolResult":
				if strings.TrimSpace(entry.Content) != "" {
					header := fmt.Sprintf("[tool_result %s]", entry.ToolName)
					if entry.IsError {
						header = fmt.Sprintf("[tool_result %s error]", entry.ToolName)
					}
					messages = append(messages, map[string]any{
						"role":    "user",
						"content": []map[string]any{{"type": "text", "text": header + "\n" + entry.Content}},
					})
				}
			}
		}
	}
	messages = append(messages, map[string]any{
		"role":    "user",
		"content": []map[string]any{{"type": "text", "text": promptText}},
	})
	return messages
}

func resolveOmfgAgentDir(ctx sdk.Context) string {
	if AgentDirForContext != nil {
		if dir := strings.TrimSpace(AgentDirForContext(ctx)); dir != "" {
			return dir
		}
	}
	if v := strings.TrimSpace(os.Getenv("PIG_CODING_AGENT_DIR")); v != "" {
		return expandPlanTilde(v)
	}
	if cfgHome, err := ctx.ConfigHome(); err == nil && strings.TrimSpace(cfgHome) != "" {
		return filepath.Join(strings.TrimSpace(cfgHome), "agent")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".pig", "agent")
	}
	return ""
}

func writeOmfgRuleAtomic(destPath, content string, fileMode os.FileMode) error {
	dir := filepath.Dir(destPath)
	dirMode := os.FileMode(0o755)
	if fileMode == 0o600 {
		dirMode = 0o700
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(dir, ".omfg-rule-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmpFile.WriteString(content); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Chmod(fileMode); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// handleOmfgCommand implements `/omfg <complaint>`.
// upstream: packages/coding-agent/src/slash-commands/builtin-lifecycle.ts:452-464
// upstream: packages/coding-agent/src/modes/controllers/omfg-controller.ts:69-296
func (m *manager) handleOmfgCommand(ctx sdk.Context, args string) error {
	complaint := strings.TrimSpace(args)
	if complaint == "" {
		notifyInfo(ctx, "Usage: /omfg <complaint>")
		return nil
	}
	if !ctx.HasUI() {
		notifyError(ctx, "/omfg requires an interactive session to review and save the generated rule.")
		return nil
	}

	modelInfo, err := ctx.GetModelInfo()
	if err != nil || modelInfo == nil || modelInfo.ID == "" {
		notifyError(ctx, "No active model available for /omfg.")
		return nil
	}
	modelObj := map[string]any{
		"id":       modelInfo.ID,
		"provider": modelInfo.Provider,
	}
	if regModel, err := ctx.ModelRegistry().Find(modelInfo.Provider, modelInfo.ID); err == nil && regModel != nil {
		modelObj = regModel
	}

	var auth map[string]any
	if rawAuth, err := ctx.GetModelAuth(modelInfo.Provider, modelInfo.ID); err == nil && len(rawAuth) > 0 {
		_ = json.Unmarshal(rawAuth, &auth)
	}

	branch, _ := ctx.GetBranch()
	history := collectOmfgHistoryOutputs(branch)

	var sysPrompt string
	if sp, err := ctx.GetSystemPrompt(); err == nil && sp != "" {
		sysPrompt = sp
	}

	var (
		failedAttempts []string
		previousRule   string
		lastCandidate  *OmfgParsedRule
		validated      bool
	)

	for {
		lastCandidate = nil
		validated = false
		for attempt := 1; attempt <= omfgMaxAttempts; attempt++ {
			promptText := RenderOmfgUserPrompt(complaint, strings.Join(failedAttempts, "\n\n"), previousRule)
			req := map[string]any{
				"messages":   buildOmfgMessages(branch, promptText),
				"toolChoice": "none",
			}
			if sysPrompt != "" {
				req["systemPrompt"] = sysPrompt
			}

			raw, err := completeOmfg(ctx, modelObj, req, auth)
			if err != nil {
				notifyError(ctx, fmt.Sprintf("/omfg failed: %v", err))
				return nil
			}
			replyText, errMsg := extractCompleteText(raw)
			if errMsg != "" {
				notifyError(ctx, fmt.Sprintf("/omfg failed: %s", errMsg))
				return nil
			}

			parsed, parseErr := ParseGeneratedOmfgRule(replyText)
			if parseErr != nil {
				failedCandidate := ExtractBalancedJSONObject(replyText)
				if failedCandidate == "" {
					failedCandidate = strings.TrimSpace(replyText)
				}
				failedAttempts = append(failedAttempts, fmt.Sprintf("Attempt %d failed: invalid rule (%v).\nFailed candidate:\n%s", attempt, parseErr, failedCandidate))
				previousRule = failedCandidate
				continue
			}

			matched, checkedRule, fb := validateOmfgRuleAgainstHistory(parsed, history)
			lastCandidate = checkedRule
			if matched {
				validated = true
				break
			}
			failedAttempts = append(failedAttempts, fmt.Sprintf("Attempt %d failed validation:\n%s\nFailed candidate:\n%s", attempt, fb, checkedRule.FileContent))
			previousRule = checkedRule.FileContent
		}

		if lastCandidate == nil {
			notifyError(ctx, "The model did not return a valid TTSR rule.")
			return nil
		}

		if !validated {
			confirmed, err := ctx.Confirm("Validation", "Couldn't confirm this rule matches earlier assistant output. Save anyway?")
			if err != nil {
				notifyError(ctx, fmt.Sprintf("/omfg confirmation failed: %v", err))
				return nil
			}
			if ok, _ := confirmed.(bool); !ok {
				notifyInfo(ctx, "Cancelled saving TTSR rule.")
				return nil
			}
		}

		// Re-validate sanitized name before constructing destination path.
		ruleName := lastCandidate.Payload.Name
		if err := ValidateOmfgRuleName(ruleName); err != nil {
			notifyError(ctx, fmt.Sprintf("Refusing to save rule with invalid name: %v", err))
			return nil
		}

		// M6: Show the generated rule preview before prompting for save destination.
		notifyInfo(ctx, fmt.Sprintf("Generated TTSR rule %q:\n\n%s", ruleName, lastCandidate.FileContent))

		choiceVal, ok, err := ctx.Select(
			fmt.Sprintf("Save TTSR rule %q where?", ruleName),
			[]string{omfgProjectOption, omfgGlobalOption, omfgAmendOption, omfgCancelOption},
		)
		if err != nil {
			notifyError(ctx, fmt.Sprintf("/omfg selection failed: %v", err))
			return nil
		}
		if !ok {
			return nil
		}
		choice, _ := choiceVal.(string)
		if choice == "" || choice == omfgCancelOption {
			return nil
		}
		if choice == omfgAmendOption {
			fbVal, fbOK, fbErr := ctx.Input("Amend TTSR rule", "Describe how to change the generated rule:")
			if fbErr != nil {
				notifyError(ctx, fmt.Sprintf("/omfg amend input failed: %v", fbErr))
				return nil
			}
			if !fbOK {
				notifyInfo(ctx, "Cancelled amending TTSR rule.")
				return nil
			}
			fb, _ := fbVal.(string)
			fb = strings.TrimSpace(fb)
			if fb == "" {
				notifyInfo(ctx, "Cancelled amending TTSR rule.")
				return nil
			}
			failedAttempts = append(failedAttempts, "User amendment feedback:\n"+fb)
			previousRule = lastCandidate.FileContent
			continue
		}

		var destPath string
		var fileMode os.FileMode
		switch choice {
		case omfgProjectOption:
			cwd := strings.TrimSpace(ctx.Cwd())
			if cwd == "" {
				notifyError(ctx, "Cannot resolve project directory for .omp/rules.")
				return nil
			}
			rulesDir, safeErr := validateProjectRulesDir(cwd)
			if safeErr != nil {
				notifyError(ctx, fmt.Sprintf("Refusing to save project rule: %v", safeErr))
				return nil
			}
			destPath = filepath.Join(rulesDir, ruleName+".md")
			fileMode = 0o644
		case omfgGlobalOption:
			agentDir := resolveOmfgAgentDir(ctx)
			if agentDir == "" {
				notifyError(ctx, "Cannot resolve global agent directory for rules.")
				return nil
			}
			destPath = filepath.Join(agentDir, "rules", ruleName+".md")
			fileMode = 0o600
		default:
			return nil
		}

		if _, statErr := os.Stat(destPath); statErr == nil {
			overwrite, err := ctx.Confirm("Overwrite TTSR rule?", fmt.Sprintf("%s already exists. Overwrite it?", destPath))
			if err != nil {
				notifyError(ctx, fmt.Sprintf("/omfg overwrite confirmation failed: %v", err))
				return nil
			}
			if b, _ := overwrite.(bool); !b {
				notifyInfo(ctx, "Cancelled overwriting existing TTSR rule.")
				return nil
			}
		}

		if err := writeOmfgRuleAtomic(destPath, lastCandidate.FileContent, fileMode); err != nil {
			notifyError(ctx, fmt.Sprintf("Failed to save TTSR rule to %s: %v", destPath, err))
			return nil
		}

		notifyInfo(ctx, fmt.Sprintf("Saved TTSR rule to %s", destPath))
		return nil
	}
}

// validateProjectRulesDir verifies that `<cwd>/.omp` and `<cwd>/.omp/rules`
// (when present) are not symlinks and that the resolved path stays inside
// the resolved `cwd`.
func validateProjectRulesDir(cwd string) (string, error) {
	resolvedCwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve project directory: %w", err)
	}
	resolvedCwd = filepath.Clean(resolvedCwd)

	ompDir := filepath.Join(cwd, ".omp")
	if fi, err := os.Lstat(ompDir); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%s is a symlink", ompDir)
		}
		if !fi.IsDir() {
			return "", fmt.Errorf("%s is not a directory", ompDir)
		}
		resolvedOmp, err := filepath.EvalSymlinks(ompDir)
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", ompDir, err)
		}
		if !isPathWithinDir(resolvedOmp, resolvedCwd) {
			return "", fmt.Errorf("%s escapes project directory", ompDir)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}

	rulesDir := filepath.Join(ompDir, "rules")
	if fi, err := os.Lstat(rulesDir); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%s is a symlink", rulesDir)
		}
		if !fi.IsDir() {
			return "", fmt.Errorf("%s is not a directory", rulesDir)
		}
		resolvedRules, err := filepath.EvalSymlinks(rulesDir)
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", rulesDir, err)
		}
		if !isPathWithinDir(resolvedRules, resolvedCwd) {
			return "", fmt.Errorf("%s escapes project directory", rulesDir)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return rulesDir, nil
}

func isPathWithinDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
