package search

import (
	"bufio"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// walkEntry is a single filesystem entry produced by walkDir.
type walkEntry struct {
	absPath string
	relPath string // forward-slash path relative to walk root
	isDir   bool
	mtime   time.Time
	size    int64
}

type walkOptions struct {
	hidden     bool
	gitignore  bool
	recursive  bool
	excludes   []*globMatcher // !glob exclusions from the caller's path list
	include    *globMatcher   // optional positive glob filter
	includeDir bool           // whether to yield matching directories (glob tool)
}

// gitignoreRule is a single parsed line from a .gitignore file.
type gitignoreRule struct {
	negated  bool
	dirOnly  bool
	anchored bool
	matcher  *globMatcher
}

type gitignoreFrame struct {
	baseRel string // "" for root, else forward-slash relative path
	rules   []gitignoreRule
}

// loadGitignore reads a .gitignore file in dir and returns its parsed rules.
func loadGitignore(dir string) []gitignoreRule {
	f, err := os.Open(filepath.Join(dir, ".gitignore"))
	if err != nil {
		return nil
	}
	defer f.Close()

	var rules []gitignoreRule
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Unescape leading \# or \!
		negated := false
		if strings.HasPrefix(line, "!") {
			negated = true
			line = line[1:]
		} else if strings.HasPrefix(line, `\#`) || strings.HasPrefix(line, `\!`) {
			line = line[1:]
		}
		line = strings.TrimRight(line, " \t")
		if line == "" {
			continue
		}
		dirOnly := strings.HasSuffix(line, "/")
		if dirOnly {
			line = strings.TrimSuffix(line, "/")
		}
		anchored := strings.Contains(line, "/")
		line = strings.TrimPrefix(line, "/")
		if line == "" {
			continue
		}
		m, err := compileGlob(line)
		if err != nil {
			continue
		}
		rules = append(rules, gitignoreRule{
			negated:  negated,
			dirOnly:  dirOnly,
			anchored: anchored,
			matcher:  m,
		})
	}
	return rules
}

func isIgnored(frames []gitignoreFrame, relPath string, isDir bool) bool {
	ignored := false
	for _, frame := range frames {
		subRel := relPath
		if frame.baseRel != "" {
			prefix := frame.baseRel + "/"
			if !strings.HasPrefix(relPath, prefix) {
				continue
			}
			subRel = relPath[len(prefix):]
		}
		baseName := subRel
		if idx := strings.LastIndexByte(subRel, '/'); idx != -1 {
			baseName = subRel[idx+1:]
		}
		for _, rule := range frame.rules {
			if rule.dirOnly && !isDir {
				continue
			}
			var matched bool
			if rule.anchored {
				matched = rule.matcher.match(subRel)
			} else {
				matched = rule.matcher.match(baseName) || rule.matcher.match(subRel)
			}
			if matched {
				ignored = !rule.negated
			}
		}
	}
	return ignored
}

// walkDir traverses root respecting gitignore, hidden-file, and exclusion rules.
// upstream: crates/pi-walker/src/lib.rs
func walkDir(ctx context.Context, root string, opts walkOptions) ([]walkEntry, error) {
	var results []walkEntry
	var initialFrames []gitignoreFrame
	if opts.gitignore {
		// Load ancestor .gitignore files up to the enclosing .git root if present.
		if rules := loadGitignore(root); len(rules) > 0 {
			initialFrames = append(initialFrames, gitignoreFrame{baseRel: "", rules: rules})
		}
	}

	var visit func(absDir, relDir string, frames []gitignoreFrame) error
	visit = func(absDir, relDir string, frames []gitignoreFrame) error {
		if ctx != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}
		entries, err := os.ReadDir(absDir)
		if err != nil {
			// Skip unreadable directories, matching pi-walker's SkipSkippable mode.
			return nil
		}
		for _, d := range entries {
			name := d.Name()
			// Always skip .git directory.
			if name == ".git" {
				continue
			}
			if !opts.hidden && strings.HasPrefix(name, ".") {
				continue
			}
			isDir := d.IsDir()
			if d.Type()&fs.ModeSymlink != 0 {
				// Do not follow symlinks by default (FollowLinks::Never).
				continue
			}
			relPath := name
			if relDir != "" {
				relPath = relDir + "/" + name
			}
			absPath := filepath.Join(absDir, name)

			if opts.gitignore && isIgnored(frames, relPath, isDir) {
				continue
			}
			if isExcluded(opts.excludes, relPath, name, isDir) {
				continue
			}

			if isDir {
				if opts.includeDir && (opts.include == nil || opts.include.match(relPath) || opts.include.match(relPath+"/")) {
					info, err := d.Info()
					var mt time.Time
					if err == nil {
						mt = info.ModTime()
					}
					results = append(results, walkEntry{
						absPath: absPath,
						relPath: relPath,
						isDir:   true,
						mtime:   mt,
					})
				}
				if opts.recursive {
					childFrames := frames
					if opts.gitignore {
						if childRules := loadGitignore(absPath); len(childRules) > 0 {
							childFrames = append(append([]gitignoreFrame(nil), frames...), gitignoreFrame{
								baseRel: relPath,
								rules:   childRules,
							})
						}
					}
					if err := visit(absPath, relPath, childFrames); err != nil {
						return err
					}
				}
				continue
			}

			if opts.include != nil && !opts.include.match(relPath) {
				continue
			}
			info, err := d.Info()
			var mt time.Time
			var sz int64
			if err == nil {
				mt = info.ModTime()
				sz = info.Size()
			}
			results = append(results, walkEntry{
				absPath: absPath,
				relPath: relPath,
				isDir:   false,
				mtime:   mt,
				size:    sz,
			})
		}
		return nil
	}

	if err := visit(root, "", initialFrames); err != nil {
		return nil, err
	}
	return results, nil
}

func isExcluded(excludes []*globMatcher, relPath, baseName string, isDir bool) bool {
	for _, ex := range excludes {
		if ex.match(relPath) || ex.match(baseName) {
			return true
		}
		if isDir && ex.match(relPath+"/") {
			return true
		}
	}
	return false
}

// globMatcher compiles a glob pattern supporting *, ?, **, [abc], and {a,b} into a Go regexp.
type globMatcher struct {
	re *regexp.Regexp
}

func compileGlob(pattern string) (*globMatcher, error) {
	p := strings.ReplaceAll(pattern, "\\", "/")
	var b strings.Builder
	b.WriteString("^")
	n := len(p)
	for i := 0; i < n; i++ {
		c := p[i]
		switch c {
		case '*':
			if i+1 < n && p[i+1] == '*' {
				if i+2 < n && p[i+2] == '/' {
					// "**/": zero or more path segments
					b.WriteString("(?:.*/)?")
					i += 2
				} else {
					b.WriteString(".*")
					i++
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '[':
			j := i + 1
			if j < n && (p[j] == '!' || p[j] == '^') {
				j++
			}
			if j < n && p[j] == ']' {
				j++
			}
			for j < n && p[j] != ']' {
				j++
			}
			if j >= n {
				b.WriteString(`\[`)
			} else {
				class := p[i+1 : j]
				if strings.HasPrefix(class, "!") {
					class = "^" + class[1:]
				}
				b.WriteString("[")
				b.WriteString(class)
				b.WriteString("]")
				i = j
			}
		case '{':
			// Support single-level brace alternation {a,b,c}
			end := strings.IndexByte(p[i:], '}')
			if end == -1 {
				b.WriteString(`\{`)
			} else {
				alts := strings.Split(p[i+1:i+end], ",")
				b.WriteString("(?:")
				for idx, alt := range alts {
					if idx > 0 {
						b.WriteString("|")
					}
					b.WriteString(regexp.QuoteMeta(alt))
				}
				b.WriteString(")")
				i += end
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, err
	}
	return &globMatcher{re: re}, nil
}

func (g *globMatcher) match(s string) bool {
	if g == nil || g.re == nil {
		return true
	}
	return g.re.MatchString(s)
}
