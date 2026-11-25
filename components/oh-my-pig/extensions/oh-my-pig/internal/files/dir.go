package files

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	readDirectoryMaxDepth   = 2
	readDirectoryChildLimit = 12
)

var prunedDirs = map[string]bool{
	"node_modules": true,
	".git":         true,
	"dist":         true,
	"build":        true,
	"target":       true,
	".next":        true,
	".cache":       true,
	"__pycache__":  true,
}

type dirNode struct {
	name       string
	isDir      bool
	mtime      time.Time
	size       int64
	depth      int
	children   []*dirNode
	dropped    int
	unreadable string
}

// readDirectory builds a directory tree matching omp's ReadTool.#readDirectory output.
// upstream: packages/coding-agent/src/tools/read.ts:2852, workspace-tree.ts:56
func readDirectory(absPath string, parsed ParsedSelector) (string, error) {
	if parsed.IsMultiRange() {
		return "", fmt.Errorf("Multi-range line selectors are not supported for directory listings.")
	}

	root, err := scanDir(absPath, ".", 0, readDirectoryMaxDepth)
	if err != nil {
		return "", fmt.Errorf("Cannot read directory: %v", err)
	}

	var output string
	if len(root.children) == 0 {
		output = "(empty directory)"
	} else {
		now := time.Now()
		var lines []string
		renderDirNode(root, now, &lines)
		output = strings.Join(lines, "\n")
	}

	if parsed.Kind == SelectorLines || parsed.Kind == SelectorTail {
		allLines := strings.Split(output, "\n")
		resolved := parsed.ResolveTail(len(allLines))
		first := resolved.Ranges[0]
		start := max(0, first.StartLine-1)
		if start >= len(allLines) {
			suggestion := "The listing is empty."
			if len(allLines) > 0 {
				suggestion = fmt.Sprintf("Use :1- to read from the start, or :%d to read the last line.", len(allLines))
			}
			return fmt.Sprintf("Line %d is beyond end of listing (%d lines total). %s", start+1, len(allLines), suggestion), nil
		}
		end := len(allLines)
		if first.EndLine > 0 {
			end = min(first.EndLine, len(allLines))
		}
		text := strings.Join(allLines[start:end], "\n")
		if end < len(allLines) {
			remaining := len(allLines) - end
			text += fmt.Sprintf("\n\n[%d more lines in listing. Use :%d- to continue]", remaining, end+1)
		}
		return text, nil
	}

	return output, nil
}

func scanDir(absPath, name string, depth, maxDepth int) (*dirNode, error) {
	entries, err := os.ReadDir(absPath)
	if err != nil {
		if depth == 0 {
			return nil, err
		}
		return &dirNode{name: name, isDir: true, depth: depth, unreadable: err.Error()}, nil
	}

	node := &dirNode{name: name, isDir: true, depth: depth}
	all := make([]*dirNode, 0, len(entries))
	for _, entry := range entries {
		ename := entry.Name()
		isDir := entry.IsDir()
		if isDir && prunedDirs[ename] {
			continue
		}
		info, infoErr := entry.Info()
		var mtime time.Time
		var size int64
		if infoErr == nil {
			mtime = info.ModTime()
			size = info.Size()
		}
		child := &dirNode{
			name:  ename,
			isDir: isDir,
			mtime: mtime,
			size:  size,
			depth: depth + 1,
		}
		all = append(all, child)
	}

	// Sort newest-first, then by name ascending (upstream: workspace-tree.ts:250).
	slices.SortFunc(all, func(a, b *dirNode) int {
		if !a.mtime.Equal(b.mtime) {
			if a.mtime.After(b.mtime) {
				return -1
			}
			return 1
		}
		return strings.Compare(a.name, b.name)
	})

	// Root is uncapped; child directories cap at readDirectoryChildLimit.
	if depth > 0 && len(all) > readDirectoryChildLimit {
		limit := readDirectoryChildLimit
		kept := make([]*dirNode, 0, limit)
		kept = append(kept, all[:limit-1]...)
		kept = append(kept, all[len(all)-1])
		node.children = kept
		node.dropped = len(all) - limit
	} else {
		node.children = all
	}

	if depth+1 < maxDepth {
		for i, child := range node.children {
			if !child.isDir {
				continue
			}
			sub, _ := scanDir(filepath.Join(absPath, child.name), child.name, depth+1, maxDepth)
			if sub != nil {
				sub.mtime = child.mtime
				node.children[i] = sub
			}
		}
	}

	return node, nil
}

func renderDirNode(node *dirNode, now time.Time, out *[]string) {
	if node.depth == 0 {
		*out = append(*out, node.name)
	} else {
		indent := strings.Repeat("  ", node.depth)
		suffix := ""
		if node.isDir {
			suffix = "/"
		}
		unreadable := ""
		if node.unreadable != "" {
			unreadable = " [unreadable: " + node.unreadable + "]"
		}
		line := fmt.Sprintf("%s- %s%s%s", indent, node.name, suffix, unreadable)
		age := formatAge(now.Sub(node.mtime))
		if !node.isDir {
			line += " " + formatBytes(node.size)
		}
		if age != "" {
			line += " " + age
		}
		*out = append(*out, line)
	}

	if node.dropped > 0 && len(node.children) > 0 {
		for _, child := range node.children[:len(node.children)-1] {
			renderDirNode(child, now, out)
		}
		indent := strings.Repeat("  ", node.depth+1)
		*out = append(*out, fmt.Sprintf("%s- … %d more", indent, node.dropped))
		renderDirNode(node.children[len(node.children)-1], now, out)
		return
	}
	for _, child := range node.children {
		renderDirNode(child, now, out)
	}
}

func formatBytes(b int64) string {
	switch {
	case b < 1024:
		return fmt.Sprintf("%dB", b)
	case b < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(b)/1024)
	case b < 1024*1024*1024:
		return fmt.Sprintf("%.1fMB", float64(b)/(1024*1024))
	default:
		return fmt.Sprintf("%.1fGB", float64(b)/(1024*1024*1024))
	}
}

// formatAge formats a duration matching omp's packages/utils/src/format.ts:106-122.
// Zero (or negative) duration returns ""; sub-minute positive durations return "just now".
func formatAge(d time.Duration) string {
	secs := int64(d / time.Second)
	if secs <= 0 {
		return ""
	}
	mins := secs / 60
	hours := mins / 60
	days := hours / 24
	weeks := days / 7
	months := days / 30
	years := days / 365

	if years > 0 {
		return fmt.Sprintf("%dy ago", years)
	}
	if months > 0 {
		return fmt.Sprintf("%dmo ago", months)
	}
	if weeks > 0 {
		return fmt.Sprintf("%dw ago", weeks)
	}
	if days > 0 {
		return fmt.Sprintf("%dd ago", days)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh ago", hours)
	}
	if mins > 0 {
		return fmt.Sprintf("%dm ago", mins)
	}
	return "just now"
}
