package pi_subagents

import (
	"strings"
	"testing"
)

func advAgent(name string, mod func(*AgentConfig)) AgentConfig {
	a := AgentConfig{Name: name, Description: name + " agent", SystemPrompt: name + " prompt", SystemPromptMode: "replace", Source: SourceProject, FilePath: "/tmp/" + name + ".md"}
	if mod != nil {
		mod(&a)
	}
	return a
}

func advertised(a *AgentConfig) { a.Advertise = new(bool); *a.Advertise = true }

func TestAdvertisedAgentPrompt(t *testing.T) {
	tw(t, "advertised-agent-prompt", "includes only opted-in, capability-permitted agents in stable order", func(t *testing.T) {
		ceiling, err := normalizeCeiling(js(t, `{"allowedAgents":["alpha","hidden","zeta"],"denyExtensions":false}`))
		if err != nil {
			t.Fatal(err)
		}
		prompt := buildAdvertisedAgentPrompt([]AgentConfig{
			advAgent("zeta", advertised), advAgent("hidden", nil),
			advAgent("disabled", func(a *AgentConfig) { advertised(a); a.Disabled = new(bool); *a.Disabled = true }),
			advAgent("alpha", advertised),
		}, ceiling)
		if prompt == "" {
			t.Fatal("no prompt")
		}
		polMatches(t, prompt, `<name>alpha</name>`)
		polMatches(t, prompt, `<name>zeta</name>`)
		polNotMatches(t, prompt, `hidden|disabled`)
		if strings.Index(prompt, "alpha") >= strings.Index(prompt, "zeta") {
			t.Error("order")
		}
	})
	tw(t, "advertised-agent-prompt", "escapes agent-owned metadata and bounds the catalog", func(t *testing.T) {
		var agents []AgentConfig
		for i := 0; i < 18; i++ {
			i := i
			agents = append(agents, advAgent("agent-"+pad2digits(i), func(a *AgentConfig) {
				advertised(a)
				a.Description = "agent " + strconvItoa(i)
				if i == 0 {
					a.Description = "<route>&" + strings.Repeat("x", 700)
				}
			}))
		}
		prompt := buildAdvertisedAgentPrompt(agents, nil)
		polMatches(t, prompt, `&lt;route&gt;&amp;`)
		polNotMatches(t, prompt, `<route>`)
		polMatches(t, prompt, `…</description>`)
		polMatches(t, prompt, `<omitted count="2" />`)
	})
	tw(t, "advertised-agent-prompt", "returns no catalog when no agent opts in", func(t *testing.T) {
		eq(t, buildAdvertisedAgentPrompt([]AgentConfig{advAgent("hidden", nil)}, nil), "", "prompt")
	})
	tw(t, "advertised-agent-prompt", "admits an exact-budget canonical name and omits it one byte over", func(t *testing.T) {
		small := func(name string) string {
			return buildAdvertisedAgentPrompt([]AgentConfig{advAgent(name, func(a *AgentConfig) { advertised(a); a.Description = "small" })}, nil)
		}
		name := strings.Repeat("x", 12288-len(small("")))
		exact := small(name)
		eq(t, len(exact), 12288, "exact budget")
		if !strings.Contains(exact, "<name>"+name+"</name>") {
			t.Error("exact name missing")
		}
		over := small(name + "x")
		polNotMatches(t, over, `<name>`)
		polMatches(t, over, `<omitted count="1"`)
	})
	tw(t, "advertised-agent-prompt", "builds a section body without the outer tag that Pi adds from the section key", func(t *testing.T) {
		agents := []AgentConfig{advAgent("alpha", advertised), advAgent("hidden", nil)}
		body := buildAdvertisedAgentCatalog(agents, nil)
		polNotMatches(t, body, `advertised_subagents`)
		polMatches(t, body, `<name>alpha</name>`)
		polNotMatches(t, body, `hidden`)
		eq(t, buildAdvertisedAgentPrompt(agents, nil), "<advertised_subagents>\n"+body+"\n</advertised_subagents>", "wrapped")
		eq(t, buildAdvertisedAgentCatalog([]AgentConfig{advAgent("hidden", nil)}, nil), "", "none")
	})
	tw(t, "advertised-agent-prompt", "bounds the section body by the same wrapped byte budget", func(t *testing.T) {
		var agents []AgentConfig
		for i := 0; i < 40; i++ {
			agents = append(agents, advAgent("agent-"+pad2digits(i), func(a *AgentConfig) { advertised(a); a.Description = strings.Repeat("🦜界", 200) }))
		}
		body := buildAdvertisedAgentCatalog(agents, nil)
		wrapped := buildAdvertisedAgentPrompt(agents, nil)
		eq(t, wrapped, "<advertised_subagents>\n"+body+"\n</advertised_subagents>", "wrapped")
		if len(wrapped) > 12288 {
			t.Errorf("%d bytes", len(wrapped))
		}
		polMatches(t, body, `<omitted count="\d+" />`)
	})
}

func pad2digits(i int) string {
	if i < 10 {
		return "0" + strconvItoa(i)
	}
	return strconvItoa(i)
}
