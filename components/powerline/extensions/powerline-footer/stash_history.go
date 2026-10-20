package powerline_footer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Stash history on disk and the prompts of the project's earlier sessions. upstream: index.ts 387-696 (getStashHistoryPath,
// getProjectSessionsPath, readRecentProjectPrompts, normalizeStashHistoryEntries, readPersistedStashHistory,
// persistStashHistory, pushStashHistory).

const (
	stashHistoryLimit                = 12
	projectPromptHistoryLimit        = 50
	projectPromptHistoryFileLimit    = 50
	projectPromptHistoryMaxBytes     = 3 * 1024 * 1024
	projectPromptHistoryFileMaxBytes = 64 * 1024
	stashPreviewWidth                = 72
)

func hasNonWhitespaceText(text string) bool { return jsTrim(text) != "" }

func getStashHistoryPath() string { return getAgentPath("powerline-footer", "stash-history.json") }

var (
	projectKeyEdges = regexp.MustCompile(`^[/\\]+|[/\\]+$`)
	projectKeyRuns  = regexp.MustCompile(`[\\/]+`)
)

func getProjectSessionsPath(cwd string) string {
	key := projectKeyRuns.ReplaceAllString(projectKeyEdges.ReplaceAllString(cwd, ""), "-")
	return filepath.Join(getAgentPath("sessions"), "--"+key+"--")
}

// getPromptHistoryText is the text of a user message, whitespace collapsed to single spaces.
func getPromptHistoryText(content any) string {
	collapse := func(s string) string { return strings.Join(strings.FieldsFunc(s, isJSSpace), " ") }
	switch c := content.(type) {
	case string:
		return collapse(c)
	case []any:
		var parts []string
		for _, block := range c {
			o := asObject(block)
			if o == nil || o.vals["type"] != "text" {
				continue
			}
			if t, ok := o.vals["text"].(string); ok {
				parts = append(parts, t)
			}
		}
		return collapse(strings.Join(parts, "\n"))
	}
	return ""
}

// readFileTail is the last maxBytes of the file; when that cuts a line the partial first line is dropped.
func readFileTail(path string, size, maxBytes int64) (string, error) {
	n := min(size, maxBytes)
	if n == 0 {
		return "", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, n)
	read, err := f.ReadAt(buf, size-n)
	if err != nil && err != io.EOF {
		return "", err
	}
	text := strings.ToValidUTF8(string(buf[:read]), "\ufffd")
	if size <= n {
		return text, nil
	}
	i := strings.IndexByte(text, '\n')
	if i < 0 {
		return "", nil
	}
	return text[i+1:], nil
}

type sessionFile struct {
	name, path string
	mtimeMs    float64
	size       int64
}

// readRecentProjectPrompts reads the user prompts of the newest session files of the project, newest first, within the
// original's file and byte bounds. A line that is a user message but not JSON fails the whole read, as in the original; the
// error text carries Go's JSON error where the original carries V8's.
func readRecentProjectPrompts(cwd string, limit int) ([]string, error) {
	dir := getProjectSessionsPath(cwd)
	if _, err := os.Stat(dir); err != nil {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []sessionFile
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		files = append(files, sessionFile{e.Name(), path, float64(info.ModTime().UnixNano()) / 1e6, info.Size()})
	}
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].mtimeMs != files[j].mtimeMs {
			return files[i].mtimeMs > files[j].mtimeMs
		}
		return files[i].name > files[j].name
	})
	if len(files) > projectPromptHistoryFileLimit {
		files = files[:projectPromptHistoryFileLimit]
	}

	var prompts []string
	seen := map[string]bool{}
	remaining := int64(projectPromptHistoryMaxBytes)
	for _, file := range files {
		if remaining <= 0 {
			break
		}
		toRead := min(file.size, projectPromptHistoryFileMaxBytes, remaining)
		remaining -= toRead
		tail, err := readFileTail(file.path, file.size, toRead)
		if err != nil {
			return nil, err
		}
		lines := strings.Split(tail, "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			line := lines[i]
			if line == "" || !strings.Contains(line, `"type":"message"`) || !strings.Contains(line, `"role":"user"`) {
				continue
			}
			v, err := parseJSON([]byte(line))
			if err != nil {
				return nil, fmt.Errorf("Failed to parse session file %s: %s", file.path, jsonErrorText(err))
			}
			entry := asObject(v)
			if entry == nil || entry.vals["type"] != "message" {
				continue
			}
			msg := asObject(entry.vals["message"])
			if msg == nil || msg.vals["role"] != "user" {
				continue
			}
			text := getPromptHistoryText(msg.vals["content"])
			if !hasNonWhitespaceText(text) || seen[text] {
				continue
			}
			seen[text] = true
			prompts = append(prompts, text)
			if len(prompts) >= limit {
				return prompts, nil
			}
		}
	}
	return prompts, nil
}

// jsonErrorText is a JSON error's message with the first letter capitalised, which is where Go's and V8's wording meet.
func jsonErrorText(err error) string {
	s := err.Error()
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func normalizeStashHistoryEntries(value any) []string {
	arr, ok := value.([]any)
	if !ok {
		return []string{}
	}
	history := []string{}
	for _, v := range arr {
		entry, ok := v.(string)
		if !ok || !hasNonWhitespaceText(entry) {
			continue
		}
		if len(history) > 0 && history[len(history)-1] == entry {
			continue
		}
		history = append(history, entry)
		if len(history) >= stashHistoryLimit {
			break
		}
	}
	return history
}

func readPersistedStashHistory() []string {
	data, err := os.ReadFile(getStashHistoryPath())
	if err != nil {
		return []string{}
	}
	v, err := parseJSON(data)
	o := asObject(v)
	if err != nil || o == nil {
		return []string{}
	}
	return normalizeStashHistoryEntries(o.vals["history"])
}

func persistStashHistory(history []string) {
	path := getStashHistoryPath()
	entries := []any{}
	for _, h := range history[:min(len(history), stashHistoryLimit)] {
		entries = append(entries, h)
	}
	payload := newObject()
	payload.set("version", float64(1))
	payload.set("history", entries)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(marshalJSON(payload, "  ")+"\n"), 0o644)
}

// pushStashHistory puts text first; it reports whether the history changed.
func pushStashHistory(history *[]string, text string) bool {
	if !hasNonWhitespaceText(text) {
		return false
	}
	if len(*history) > 0 && (*history)[0] == text {
		return false
	}
	h := append([]string{text}, *history...)
	if len(h) > stashHistoryLimit {
		h = h[:stashHistoryLimit]
	}
	*history = h
	return true
}
