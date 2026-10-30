package pi_permission_system

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// What the original enforces and the port does not read yet. Each is a way a call could be allowed here while the original
// denies or asks about it, so each refuses (fail-closed) until it is ported: project scope (getProjectConfigPath), agent scope
// (the agent frontmatter, src/config/policy-loader.ts loadScopeConfigFrom) and the `/skill:` input gate (permission-gate-handler.ts
// handleInput).

// activeAgentTag is the tag pi-agent-router puts in the system prompt (src/session/active-agent.ts ACTIVE_AGENT_TAG_REGEX).
var activeAgentTag = regexp.MustCompile(`(?i)<active_agent\s+name=["']([^"']+)["'][^>]*>`)

// agentFromSystemPrompt is getActiveAgentNameFromSystemPrompt.
func agentFromSystemPrompt(prompt string) string {
	m := activeAgentTag.FindStringSubmatch(prompt)
	if m == nil {
		return ""
	}
	return jsTrim(m[1])
}

// agentFromEntries is getActiveAgentName: the name of the last `active_agent` custom entry; a null name clears it.
func agentFromEntries(entries []json.RawMessage) string {
	for i := len(entries) - 1; i >= 0; i-- {
		var e struct {
			Type       string `json:"type"`
			CustomType string `json:"customType"`
			Data       *struct {
				Name json.RawMessage `json:"name"`
			} `json:"data"`
		}
		if json.Unmarshal(entries[i], &e) != nil || e.Type != "custom" || e.CustomType != "active_agent" {
			continue
		}
		if e.Data == nil {
			continue
		}
		var name *string
		if json.Unmarshal(e.Data.Name, &name) != nil {
			continue
		}
		if name != nil {
			if n := jsTrim(*name); n != "" {
				return n
			}
			continue
		}
		if string(e.Data.Name) == "null" {
			return ""
		}
	}
	return ""
}

var permissionFrontmatter = regexp.MustCompile(`(?m)^permission\s*:`)

// agentFileHasPermission reports whether an agent definition file carries a permission block in its frontmatter. An unreadable
// file counts: the original fails closed for it too.
func agentFileHasPermission(path string) (exists, has bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		_, statErr := os.Stat(path)
		return statErr == nil, statErr == nil
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return true, false
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return true, false
	}
	return true, permissionFrontmatter.MatchString(text[4 : 4+end])
}

func projectConfigPath(cwd string) string {
	return filepath.Join(cwd, ".pi", "extensions", extensionID, "config.json")
}

// unportedScope names the permission scope the original would merge into this call's decision and the port cannot read, or "".
// A project scope counts while the project is trusted (or trust cannot be told); an agent scope counts for the active agent.
func (s *state) unportedScope(ctx sdk.Context) string {
	trusted, err := ctx.IsProjectTrusted()
	if err != nil {
		trusted = true
	}
	cwd := ctx.Cwd()
	if trusted && cwd != "" {
		if _, err := os.Stat(projectConfigPath(cwd)); err == nil {
			return "project permission rules (" + projectConfigPath(cwd) + ")"
		}
	}
	dirs := []string{filepath.Join(agentDir(), "agents")}
	if trusted && cwd != "" {
		dirs = append(dirs, filepath.Join(cwd, ".pi", "agents"))
	}
	// Agent definitions that carry permissions; only then is the active agent worth asking for (it reads the session).
	var withPermission []string
	for _, d := range dirs {
		files, _ := filepath.Glob(filepath.Join(d, "*.md"))
		for _, path := range files {
			if _, has := agentFileHasPermission(path); has {
				withPermission = append(withPermission, path)
			}
		}
	}
	if len(withPermission) == 0 {
		return ""
	}
	name := ""
	entries, err := ctx.GetEntries()
	if err == nil {
		name = agentFromEntries(entries)
	}
	if name == "" {
		s.mu.Lock()
		name = s.agentName
		s.mu.Unlock()
	}
	if name == "" {
		if err != nil {
			// The active agent cannot be told, so any agent definition with permissions could apply.
			return "agent permission rules (" + withPermission[0] + ", the active agent could not be determined)"
		}
		return ""
	}
	for _, d := range dirs {
		path := filepath.Join(d, name+".md")
		if _, has := agentFileHasPermission(path); has {
			return "agent permission rules ('" + name + "' in " + path + ")"
		}
	}
	return ""
}

// extractSkillNameFromInput is the skill a `/skill:<name>` input invokes, "" for any other input (permission-gate-handler.ts).
func extractSkillNameFromInput(text string) string {
	t := jsTrim(text)
	if !strings.HasPrefix(t, "/skill:") {
		return ""
	}
	rest := t[len("/skill:"):]
	if rest == "" {
		return ""
	}
	if i := strings.IndexFunc(rest, isJSSpace); i >= 0 {
		rest = rest[:i]
	}
	return jsTrim(rest)
}

// formatSkillDenyNotice is the warning for a denied skill (skill-input-gate-pipeline.ts); it names the agent when there is one.
func formatSkillDenyNotice(skill, agent string) string {
	if agent != "" {
		return "Skill '" + skill + "' is not permitted for agent '" + agent + "'."
	}
	return "Skill '" + skill + "' is not permitted by the current skill policy."
}

// skillInput is the `input` handler: a `/skill:<name>` input is decided by the skill surface for that name. An
// allow continues, a deny is handled with a warning, and an ask is put to the approval dialog (which carries no
// session option for the skill gates, as in the original); a scope the port cannot read is refused.
func (s *state) skillInput(ctx sdk.Context, data map[string]any) (any, error) {
	text, _ := data["text"].(string)
	skill := extractSkillNameFromInput(text)
	if skill == "" {
		return nil, nil
	}
	handled := map[string]any{"action": "handled"}
	if feature := s.unportedScope(ctx); feature != "" {
		ctx.Notify(refusal(feature), "warning")
		return handled, nil
	}
	s.mu.Lock()
	agent := s.agentName
	s.mu.Unlock()
	rule := Evaluate("skill", skill, s.current().rules, PosixPathFlavor, "")
	switch rule.Action {
	case "allow":
		return nil, nil
	case "deny":
		ctx.Notify(formatSkillDenyNotice(skill, agent), "warning")
	default:
		a := skillAsk(skill, "")
		if d := promptAsk(ctx, a, false); !d.approved {
			return handled, nil
		}
		return nil, nil // approved: the input continues, as an allow does
	}
	return handled, nil
}

// unportedOptions lists the config options that are set and that nothing in the port reads yet: the dialog, forwarding,
// logging and infrastructure-read settings of the original.
func unportedOptions(root *jsObject) []string {
	var out []string
	for _, k := range []string{"debugLog", "permissionReviewLog", "doublePressToConfirm", "permissionDialogKeys", "promptNotifications",
		"forwardingTimeoutMs", "promptMaxRows", "promptFieldMaxWidth", "reviewLogFieldMaxWidth", "piInfrastructureReadPaths",
		"authorizerChain"} {
		v, ok := root.vals[k]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case bool:
			if !x {
				continue
			}
		case *jsObject:
			if len(x.order()) == 0 {
				continue
			}
		case []any:
			if len(x) == 0 {
				continue
			}
		}
		out = append(out, k)
	}
	return out
}
