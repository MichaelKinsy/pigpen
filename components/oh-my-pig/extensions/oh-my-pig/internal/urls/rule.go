package urls

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

// upstream: packages/coding-agent/src/internal-urls/rule-protocol.ts

//go:embed prompts/rule.md
var rulePromptDoc string

// RuleEntry describes a loaded rule addressable via `rule://<name>`.
// upstream: capability/rule.ts Rule.
type RuleEntry struct {
	Name        string
	Description string
	Path        string
	Content     string
}

var (
	rulesMu       sync.RWMutex
	activeRules   []RuleEntry
	rulesProvider func(c Context) []RuleEntry
)

// SetActiveRules installs a process-wide rule list.
func SetActiveRules(rules []RuleEntry) {
	rulesMu.Lock()
	activeRules = append([]RuleEntry(nil), rules...)
	rulesMu.Unlock()
}

// SetRulesProvider installs a per-context rule provider (e.g. for subagent-scoped rules).
func SetRulesProvider(fn func(c Context) []RuleEntry) {
	rulesMu.Lock()
	rulesProvider = fn
	rulesMu.Unlock()
}

// ResetRulesForTests clears active rules and provider.
func ResetRulesForTests() {
	rulesMu.Lock()
	activeRules = nil
	rulesProvider = nil
	rulesMu.Unlock()
}

func resolveActiveRules(c Context) []RuleEntry {
	rulesMu.RLock()
	prov := rulesProvider
	active := activeRules
	rulesMu.RUnlock()
	if prov != nil {
		if r := prov(c); r != nil {
			return r
		}
	}
	return active
}

func lookupRule(u URL, c Context) (*RuleEntry, []RuleEntry, error) {
	ruleName := u.Host
	if ruleName == "" {
		return nil, nil, errors.New("rule:// URL requires a rule name: rule://<name>")
	}
	rules := resolveActiveRules(c)
	for i := range rules {
		if rules[i].Name == ruleName {
			r := rules[i]
			return &r, rules, nil
		}
	}
	return nil, rules, nil
}

// RuleHandler serves `rule://<name>` URLs.
// upstream: rule-protocol.ts RuleProtocolHandler.
type RuleHandler struct{}

func (RuleHandler) Scheme() string { return "rule" }

func (RuleHandler) Spec() Spec {
	return Spec{
		Backing:   BackingVirtual,
		Selectors: SelectorsLines,
		Immutable: true,
		Linkable:  true,
	}
}

func (RuleHandler) PromptDoc(host SchemeHost) string {
	if host.RuleCount <= 0 {
		return ""
	}
	return strings.TrimSpace(rulePromptDoc)
}

func (RuleHandler) Resolve(c Context, u URL) (*Resource, error) {
	rule, rules, err := lookupRule(u, c)
	if err != nil {
		return nil, err
	}
	if rule == nil {
		avail := "none"
		if len(rules) > 0 {
			names := make([]string, len(rules))
			for i, r := range rules {
				names[i] = r.Name
			}
			avail = strings.Join(names, ", ")
		}
		return nil, fmt.Errorf("Unknown rule: %s\nAvailable: %s", u.Host, avail)
	}
	return &Resource{
		URL:         u.Href,
		Content:     rule.Content,
		ContentType: "text/markdown",
		SourcePath:  rule.Path,
	}, nil
}

func (RuleHandler) Locate(c Context, u URL, _ LocateOptions) (string, bool, error) {
	rule, _, err := lookupRule(u, c)
	if err != nil {
		return "", false, err
	}
	if rule == nil || rule.Path == "" {
		return "", false, nil
	}
	if _, err := os.Stat(rule.Path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	return rule.Path, true, nil
}

func (RuleHandler) Complete(c Context, _ string) ([]Completion, error) {
	rules := resolveActiveRules(c)
	out := make([]Completion, len(rules))
	for i, r := range rules {
		out[i] = Completion{
			Value:       r.Name,
			Description: r.Description,
		}
	}
	return out, nil
}
