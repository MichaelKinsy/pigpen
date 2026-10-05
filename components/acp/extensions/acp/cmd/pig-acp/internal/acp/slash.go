package acp

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// FileSlashCommand is a prompt template file used as a slash command.
type FileSlashCommand struct {
	Name        string
	Description string
	Content     string
	Source      string // "(user)", "(project)", "(project:frontend)"
}

// AvailableCommand is an ACP available command.
type AvailableCommand struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Input       map[string]any `json:"input,omitempty"`
}

var frontmatterLine = regexp.MustCompile(`^(\w+):\s*(.*)$`)

func parseFrontmatter(content string) (map[string]string, string) {
	fm := map[string]string{}
	if !strings.HasPrefix(content, "---") {
		return fm, content
	}
	end := strings.Index(content[3:], "\n---")
	if end == -1 {
		return fm, content
	}
	end += 3
	block := ""
	if len(content) >= 4 {
		block = content[4:end]
	}
	remaining := jsTrim(content[end+4:])
	for _, line := range strings.Split(block, "\n") {
		if m := frontmatterLine.FindStringSubmatch(line); m != nil {
			fm[m[1]] = jsTrim(m[2])
		}
	}
	return fm, remaining
}

func loadCommandsFromDir(dir, source, subdir string) []FileSlashCommand {
	var commands []FileSlashCommand
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		isDir, isFile := e.IsDir(), e.Type().IsRegular()
		if e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(full); err == nil {
				isDir, isFile = st.IsDir(), st.Mode().IsRegular()
			}
		}
		if isDir {
			sub := e.Name()
			if subdir != "" {
				sub = subdir + ":" + e.Name()
			}
			commands = append(commands, loadCommandsFromDir(full, source, sub)...)
			continue
		}
		if !isFile || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		fm, content := parseFrontmatter(string(raw))
		name := strings.TrimSuffix(e.Name(), ".md")
		src := "(" + source + ")"
		if subdir != "" {
			src = "(" + source + ":" + subdir + ")"
		}
		desc := fm["description"]
		if desc == "" {
			for _, l := range strings.Split(content, "\n") {
				if jsTrim(l) != "" {
					desc = truncateRunes(l, 60)
					if len([]rune(l)) > 60 {
						desc += "..."
					}
					break
				}
			}
		}
		if desc != "" {
			desc += " " + src
		} else {
			desc = src
		}
		commands = append(commands, FileSlashCommand{Name: name, Description: desc, Content: content, Source: src})
	}
	return commands
}

// LoadSlashCommands loads user then project prompt templates:
// <agent dir>/prompts and <cwd>/.pig/prompts.
func LoadSlashCommands(cwd string) []FileSlashCommand {
	var commands []FileSlashCommand
	commands = append(commands, loadCommandsFromDir(filepath.Join(AgentDir(), "prompts"), "user", "")...)
	commands = append(commands, loadCommandsFromDir(filepath.Join(absPathFrom(cwd), ProjectDirName(), "prompts"), "project", "")...)
	return commands
}

// ToAvailableCommands converts file commands, de-duplicating by name (first wins).
func ToAvailableCommands(cmds []FileSlashCommand) []AvailableCommand {
	seen := map[string]bool{}
	out := []AvailableCommand{}
	for _, c := range cmds {
		if seen[c.Name] {
			continue
		}
		seen[c.Name] = true
		out = append(out, AvailableCommand{Name: c.Name, Description: c.Description})
	}
	return out
}

// ParseCommandArgs splits arguments with bash-style quotes.
func ParseCommandArgs(s string) []string {
	var args []string
	var cur strings.Builder
	var quote rune
	for _, ch := range s {
		if quote != 0 {
			if ch == quote {
				quote = 0
			} else {
				cur.WriteRune(ch)
			}
			continue
		}
		switch {
		case ch == '"' || ch == '\'':
			quote = ch
		case ch == ' ' || ch == '\t':
			if cur.Len() > 0 {
				args = append(args, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(ch)
		}
	}
	if cur.Len() > 0 {
		args = append(args, cur.String())
	}
	return args
}

var digitsRE = regexp.MustCompile(`\$(\d+)`)

// SubstituteArgs replaces $1.. and $@.
func SubstituteArgs(content string, args []string) string {
	result := strings.ReplaceAll(content, "$@", strings.Join(args, " "))
	return digitsRE.ReplaceAllStringFunc(result, func(m string) string {
		n, err := strconv.Atoi(m[1:])
		if err != nil || n-1 < 0 || n-1 >= len(args) {
			return ""
		}
		return args[n-1]
	})
}

// ExpandSlashCommand expands a leading /command that names a file command.
func ExpandSlashCommand(text string, cmds []FileSlashCommand) string {
	if !strings.HasPrefix(text, "/") {
		return text
	}
	space := strings.Index(text, " ")
	name, argStr := text[1:], ""
	if space != -1 {
		name, argStr = text[1:space], text[space+1:]
	}
	for _, c := range cmds {
		if c.Name == name {
			return SubstituteArgs(c.Content, ParseCommandArgs(argStr))
		}
	}
	return text
}

// MergeCommands keeps order and de-duplicates by name (first wins).
func MergeCommands(a, b []AvailableCommand) []AvailableCommand {
	seen := map[string]bool{}
	out := []AvailableCommand{}
	for _, list := range [][]AvailableCommand{a, b} {
		for _, c := range list {
			if seen[c.Name] {
				continue
			}
			seen[c.Name] = true
			out = append(out, c)
		}
	}
	return out
}

// PiCommandsOptions select which `get_commands` entries are advertised.
type PiCommandsOptions struct {
	EnableSkillCommands      bool
	IncludeExtensionCommands bool
}

func describeFallback(c map[string]any) string {
	var parts []string
	if s, _ := c["source"].(string); s != "" {
		parts = append(parts, s)
	}
	if s, _ := c["location"].(string); s != "" {
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return "(command)"
	}
	return "(" + strings.Join(parts, ":") + ")"
}

// ToAvailableCommandsFromPiGetCommands converts `get_commands` data.
func ToAvailableCommandsFromPiGetCommands(data any, opts PiCommandsOptions) []AvailableCommand {
	root := asObject(data)
	raw, ok := root["commands"].([]any)
	if !ok {
		raw, _ = asObject(root["data"])["commands"].([]any)
	}
	out := []AvailableCommand{}
	for _, item := range raw {
		c := asObject(item)
		name := ""
		if s, ok := c["name"].(string); ok {
			name = jsTrim(s)
		}
		if name == "" {
			continue
		}
		if src, _ := c["source"].(string); !opts.IncludeExtensionCommands && src == "extension" {
			continue
		}
		if !opts.EnableSkillCommands && strings.HasPrefix(name, "skill:") {
			continue
		}
		desc := ""
		if s, ok := c["description"].(string); ok {
			desc = jsTrim(s)
		}
		if desc == "" {
			desc = describeFallback(c)
		}
		out = append(out, AvailableCommand{Name: name, Description: desc})
	}
	return out
}

// BuiltinAvailableCommands are the commands the adapter handles itself; they never reach the model.
func BuiltinAvailableCommands() []AvailableCommand {
	return []AvailableCommand{
		{Name: "compact", Description: "Manually compact the session context", Input: map[string]any{"hint": "optional custom instructions"}},
		{Name: "autocompact", Description: "Toggle automatic context compaction", Input: map[string]any{"hint": "on|off|toggle"}},
		{Name: "export", Description: "Export session to an HTML file in the session cwd"},
		{Name: "session", Description: "Show session stats (messages, tokens, cost, session file)"},
		{Name: "name", Description: "Set session display name", Input: map[string]any{"hint": "<name>"}},
		{Name: "steering", Description: "Get/set pig steering message delivery mode (how queued steering messages are delivered)", Input: map[string]any{"hint": "(no args to show) all | one-at-a-time"}},
		{Name: "follow-up", Description: "Get/set pig follow-up message delivery mode (how queued follow-up messages are delivered)", Input: map[string]any{"hint": "(no args to show) all | one-at-a-time"}},
		{Name: "changelog", Description: "Show pig changelog"},
	}
}
