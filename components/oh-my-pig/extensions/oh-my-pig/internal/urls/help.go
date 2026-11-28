package urls

import (
	_ "embed"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/internal-urls/help-protocol.ts,
//           packages/coding-agent/src/tools/doc-topics.ts,
//           packages/coding-agent/src/tools/eval-doc-topics.ts

//go:embed prompts/help-reference.md
var helpReferenceDoc string

//go:embed prompts/read-sources.md
var readSourcesTopicDoc string

//go:embed prompts/eval-helpers.md
var evalHelpersTopicDoc string

//go:embed prompts/eval-judge.md
var evalJudgeTopicDoc string

//go:embed prompts/eval-agents.md
var evalAgentsTopicDoc string

var helpPathRE = regexp.MustCompile(`^(?:([^/?#\s]+)(?:/([^/?#\s]+))?/?)?$`)

// HelpTopicProvider supplies a map of topic -> markdown for a tool.
type HelpTopicProvider func(c Context) map[string]string

var (
	helpMu             sync.RWMutex
	helpTopicProviders = map[string]HelpTopicProvider{}
	evalPreludeDocsFn  func(sdk.Context) map[string]string
)

// RegisterHelpTopics registers or replaces on-demand help topics for a tool
// (`help://<tool>/<topic>`).
func RegisterHelpTopics(tool string, provider HelpTopicProvider) {
	helpMu.Lock()
	helpTopicProviders[tool] = provider
	helpMu.Unlock()
}

// SetEvalPreludeDocsProvider installs a callback returning enabled eval prelude
// docs (prelude name -> markdown) for `help://eval/<name>`. Using a callback
// avoids an import cycle between internal/urls and internal/eval.
func SetEvalPreludeDocsProvider(fn func(sdk.Context) map[string]string) {
	helpMu.Lock()
	evalPreludeDocsFn = fn
	helpMu.Unlock()
}

// ResetHelpTopicsForTests clears custom tool help topic providers.
func ResetHelpTopicsForTests() {
	helpMu.Lock()
	helpTopicProviders = map[string]HelpTopicProvider{}
	evalPreludeDocsFn = nil
	helpMu.Unlock()
}

// ParseHelpURL parses `help://`, `help://<tool>`, and `help://<tool>/<topic>`.
// Returns ok=false for anything else.
// upstream: tools/doc-topics.ts parseHelpUrl.
func ParseHelpURL(input string) (tool, topic string, ok bool) {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(strings.ToLower(trimmed), "help://") {
		return "", "", false
	}
	m := helpPathRE.FindStringSubmatch(trimmed[len("help://"):])
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// HelpHandler serves `help://<tool>` and `help://<tool>/<topic>` on-demand reference docs.
// upstream: help-protocol.ts HelpProtocolHandler.
type HelpHandler struct{}

func (HelpHandler) Scheme() string { return "help" }

func (HelpHandler) Spec() Spec {
	return Spec{
		Backing:           BackingVirtual,
		Selectors:         SelectorsLines,
		Immutable:         true,
		CompactTranscript: true,
	}
}

func (HelpHandler) PromptReference() string {
	return strings.TrimSpace(helpReferenceDoc)
}

func (HelpHandler) Resolve(c Context, u URL) (*Resource, error) {
	tool, topic, ok := ParseHelpURL(u.Href)
	if !ok {
		return nil, fmt.Errorf("Invalid help:// URL: %s. Use help://<tool>/<topic>.", u.Href)
	}
	if tool == "" {
		return nil, fmt.Errorf("help:// needs a tool and topic: use help://<tool>/<topic>.")
	}
	topics, order, err := resolveToolTopics(c, tool)
	if err != nil {
		return nil, err
	}
	var content string
	if topic == "" {
		lines := make([]string, len(order))
		for i, k := range order {
			lines[i] = fmt.Sprintf("help://%s/%s", tool, k)
		}
		content = strings.Join(lines, "\n")
	} else {
		doc, exists := topics[topic]
		if !exists {
			return nil, fmt.Errorf("Unknown topic '%s' for %s. Available: %s.", topic, tool, strings.Join(order, ", "))
		}
		content = doc
	}
	return &Resource{
		URL:         u.Href,
		Content:     content,
		ContentType: "text/plain",
	}, nil
}

func resolveToolTopics(c Context, tool string) (map[string]string, []string, error) {
	helpMu.RLock()
	prov := helpTopicProviders[tool]
	helpMu.RUnlock()
	if prov != nil {
		topics := prov(c)
		if len(topics) == 0 {
			return nil, nil, fmt.Errorf("Tool '%s' has no doc topics.", tool)
		}
		keys := make([]string, 0, len(topics))
		for k := range topics {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return topics, keys, nil
	}

	switch tool {
	case "read":
		refLines := Reference()
		formatted := make([]string, len(refLines))
		for i, line := range refLines {
			formatted[i] = "- " + line
		}
		return map[string]string{
			"sources": strings.TrimSpace(readSourcesTopicDoc),
			"urls":    strings.Join(formatted, "\n"),
		}, []string{"sources", "urls"}, nil
	case "eval":
		// upstream: tools/eval-doc-topics.ts:83-92 order: judge, helpers, agents, then preludes.
		topics := map[string]string{
			"judge":   strings.TrimSpace(evalJudgeTopicDoc),
			"helpers": strings.TrimSpace(evalHelpersTopicDoc),
			"agents":  strings.TrimSpace(evalAgentsTopicDoc),
		}
		order := []string{"judge", "helpers", "agents"}
		helpMu.RLock()
		fn := evalPreludeDocsFn
		helpMu.RUnlock()
		if fn != nil {
			preludes := fn(c.SDK)
			pNames := make([]string, 0, len(preludes))
			for name := range preludes {
				pNames = append(pNames, name)
			}
			sort.Strings(pNames)
			for _, name := range pNames {
				if trimmed := strings.TrimSpace(preludes[name]); trimmed != "" {
					if _, exists := topics[name]; !exists {
						order = append(order, name)
					}
					topics[name] = trimmed
				}
			}
		}
		return topics, order, nil
	default:
		// upstream: help-protocol.ts:70 — distinguish an active tool without doc topics
		// from a tool absent in this session.
		if c.SDK != (sdk.Context{}) {
			if active, err := c.SDK.GetActiveTools(); err == nil {
				for _, name := range active {
					if name == tool {
						return nil, nil, fmt.Errorf("Tool '%s' has no doc topics.", tool)
					}
				}
			}
		}
		return nil, nil, fmt.Errorf("No tool '%s' in this session.", tool)
	}
}
