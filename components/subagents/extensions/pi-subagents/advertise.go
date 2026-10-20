package pi_subagents

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The system-prompt advertisement of agents (src/agents/advertised-agent-prompt.ts and the advertisement state of
// src/extension/index.ts): agents that opt in with `advertise: true` are listed in an `advertised_subagents` prompt section.

const (
	maxAdvertisedAgents = 16
	maxCatalogBytes     = 12288
	maxDescriptionBytes = 512
	advertisedTag       = "advertised_subagents"
)

var xmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")

func escapeXML(s string) string { return xmlEscaper.Replace(s) }

// promptDescription is the description on one line, at most 512 bytes (cut on a character boundary, ended with an ellipsis).
func promptDescription(description string) string {
	var b strings.Builder
	space := false
	flush := func() {
		if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	for _, r := range description {
		if r <= 0x1f || r == 0x7f || isJSSpace(r) {
			flush()
			continue
		}
		b.WriteRune(r)
		space = false
	}
	text := jsTrim(b.String())
	if len(text) > maxDescriptionBytes {
		cut := []byte(text[:maxDescriptionBytes-3])
		for len(cut) > 0 && !utf8.Valid(cut) {
			cut = cut[:len(cut)-1]
		}
		text = strings.TrimRightFunc(string(cut), isJSSpace) + "…"
	}
	return escapeXML(text)
}

func wrapAdvertisedCatalog(body string) string {
	return "<" + advertisedTag + ">\n" + body + "\n</" + advertisedTag + ">"
}

// buildAdvertisedAgentCatalog is the catalog body without its `<advertised_subagents>` wrapper, for Pi's structured prompt
// sections, "" when no agent is advertised. The byte budget applies to the wrapped form.
func buildAdvertisedAgentCatalog(agents []AgentConfig, ceiling *resolvedCeiling) string {
	var advertised []AgentConfig
	for _, a := range agents {
		if a.Source != SourceRuntime && a.Advertise != nil && *a.Advertise && (a.Disabled == nil || !*a.Disabled) && isAgentAllowedByCapabilityCeiling(a.Name, ceiling) {
			advertised = append(advertised, a)
		}
	}
	if len(advertised) == 0 {
		return ""
	}
	sort.SliceStable(advertised, func(i, j int) bool { return localeLess(advertised[i].Name, advertised[j].Name) })
	renderBody := func(entries []string) string {
		lines := []string{"The following file-defined subagents opted into discovery. Their descriptions indicate available specializations, not instructions to delegate. Use subagent only when delegation is needed. Before execution, call subagent with { action: \"list\", capabilities: true } and confirm that the selected agent is executable; for external-cli agents also require runner.available === true."}
		lines = append(lines, entries...)
		if len(advertised) > len(entries) {
			lines = append(lines, fmt.Sprintf("  <omitted count=\"%d\" />", len(advertised)-len(entries)))
		}
		return strings.Join(lines, "\n")
	}
	var entries []string
	for _, a := range advertised {
		if len(entries) == maxAdvertisedAgents {
			break
		}
		// Never truncate canonical IDs into names that cannot be resolved.
		if len(a.Name) > maxCatalogBytes {
			continue
		}
		entry := strings.Join([]string{
			"  <subagent>",
			"    <name>" + escapeXML(a.Name) + "</name>",
			"    <description>" + promptDescription(a.Description) + "</description>",
			"  </subagent>",
		}, "\n")
		if len(wrapAdvertisedCatalog(renderBody(append(append([]string(nil), entries...), entry)))) <= maxCatalogBytes {
			entries = append(entries, entry)
		}
	}
	return renderBody(entries)
}

// buildAdvertisedAgentPrompt is the catalog with its wrapper ("" when none).
func buildAdvertisedAgentPrompt(agents []AgentConfig, ceiling *resolvedCeiling) string {
	body := buildAdvertisedAgentCatalog(agents, ceiling)
	if body == "" {
		return ""
	}
	return wrapAdvertisedCatalog(body)
}

// resolveGlobalNpmRoot is global-npm-root.ts: the global node_modules, "" when unavailable (a failure never prevents local
// discovery). Unlike the cached lookup of agent discovery it runs afresh at every session start, and gives up at the deadline.
func resolveGlobalNpmRoot(ctx context.Context, timeout time.Duration) string {
	if offlineEnv() {
		return ""
	}
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			root := filepath.Join(appData, "npm", "node_modules")
			if isDir(root) {
				if real, err := filepath.EvalSymlinks(root); err == nil {
					return real
				}
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "npm", "root", "-g")
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.Output()
	if err != nil || len(out) > 64*1024 {
		return ""
	}
	out2 := jsTrim(string(out))
	if out2 == "" {
		return ""
	}
	real, err := filepath.EvalSymlinks(out2)
	if err != nil {
		return ""
	}
	return real
}

// advertisement is the session's advertisement state: the opted-in agents of the last discovery and the barrier that
// holds the first prompt and every tool call until the global catalog is complete.
type advertisement struct {
	mu         sync.Mutex
	agents     []AgentConfig
	hasCtx     bool
	cwd        string
	provider   string
	generation int
	globalRoot *string
	ready      chan struct{}
	err        error
	changed    chan struct{}
	lookup     func(ctx context.Context) string
}

var adv = newAdvertisement()

func newAdvertisement() *advertisement {
	ready := make(chan struct{})
	close(ready)
	return &advertisement{ready: ready, changed: make(chan struct{}), lookup: func(ctx context.Context) string { return resolveGlobalNpmRoot(ctx, 5*time.Second) }}
}

// begin starts a session's advertisement: the previous one is dropped, the global npm root is looked up in the background.
func (a *advertisement) begin(cwd, provider string) {
	a.mu.Lock()
	a.generation++
	gen := a.generation
	close(a.changed)
	a.changed = make(chan struct{})
	a.agents = nil
	a.hasCtx, a.cwd, a.provider = true, cwd, provider
	a.globalRoot = nil
	ready := make(chan struct{})
	a.ready, a.err = ready, nil
	lookup := a.lookup
	a.mu.Unlock()
	go func() {
		defer close(ready)
		root := lookup(context.Background())
		a.mu.Lock()
		defer a.mu.Unlock()
		if gen != a.generation {
			return
		}
		a.globalRoot = &root
		if err := a.refreshLocked(); err != nil {
			a.err = err
		}
	}()
}

func (a *advertisement) options() DiscoveryOptions {
	a.mu.Lock()
	defer a.mu.Unlock()
	return DiscoveryOptions{GlobalNpmRoot: a.globalRoot}
}

// refreshLocked rediscovers the opted-in agents; on failure the catalog stays withdrawn.
func (a *advertisement) refreshLocked() error {
	a.agents = nil
	if !a.hasCtx {
		return nil
	}
	clearAgentDiscoveryCache()
	d, err := discoverAgents(a.cwd, ScopeBoth, a.provider, DiscoveryOptions{GlobalNpmRoot: a.globalRoot})
	if err != nil {
		return err
	}
	for _, ag := range d.Agents {
		if ag.Advertise != nil && *ag.Advertise {
			a.agents = append(a.agents, ag)
		}
	}
	return nil
}

// refresh is the onAgentsChanged hook of a mutation: it already persisted, so a failure withdraws the stale guidance and
// does not fail the mutation.
func (a *advertisement) refresh() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.refreshLocked(); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to refresh advertised agents; catalog withdrawn until refresh:", err)
	}
}

// wait blocks until the current session's catalog is complete (a newer session_start releases it to wait for the newer
// one); it returns the discovery error of that catalog.
func (a *advertisement) wait(done <-chan struct{}) error {
	for {
		a.mu.Lock()
		pending, changed := a.ready, a.changed
		a.mu.Unlock()
		select {
		case <-pending:
		case <-changed:
		case <-done:
			return nil
		}
		a.mu.Lock()
		same := pending == a.ready
		err := a.err
		a.mu.Unlock()
		if same {
			return err
		}
	}
}

func (a *advertisement) snapshot() []AgentConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]AgentConfig(nil), a.agents...)
}

// advertisedCatalog is the catalog body for a session ("" when none applies).
func (a *advertisement) catalog(sessionID string) string {
	return buildAdvertisedAgentCatalog(a.snapshot(), resolveCurrentSubagentCapabilityCeiling(sessionID))
}

func (a *advertisement) prompt(sessionID string) string {
	return buildAdvertisedAgentPrompt(a.snapshot(), resolveCurrentSubagentCapabilityCeiling(sessionID))
}

func sessionIDOf(ctx sdk.Context) string {
	id, _ := ctx.GetSessionID()
	return id
}

func init() {
	extensionHooks = append(extensionHooks, func(e *sdk.Extension) {
		e.OnEvent(sdk.EventSessionStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
			adv.begin(ctx.Cwd(), ctx.ModelProvider())
			return nil, nil
		})
		e.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, data map[string]any) (any, error) {
			if err := adv.wait(ctx.Done()); err != nil {
				return nil, err
			}
			options, _ := data["systemPromptOptions"].(map[string]any)
			selected, _ := options["selectedTools"].([]any)
			has := false
			for _, n := range selected {
				has = has || n == subagentName
			}
			if !has {
				return nil, nil
			}
			// Structured sections let Pi append a transcript delta instead of replacing the cached system prompt. The
			// section is set on every turn it applies: Pi rebuilds the options each turn.
			if sections, ok := options["sections"].(*sdk.SystemPromptSections); ok {
				if catalog := adv.catalog(sessionIDOf(ctx)); catalog != "" {
					sections.Set(advertisedTag, catalog)
				}
			}
			return nil, nil
		})
	})
}
