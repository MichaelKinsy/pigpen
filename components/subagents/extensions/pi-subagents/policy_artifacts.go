package pi_subagents

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Port of src/shared/artifacts.ts: where a run's debug artifacts (input, output, metadata) go.

const (
	cleanupMarkerFile           = ".last-cleanup"
	projectSubagentsRelativeDir = ".pi/subagents"
)

var projectArtifactPaths = func() []string {
	base := projectSubagentsRelativeDir
	return []string{
		base + "/artifacts/output.md", base + "/artifacts/run_worker_input.md", base + "/artifacts/run_worker_output.md",
		base + "/artifacts/run_worker.jsonl", base + "/artifacts/run_worker_transcript.jsonl", base + "/artifacts/run_worker_meta.json",
		base + "/artifacts/progress/run/progress.md", base + "/artifacts/outputs/output.md", base + "/artifacts/outputs/run/output.md",
		base + "/chain-runs/run.json",
	}
}()

var globEscape = regexp.MustCompile(`[|\\{}()\[\]^$+?.]`)

// globMatchesPath is the original's small glob matcher (** , *, ?, [class]) over a slash path.
func globMatchesPath(pattern, filePath string) bool {
	var expr strings.Builder
	expr.WriteString("^")
	r := []rune(pattern)
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == '*' && i+1 < len(r) && r[i+1] == '*':
			i++
			if i+1 < len(r) && r[i+1] == '/' {
				i++
				expr.WriteString("(?:.*/)?")
			} else {
				expr.WriteString(".*")
			}
		case c == '*':
			expr.WriteString("[^/]*")
		case c == '?':
			expr.WriteString("[^/]")
		case c == '[':
			end := -1
			for j := i + 1; j < len(r); j++ {
				if r[j] == ']' {
					end = j
					break
				}
			}
			if end == -1 {
				expr.WriteString(`\[`)
			} else {
				expr.WriteString(string(r[i : end+1]))
				i = end
			}
		default:
			expr.WriteString(globEscape.ReplaceAllStringFunc(string(c), func(m string) string { return `\` + m }))
		}
	}
	re, err := regexp.Compile("(?s)" + expr.String() + "$")
	return err == nil && re.MatchString(filePath)
}

func normalizeArtifactPattern(p string) string {
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	return strings.TrimRight(p, "/")
}

func patternMatchesArtifactPath(pattern, artifactPath string) bool {
	n := normalizeArtifactPattern(pattern)
	return n == projectSubagentsRelativeDir || n == "*" || strings.HasPrefix(artifactPath, n+"/") || globMatchesPath(n, artifactPath)
}

func ignoreFileExcludesProjectArtifacts(filePath string) bool {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return false
	}
	excluded := map[string]bool{}
	for _, raw := range regexp.MustCompile(`\r?\n`).Split(string(data), -1) {
		line := jsTrim(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		negated := strings.HasPrefix(line, "!")
		pattern := strings.TrimPrefix(line, "!")
		for _, ap := range projectArtifactPaths {
			if !patternMatchesArtifactPath(pattern, ap) {
				continue
			}
			if negated {
				delete(excluded, ap)
			} else {
				excluded[ap] = true
			}
		}
	}
	return len(excluded) == len(projectArtifactPaths)
}

// filesIncludeProjectArtifacts: nil when files is not a list, else whether package.json files include any artifact path.
func filesIncludeProjectArtifacts(files any) *bool {
	arr, ok := asArray(files)
	if !ok {
		return nil
	}
	included := map[string]bool{}
	for _, e := range arr {
		s, ok := e.(string)
		if !ok {
			continue
		}
		negated := strings.HasPrefix(s, "!")
		pattern := strings.TrimPrefix(s, "!")
		for _, ap := range projectArtifactPaths {
			if !patternMatchesArtifactPath(pattern, ap) {
				continue
			}
			if negated {
				delete(included, ap)
			} else {
				included[ap] = true
			}
		}
	}
	r := len(included) > 0
	return &r
}

// getProjectArtifactPackagingWarning warns when project artifacts can enter npm packages ("" when not).
func getProjectArtifactPackagingWarning(cwd string) string {
	data, err := os.ReadFile(filepath.Join(cwd, "package.json"))
	if err != nil {
		return ""
	}
	parsed, msg := jsJSONParse(string(data))
	pkg, ok := asObject(parsed)
	if msg != "" || !ok {
		return ""
	}
	includes := filesIncludeProjectArtifacts(pkg.vals["files"])
	if includes != nil && !*includes {
		return ""
	}
	ignorePath := filepath.Join(cwd, ".npmignore")
	if _, err := os.Stat(ignorePath); err != nil {
		ignorePath = filepath.Join(cwd, ".gitignore")
	}
	if includes == nil && ignoreFileExcludesProjectArtifacts(ignorePath) {
		return ""
	}
	return "Project-scoped subagent artifacts can be included when this package is published. Add '.pi/subagents/' to .npmignore, restrict package.json files, or set artifactDir to 'session' or 'temp'."
}

func getProjectSubagentsDir(cwd string) string { return filepath.Join(cwd, ".pi", "subagents") }
func getProjectArtifactsDir(cwd string) string {
	return filepath.Join(getProjectSubagentsDir(cwd), "artifacts")
}
func getProjectChainRunsDir(cwd string) string {
	return filepath.Join(getProjectSubagentsDir(cwd), "chain-runs")
}

func unsupportedArtifactDir(pref string) error {
	return fmt.Errorf(`Unsupported artifactDir %s; expected "project", "session", or "temp".`, marshalJSON(pref, ""))
}

// getChainRunsDir routes chain scratch files by the artifact preference ("" is "session").
func getChainRunsDir(projectCwd, pref string) (string, error) {
	switch pref {
	case "project":
		return getProjectChainRunsDir(projectCwd), nil
	case "", "session", "temp":
		return chainRunsDir(), nil
	}
	return "", unsupportedArtifactDir(pref)
}

// getArtifactsDir: session artifacts sit beside the session file ("subagent-artifacts"), else in the temp root; "project" keeps
// them in the project when a cwd is given.
func getArtifactsDir(sessionFile, projectCwd, pref string) (string, error) {
	switch pref {
	case "", "session":
		if sessionFile != "" {
			return filepath.Join(filepath.Dir(sessionFile), "subagent-artifacts"), nil
		}
		return tempArtifactsDir(), nil
	case "temp":
		return tempArtifactsDir(), nil
	case "project":
		if projectCwd != "" {
			return getProjectArtifactsDir(projectCwd), nil
		}
		if sessionFile != "" {
			return filepath.Join(filepath.Dir(sessionFile), "subagent-artifacts"), nil
		}
		return tempArtifactsDir(), nil
	}
	return "", unsupportedArtifactDir(pref)
}

var unsafeArtifactName = regexp.MustCompile(`[^\w.-]`)

// getArtifactPaths is the artifact file names of one child.
func getArtifactPaths(artifactsDir, runID, agent string, index *int) artifactPaths {
	suffix := ""
	if index != nil {
		suffix = fmt.Sprintf("_%d", *index)
	}
	base := runID + "_" + unsafeArtifactName.ReplaceAllString(agent, "_") + suffix
	return artifactPaths{
		InputPath: filepath.Join(artifactsDir, base+"_input.md"), OutputPath: filepath.Join(artifactsDir, base+"_output.md"),
		JSONLPath: filepath.Join(artifactsDir, base+".jsonl"), TranscriptPath: filepath.Join(artifactsDir, base+"_transcript.jsonl"),
		MetadataPath: filepath.Join(artifactsDir, base+"_meta.json"),
	}
}

func ensureArtifactsDir(dir string) error { return os.MkdirAll(dir, 0o777) }

func writeArtifact(filePath, content string) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o777); err != nil {
		return err
	}
	return os.WriteFile(filePath, []byte(content), 0o666)
}

// formatOutputArtifactContent is the output file's text: the output, or the failure report when a failed run gave none.
func formatOutputArtifactContent(output, errText, transcriptPath, metadataPath string) string {
	if jsTrim(output) != "" || errText == "" {
		return output
	}
	lines := []string{"Subagent run failed before producing output.", "", "Error:", errText}
	if transcriptPath != "" {
		lines = append(lines, "", "Transcript: "+transcriptPath)
	}
	if metadataPath != "" {
		lines = append(lines, "Metadata: "+metadataPath)
	}
	return strings.Join(lines, "\n")
}

func writeMetadata(filePath string, metadata any) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o777); err != nil {
		return err
	}
	return os.WriteFile(filePath, []byte(marshalJSON(metadata, "  ")), 0o666)
}

func appendJSONL(filePath, line string) error {
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}

// cleanupOldArtifacts removes files older than maxAgeDays, at most once a day per directory (best effort).
func cleanupOldArtifacts(dir string, maxAgeDays float64) error {
	if maxAgeDays <= 0 {
		return nil
	}
	if _, err := os.Stat(dir); err != nil {
		return nil
	}
	marker := filepath.Join(dir, cleanupMarkerFile)
	now := time.Now()
	if st, err := os.Stat(marker); err == nil && now.Sub(st.ModTime()) < 24*time.Hour {
		return nil
	}
	cutoff := now.Add(-time.Duration(maxAgeDays * 24 * float64(time.Hour)))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == cleanupMarkerFile {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if st, err := os.Stat(p); err == nil && st.ModTime().Before(cutoff) {
			_ = os.Remove(p)
		}
	}
	return os.WriteFile(marker, []byte(fmt.Sprint(now.UnixMilli())), 0o666)
}
