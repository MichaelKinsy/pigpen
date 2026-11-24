package prompt

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// EnvEntry is one label/value row inside <workstation>.
// upstream: packages/coding-agent/src/system-prompt.ts:221-226
type EnvEntry struct {
	Label string
	Value string
}

// ActiveRepoContext describes a single direct-child git repo when cwd is outside git.
// upstream: packages/coding-agent/src/utils/active-repo-context.ts:15-24
type ActiveRepoContext struct {
	Cwd              string
	RepoRoot         string
	RelativeRepoRoot string
}

// DefaultEnvironmentInfo returns platform/release and CPU architecture in omp's Node/Bun format.
// upstream: packages/coding-agent/src/system-prompt.ts:221-226
func DefaultEnvironmentInfo() []EnvEntry {
	platform := nodePlatform(runtime.GOOS)
	release := osRelease()
	osVal := platform
	if release != "" {
		osVal = platform + " " + release
	}
	return []EnvEntry{
		{Label: "OS", Value: osVal},
		{Label: "Arch", Value: nodeArch(runtime.GOARCH)},
	}
}

func nodePlatform(goos string) string {
	if goos == "windows" {
		return "win32"
	}
	return goos
}

func nodeArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	default:
		return goarch
	}
}

// NormalizePromptPath converts backslashes to forward slashes for prompt interpolation.
// upstream: packages/coding-agent/src/utils/prompt-path.ts:1-3
func NormalizePromptPath(value string) string {
	return strings.ReplaceAll(value, `\`, "/")
}

// FormatLocalCalendarDate formats t as YYYY-MM-DD in its local timezone.
// upstream: packages/tui/src/chrome/local-date.ts:2-7
func FormatLocalCalendarDate(t time.Time) string {
	return t.Local().Format("2006-01-02")
}

// ResolveActiveRepoContext detects the case where cwd is outside any git repo
// and contains exactly one direct-child directory that is itself a git repo.
// upstream: packages/coding-agent/src/utils/active-repo-context.ts:105-139
func ResolveActiveRepoContext(cwd string) *ActiveRepoContext {
	if cwd == "" {
		return nil
	}
	resolvedCwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil
	}
	if findGitRoot(resolvedCwd) != "" {
		return nil
	}
	entries, err := os.ReadDir(resolvedCwd)
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	var found *ActiveRepoContext
	for _, entry := range entries {
		childPath := filepath.Join(resolvedCwd, entry.Name())
		if !entry.IsDir() {
			if entry.Type()&os.ModeSymlink == 0 {
				continue
			}
			info, err := os.Stat(childPath)
			if err != nil || !info.IsDir() {
				continue
			}
		}
		if !hasDirectGitRepo(childPath) {
			continue
		}
		if found != nil {
			// More than one direct-child repo: ambiguous.
			return nil
		}
		rel, err := filepath.Rel(resolvedCwd, childPath)
		if err != nil {
			rel = entry.Name()
		}
		found = &ActiveRepoContext{
			Cwd:              resolvedCwd,
			RepoRoot:         childPath,
			RelativeRepoRoot: NormalizePromptPath(rel),
		}
	}
	return found
}

// hasDirectGitRepo reports whether childPath itself is a git repository root
// with a readable HEAD file (supporting both .git directories and .git gitfiles).
// upstream: packages/coding-agent/src/utils/active-repo-context.ts:79-103
func hasDirectGitRepo(childPath string) bool {
	dotGit := filepath.Join(childPath, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return false
	}
	if info.IsDir() {
		headInfo, err := os.Stat(filepath.Join(dotGit, "HEAD"))
		return err == nil && headInfo.Mode().IsRegular()
	}
	if !info.Mode().IsRegular() {
		return false
	}
	gitDir := parseGitFileDir(dotGit, childPath)
	if gitDir == "" {
		return false
	}
	headInfo, err := os.Stat(filepath.Join(gitDir, "HEAD"))
	return err == nil && headInfo.Mode().IsRegular()
}

func parseGitFileDir(dotGitPath, repoDir string) string {
	data, err := os.ReadFile(dotGitPath)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(data))
	const prefix = "gitdir:"
	if !strings.HasPrefix(strings.ToLower(line), prefix) {
		return ""
	}
	target := strings.TrimSpace(line[len(prefix):])
	if target == "" {
		return ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(repoDir, target)
	}
	return filepath.Clean(target)
}

// findGitRoot walks up from dir looking for a .git directory or gitfile.
func findGitRoot(dir string) string {
	cur, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if hasDirectGitRepo(cur) {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

func sameCleanPath(a, b string) bool {
	ca := filepath.Clean(a)
	cb := filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(ca, cb)
	}
	return ca == cb
}
