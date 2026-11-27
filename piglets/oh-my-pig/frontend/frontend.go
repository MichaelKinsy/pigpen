// Package frontend is oh-my-pig's frontend member: pig-tern's member with omp's look fixed on. The drawing
// itself lives in pig-tern's package, so both Piglets share one implementation of the Tern Surface Protocol.
package frontend

import (
	"errors"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
	ternfe "github.com/MichaelKinsy/pigpen/piglets/pig-tern/frontend"
)

// Frontend is the member's factory, the symbol the Piglet build registers.
func Frontend() fe.Frontend {
	return ternfe.FrontendWith(ternfe.Options{
		Omp:       true,
		OnSurface: surface.SetNative,
		Name:      "oh-my-pig",
		Version:   surface.Version,
		// The host gives a frontend the model id; the extension knows its name.
		ModelDisplayName: surface.ModelDisplayName,
		// ModelMeta supplies per-model context window, cost, and thinking levels
		// from the extension's ModelRegistry for Tern's omp model picker.
		ModelMeta: func(provider, id string) (ternfe.ModelMeta, bool) {
			m, ok := surface.LookupModel(provider, id)
			if !ok {
				return ternfe.ModelMeta{}, false
			}
			return ternfe.ModelMeta{
				ID:              m.ID,
				Provider:        m.Provider,
				Name:            m.Name,
				ContextWindow:   m.ContextWindow,
				InputCostPer1M:  m.InputCostPer1M,
				OutputCostPer1M: m.OutputCostPer1M,
				Reasoning:       m.Reasoning,
				ThinkingLevels:  m.ThinkingLevels,
			}, true
		},
		// OnThinkingLevel applies a thinking level picked on the model picker's
		// slider immediately via the extension SDK.
		OnThinkingLevel: surface.RequestThinkingLevel,
		// Relative paths are resolved against the working directory as omp
		// spells it.
		DisplayPath: surface.DisplayPath,
		AgentHubSnapshot: func() ternfe.AgentHubSnapshot {
			return convertAgentHubSnapshot(surface.GetAgentHubSnapshot())
		},
		OnAgentHubChange: func(fn func(ternfe.AgentHubSnapshot)) func() {
			if fn == nil {
				return func() {}
			}
			return surface.OnAgentHubChange(func(snap surface.AgentHubSnapshot) {
				fn(convertAgentHubSnapshot(snap))
			})
		},
		SetAgentHubOpener: surface.SetAgentHubOpener,
		LoadAgentTranscript: func(agentID string) []ternfe.AgentHubTranscriptEntry {
			h := surface.GetAgentHubHandlers()
			if h.LoadTranscript == nil {
				return nil
			}
			src := h.LoadTranscript(agentID)
			if len(src) == 0 {
				return nil
			}
			out := make([]ternfe.AgentHubTranscriptEntry, len(src))
			for i, e := range src {
				out[i] = ternfe.AgentHubTranscriptEntry{
					Role:        e.Role,
					Text:        e.Text,
					Thinking:    e.Thinking,
					ToolName:    e.ToolName,
					ToolArgs:    e.ToolArgs,
					ToolOutput:  e.ToolOutput,
					IsError:     e.IsError,
					TimestampMs: e.TimestampMs,
				}
			}
			return out
		},
		KillAgent: func(agentID string) (string, error) {
			h := surface.GetAgentHubHandlers()
			if h.Kill == nil {
				return "", errors.New("agent runtime unavailable")
			}
			return h.Kill(agentID)
		},
		ReviveAgent: func(agentID string) (string, error) {
			h := surface.GetAgentHubHandlers()
			if h.Revive == nil {
				return "", errors.New("agent runtime unavailable")
			}
			return h.Revive(agentID)
		},
		SendAgentMessage: func(agentID, body string) error {
			h := surface.GetAgentHubHandlers()
			if h.SendMessage == nil {
				return errors.New("agent runtime unavailable")
			}
			return h.SendMessage(agentID, body)
		},
		LookupCompaction: func(tokensBefore int) (ternfe.CompactionMeta, bool) {
			m, ok := surface.LookupCompaction(tokensBefore)
			if !ok {
				return ternfe.CompactionMeta{}, false
			}
			return ternfe.CompactionMeta{
				Method:       m.Method,
				TokensBefore: m.TokensBefore,
				TokensAfter:  m.TokensAfter,
				FrameCount:   m.FrameCount,
				Summary:      m.Summary,
				Warning:      m.Warning,
			}, true
		},
	})
}

func convertAgentHubSnapshot(s surface.AgentHubSnapshot) ternfe.AgentHubSnapshot {
	out := ternfe.AgentHubSnapshot{
		RunningCount:     s.RunningCount,
		InspectableCount: s.InspectableCount,
		SubagentCost:     s.SubagentCost,
		TotalDurationMs:  s.TotalDurationMs,
		TotalRequests:    s.TotalRequests,
		TotalTokens:      s.TotalTokens,
		TotalTools:       s.TotalTools,
	}
	if len(s.Agents) > 0 {
		out.Agents = make([]ternfe.AgentHubItem, len(s.Agents))
		for i, a := range s.Agents {
			out.Agents[i] = ternfe.AgentHubItem{
				ID:            a.ID,
				DisplayName:   a.DisplayName,
				AgentType:     a.AgentType,
				ParentID:      a.ParentID,
				Status:        a.Status,
				Task:          a.Task,
				Activity:      a.Activity,
				Model:         a.Model,
				ModelRole:     a.ModelRole,
				ThinkingLevel: a.ThinkingLevel,
				Cost:          a.Cost,
				DurationMs:    a.DurationMs,
				StartedAtMs:   a.StartedAtMs,
				Requests:      a.Requests,
				Tools:         a.Tools,
				Tokens:        a.Tokens,
				ContextTokens: a.ContextTokens,
				ContextWindow: a.ContextWindow,
				CreatedAtMs:   a.CreatedAtMs,
				LastActiveMs:  a.LastActiveMs,
				SessionFile:   a.SessionFile,
				CanRevive:     a.CanRevive,
			}
		}
	}
	if len(s.Activity) > 0 {
		out.Activity = make([]ternfe.AgentHubActivity, len(s.Activity))
		for i, a := range s.Activity {
			out.Activity[i] = ternfe.AgentHubActivity{
				ID:          a.ID,
				AgentID:     a.AgentID,
				Kind:        a.Kind,
				Title:       a.Title,
				Detail:      a.Detail,
				Status:      a.Status,
				TimestampMs: a.TimestampMs,
			}
		}
	}
	return out
}
