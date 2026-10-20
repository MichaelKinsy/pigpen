package warden

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// The project's lesson file (prefs.ts PrefsStore): the agent's lessons, its notes that repeated a listed user
// preference, and the groups the user forgot. It lives under warden's data folder, never in a rules file or a
// session file.

// LessonRecord is a lesson or a confirmation recorded in one session.
type LessonRecord struct {
	Session string `json:"session"`
	At      int64  `json:"at"`
}

// Lesson is a standing lesson the agent recorded with warden_remember, redacted.
type Lesson struct {
	Text    string         `json:"text"`
	Records []LessonRecord `json:"records"`
}

// Forgotten is a group the user dropped with `/warden prefs forget`: its wordings, so later rewordings stay dropped.
type Forgotten struct {
	Text    string     `json:"text"`
	Members [][]string `json:"members"`
	At      int64      `json:"at"`
}

// PrefsStore is the content of one project's lesson file.
type PrefsStore struct {
	Lessons       []Lesson    `json:"lessons"`
	Confirmations []Lesson    `json:"confirmations"`
	Forgotten     []Forgotten `json:"forgotten"`
}

func emptyPrefsStore() PrefsStore { return PrefsStore{} }

// prefsStorePath is <data folder>/prefs/<first 12 hex of sha256(project)>.json.
func prefsStorePath(project, dataDir string) string {
	sum := sha256.Sum256([]byte(project))
	return filepath.Join(dataDir, "prefs", hex.EncodeToString(sum[:])[:12]+".json")
}

// prettyJSON is `JSON.stringify(value, null, 2) + "\n"`: two-space indent, no HTML escaping, empty arrays as [].
func prettyJSON(value any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func normalizedLessons(in []Lesson) []Lesson {
	out := make([]Lesson, 0, len(in))
	for _, l := range in {
		if l.Records == nil {
			l.Records = []LessonRecord{}
		}
		out = append(out, l)
	}
	return out
}

func (s PrefsStore) normalized() PrefsStore {
	forgotten := make([]Forgotten, 0, len(s.Forgotten))
	for _, f := range s.Forgotten {
		members := make([][]string, 0, len(f.Members))
		for _, m := range f.Members {
			if m == nil {
				m = []string{}
			}
			members = append(members, m)
		}
		f.Members = members
		forgotten = append(forgotten, f)
	}
	return PrefsStore{Lessons: normalizedLessons(s.Lessons), Confirmations: normalizedLessons(s.Confirmations), Forgotten: forgotten}
}

func asNumber(v any) (int64, bool) {
	f, ok := v.(float64)
	return int64(f), ok
}

func parseLessons(value any) []Lesson {
	list, _ := value.([]any)
	out := []Lesson{}
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		text, okText := m["text"].(string)
		records, okRecords := m["records"].([]any)
		if !okText || !okRecords {
			continue
		}
		lesson := Lesson{Text: text, Records: []LessonRecord{}}
		valid := true
		for _, r := range records {
			rm, ok := r.(map[string]any)
			if !ok {
				valid = false
				break
			}
			session, okSession := rm["session"].(string)
			at, okAt := asNumber(rm["at"])
			if !okSession || !okAt {
				valid = false
				break
			}
			lesson.Records = append(lesson.Records, LessonRecord{Session: session, At: at})
		}
		if valid {
			out = append(out, lesson)
		}
	}
	return out
}

func parseForgotten(value any) []Forgotten {
	list, _ := value.([]any)
	out := []Forgotten{}
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		text, okText := m["text"].(string)
		at, okAt := asNumber(m["at"])
		members, okMembers := m["members"].([]any)
		if !okText || !okAt || !okMembers {
			continue
		}
		entry := Forgotten{Text: text, At: at, Members: [][]string{}}
		valid := true
		for _, member := range members {
			words, ok := member.([]any)
			if !ok {
				valid = false
				break
			}
			names := []string{}
			for _, w := range words {
				s, ok := w.(string)
				if !ok {
					valid = false
					break
				}
				names = append(names, s)
			}
			if !valid {
				break
			}
			entry.Members = append(entry.Members, names)
		}
		if valid {
			out = append(out, entry)
		}
	}
	return out
}

// readPrefsStore: a missing file is an empty store. An unreadable or malformed one is an error: without the
// forgotten list, a group the user dropped could reach the agent again, so the caller injects nothing and says why.
func readPrefsStore(path string) (PrefsStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptyPrefsStore(), nil
		}
		return PrefsStore{}, err
	}
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return PrefsStore{}, err
	}
	switch obj := raw.(type) {
	case map[string]any:
		return PrefsStore{Lessons: parseLessons(obj["lessons"]), Confirmations: parseLessons(obj["confirmations"]), Forgotten: parseForgotten(obj["forgotten"])}, nil
	case []any:
		// An array is an object to JavaScript's typeof check: none of its lists exist, so the store is empty.
		return emptyPrefsStore(), nil
	}
	return PrefsStore{}, errors.New("the lesson file is not a JSON object")
}

// writePrefsStore writes the whole file to a temporary file and renames it, so a crash never leaves half a file.
func writePrefsStore(path string, store PrefsStore) error {
	data, err := prettyJSON(store.normalized())
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, data)
}

// changePrefsStore reads, applies one change, and writes it back under the file's queue, so two lessons or forgets in
// one process run in turn and cannot lose each other's update. change returns a value to hand back and the new
// store, if any.
func changePrefsStore[T any](path string, change func(PrefsStore) (T, *PrefsStore)) (T, error) {
	return Serialize(path, func() (T, error) {
		var zero T
		store, err := readPrefsStore(path)
		if err != nil {
			return zero, err
		}
		value, next := change(store)
		if next != nil {
			if err := writePrefsStore(path, *next); err != nil {
				return zero, err
			}
		}
		return value, nil
	})
}
