package acp

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var unsafeIDChars = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func (a *Agent) say(sessionID, text string) error {
	return a.conn.SessionUpdate(sessionID, textChunk("agent_message_chunk", text))
}

func endTurn() (PromptResponse, error) { return PromptResponse{StopReason: StopEndTurn}, nil }

// Prompt runs one prompt turn, or an adapter-side slash command (compact, session, name, steering,
// follow-up, changelog, export, autocompact). Those never reach the model.
func (a *Agent) Prompt(req PromptRequest) (PromptResponse, error) {
	sess, err := a.restoreSession(req.SessionID, "", nil)
	if err != nil {
		return PromptResponse{}, err
	}
	message, images := PromptToPiMessage(req.Prompt)

	if len(images) == 0 && strings.HasPrefix(strings.TrimLeftFunc(message, isJSSpace), "/") {
		trimmed := jsTrim(message)
		space := strings.Index(trimmed, " ")
		cmd, argStr := trimmed[1:], ""
		if space != -1 {
			cmd, argStr = trimmed[1:space], trimmed[space+1:]
		}
		args := ParseCommandArgs(argStr)
		if handled, resp, err := a.builtin(sess, cmd, args); handled {
			return resp, err
		}
	}

	result := <-sess.Prompt(message, images)
	if result.Err != nil {
		return PromptResponse{}, result.Err
	}
	reason := result.Reason
	// ACP has no "error" stop reason: a failed turn ends as end_turn unless it was cancelled.
	if reason == StopError {
		reason = StopEndTurn
		if sess.WasCancelRequested() {
			reason = StopCancelled
		}
	}
	return PromptResponse{StopReason: reason}, nil
}

func isJSSpace(r rune) bool { return jsTrim(string(r)) == "" }

func (a *Agent) builtin(sess ActiveSession, cmd string, args []string) (bool, PromptResponse, error) {
	id, proc := sess.ID(), sess.Proc()
	done := func(err error) (bool, PromptResponse, error) {
		if err != nil {
			return true, PromptResponse{}, err
		}
		r, _ := endTurn()
		return true, r, nil
	}
	switch cmd {
	case "compact":
		custom := jsTrim(strings.Join(args, " "))
		res, err := proc.Compact(custom)
		if err != nil {
			return done(err)
		}
		header := "Compaction completed."
		if custom != "" {
			header += " (custom instructions applied)"
		}
		if n, ok := asNumber(res["tokensBefore"]); ok {
			header += "\nTokens before: " + jsNumber(n)
		}
		text := header
		if summary, ok := res["summary"].(string); ok && summary != "" {
			text += "\n\n" + summary
		}
		return done(a.say(id, text))

	case "session":
		stats, err := proc.GetSessionStats(0)
		if err != nil {
			return done(err)
		}
		return done(a.say(id, sessionStatsText(stats)))

	case "name":
		name := jsTrim(strings.Join(args, " "))
		if name == "" {
			return done(a.say(id, "Usage: /name <name>"))
		}
		if err := proc.SetSessionName(name); err != nil {
			msg := err.Error()
			hint := ""
			if strings.Contains(strings.ToLower(msg), "set_session_name") {
				hint = " This requires a newer pi version that supports `set_session_name` in RPC mode."
			}
			return done(a.say(id, "Failed to set session name: "+msg+hint))
		}
		if err := a.send(id, Update{"sessionUpdate": "session_info_update", "title": name, "updatedAt": nowISO()}); err != nil {
			return done(err)
		}
		return done(a.say(id, "Session name set: "+name))

	case "steering":
		return a.deliveryMode(sess, "Steering", "steering", "steeringMode", args, proc.SetSteeringMode)
	case "follow-up":
		return a.deliveryMode(sess, "Follow-up", "follow-up", "followUpMode", args, proc.SetFollowUpMode)

	case "changelog":
		return done(a.changelog(id))

	case "export":
		return done(a.export(sess))

	case "autocompact":
		mode := "toggle"
		if len(args) > 0 {
			mode = strings.ToLower(args[0])
		}
		var enabled bool
		switch mode {
		case "on", "true", "enable", "enabled":
			enabled = true
		case "off", "false", "disable", "disabled":
			enabled = false
		default:
			state, err := proc.GetState()
			if err != nil {
				return done(err)
			}
			enabled = !truthy(state["autoCompactionEnabled"])
		}
		if err := proc.SetAutoCompaction(enabled); err != nil {
			return done(err)
		}
		word := "disabled"
		if enabled {
			word = "enabled"
		}
		return done(a.say(id, "Auto-compaction "+word+"."))
	}
	return false, PromptResponse{}, nil
}

func nowISO() string { return timeNowUTC().Format("2006-01-02T15:04:05.000Z") }

func sessionStatsText(stats SessionStats) string {
	var lines []string
	if v := stats["sessionId"]; truthy(v) {
		lines = append(lines, "Session: "+jsString(v))
	}
	if v := stats["sessionFile"]; truthy(v) {
		lines = append(lines, "Session file: "+jsString(v))
	}
	if n, ok := asNumber(stats["totalMessages"]); ok {
		lines = append(lines, "Messages: "+jsNumber(n))
	}
	if n, ok := asNumber(stats["cost"]); ok {
		lines = append(lines, "Cost: "+jsNumber(n))
	}
	if t := asObject(stats["tokens"]); t != nil {
		var parts []string
		for _, p := range [][2]string{{"input", "in"}, {"output", "out"}, {"cacheRead", "cache read"}, {"cacheWrite", "cache write"}, {"total", "total"}} {
			if n, ok := asNumber(t[p[0]]); ok {
				parts = append(parts, p[1]+" "+jsNumber(n))
			}
		}
		if len(parts) > 0 {
			lines = append(lines, "Tokens: "+strings.Join(parts, ", "))
		}
	}
	if len(lines) > 0 {
		return strings.Join(lines, "\n")
	}
	return "Session stats:\n" + jsonStringify(stats)
}

func (a *Agent) deliveryMode(sess ActiveSession, label, cmd, stateKey string, args []string, set func(string) error) (bool, PromptResponse, error) {
	id, proc := sess.ID(), sess.Proc()
	fail := func(err error) (bool, PromptResponse, error) { return true, PromptResponse{}, err }
	ok := func(err error) (bool, PromptResponse, error) {
		if err != nil {
			return fail(err)
		}
		r, _ := endTurn()
		return true, r, nil
	}
	mode := ""
	if len(args) > 0 {
		mode = strings.ToLower(args[0])
	}
	state, err := proc.GetState()
	if err != nil {
		return fail(err)
	}
	current := ""
	if v, has := state[stateKey]; has && v != nil {
		current = jsString(v)
	}
	if mode == "" {
		if current == "" {
			current = "unknown"
		}
		return ok(a.say(id, label+" mode: "+current))
	}
	if mode != "all" && mode != "one-at-a-time" {
		return ok(a.say(id, "Usage: /"+cmd+" all | /"+cmd+" one-at-a-time"))
	}
	if err := set(mode); err != nil {
		return fail(err)
	}
	return ok(a.say(id, label+" mode set to: "+mode))
}

func (a *Agent) changelog(id string) error {
	var path string
	if a.changelogPath != nil {
		path = a.changelogPath()
	} else {
		path = FindChangelog(a.pigCommand())
	}
	if path == "" {
		return a.say(id, "Changelog not found (couldn't locate pig installation).")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return a.say(id, "Failed to read changelog: "+err.Error())
	}
	text := string(raw)
	// Keep it a reasonable size in chat.
	if r := []rune(text); len(r) > maxChangelogChars {
		text = string(r[:maxChangelogChars]) + "\n\n...(truncated)..."
	}
	return a.say(id, text)
}

func (a *Agent) export(sess ActiveSession) error {
	id, proc := sess.ID(), sess.Proc()
	// Always exported into the session cwd; no user-provided path. pig's export_html reads the session
	// file, and fails on a missing or empty one, so that is guarded here.
	state, err := proc.GetState()
	if err != nil {
		return err
	}
	file, _ := state["sessionFile"].(string)
	count := 0.0
	if n, ok := asNumber(state["messageCount"]); ok {
		count = n
	}
	exists := false
	if file != "" {
		_, serr := os.Stat(file)
		exists = serr == nil
	}
	if file == "" || count == 0 || !exists {
		return a.say(id, "Nothing to export yet (no session messages). Send a prompt first.")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return a.say(id, "Couldn't read session file for export. Try sending a prompt first.")
	}
	if jsTrim(string(raw)) == "" {
		return a.say(id, "Nothing to export yet (empty session file). Send a prompt first.")
	}
	safe := unsafeIDChars.ReplaceAllString(id, "_")
	out := filepath.Join(sess.Cwd(), "pi-session-"+safe+".html")
	path, err := proc.ExportHTML(out)
	if err != nil {
		return a.say(id, "Export failed: "+err.Error())
	}
	if path == "" {
		return a.say(id, "Export failed: no output path returned by pi.")
	}
	uri := "file://" + path
	// A short prefix plus a resource link: many clients concatenate chunks into one message.
	if err := a.say(id, "Session exported: "); err != nil {
		return err
	}
	return a.conn.SessionUpdate(id, Update{"sessionUpdate": "agent_message_chunk", "content": map[string]any{
		"type": "resource_link", "name": fmt.Sprintf("pi-session-%s.html", safe), "uri": uri, "mimeType": "text/html", "title": "Session exported"}})
}
