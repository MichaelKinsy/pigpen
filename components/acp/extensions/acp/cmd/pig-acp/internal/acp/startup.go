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

	"github.com/MichaelKinsy/pigpen/acp/cmd/pig-acp/internal/pirpc"
)

var leadingV = regexp.MustCompile(`^[vV]`)

// parsePigVersionLine extracts PiG's own version from the first line of `pig version` (`pig: 0.8.0`).
func parsePigVersionLine(out string) string {
	first, _, _ := strings.Cut(out, "\n")
	first = jsTrim(first)
	rest, ok := strings.CutPrefix(first, "pig:")
	if !ok {
		return ""
	}
	v := leadingV.ReplaceAllString(jsTrim(rest), "")
	if v == "" {
		return ""
	}
	// Strip any trailing token (e.g. build suffix after whitespace) while keeping semver(+meta).
	if sp := strings.IndexAny(v, " \t"); sp >= 0 {
		v = v[:sp]
	}
	return v
}

// pigVersionProbeTimeout bounds `pig version` so a binary without a `version` subcommand (for example an
// older pre-0.2.0 build or a non-PiG command that treats `version` as a prompt) cannot stall session startup.
var pigVersionProbeTimeout = 2 * time.Second

// pigVersionHeader returns PiG's own version: from `pig version` (first line `pig: <ver>`, supported in every
// public PiG release since v0.2.0 and required from 0.8.0 where `--version` reports the upstream Pi version),
// falling back to `pig --version` for binaries that do not answer `version`. Stdin is closed (nil -> /dev/null)
// and `pig version` runs under a short deadline with PIG_OFFLINE/PI_OFFLINE set so a binary without the
// subcommand cannot read stdin or make an outbound model call before falling back. Returns "" when the
// executable does not answer.
func pigVersionHeader(piCommand string) string {
	piCommand = pirpc.Command(piCommand)
	versionCtx, versionCancel := context.WithTimeout(context.Background(), pigVersionProbeTimeout)
	defer versionCancel()
	vcmd := exec.CommandContext(versionCtx, piCommand, "version")
	vcmd.Stdin = nil
	vcmd.WaitDelay = 100 * time.Millisecond
	vcmd.Env = append(os.Environ(), "PIG_OFFLINE=1", "PI_OFFLINE=1")
	if out, err := vcmd.Output(); err == nil {
		if v := parsePigVersionLine(string(out)); v != "" {
			return v
		}
	}
	flagCtx, flagCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer flagCancel()
	cmd := exec.CommandContext(flagCtx, piCommand, "--version")
	cmd.Stdin = nil
	cmd.WaitDelay = 100 * time.Millisecond
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

// BuildStartupInfo builds the markdown "startup info" block (the original's buildStartupInfo).
func BuildStartupInfo(cwd, piCommand, updateNotice string) string {
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

	if updateNotice != "" {
		md = append(md, "---", updateNotice, "")
	}

	return jsTrim(strings.Join(md, "\n")) + "\n"
}
