package warden

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SkipDirs are the directories source-file discovery does not enter (discover.ts SKIP_DIRS).
var SkipDirs = map[string]bool{
	".git": true, "node_modules": true, "dist": true, "build": true, ".next": true, ".output": true,
	"coverage": true, ".cache": true, "__pycache__": true, ".pi-warden": true, "vendor": true,
	".turbo": true, ".vercel": true, ".netlify": true, ".svelte-kit": true,
}

// SourceRE matches the file names discovery counts as source (discover.ts SOURCE_RE).
var SourceRE = regexp.MustCompile(`\.(ts|tsx|js|jsx|rs|py|go|vue|svelte|rb|php|java|kt|swift|cs|cpp|c|h)$`)

// DiscoverSourceFiles walks dir and returns source-file paths relative to it, skipping SkipDirs and dot entries and
// stopping after max files. An unreadable directory contributes nothing (upstream only logs a warning).
func DiscoverSourceFiles(dir string, max int) []string {
	files := []string{}
	var walk func(current string)
	walk = func(current string) {
		if len(files) >= max {
			return
		}
		entries, err := os.ReadDir(current)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if len(files) >= max {
				return
			}
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			full := filepath.Join(current, name)
			switch {
			case entry.IsDir():
				if SkipDirs[name] {
					continue
				}
				walk(full)
			case entry.Type().IsRegular() && SourceRE.MatchString(name):
				files = append(files, full[len(dir)+1:])
			}
		}
	}
	walk(dir)
	return files
}

var projectManifests = []string{"package.json", "Cargo.toml", "pyproject.toml", "go.mod"}

func hasManifest(dir string) bool {
	for _, m := range projectManifests {
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
			return true
		}
	}
	return false
}

// FindProjects returns the workspace root when it has a manifest, then every direct subdirectory that has one.
func FindProjects(cwd string) []string {
	projects := []string{}
	if hasManifest(cwd) {
		projects = append(projects, cwd)
	}
	entries, err := os.ReadDir(cwd)
	if err != nil {
		return projects
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "node_modules" || strings.HasPrefix(name, ".") {
			continue
		}
		sub := filepath.Join(cwd, name)
		if !hasManifest(sub) {
			continue
		}
		dup := false
		for _, p := range projects {
			if p == sub {
				dup = true
			}
		}
		if !dup {
			projects = append(projects, sub)
		}
	}
	return projects
}
