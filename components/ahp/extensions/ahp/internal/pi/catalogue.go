package pi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pisession"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// The session catalogue (port of src/pi/session-catalogue.ts): the set of Pi session files under
// a root, most recently modified first, merged with live sessions the host is running.
//
// The catalogue is read-only. It opens files only to summarise them and never rewrites or
// migrates one; Pi owns those files.

const (
	defaultCataloguePage = 30
	maxCataloguePage     = 100
	headerScanBytes      = 8192
)

type sessionFile struct {
	path    string
	mtimeMs float64
}

type catalogueEntry struct {
	sessionFile
	file    string
	onDisk  bool
	summary *ahptypes.SessionSummary
}

func mtimeMs(info os.FileInfo) float64 { return float64(info.ModTime().UnixNano()) / 1e6 }

func listSessionFiles(root string) ([]sessionFile, error) {
	dirs, err := os.ReadDir(root)
	if err != nil {
		// No sessions directory yet is an empty catalogue, not an error.
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var files []sessionFile
	for _, d := range dirs {
		dirPath := filepath.Join(root, d.Name())
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			continue // a file at the top level, or a race with deletion
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(dirPath, e.Name())
			info, err := os.Stat(path)
			if err != nil {
				continue // deleted between the directory read and the stat
			}
			files = append(files, sessionFile{path, mtimeMs(info)})
		}
	}
	sortFiles(files)
	return files, nil
}

// sortFiles orders by recency; ties break by path so the keyset cursor is total and stable.
func sortFiles(files []sessionFile) {
	sort.Slice(files, func(i, j int) bool {
		if files[i].mtimeMs != files[j].mtimeMs {
			return files[i].mtimeMs > files[j].mtimeMs
		}
		return files[i].path < files[j].path
	})
}

func encodeCursor(f sessionFile) string {
	raw := strconv.FormatFloat(f.mtimeMs, 'f', -1, 64) + "\x00" + f.path
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(cursor string) (sessionFile, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	bad := wire.InvalidParams("Unrecognised listSessions cursor")
	if err != nil {
		return sessionFile{}, bad
	}
	s := string(raw)
	sep := strings.IndexByte(s, 0)
	if sep < 0 {
		return sessionFile{}, bad
	}
	ms, err := strconv.ParseFloat(s[:sep], 64)
	path := s[sep+1:]
	if err != nil || math.IsInf(ms, 0) || math.IsNaN(ms) || path == "" {
		return sessionFile{}, bad
	}
	return sessionFile{path, ms}, nil
}

// readSessionID reads the id from a session file's header line without loading the file.
func readSessionID(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	buf := make([]byte, headerScanBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", false
	}
	line := buf[:n]
	if i := strings.IndexByte(string(line), '\n'); i >= 0 {
		line = line[:i]
	}
	var header struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if json.Unmarshal(line, &header) != nil || header.Type != "session" || header.ID == "" {
		return "", false
	}
	return header.ID, true
}

func isoMs(ms float64) string {
	return time.UnixMilli(int64(ms)).UTC().Format("2006-01-02T15:04:05.000Z")
}

// SessionName mirrors Pi: the latest session_info name.
func sessionNameOf(entries []pisession.Entry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type() == "session_info" {
			s, _ := entries[i]["name"].(string)
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// firstUserMessage is the first non-blank user text in a session.
func firstUserMessage(entries []pisession.Entry) string {
	for _, e := range entries {
		if msg := e.Message(); msg != nil && msg["role"] == "user" {
			if text := strings.TrimSpace(mapper.TextFromPiUserContent(msg["content"])); text != "" {
				return text
			}
		}
	}
	return ""
}

func readSessionSummary(f sessionFile) (ahptypes.SessionSummary, bool) {
	all, err := pisession.LoadEntries(f.path)
	if err != nil || len(all) == 0 {
		return ahptypes.SessionSummary{}, false
	}
	header, entries := all[0], all[1:]
	sessionID, _ := header["id"].(string)
	if sessionID == "" {
		return ahptypes.SessionSummary{}, false
	}
	createdAt := ""
	if len(entries) > 0 {
		createdAt = entries[0].Timestamp()
	}
	if createdAt == "" {
		createdAt = isoMs(f.mtimeMs)
	}
	meta, _ := json.Marshal(f.path)
	summary := ahptypes.SessionSummary{
		Resource: wire.SessionURI(sessionID), Provider: Provider,
		Title: mapper.SessionDisplayTitle(sessionNameOf(entries), firstUserMessage(entries)),
		// Idle by definition, and reported as read. Read/unread is not modelled: Pi has no such
		// concept, so tracking it would mean this host inventing durable state of its own, and
		// without archiving to go with it, a catalogue where everything is permanently unread is
		// worse than one that is quiet.
		Status:     ahptypes.SessionStatusIdle | ahptypes.SessionStatusIsRead,
		CreatedAt:  createdAt,
		ModifiedAt: isoMs(f.mtimeMs),
		Meta:       map[string]json.RawMessage{"piSessionFile": meta},
	}
	if cwd, _ := header["cwd"].(string); cwd != "" {
		summary.WorkingDirectories = []ahptypes.URI{wire.PathToFileURI(cwd)}
	}
	return summary, true
}

// Catalogue lists Pi session files under a root.
type Catalogue struct {
	root string

	mu    sync.Mutex
	index map[string]string // session URI -> file
}

// NewCatalogue creates a catalogue over a sessions root (one sub-directory per working directory).
func NewCatalogue(root string) *Catalogue { return &Catalogue{root: root, index: map[string]string{}} }

// FileFor is the file last seen for a session URI.
func (c *Catalogue) FileFor(uri string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f, ok := c.index[uri]
	return f, ok
}

// FindSessionFile locates the file behind a session id, "" if there is none.
func (c *Catalogue) FindSessionFile(sessionID string) (string, error) {
	uri := wire.SessionURI(sessionID)
	c.mu.Lock()
	cached, ok := c.index[uri]
	c.mu.Unlock()
	if ok {
		// The cache is only an index hint: deletion by this host, Pi, or the user must invalidate
		// a path before it is returned again.
		if id, ok := readSessionID(cached); ok && id == sessionID {
			return cached, nil
		}
		c.mu.Lock()
		delete(c.index, uri)
		c.mu.Unlock()
	}
	files, err := listSessionFiles(c.root)
	if err != nil {
		return "", err
	}
	for _, f := range files {
		id, ok := readSessionID(f.path)
		if ok {
			c.mu.Lock()
			c.index[wire.SessionURI(id)] = f.path
			c.mu.Unlock()
		}
		if ok && id == sessionID {
			return f.path, nil
		}
	}
	return "", nil
}

// List serves listSessions: one page of the catalogue, live sessions overriding what is on disk.
func (c *Catalogue) List(_ context.Context, limit *int64, cursor *string, live func() []LiveCatalogueEntry) (ahptypes.ListSessionsResult, error) {
	pageSize := int64(defaultCataloguePage)
	if limit != nil {
		pageSize = *limit
	}
	if pageSize < 1 {
		pageSize = 1
	}
	if pageSize > maxCataloguePage {
		pageSize = maxCataloguePage
	}
	files, err := listSessionFiles(c.root)
	if err != nil {
		return ahptypes.ListSessionsResult{}, err
	}
	entries := map[string]*catalogueEntry{}
	for _, f := range files {
		entries[f.path] = &catalogueEntry{sessionFile: f, file: f.path, onDisk: true}
	}
	// Live state is read after disk discovery, and the page is built without further waiting, so a
	// turn transition cannot make the returned page older than a notification sent before it.
	if live != nil {
		for _, item := range live() {
			ms := 0.0
			if t, err := time.Parse(time.RFC3339Nano, item.Summary.ModifiedAt); err == nil {
				ms = float64(t.UnixNano()) / 1e6
			}
			path := item.File
			if path == "" {
				path = "live:" + item.Summary.Resource
			}
			summary := item.Summary
			entry := &catalogueEntry{sessionFile: sessionFile{path, ms}, file: item.File, summary: &summary}
			if existing := entries[path]; existing != nil && existing.onDisk {
				entry.onDisk = true
			}
			entries[path] = entry
		}
	}
	ordered := make([]*catalogueEntry, 0, len(entries))
	for _, e := range entries {
		ordered = append(ordered, e)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].mtimeMs != ordered[j].mtimeMs {
			return ordered[i].mtimeMs > ordered[j].mtimeMs
		}
		return ordered[i].path < ordered[j].path
	})

	start := 0
	if cursor != nil {
		after, err := decodeCursor(*cursor)
		if err != nil {
			return ahptypes.ListSessionsResult{}, err
		}
		index := -1
		for i, e := range ordered {
			if e.path == after.path {
				index = i
				break
			}
		}
		if index < 0 {
			// The entry the cursor named is gone (deleted between pages): fall back to the
			// position it would have occupied rather than failing the whole call.
			for i, e := range ordered {
				if e.mtimeMs < after.mtimeMs || (e.mtimeMs == after.mtimeMs && e.path > after.path) {
					index = i
					break
				}
			}
			if index < 0 {
				return ahptypes.ListSessionsResult{Items: []ahptypes.SessionSummary{}}, nil
			}
			start = index
		} else {
			start = index + 1
		}
	}
	end := start + int(pageSize)
	if end > len(ordered) {
		end = len(ordered)
	}
	page := ordered[start:end]
	items := []ahptypes.SessionSummary{}
	for _, e := range page {
		var summary ahptypes.SessionSummary
		ok := false
		switch {
		case e.summary != nil:
			summary, ok = *e.summary, true
		case e.file != "":
			summary, ok = readSessionSummary(sessionFile{e.file, e.mtimeMs})
		}
		if !ok {
			continue
		}
		if e.file != "" && e.onDisk {
			c.mu.Lock()
			c.index[summary.Resource] = e.file
			c.mu.Unlock()
		}
		items = append(items, summary)
	}
	result := ahptypes.ListSessionsResult{Items: items}
	if end < len(ordered) && len(page) > 0 {
		next := encodeCursor(page[len(page)-1].sessionFile)
		result.NextCursor = &next
	}
	return result, nil
}
