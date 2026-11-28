package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

func mustRawJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return b
}

func currentBranchFixtureEntries(t *testing.T) []json.RawMessage {
	t.Helper()
	ts := "2026-11-27T12:00:00Z"
	return []json.RawMessage{
		mustRawJSON(t, map[string]any{
			"type":      "message",
			"id":        "before-first-compaction",
			"parentId":  nil,
			"timestamp": ts,
			"message": map[string]any{
				"role":    "user",
				"content": "oldest raw request survives",
			},
		}),
		mustRawJSON(t, map[string]any{
			"type":      "message",
			"id":        "sibling-abandoned",
			"parentId":  "before-first-compaction",
			"timestamp": ts,
			"message": map[string]any{
				"role":    "user",
				"content": "sibling branch text must not leak",
			},
		}),
		mustRawJSON(t, map[string]any{
			"type":             "compaction",
			"id":               "first-compaction",
			"parentId":         "before-first-compaction",
			"timestamp":        ts,
			"summary":          "first compacted window",
			"firstKeptEntryId": "between-compactions",
			"tokensBefore":     100,
		}),
		mustRawJSON(t, map[string]any{
			"type":      "message",
			"id":        "between-compactions",
			"parentId":  "first-compaction",
			"timestamp": ts,
			"message": map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "thinking", "thinking": "visible reasoning"},
					map[string]any{"type": "text", "text": "middle assistant reply"},
					map[string]any{
						"type":      "toolCall",
						"id":        "tc-1",
						"name":      "read",
						"arguments": map[string]any{"path": "notes.txt"},
					},
				},
			},
		}),
		mustRawJSON(t, map[string]any{
			"type":      "message",
			"id":        "tool-res-1",
			"parentId":  "between-compactions",
			"timestamp": ts,
			"message": map[string]any{
				"role":       "toolResult",
				"toolName":   "read",
				"toolCallId": "tc-1",
				"isError":    false,
				"content":    []any{map[string]any{"type": "text", "text": "file body line"}},
			},
		}),
		mustRawJSON(t, map[string]any{
			"type":      "reset_boundary",
			"id":        "window-reset",
			"parentId":  "tool-res-1",
			"timestamp": ts,
		}),
		mustRawJSON(t, map[string]any{
			"type":      "message",
			"id":        "latest-entry",
			"parentId":  "window-reset",
			"timestamp": ts,
			"message": map[string]any{
				"role":    "user",
				"content": "latest raw request survives",
			},
		}),
	}
}

func TestHistoryCurrentFullUncompactedBranch(t *testing.T) {
	rt := NewRuntime(DefaultConfig())
	ext := sdk.New("test-history-current-full")
	RegisterWithRuntime(ext, rt)

	entries := currentBranchFixtureEntries(t)
	h := startFakeHost(t, ext, fakeHostOptions{
		SessionID:      "sess-history-full",
		SessionEntries: entries,
		LeafID:         "latest-entry",
	})
	h.fireEvent(sdk.EventSessionStart, map[string]any{"reason": "startup"})
	ctx := rt.sessionContextFor("sess-history-full", sdk.Context{})

	res, err := urls.Resolve(urls.Context{SDK: ctx, Cwd: h.opts.Cwd}, "history://current/full")
	if err != nil {
		t.Fatalf("resolve history://current/full: %v", err)
	}
	if res.ContentType != "text/markdown" {
		t.Fatalf("contentType = %q, want text/markdown", res.ContentType)
	}
	if res.SourcePath != "" {
		t.Fatalf("expected empty sourcePath for in-memory branch dump, got %q", res.SourcePath)
	}
	for _, want := range []string{
		"# Current branch — full history",
		"## Entry before-first-compaction · message",
		"### user",
		"oldest raw request survives",
		"## Entry first-compaction · compaction",
		"> Context window boundary: compaction",
		"First kept entry: between-compactions",
		"Tokens before: 100",
		"first compacted window",
		"<thinking>",
		"visible reasoning",
		"</thinking>",
		"middle assistant reply",
		"#### tool call: read (tc-1)",
		`"path": "notes.txt"`,
		"### tool result: read (tc-1)",
		"file body line",
		"## Entry window-reset · reset_boundary",
		"> Context window boundary: reset",
		"latest raw request survives",
	} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("history://current/full missing %q in:\n%s", want, res.Content)
		}
	}
	if strings.Contains(res.Content, "sibling branch text must not leak") {
		t.Fatalf("abandoned sibling branch leaked into history://current/full:\n%s", res.Content)
	}

	// Bare history://current resolves to current/full when no agent is named "current".
	bareRes, err := urls.Resolve(urls.Context{SDK: ctx, Cwd: h.opts.Cwd}, "history://current")
	if err != nil {
		t.Fatalf("resolve bare history://current: %v", err)
	}
	if bareRes.Content != res.Content {
		t.Fatalf("bare history://current differed from history://current/full")
	}

	// Locate returns false (renders from memory, no backing session file).
	if path, ok, err := urls.Locate(urls.Context{SDK: ctx, Cwd: h.opts.Cwd}, "history://current/full", urls.LocateOptions{}); err != nil || ok || path != "" {
		t.Fatalf("Locate(history://current/full) = (%q, %v, %v), want (\"\", false, nil)", path, ok, err)
	}

	// Malformed current sub-routes fail with omp's exact error.
	for _, bad := range []string{"history://current/extra", "history://current/full?foo=bar"} {
		if _, err := urls.Resolve(urls.Context{SDK: ctx, Cwd: h.opts.Cwd}, bad); err == nil || !strings.Contains(err.Error(), "Invalid history://current route") {
			t.Fatalf("Resolve(%q) err = %v, want Invalid history://current route", bad, err)
		}
	}

	// Missing bound session context returns a clear error.
	if _, err := urls.Resolve(urls.Context{}, "history://current/full"); err == nil || !strings.Contains(err.Error(), "bound live session branch") {
		t.Fatalf("Resolve without SDK ctx err = %v, want bound live session branch error", err)
	}
}

func TestHistoryCurrentPreservesNamedAgentCurrent(t *testing.T) {
	rt := NewRuntime(DefaultConfig())
	ext := sdk.New("test-history-named-current")
	RegisterWithRuntime(ext, rt)

	h := startFakeHost(t, ext, fakeHostOptions{
		SessionID:      "sess-named-current",
		SessionEntries: currentBranchFixtureEntries(t),
		LeafID:         "latest-entry",
	})
	h.fireEvent(sdk.EventSessionStart, map[string]any{"reason": "startup"})
	ctx := rt.sessionContextFor("sess-named-current", sdk.Context{})

	rt.Registry.Register(&AgentRef{
		ID:        "current",
		Kind:      "sub",
		ParentID:  MainAgentID,
		Status:    "idle",
		SessionID: "sess-named-current",
		Messages: []map[string]any{
			{"role": "user", "content": "named current transcript"},
		},
	})

	bareRes, err := urls.Resolve(urls.Context{SDK: ctx, Cwd: h.opts.Cwd}, "history://current")
	if err != nil {
		t.Fatalf("resolve history://current with named agent: %v", err)
	}
	if !strings.Contains(bareRes.Content, "named current transcript") {
		t.Fatalf("expected named agent 'current' transcript, got:\n%s", bareRes.Content)
	}

	// history://current/full still resolves the caller's own branch.
	fullRes, err := urls.Resolve(urls.Context{SDK: ctx, Cwd: h.opts.Cwd}, "history://current/full")
	if err != nil {
		t.Fatalf("resolve history://current/full with named agent: %v", err)
	}
	if !strings.Contains(fullRes.Content, "oldest raw request survives") {
		t.Fatalf("expected caller branch dump for history://current/full, got:\n%s", fullRes.Content)
	}
}

func TestFormatCurrentBranchFullHistoryRichVariants(t *testing.T) {
	ts := "2026-11-27T12:00:00Z"
	content := FormatCurrentBranchFullHistory([]map[string]any{
		{
			"type":      "message",
			"id":        "a1",
			"parentId":  nil,
			"timestamp": ts,
			"message": map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "redactedThinking", "data": "ENCRYPTED-BLOB"},
					map[string]any{
						"type": "anthropicServerTool",
						"block": map[string]any{
							"type":   "server_tool_use",
							"name":   "web_search",
							"caller": "assistant",
						},
					},
					map[string]any{"type": "image", "mimeType": "image/png", "data": "aGVsbG8="},
					map[string]any{
						"type": "fallback",
						"from": map[string]any{"model": "claude-primary"},
						"to":   map[string]any{"model": "claude-backup"},
					},
				},
			},
		},
		{
			"type":      "message",
			"id":        "b1",
			"parentId":  "a1",
			"timestamp": ts,
			"message": map[string]any{
				"role":      "bashExecution",
				"command":   "git status",
				"output":    "working tree clean",
				"exitCode":  0,
				"cancelled": false,
				"truncated": false,
			},
		},
		{
			"type":      "message",
			"id":        "p1",
			"parentId":  "b1",
			"timestamp": ts,
			"message": map[string]any{
				"role":      "pythonExecution",
				"code":      "print('ok')",
				"output":    "ok",
				"exitCode":  0,
				"cancelled": false,
				"truncated": false,
			},
		},
	})
	for _, want := range []string{
		"[redacted thinking: 14 chars omitted]",
		"#### server tool: server_tool_use",
		`"caller": "assistant"`,
		"[image: image/png, 8 base64 chars omitted]",
		"[provider fallback: claude-primary to claude-backup]",
		"working tree clean",
		`"truncated": false`,
		"print('ok')",
		"Output:",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("rich history dump missing %q in:\n%s", want, content)
		}
	}
	if strings.Contains(content, "ENCRYPTED-BLOB") || strings.Contains(content, "aGVsbG8=") {
		t.Fatalf("redacted/base64 payload must not appear in full history dump:\n%s", content)
	}
}

func TestAutoloadSkillsParsedAndPassedToChildSession(t *testing.T) {
	// 1. Frontmatter parsing: YAML array, flow list, CSV, and kebab-case alias.
	cases := []struct {
		name string
		md   string
		want []string
	}{
		{
			name: "yaml block array",
			md:   "---\nname: skill-worker\ndescription: Uses autoload skills\nautoloadSkills:\n  - alpha-skill\n  - beta-skill\n---\nPrompt body\n",
			want: []string{"alpha-skill", "beta-skill"},
		},
		{
			name: "csv string",
			md:   "---\nname: csv-worker\ndescription: CSV autoload skills\nautoloadSkills: alpha-skill, , beta-skill \n---\nPrompt body\n",
			want: []string{"alpha-skill", "beta-skill"},
		},
		{
			name: "kebab alias flow array",
			md:   "---\nname: kebab-worker\ndescription: Kebab autoload skills\nautoload-skills: [gamma-skill]\n---\nPrompt body\n",
			want: []string{"gamma-skill"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def, err := ParseAgent("worker.md", tc.md, SourceProject)
			if err != nil {
				t.Fatalf("ParseAgent: %v", err)
			}
			if !slices.Equal(def.AutoloadSkills, tc.want) {
				t.Fatalf("AutoloadSkills = %v, want %v", def.AutoloadSkills, tc.want)
			}
		})
	}

	// 2. DiscoverAgents loads autoloadSkills from project agent file, and RunSubagent
	// forwards them to CreateAgentSessionOptions.Resources.SkillNames.
	cwd := t.TempDir()
	agentDir := filepath.Join(cwd, ".omp", "agents")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agentFile := filepath.Join(agentDir, "specialist.md")
	agentContent := "---\nname: specialist\ndescription: Specialist with skills\nautoloadSkills:\n  - git-expert\n  - go-testing\n---\nRun specialized checks.\n"
	if err := os.WriteFile(agentFile, []byte(agentContent), 0o644); err != nil {
		t.Fatal(err)
	}
	disc := DiscoverAgents(DiscoverOptions{Cwd: cwd, ConfigDirName: ".omp", Home: cwd})
	ag, ok := GetAgent(disc.Agents, "specialist")
	if !ok {
		t.Fatalf("expected discovered agent 'specialist'")
	}
	if !slices.Equal(ag.AutoloadSkills, []string{"git-expert", "go-testing"}) {
		t.Fatalf("discovered AutoloadSkills = %v, want [git-expert go-testing]", ag.AutoloadSkills)
	}
}
