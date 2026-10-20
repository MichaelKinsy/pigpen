package pi_subagents

import (
	"path/filepath"
	"testing"
)

func TestProjectLocalArtifactPaths(t *testing.T) {
	pkgDir := func(t *testing.T, packageJSON string, ignoreName, ignoreContent string) string {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "package.json"), packageJSON)
		if ignoreName != "" {
			writeFile(t, filepath.Join(dir, ignoreName), ignoreContent)
		}
		return dir
	}
	tw(t, "artifacts", "warns only when package settings can include project artifacts", func(t *testing.T) {
		warns := func(dir string) bool { return getProjectArtifactPackagingWarning(dir) != "" }
		files := func(name string, patterns ...string) string {
			o := newObject()
			o.set("name", name)
			var l []any
			for _, p := range patterns {
				l = append(l, p)
			}
			o.set("files", l)
			return marshalJSON(o, "")
		}
		polMatches(t, getProjectArtifactPackagingWarning(pkgDir(t, `{"name":"unsafe"}`, "", "")), `\.npmignore`)
		equal(t, warns(pkgDir(t, `{"name":"gitignored"}`, ".gitignore", ".pi/subagents/\n")), false, "gitignored")
		equal(t, warns(pkgDir(t, `{"name":"globignored"}`, ".npmignore", "**/.pi/subagents/**\n")), false, "globignored")
		equal(t, warns(pkgDir(t, `{"name":"classignored"}`, ".npmignore", "[.]pi/subagents/**\n")), false, "classignored")
		polMatches(t, getProjectArtifactPackagingWarning(pkgDir(t, files("explicit-files-over-ignore", ".pi/subagents/**"), ".npmignore", ".pi/subagents/\n")), `artifactDir`)
		equal(t, warns(pkgDir(t, files("restricted", "src/**"), "", "")), false, "restricted")
		equal(t, warns(pkgDir(t, files("malformed-pattern", "[z-a]"), "", "")), false, "malformed")
		for _, p := range []string{"**/*", "*", ".pi/subagents/**", ".pi/subagents/artifacts/**", ".pi/subagents/artifacts", ".pi/subagents/artifacts/*_input.md", ".pi/subagents/artifacts/*_output.md", ".pi/subagents/artifacts/*.jsonl", ".pi/subagents/artifacts/*_meta.json", ".pi/subagents/artifacts/progress/**", ".pi/subagents/artifacts/outputs/**", ".pi/subagents/artifacts/outputs/*/*.md", ".pi/subagents/chain-runs/**", ".pi/subagents/chain-runs"} {
			polMatches(t, getProjectArtifactPackagingWarning(pkgDir(t, files("included", p), "", "")), `artifactDir`)
		}
		equal(t, warns(pkgDir(t, files("excluded-after-include", ".pi/subagents/**", "!.pi/subagents/**"), "", "")), false, "excluded after include")
	})
	tw(t, "artifacts", "keeps project-scoped paths available as an explicit opt-in", func(t *testing.T) {
		cwd := filepath.Join("tmp", "repo")
		equal(t, getProjectSubagentsDir(cwd), filepath.Join(cwd, ".pi/subagents"), "subagents")
		equal(t, getProjectArtifactsDir(cwd), filepath.Join(cwd, ".pi/subagents", "artifacts"), "artifacts")
		equal(t, getProjectChainRunsDir(cwd), filepath.Join(cwd, ".pi/subagents", "chain-runs"), "chain-runs")
		got, _ := getArtifactsDir("", cwd, "project")
		equal(t, got, filepath.Join(cwd, ".pi/subagents", "artifacts"), "project artifacts")
	})
	tw(t, "artifacts", "routes chain scratch files according to the artifact preference", func(t *testing.T) {
		cwd := filepath.Join("tmp", "repo")
		for pref, want := range map[string]string{"": chainRunsDir(), "project": getProjectChainRunsDir(cwd), "session": chainRunsDir(), "temp": chainRunsDir()} {
			got, err := getChainRunsDir(cwd, pref)
			equal(t, [2]any{got, err}, [2]any{want, error(nil)}, "preference "+pref)
		}
		_, err := getChainRunsDir(cwd, "elsewhere")
		equal(t, err.Error(), `Unsupported artifactDir "elsewhere"; expected "project", "session", or "temp".`, "unsupported")
	})
	tw(t, "artifacts", "defaults artifacts to the session directory and falls back to temp", func(t *testing.T) {
		cwd := filepath.Join("tmp", "repo")
		got, _ := getArtifactsDir(filepath.Join("tmp", "sessions", "parent.jsonl"), cwd, "")
		equal(t, got, filepath.Join("tmp", "sessions", "subagent-artifacts"), "session")
		got, _ = getArtifactsDir("", cwd, "")
		equal(t, got, tempArtifactsDir(), "temp")
	})
}
