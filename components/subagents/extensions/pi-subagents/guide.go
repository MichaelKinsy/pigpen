package pi_subagents

import (
	"embed"
	"fmt"
	"strings"
)

// The packaged subagents guide (src/extension/subagent-guide.ts). The original reads README.md and docs/*.md from its install
// directory; the port embeds the same files (guide/, copied from the pinned original) so the single binary serves them.

//go:embed guide/*.md
var guideFS embed.FS

var guideTopics = []string{"overview", "workflows", "agents", "missions", "observability", "tool-reference", "configuration", "models", "watchdog", "extension-api", "council"}

// The council topic serves the council skill with the references it tells the model to read: it must work under `pi --no-skills`.
var councilGuideFiles = [][2]string{
	{"skills/council-mode/SKILL.md", "guide/council-skill.md"},
	{"skills/council-mode/references/pass-contracts.md", "guide/council-pass-contracts.md"},
	{"skills/pi-subagents/references/execution-controls.md", "guide/council-execution-controls.md"},
}

// readSubagentGuide returns the text of a guide topic ("" is the overview); an unknown topic is a message, not an error.
func readSubagentGuide(topic string) (string, error) {
	known := false
	for _, t := range guideTopics {
		known = known || t == topic
	}
	if !known {
		return fmt.Sprintf("Unknown subagents guide topic '%s'. Valid topics: %s. No files were changed.", topic, strings.Join(guideTopics, ", ")), nil
	}
	read := func(embedded string) (string, error) {
		b, err := guideFS.ReadFile(embedded)
		return string(b), err
	}
	switch topic {
	case "overview":
		text, err := read("guide/overview.md")
		return text, wrapGuideErr(topic, err)
	case "council":
		var parts []string
		for _, f := range councilGuideFiles {
			text, err := read(f[1])
			if err != nil {
				return "", wrapGuideErr(topic, err)
			}
			parts = append(parts, fmt.Sprintf("<!-- %s -->\n\n%s", f[0], text))
		}
		return strings.Join(parts, "\n\n"), nil
	}
	text, err := read("guide/" + topic + ".md")
	return text, wrapGuideErr(topic, err)
}

func wrapGuideErr(topic string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("Failed to read packaged subagents guide '%s': %v", topic, err)
}
