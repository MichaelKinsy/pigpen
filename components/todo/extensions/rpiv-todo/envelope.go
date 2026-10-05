package rpiv_todo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// taskDetails is the persistence and replay snapshot every successful call returns under `details`.
// Replay reads this exact shape back, so its field names are pinned by cross-version compatibility.
// upstream: tool/types.ts:49-55.
type taskDetails struct {
	Action string
	Params params
	Tasks  []task
	NextID int
	Error  string
}

// toolEnvelope is the model-facing result: one text block plus the details snapshot.
type toolEnvelope struct {
	Text    string
	Details taskDetails
}

// jsonString is the compact JSON of v without HTML escaping; marshal failures read as "null".
func jsonString(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "null"
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// MarshalJSON writes a task with the original's field order (id, subject, then the optional fields in the
// order a create adds them, status where the object literal puts it) and only the fields that are set.
func (t task) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	field := func(name string, v any) {
		if b.Len() > 1 {
			b.WriteByte(',')
		}
		b.WriteString(jsonString(name))
		b.WriteByte(':')
		b.WriteString(jsonString(v))
	}
	b.WriteByte('{')
	field("id", t.ID)
	field("subject", t.Subject)
	if t.Description != nil {
		field("description", *t.Description)
	}
	if t.ActiveForm != nil {
		field("activeForm", *t.ActiveForm)
	}
	field("status", t.Status)
	if len(t.BlockedBy) > 0 {
		field("blockedBy", t.BlockedBy)
	}
	if t.Owner != nil {
		field("owner", *t.Owner)
	}
	if t.Metadata != nil {
		field("metadata", t.Metadata)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// marshalDetails serialises the details snapshot with the original's field names and order.
func marshalDetails(d taskDetails) ([]byte, error) {
	tasks := d.Tasks
	if tasks == nil {
		tasks = []task{}
	}
	p := d.Params
	if p == nil {
		p = params{}
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, `{"action":%s,"params":%s,"tasks":%s,"nextId":%d`, jsonString(d.Action), jsonString(p), jsonString(tasks), d.NextID)
	if d.Error != "" {
		fmt.Fprintf(&b, `,"error":%s`, jsonString(d.Error))
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func idList(ids []float64, sep string) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = "#" + jsNumber(id)
	}
	return strings.Join(parts, sep)
}

// formatListLine is `[status] #id subject [(activeForm)] [⛓ #dep,…]`, used by the list action only.
// upstream: tool/response-envelope.ts:13-17.
func formatListLine(t task) string {
	block := ""
	if len(t.BlockedBy) > 0 {
		block = " ⛓ " + idList(t.BlockedBy, ",")
	}
	form := ""
	if t.Status == statusInProgress && t.ActiveForm != nil && *t.ActiveForm != "" {
		form = " (" + sanitizeTerminalText(*t.ActiveForm) + ")"
	}
	return fmt.Sprintf("[%s] #%d %s%s%s", t.Status, t.ID, sanitizeTerminalText(t.Subject), form, block)
}

// formatGetLines is the multi-line presentation of the get action: description, activeForm, blockedBy,
// blocks, owner, in that order. upstream: tool/response-envelope.ts:25-38.
func formatGetLines(t task, s taskState) string {
	lines := []string{fmt.Sprintf("#%d [%s] %s", t.ID, t.Status, sanitizeTerminalText(t.Subject))}
	if t.Description != nil && *t.Description != "" {
		lines = append(lines, "  description: "+sanitizeTerminalText(*t.Description))
	}
	if t.ActiveForm != nil && *t.ActiveForm != "" {
		lines = append(lines, "  activeForm: "+sanitizeTerminalText(*t.ActiveForm))
	}
	if len(t.BlockedBy) > 0 {
		lines = append(lines, "  blockedBy: "+idList(t.BlockedBy, ", "))
	}
	if blocks := deriveBlocks(s.Tasks)[t.ID]; len(blocks) > 0 {
		parts := make([]string, len(blocks))
		for i, id := range blocks {
			parts[i] = fmt.Sprintf("#%d", id)
		}
		lines = append(lines, "  blocks: "+strings.Join(parts, ", "))
	}
	if t.Owner != nil && *t.Owner != "" {
		lines = append(lines, "  owner: "+sanitizeTerminalText(*t.Owner))
	}
	return strings.Join(lines, "\n")
}

// formatContent renders an op as the text the model reads. upstream: tool/response-envelope.ts:51-85.
func formatContent(o op, s taskState) string {
	switch o.Kind {
	case "create":
		if i := findByNumber(s.Tasks, float64(o.TaskID)); i != -1 {
			return fmt.Sprintf("Created #%d: %s (pending)", s.Tasks[i].ID, sanitizeTerminalText(s.Tasks[i].Subject))
		}
		return fmt.Sprintf("Created #%d", o.TaskID)
	case "update":
		if !o.Changed {
			return fmt.Sprintf("No change: #%d already matches the requested values (status: %s)", o.ID, o.ToStatus)
		}
		transition := ""
		if o.FromStatus != o.ToStatus {
			transition = fmt.Sprintf(" (%s → %s)", o.FromStatus, o.ToStatus)
		}
		return fmt.Sprintf("Updated #%d%s", o.ID, transition)
	case "delete":
		return fmt.Sprintf("Deleted #%d: %s", o.ID, sanitizeTerminalText(o.Subject))
	case "clear":
		return fmt.Sprintf("Cleared %d tasks", o.Count)
	case "list":
		view := s.Tasks
		if !o.IncludeDeleted {
			view = filterTasks(view, func(t task) bool { return t.Status != statusDeleted })
		}
		if o.StatusFilter != "" {
			view = filterTasks(view, func(t task) bool { return t.Status == o.StatusFilter })
		}
		if len(view) == 0 {
			return "No tasks"
		}
		lines := make([]string, len(view))
		for i, t := range view {
			lines[i] = formatListLine(t)
		}
		return strings.Join(lines, "\n")
	case "get":
		return formatGetLines(o.Task, s)
	case "error":
		return "Error: " + o.Message
	}
	return ""
}

// buildToolResult builds the model-facing envelope after the store committed the reducer's new state.
// upstream: tool/response-envelope.ts:87-98.
func buildToolResult(action string, p params, s taskState, o op) toolEnvelope {
	d := taskDetails{Action: action, Params: p, Tasks: s.Tasks, NextID: s.NextID}
	if o.Kind == "error" {
		d.Error = o.Message
	}
	return toolEnvelope{Text: formatContent(o, s), Details: d}
}
