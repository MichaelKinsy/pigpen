package warden

// WidgetConfig (widget.ts 33-51): the status line above the editor and the trace panel. The per-guard templates
// are strings; Templates in widget.go holds the three the port renders today.
type WidgetConfig struct {
	Enabled bool `json:"enabled"`
	// Placement: aboveEditor | belowEditor.
	Placement string `json:"placement"`
	// BarMode: stack | live.
	BarMode  string `json:"barMode"`
	Shortcut string `json:"shortcut"`
	// PanelWidth is a percentage string such as "40%" or a column count (number).
	PanelWidth any    `json:"panelWidth"`
	Action     string `json:"action"`
	Stuck      string `json:"stuck"`
	Done       string `json:"done"`
	Prose      string `json:"prose"`
	Security   string `json:"security"`
	Context    string `json:"context"`
	Runaway    string `json:"runaway"`
	Rules      string `json:"rules"`
	Subagent   string `json:"subagent"`
}

func defaultWidgetConfig() WidgetConfig {
	return WidgetConfig{
		Enabled: true, Placement: "aboveEditor", BarMode: "live", Shortcut: "ctrl+shift+w", PanelWidth: "40%",
		Action:   DefaultTemplates.Action,
		Stuck:    DefaultTemplates.Stuck,
		Done:     DefaultTemplates.Done,
		Prose:    "warden · prose · wordy {wordy} · clichés {cliches} · jargon {jargon} · {flags} · {status}",
		Security: "warden · security · {tool} · injection {injection} · exfiltration {exfiltration} · {status}",
		Context:  "warden · context · {tool} · {retention} · saved {bytesSaved} bytes",
		Runaway:  "warden · runaway · {kind} · {count}× repeated · {chars} chars · {signal} · {status}",
		Rules:    "warden · rules · {tool} {path} · {asked} rules · {violations} · {status}",
		Subagent: "warden · subagent · {agent} · {kind} · {wake} · {status}",
	}
}
