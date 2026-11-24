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

// fileMatchSection is one file's rendered grep lines plus optional snapshot tag.
// upstream: packages/tui/src/tools/grouped-file-output.ts:95-99
type fileMatchSection struct {
	model []string
	tag   string
}

// formatFileMatches renders per-file grep matches in grouped or flat mode.
// upstream: packages/tui/src/tools/grouped-file-output.ts:107-137
func formatFileMatches(files []string, grouped bool, renderFile func(filePath string) fileMatchSection) []string {
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
		var model []string
		emitted := false
		walkPathTree(tree, 0, func(ev groupedTreeEvent) {
			hashes := strings.Repeat("#", ev.depth+1)
			if emitted && (ev.depth == 0 || ev.kind == "dir") {
				model = append(model, "")
			}
			emitted = true
			if ev.kind == "dir" {
				model = append(model, hashes+" "+ev.name+"/")
				return
			}
			sec := sections[ev.key]
			suffix := ""
			if sec.tag != "" {
				suffix = "#" + sec.tag
			}
			model = append(model, hashes+" "+ev.name+suffix)
			model = append(model, sec.model...)
		})
		return model
	}

	var model []string
	for _, fp := range files {
		sec := renderFile(fp)
		if len(sec.model) == 0 {
			continue
		}
		if len(model) > 0 {
			model = append(model, "")
		}
		if sec.tag != "" {
			model = append(model, "["+fp+"#"+sec.tag+"]")
		}
		model = append(model, sec.model...)
	}
	return model
}
