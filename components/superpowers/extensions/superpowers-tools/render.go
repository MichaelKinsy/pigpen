package superpowers_tools

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// A Go port of skills/writing-skills/render-graphs.js (obra/superpowers v7.0.0): the ```dot blocks of a Skill's SKILL.md
// rendered to SVG with graphviz's dot.

type dotBlock struct{ name, content string }

var (
	dotFenceRe  = regexp.MustCompile("(?s)```dot\n(.*?)```")
	digraphName = regexp.MustCompile(`digraph\s+(\w+)`)
	graphBodyRe = regexp.MustCompile(`(?s)digraph\s+\w+\s*\{(.*)\}`)
	rankdirRe   = regexp.MustCompile(`(?m)^[ \t\f\v\r\n]*rankdir[ \t\f\v\r\n]*=[ \t\f\v\r\n]*\w+[ \t\f\v\r\n]*;?[ \t\f\v\r\n]*$`)
)

// jsTrimSpace is String.prototype.trim.
func jsTrimSpace(s string) string { return jsTrim(s) }

func extractDotBlocks(markdown string) []dotBlock {
	var blocks []dotBlock
	for _, m := range dotFenceRe.FindAllStringSubmatch(markdown, -1) {
		content := jsTrimSpace(m[1])
		name := fmt.Sprintf("graph_%d", len(blocks)+1)
		if nm := digraphName.FindStringSubmatch(content); nm != nil {
			name = nm[1]
		}
		blocks = append(blocks, dotBlock{name, content})
	}
	return blocks
}

func extractGraphBody(dot string) string {
	m := graphBodyRe.FindStringSubmatch(dot)
	if m == nil {
		return ""
	}
	return jsTrimSpace(rankdirRe.ReplaceAllString(m[1], ""))
}

func combineGraphs(blocks []dotBlock, skillName string) string {
	bodies := make([]string, len(blocks))
	for i, b := range blocks {
		lines := strings.Split(extractGraphBody(b.content), "\n")
		for j := range lines {
			lines[j] = "  " + lines[j]
		}
		bodies[i] = fmt.Sprintf("  subgraph cluster_%d {\n    label=\"%s\";\n    %s\n  }", i, b.name, strings.Join(lines, "\n"))
	}
	return fmt.Sprintf("digraph %s_combined {\n  rankdir=TB;\n  compound=true;\n  newrank=true;\n\n%s\n}", skillName, strings.Join(bodies, "\n\n"))
}

// RenderGraphs is `render-graphs.js <skill-dir> [--combine]`: it returns the lines the script prints and an error for
// what the script reports with a non-zero exit. dot is the graphviz binary (a test points it at a stand-in).
func RenderGraphs(skillDirArg string, combine bool, dot string) (string, error) {
	skillDir, err := filepath.Abs(skillDirArg)
	if err != nil {
		return "", err
	}
	skillFile := filepath.Join(skillDir, "SKILL.md")
	skillName := strings.ReplaceAll(filepath.Base(skillDir), "-", "_")
	if _, err := os.Stat(skillFile); err != nil {
		return "", fmt.Errorf("Error: %s not found", skillFile)
	}
	if err := exec.Command(dot, "-V").Run(); err != nil {
		return "", fmt.Errorf("Error: graphviz (dot) not found. Install with:\n  brew install graphviz    # macOS\n  apt install graphviz     # Linux")
	}
	raw, err := os.ReadFile(skillFile)
	if err != nil {
		return "", err
	}
	blocks := extractDotBlocks(string(raw))
	var out strings.Builder
	if len(blocks) == 0 {
		return "No ```dot blocks found in " + skillFile + "\n", nil
	}
	fmt.Fprintf(&out, "Found %d diagram(s) in %s/SKILL.md\n", len(blocks), filepath.Base(skillDir))
	outputDir := filepath.Join(skillDir, "diagrams")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return out.String(), err
	}
	render := func(source string) ([]byte, error) {
		cmd := exec.Command(dot, "-Tsvg")
		cmd.Stdin = strings.NewReader(source)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("Error running dot: %v\n%s", err, stderr.String())
		}
		return stdout.Bytes(), nil
	}
	var failures []string
	if combine {
		combined := combineGraphs(blocks, skillName)
		if svg, err := render(combined); err == nil {
			_ = os.WriteFile(filepath.Join(outputDir, skillName+"_combined.svg"), svg, 0o644)
			fmt.Fprintf(&out, "  Rendered: %s_combined.svg\n", skillName)
			_ = os.WriteFile(filepath.Join(outputDir, skillName+"_combined.dot"), []byte(combined), 0o644)
			fmt.Fprintf(&out, "  Source: %s_combined.dot\n", skillName)
		} else {
			failures = append(failures, err.Error(), "  Failed to render combined diagram")
		}
	} else {
		for _, b := range blocks {
			if svg, err := render(b.content); err == nil {
				_ = os.WriteFile(filepath.Join(outputDir, b.name+".svg"), svg, 0o644)
				fmt.Fprintf(&out, "  Rendered: %s.svg\n", b.name)
			} else {
				failures = append(failures, err.Error(), "  Failed: "+b.name)
			}
		}
	}
	fmt.Fprintf(&out, "\nOutput: %s/\n", outputDir)
	if len(failures) > 0 {
		out.WriteString(strings.Join(failures, "\n") + "\n")
	}
	return out.String(), nil
}
