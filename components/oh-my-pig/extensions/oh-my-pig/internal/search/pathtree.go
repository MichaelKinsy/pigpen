package search

import (
	"regexp"
	"strings"
)

// upstream: packages/utils/src/path-tree.ts
var urlLikePathRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`)

func isURLLikePath(p string) bool {
	return urlLikePathRe.MatchString(p)
}

type pathTreeFile struct {
	name string
	key  string
}

type pathTreeSubdir struct {
	name string
	node *pathTreeNode
}

type pathTreeNode struct {
	files     []pathTreeFile
	fileNames map[string]struct{}
	subdirs   []pathTreeSubdir
	dirIndex  map[string]*pathTreeNode
}

type pathTreeInput struct {
	path  string
	isDir bool
	key   string
}

type groupedTreeEvent struct {
	kind  string // "dir" or "file"
	depth int
	name  string
	key   string
}

func newPathTreeNode() *pathTreeNode {
	return &pathTreeNode{
		fileNames: make(map[string]struct{}),
		dirIndex:  make(map[string]*pathTreeNode),
	}
}

func (n *pathTreeNode) addFile(name, key string) {
	if _, exists := n.fileNames[name]; exists {
		return
	}
	n.fileNames[name] = struct{}{}
	n.files = append(n.files, pathTreeFile{name: name, key: key})
}

func buildPathTree(entries []pathTreeInput) *pathTreeNode {
	root := newPathTreeNode()
	for _, entry := range entries {
		normalized := strings.ReplaceAll(entry.path, "\\", "/")
		fileKey := entry.key
		if fileKey == "" {
			fileKey = entry.path
		}
		if isURLLikePath(normalized) {
			root.addFile(normalized, fileKey)
			continue
		}
		trimmed := strings.TrimSuffix(normalized, "/")
		if trimmed == "" {
			continue
		}
		segments := strings.Split(trimmed, "/")
		dirCount := len(segments) - 1
		if entry.isDir {
			dirCount = len(segments)
		}
		node := root
		for i := range dirCount {
			seg := segments[i]
			child, ok := node.dirIndex[seg]
			if !ok {
				child = newPathTreeNode()
				node.dirIndex[seg] = child
				node.subdirs = append(node.subdirs, pathTreeSubdir{name: seg, node: child})
			}
			node = child
		}
		if !entry.isDir {
			node.addFile(segments[len(segments)-1], fileKey)
		}
	}
	return root
}

func walkPathTree(node *pathTreeNode, depth int, emit func(groupedTreeEvent)) {
	for _, f := range node.files {
		emit(groupedTreeEvent{kind: "file", depth: depth, name: f.name, key: f.key})
	}
	for _, sub := range node.subdirs {
		dirNode := sub.node
		parts := []string{sub.name}
		for len(dirNode.files) == 0 && len(dirNode.subdirs) == 1 {
			only := dirNode.subdirs[0]
			parts = append(parts, only.name)
			dirNode = only.node
		}
		emit(groupedTreeEvent{kind: "dir", depth: depth, name: strings.Join(parts, "/")})
		walkPathTree(dirNode, depth+1, emit)
	}
}

// formatGroupedPaths renders a flat path list as a grouped, prefix-folded directory tree.
// upstream: packages/utils/src/path-tree.ts:135-147
func formatGroupedPaths(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	inputs := make([]pathTreeInput, len(paths))
	for i, p := range paths {
		inputs[i] = pathTreeInput{path: p, isDir: strings.HasSuffix(p, "/")}
	}
	tree := buildPathTree(inputs)
	var lines []string
	walkPathTree(tree, 0, func(ev groupedTreeEvent) {
		if ev.kind == "dir" {
			lines = append(lines, strings.Repeat("#", ev.depth+1)+" "+ev.name+"/")
		} else {
			lines = append(lines, ev.name)
		}
	})
	return strings.Join(lines, "\n")
}

// fileMatchSection is one file's rendered grep lines (model & display) plus optional snapshot tag.
// upstream: packages/tui/src/tools/grouped-file-output.ts:110-114
type fileMatchSection struct {
	model   []string
	display []string
	tag     string
}

// groupedFilesOutput mirrors GroupedFilesOutput in grouped-file-output.ts:42-47.
type groupedFilesOutput struct {
	model     []string
	display   []string
	modelEnds map[string]int
}

// formatFileMatches renders per-file grep matches in grouped or flat mode.
// upstream: packages/tui/src/tools/grouped-file-output.ts:122-154
func formatFileMatches(files []string, grouped bool, renderFile func(filePath string) fileMatchSection) groupedFilesOutput {
	out := groupedFilesOutput{
		modelEnds: make(map[string]int, len(files)),
	}
	if grouped {
		sections := make(map[string]fileMatchSection, len(files))
		var inputs []pathTreeInput
		for _, fp := range files {
			if _, seen := sections[fp]; seen {
				continue
			}
			sec := renderFile(fp)
			if len(sec.model) == 0 {
				continue
			}
			sections[fp] = sec
			inputs = append(inputs, pathTreeInput{path: fp, isDir: false, key: fp})
		}
		tree := buildPathTree(inputs)
		emitted := false
		walkPathTree(tree, 0, func(ev groupedTreeEvent) {
			hashes := strings.Repeat("#", ev.depth+1)
			if emitted && (ev.depth == 0 || ev.kind == "dir") {
				out.model = append(out.model, "")
				out.display = append(out.display, "")
			}
			emitted = true
			if ev.kind == "dir" {
				hdr := hashes + " " + ev.name + "/"
				out.model = append(out.model, hdr)
				out.display = append(out.display, hdr)
				return
			}
			sec := sections[ev.key]
			suffix := ""
			if sec.tag != "" {
				suffix = "#" + sec.tag
			}
			hdr := hashes + " " + ev.name + suffix
			out.model = append(out.model, hdr)
			out.model = append(out.model, sec.model...)
			out.display = append(out.display, hdr)
			if len(sec.display) > 0 {
				out.display = append(out.display, sec.display...)
			} else {
				out.display = append(out.display, sec.model...)
			}
			out.modelEnds[ev.key] = len(out.model)
		})
		return out
	}

	for _, fp := range files {
		sec := renderFile(fp)
		if len(sec.model) == 0 {
			continue
		}
		if len(out.model) > 0 {
			out.model = append(out.model, "")
			out.display = append(out.display, "")
		}
		if sec.tag != "" {
			out.model = append(out.model, "["+fp+"#"+sec.tag+"]")
		}
		out.model = append(out.model, sec.model...)
		if len(sec.display) > 0 {
			out.display = append(out.display, sec.display...)
		} else {
			out.display = append(out.display, sec.model...)
		}
		out.modelEnds[fp] = len(out.model)
	}
	return out
}
