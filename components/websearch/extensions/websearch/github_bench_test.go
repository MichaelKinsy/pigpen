package websearch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// GitHub extraction on representative inputs, with the fake gh and git programs of
// port/bench-github.mjs (the same scripts run the original under Node):
//
//	go test -run xxx -bench GitHub -benchmem
const benchCloneGit = `for a; do dest="$a"; done
mkdir -p "$dest/src"
i=0
while [ $i -lt 60 ]; do printf 'x' > "$dest/src/f$i.txt"; i=$((i+1)); done
printf '# Bench\n' > "$dest/README.md"`

const benchPRGh = `case "$*" in
  '--version') echo "gh version 2.0.0"; exit 0;;
  'pr view'*) cat "$BENCH_PR_JSON"; exit 0;;
  'api'*) echo '[]'; exit 0;;
esac
exit 1`

func benchWorld(b *testing.B) (bin string) {
	b.Helper()
	if _, err := os.Stat("/bin/sh"); err != nil {
		b.Skip("no /bin/sh")
	}
	root := b.TempDir()
	bin = filepath.Join(root, "bin")
	agent := filepath.Join(root, "agent")
	for _, d := range []string{bin, agent} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			b.Fatal(err)
		}
	}
	b.Setenv("HOME", root)
	b.Setenv("PIG_CODING_AGENT_DIR", agent)
	b.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ResetCaches()
	b.Cleanup(ResetCaches)
	if err := os.WriteFile(filepath.Join(agent, "web-search.json"), []byte(`{"githubClone":{"clonePath":"`+filepath.Join(root, "clones")+`"}}`), 0o600); err != nil {
		b.Fatal(err)
	}
	return bin
}

func benchShim(b *testing.B, bin, name, script string) {
	b.Helper()
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		b.Fatal(err)
	}
}

// BenchmarkGitHubCloneView clones a 60-file repository through a fake git and renders its tree and README.
func BenchmarkGitHubCloneView(b *testing.B) {
	bin := benchWorld(b)
	benchShim(b, bin, "gh", "exit 1")
	benchShim(b, bin, "git", benchCloneGit)
	s := newGitHubState()
	s.hintOut = &discard{}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		res, err := s.extractGitHub(bg(), "https://github.com/o/r", true)
		if err != nil || res == nil {
			b.Fatalf("%v %v", res, err)
		}
		s.clearCloneCache()
	}
}

// BenchmarkGitHubPullRequestViaGh renders a pull request from gh view and one page of review comments.
func BenchmarkGitHubPullRequestViaGh(b *testing.B) {
	bin := benchWorld(b)
	jsonPath := filepath.Join(filepath.Dir(bin), "pr.json")
	if err := os.WriteFile(jsonPath, []byte(benchPRJSON(b)), 0o644); err != nil {
		b.Fatal(err)
	}
	b.Setenv("BENCH_PR_JSON", jsonPath)
	benchShim(b, bin, "gh", benchPRGh)
	s := newGitHubState()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		res, err := s.extractGitHubIssuePR(bg(), "https://github.com/o/r/pull/7", ExtractOptions{})
		if err != nil || res == nil || res.Error != nil {
			b.Fatalf("%+v %v", res, err)
		}
	}
}

// BenchmarkGitHubRenderPullRequest is the renderer alone on a pull request with 60 files, 25 commits and 18 comments.
func BenchmarkGitHubRenderPullRequest(b *testing.B) {
	var view map[string]any
	if err := json.Unmarshal([]byte(benchPRJSON(b)), &view); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		res := renderGitHubPRIssue(issuePRRender{URL: "u", Owner: "o", Repo: "r", Kind: "pull", Number: 7, View: view})
		if res.Content == "" {
			b.Fatal("empty")
		}
	}
}

// benchPRJSON is the gh pr view answer of the benchmarks: port/bench-github-pr.json, which port/bench-github.mjs also serves to the original.
func benchPRJSON(b *testing.B) string {
	b.Helper()
	data, err := os.ReadFile(filepath.FromSlash("../../port/bench-github-pr.json"))
	if err != nil {
		b.Skip("port/bench-github-pr.json is not beside this module")
	}
	return string(data)
}
