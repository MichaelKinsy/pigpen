// Package superpowers_tools is the Go port of the two Node helpers of obra/superpowers v7.0.0 that its Skills tell the model
// to run: the visual companion server of the brainstorming Skill (skills/brainstorming/scripts/server.cjs,
// start-server.sh, stop-server.sh) and the flowchart renderer of the writing-skills Skill (render-graphs.js). Each is a
// tool here, so no Node is needed at run time.
package superpowers_tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// isJSSpace and jsTrim are String.prototype.trim's whitespace set.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\u1680', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
		return true
	}
	return r >= '\u2000' && r <= '\u200a'
}

func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

func str(params map[string]any, key string) string {
	s, _ := params[key].(string)
	return s
}

// Extension returns the tools extension.
func Extension() *sdk.Extension {
	e := sdk.New("superpowers-tools")
	e.ToolWithGuidelines("brainstorm_server", "Start or stop the visual companion server of the brainstorming Skill (what `bash scripts/start-server.sh` and `bash scripts/stop-server.sh` do in the original). `start` returns the server-started JSON (url with the session key, screen_dir, state_dir). `stop` takes the session directory.",
		sdk.Schema{
			"type":     "object",
			"required": []string{"action"},
			"properties": map[string]any{
				"action":               map[string]any{"type": "string", "enum": []string{"start", "stop"}},
				"project_dir":          map[string]any{"type": "string", "description": "start: store session files under <path>/.superpowers/brainstorm/ and keep the port and key across restarts (start-server.sh --project-dir)"},
				"host":                 map[string]any{"type": "string", "description": "start: interface to bind, default 127.0.0.1 (--host)"},
				"url_host":             map[string]any{"type": "string", "description": "start: hostname shown in the returned URL (--url-host)"},
				"idle_timeout_minutes": map[string]any{"type": "integer", "description": "start: shut down after this many idle minutes, default 240 (--idle-timeout-minutes)"},
				"open":                 map[string]any{"type": "boolean", "description": "start: open the browser on the first screen; only after the user approved the companion (--open)"},
				"session_dir":          map[string]any{"type": "string", "description": "stop: the session directory (stop-server.sh <session_dir>), the parent of screen_dir"},
			},
		},
		[]string{"The brainstorming Skill's visual companion is started with the brainstorm_server tool (action start) and stopped with it (action stop, session_dir), not with `bash scripts/start-server.sh` or `bash scripts/stop-server.sh`; the options are the scripts' (project_dir, host, url_host, idle_timeout_minutes, open). The server runs inside this session, so there is no foreground or background mode."},
		func(_ sdk.Context, params map[string]any) (any, error) {
			switch str(params, "action") {
			case "start":
				opts := CompanionOptions{ProjectDir: str(params, "project_dir"), Host: str(params, "host"), URLHost: str(params, "url_host")}
				if v, ok := params["idle_timeout_minutes"].(float64); ok {
					if v < 1 || v != float64(int(v)) {
						return nil, fmt.Errorf(`{"error": "--idle-timeout-minutes must be a positive integer"}`)
					}
					opts.IdleTimeout = time.Duration(v) * time.Minute
				}
				opts.Open, _ = params["open"].(bool)
				c, info, err := StartCompanion(opts)
				if err != nil {
					return nil, err
				}
				companionsMu.Lock()
				companions[c.SessionDir()] = c
				companionsMu.Unlock()
				raw, _ := json.Marshal(info)
				return string(raw), nil
			case "stop":
				raw, _ := json.Marshal(StopCompanion(str(params, "session_dir")))
				return string(raw), nil
			}
			return nil, fmt.Errorf("action must be start or stop")
		})
	e.ToolWithGuidelines("render_skill_graphs", "Render the ```dot diagrams of a Skill's SKILL.md to SVG with graphviz's dot, into <skill-dir>/diagrams/ (what `node render-graphs.js <skill-directory> [--combine]` does in the original). Needs graphviz installed.",
		sdk.Schema{
			"type":     "object",
			"required": []string{"skill_dir"},
			"properties": map[string]any{
				"skill_dir": map[string]any{"type": "string", "description": "the Skill's directory, the one that holds SKILL.md"},
				"combine":   map[string]any{"type": "boolean", "description": "render all diagrams into one SVG (--combine)"},
			},
		},
		[]string{"The writing-skills Skill's render-graphs.js is the render_skill_graphs tool: do not run `node ./render-graphs.js`."},
		func(_ sdk.Context, params map[string]any) (any, error) {
			dir := str(params, "skill_dir")
			if dir == "" {
				return nil, fmt.Errorf("Usage: render-graphs.js <skill-directory> [--combine]")
			}
			combine, _ := params["combine"].(bool)
			out, err := RenderGraphs(dir, combine, "dot")
			if err != nil {
				return nil, err
			}
			return out, nil
		})
	return e
}
