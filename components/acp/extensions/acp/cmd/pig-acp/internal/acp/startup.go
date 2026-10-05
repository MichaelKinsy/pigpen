package acp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var leadingV = regexp.MustCompile(`^[vV]`)

// pigVersionHeader is `pig --version`, or "" when the executable does not answer.
func pigVersionHeader(piCommand string) string {
	if piCommand == "" {
		piCommand = "pig"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, piCommand, "--version")
	out, err := cmd.Output()
	text := jsTrim(string(out))
	if text == "" && err != nil {
		return ""
	}
	if text == "" {
		if ee, ok := err.(*exec.ExitError); ok {
			text = jsTrim(string(ee.Stderr))
		}
	}
	return leadingV.ReplaceAllString(text, "")
}

func readDirNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func isDirPath(p string) (isDir, isFile, ok bool) {
	st, err := os.Stat(p)
	if err != nil {
		return false, false, false
	}
	return st.IsDir(), st.Mode().IsRegular(), true
}

func skillsFromRoot(root string) []string {
	var items []string
	for _, e := range readDirNames(root) {
		p := filepath.Join(root, e)
		if _, isFile, ok := isDirPath(p); ok && isFile && strings.HasSuffix(strings.ToLower(e), ".md") {
			items = append(items, p)
		}
	}
	stack := []string{root}
	for len(stack) > 0 {
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, name := range readDirNames(dir) {
			if name == "node_modules" || name == ".git" {
				continue
			}
			p := filepath.Join(dir, name)
			isDir, isFile, ok := isDirPath(p)
			if !ok {
				continue
			}
			if isDir {
				stack = append(stack, p)
			} else if isFile && name == "SKILL.md" {
				items = append(items, p)
			}
		}
	}
	return items
}

// BuildStartupInfo builds the markdown "startup info" block (the original's buildStartupInfo,
// without its npm update notice: see PORT.md, gap A18).
func BuildStartupInfo(cwd, piCommand string) string {
	var md []string
	if v := pigVersionHeader(piCommand); v != "" {
		md = append(md, "pig v"+v, "---", "")
	}
	addSection := func(title string, items []string) {
		var cleaned []string
		for _, s := range items {
			if s = jsTrim(s); s != "" {
				cleaned = append(cleaned, s)
			}
		}
		if len(cleaned) == 0 {
			return
		}
		md = append(md, "## "+title)
		for _, item := range cleaned {
			md = append(md, "- "+item)
		}
		md = append(md, "")
	}

	var context []string
	contextPath := filepath.Join(cwd, "AGENTS.md")
	if _, err := os.Stat(contextPath); err == nil {
		context = append(context, contextPath)
	}
	addSection("Context", context)

	var skills []string
	skills = append(skills, skillsFromRoot(filepath.Join(AgentDir(), "skills"))...)
	skills = append(skills, skillsFromRoot(filepath.Join(userHome(), ".agents", "skills"))...)
	skills = append(skills, skillsFromRoot(filepath.Join(cwd, ProjectDirName(), "skills"))...)
	addSection("Skills", skills)

	var prompts []string
	for _, f := range readDirNames(filepath.Join(AgentDir(), "prompts")) {
		if strings.HasSuffix(f, ".md") {
			prompts = append(prompts, "/"+strings.TrimSuffix(f, ".md"))
		}
	}
	addSection("Prompts", prompts)

	var exts []string
	extDir := filepath.Join(AgentDir(), "extensions")
	for _, f := range readDirNames(extDir) {
		if strings.HasSuffix(f, ".ts") || strings.HasSuffix(f, ".js") {
			exts = append(exts, filepath.Join(extDir, f))
		}
	}
	for _, sp := range []string{filepath.Join(AgentDir(), "settings.json"), filepath.Join(cwd, ProjectDirName(), "settings.json")} {
		settings := readJSONObject(sp)
		pkgs, _ := settings["packages"].([]any)
		for _, p := range pkgs {
			s := jsString(p)
			if strings.HasPrefix(s, "npm:") {
				exts = append(exts, s+"\n  - index.ts")
			} else {
				exts = append(exts, s)
			}
		}
	}
	addSection("Extensions", exts)

	return jsTrim(strings.Join(md, "\n")) + "\n"
}
