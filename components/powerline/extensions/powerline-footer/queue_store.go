package powerline_footer

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The persistent prompt queue. upstream: queue/types.ts and queue/store.ts (the store keeps its file format, its lock
// directory and its 24 hour retention of sent items, so a Go port and the original can share one inbox).

type queueIntent string
type queueStatus string

const (
	intentSteer       queueIntent = "steer"
	intentFollowUp    queueIntent = "follow-up"
	intentPostCompact queueIntent = "post-compact"

	statusQueued     queueStatus = "queued"
	statusBlocked    queueStatus = "blocked"
	statusDelivering queueStatus = "delivering"
	statusSent       queueStatus = "sent"
	statusFailed     queueStatus = "failed"

	targetCurrentSession = "current-session"
	targetProject        = "project"
	targetGlobal         = "global"
)

// queueTarget is QueueTarget: Kind is "current-session", "project" (Cwd, optional Alias) or "global".
type queueTarget struct{ Kind, Cwd, Alias string }

// queueSource is QueueSource; an empty SessionID is the original's undefined.
type queueSource struct{ Cwd, SessionID string }

// queueContext is QueueContext (same shape as QueueSource).
type queueContext = queueSource

// queueItem is PowerlineQueueItem. An empty Error is the original's undefined.
type queueItem struct {
	ID, Text             string
	CreatedAt, UpdatedAt float64
	Source               queueSource
	Target               queueTarget
	Intent               queueIntent
	Status               queueStatus
	Error                string
}

// createQueueItemInput is CreateQueueItemInput; a nil Now is Date.now() and an empty Status is "queued".
type createQueueItemInput struct {
	Text   string
	Source queueSource
	Target queueTarget
	Intent queueIntent
	Status queueStatus
	Now    *float64
}

// queueUpdates is Partial<Omit<PowerlineQueueItem, "id" | "createdAt">> for the fields the extension updates. An empty Status
// is unchanged; SetError writes Error (an empty one removes it, as `error: undefined` does); a nil UpdatedAt is Date.now().
type queueUpdates struct {
	Status    queueStatus
	SetError  bool
	Error     string
	Target    *queueTarget
	Text      *string
	UpdatedAt *float64
}

const (
	queueStoreDir     = "powerline-footer"
	queueInboxFile    = "inbox.jsonl"
	queueAliasesFile  = "projects.json"
	queueLockRetry    = 25 * time.Millisecond
	queueLockTimeout  = 2 * time.Second
	queueSentRetained = 24 * 60 * 60 * 1000
)

var (
	// The file access of the store goes through these two seams so a test can prove that a code path never touches the inbox
	// (the upstream test does it with a mock of fs.readFileSync and fs.statSync).
	queueReadFile = os.ReadFile
	queueStatFile = os.Stat

	aliasPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
)

// normalizeQueueCwd is path.resolve.
func normalizeQueueCwd(cwd string) string {
	if abs, err := filepath.Abs(cwd); err == nil {
		return abs
	}
	return filepath.Clean(cwd)
}

func normalizeOptionalString(v any) string {
	if s, ok := v.(string); ok {
		return jsTrim(s)
	}
	return ""
}

func normalizeTarget(v any) (queueTarget, bool) {
	o := asObject(v)
	if o == nil {
		return queueTarget{}, false
	}
	switch o.vals["kind"] {
	case targetCurrentSession:
		return queueTarget{Kind: targetCurrentSession}, true
	case targetGlobal:
		return queueTarget{Kind: targetGlobal}, true
	case targetProject:
		if cwd, ok := o.vals["cwd"].(string); ok && jsTrim(cwd) != "" {
			return queueTarget{Kind: targetProject, Cwd: normalizeQueueCwd(cwd), Alias: normalizeOptionalString(o.vals["alias"])}, true
		}
	}
	return queueTarget{}, false
}

func normalizeItem(v any) (queueItem, bool) {
	o := asObject(v)
	if o == nil {
		return queueItem{}, false
	}
	id, ok := o.vals["id"].(string)
	if !ok || jsTrim(id) == "" {
		return queueItem{}, false
	}
	text, ok := o.vals["text"].(string)
	if !ok || jsTrim(text) == "" {
		return queueItem{}, false
	}
	createdAt, ok := o.vals["createdAt"].(float64)
	if !ok {
		return queueItem{}, false
	}
	updatedAt, ok := o.vals["updatedAt"].(float64)
	if !ok {
		return queueItem{}, false
	}
	source := asObject(o.vals["source"])
	if source == nil {
		return queueItem{}, false
	}
	cwd, ok := source.vals["cwd"].(string)
	if !ok || jsTrim(cwd) == "" {
		return queueItem{}, false
	}
	target, ok := normalizeTarget(o.vals["target"])
	if !ok {
		return queueItem{}, false
	}
	intent := queueIntent("")
	if s, _ := o.vals["intent"].(string); s == string(intentSteer) || s == string(intentFollowUp) || s == string(intentPostCompact) {
		intent = queueIntent(s)
	}
	status := queueStatus("")
	switch s, _ := o.vals["status"].(string); queueStatus(s) {
	case statusQueued, statusBlocked, statusDelivering, statusSent, statusFailed:
		status = queueStatus(s)
	}
	if intent == "" || status == "" {
		return queueItem{}, false
	}
	return queueItem{
		ID: jsTrim(id), Text: text, CreatedAt: createdAt, UpdatedAt: updatedAt,
		Source: queueSource{Cwd: normalizeQueueCwd(cwd), SessionID: normalizeOptionalString(source.vals["sessionId"])},
		Target: target, Intent: intent, Status: status, Error: normalizeOptionalString(o.vals["error"]),
	}, true
}

// toJSON is the object JSON.stringify writes for an item: keys in the original's order, undefined fields left out.
func (it queueItem) toJSON() *jsObject {
	source := newObject()
	source.set("cwd", it.Source.Cwd)
	if it.Source.SessionID != "" {
		source.set("sessionId", it.Source.SessionID)
	}
	target := newObject()
	target.set("kind", it.Target.Kind)
	if it.Target.Kind == targetProject {
		target.set("cwd", it.Target.Cwd)
		if it.Target.Alias != "" {
			target.set("alias", it.Target.Alias)
		}
	}
	o := newObject()
	o.set("id", it.ID)
	o.set("text", it.Text)
	o.set("createdAt", it.CreatedAt)
	o.set("updatedAt", it.UpdatedAt)
	o.set("source", source)
	o.set("target", target)
	o.set("intent", string(it.Intent))
	o.set("status", string(it.Status))
	if it.Error != "" {
		o.set("error", it.Error)
	}
	return o
}

func nowMs() float64 { return float64(clock()) }

// createQueueItem builds an item with a random 8 character id (randomUUID().slice(0, 8)).
func createQueueItem(in createQueueItemInput) (queueItem, error) {
	now := nowMs()
	if in.Now != nil {
		now = *in.Now
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return queueItem{}, err
	}
	status := in.Status
	if status == "" {
		status = statusQueued
	}
	target := in.Target
	if normalized, ok := normalizeTarget(queueTargetObject(in.Target)); ok {
		target = normalized
	}
	return queueItem{
		ID: hex.EncodeToString(b[:]), Text: in.Text, CreatedAt: now, UpdatedAt: now,
		Source: queueSource{Cwd: normalizeQueueCwd(in.Source.Cwd), SessionID: jsTrim(in.Source.SessionID)},
		Target: target, Intent: in.Intent, Status: status,
	}, nil
}

func queueTargetObject(t queueTarget) *jsObject {
	o := newObject()
	o.set("kind", t.Kind)
	if t.Kind == targetProject {
		o.set("cwd", t.Cwd)
		if t.Alias != "" {
			o.set("alias", t.Alias)
		}
	}
	return o
}

// queueStore is PowerlineQueueStore. It is safe for concurrent use inside one process; across processes the lock directory
// serialises writers exactly as the original does.
type queueStore struct {
	inboxPath, aliasesPath string

	mu       sync.Mutex
	snapshot *displaySnapshot
}

type displaySnapshot struct {
	fingerprint string
	items       []queueItem
}

func newQueueStore(inboxPath, aliasesPath string) *queueStore {
	return &queueStore{inboxPath: inboxPath, aliasesPath: aliasesPath}
}

// defaultQueueStore is `new PowerlineQueueStore()`: the inbox and the aliases under the agent directory.
func defaultQueueStore() *queueStore {
	return newQueueStore(getAgentPath(queueStoreDir, queueInboxFile), getAgentPath(queueStoreDir, queueAliasesFile))
}

func isNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// list reads the inbox, oldest first; a malformed line is ignored.
func (s *queueStore) list() ([]queueItem, error) {
	data, err := queueReadFile(s.inboxPath)
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var items []queueItem
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := jsTrim(line)
		if trimmed == "" {
			continue
		}
		v, err := parseJSON([]byte(trimmed))
		if err != nil {
			continue
		}
		if item, ok := normalizeItem(v); ok {
			items = append(items, item)
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].CreatedAt < items[j].CreatedAt })
	return items, nil
}

func (s *queueStore) add(in createQueueItemInput) (queueItem, error) {
	item, err := createQueueItem(in)
	if err != nil {
		return queueItem{}, err
	}
	err = s.withStoreLock(func() error {
		items, err := s.list()
		if err != nil {
			return err
		}
		return s.writeItems(append(items, item))
	})
	return item, err
}

// get finds the one item whose id is, or starts with, idPrefix.
func (s *queueStore) get(idPrefix string) (*queueItem, error) {
	normalized := jsTrim(idPrefix)
	if normalized == "" {
		return nil, nil
	}
	items, err := s.list()
	if err != nil {
		return nil, err
	}
	var matches []queueItem
	for _, item := range items {
		if item.ID == normalized || strings.HasPrefix(item.ID, normalized) {
			matches = append(matches, item)
		}
	}
	if len(matches) != 1 {
		return nil, nil
	}
	return &matches[0], nil
}

// update applies updates to the item with this id under the store lock; it returns nil when there is no such item.
func (s *queueStore) update(id string, u queueUpdates) (*queueItem, error) {
	var updated *queueItem
	err := s.withStoreLock(func() error {
		items, err := s.list()
		if err != nil {
			return err
		}
		for i := range items {
			if items[i].ID != id {
				continue
			}
			if u.Status != "" {
				items[i].Status = u.Status
			}
			if u.SetError {
				items[i].Error = u.Error
			}
			if u.Target != nil {
				items[i].Target = *u.Target
			}
			if u.Text != nil {
				items[i].Text = *u.Text
			}
			if u.UpdatedAt != nil {
				items[i].UpdatedAt = *u.UpdatedAt
			} else {
				items[i].UpdatedAt = nowMs()
			}
			item := items[i]
			updated = &item
		}
		if updated == nil {
			return nil
		}
		return s.writeItems(items)
	})
	return updated, err
}

// clear marks an item sent and removes its error.
func (s *queueStore) clear(id string) (*queueItem, error) {
	return s.update(id, queueUpdates{Status: statusSent, SetError: true})
}

func (s *queueStore) activeItems(context queueContext) ([]queueItem, error) {
	items, err := s.list()
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(items, func(item queueItem) bool { return !isActiveForContext(item, context) }), nil
}

// queuedDeliveryItems lists the queued items of the context; an empty intent matches every intent.
func (s *queueStore) queuedDeliveryItems(context queueContext, intent queueIntent) ([]queueItem, error) {
	items, err := s.activeItems(context)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(items, func(item queueItem) bool {
		return item.Status != statusQueued || (intent != "" && item.Intent != intent)
	}), nil
}

// summarize is the queue summary the status bar shows. It reads the inbox through a snapshot that a changed file invalidates.
func (s *queueStore) summarize(context queueContext, compacting bool) (queueSummary, error) {
	all, err := s.displayItems()
	if err != nil {
		return queueSummary{}, err
	}
	// One pass, no copies: the first blocked item leads, else the first active one.
	cwd := normalizeQueueCwd(context.Cwd)
	summary := queueSummary{Compacting: compacting}
	var firstActive, firstBlocked *queueItem
	for i := range all {
		item := &all[i]
		if !isActive(item, context.SessionID, cwd) {
			continue
		}
		summary.QueueCount++
		if firstActive == nil {
			firstActive = item
		}
		if item.Status == statusBlocked || item.Status == statusFailed {
			summary.BlockedCount++
			if firstBlocked == nil {
				firstBlocked = item
			}
		}
	}
	leading := firstActive
	if firstBlocked != nil {
		leading = firstBlocked
	}
	if leading != nil {
		summary.LeadingText, summary.LeadingIntent, summary.LeadingStatus = leading.Text, leading.Intent, leading.Status
	}
	return summary, nil
}

// displayItems is the snapshot only summaries use; delivery and locked mutations keep fresh reads. The cached items never
// escape through the public item-returning methods (summarize copies what it keeps).
func (s *queueStore) displayItems() (items []queueItem, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if err != nil {
			s.snapshot = nil
		}
	}()
	fingerprint, exists, err := s.inboxFingerprint()
	if err != nil {
		return nil, err
	}
	if !exists {
		s.snapshot = nil
		return nil, nil
	}
	// Stat alone does not detect parent-directory or ACL permission changes.
	f, err := os.Open(s.inboxPath)
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	if s.snapshot != nil && s.snapshot.fingerprint == fingerprint {
		return s.snapshot.items, nil
	}
	s.snapshot = nil
	items, err = s.list()
	if err != nil {
		return nil, err
	}
	// Do not retain a read that raced an external write or atomic replacement.
	if again, exists, err := s.inboxFingerprint(); err != nil {
		return nil, err
	} else if exists && again == fingerprint {
		s.snapshot = &displaySnapshot{fingerprint, slices.Clone(items)}
	}
	return items, nil
}

func (s *queueStore) inboxFingerprint() (fingerprint string, exists bool, err error) {
	fi, err := queueStatFile(s.inboxPath)
	if err != nil {
		if isNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return inboxIdentity(fi), true, nil
}

func genericIdentity(fi os.FileInfo) string {
	return fmt.Sprintf("%d:%d:%d", fi.Size(), fi.ModTime().UnixNano(), fi.Mode())
}

// readAliases returns the valid aliases in Object.entries order with their paths resolved.
func (s *queueStore) readAliases() (*jsObject, error) {
	aliases := newObject()
	data, err := os.ReadFile(s.aliasesPath)
	if err != nil {
		if isNotExist(err) {
			return aliases, nil
		}
		return nil, err
	}
	v, err := parseJSON(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.aliasesPath, err)
	}
	parsed := asObject(v)
	if parsed == nil {
		return aliases, nil
	}
	for _, alias := range parsed.order() {
		// `aliases["__proto__"] = "..."` sets the prototype in the original and so records nothing.
		if cwd, ok := parsed.vals[alias].(string); ok && alias != "__proto__" && aliasPattern.MatchString(alias) && jsTrim(cwd) != "" {
			aliases.set(alias, normalizeQueueCwd(cwd))
		}
	}
	return aliases, nil
}

// setAlias records alias -> cwd; the alias may only use letters, digits, dashes and underscores.
func (s *queueStore) setAlias(alias, cwd string) error {
	normalized := jsTrim(alias)
	if !aliasPattern.MatchString(normalized) {
		return errors.New("Alias must contain only letters, numbers, dashes, or underscores")
	}
	return s.withStoreLock(func() error {
		aliases, err := s.readAliases()
		if err != nil {
			return err
		}
		aliases.set(normalized, normalizeQueueCwd(cwd))
		return s.writeJSON(s.aliasesPath, marshalJSON(aliases, "  ")+"\n")
	})
}

// resolveAlias is the path of an alias, or "" and false when there is none.
func (s *queueStore) resolveAlias(alias string) (string, bool, error) {
	aliases, err := s.readAliases()
	if err != nil {
		return "", false, err
	}
	cwd, ok := aliases.vals[alias].(string)
	return cwd, ok, nil
}

// withStoreLock takes the lock directory next to the inbox, waiting up to two seconds, and never steals an existing one.
func (s *queueStore) withStoreLock(fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(s.inboxPath), 0o777); err != nil {
		return err
	}
	lockPath := s.inboxPath + ".lock"
	startedAt := time.Now()
	for {
		err := os.Mkdir(lockPath, 0o777)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		if time.Since(startedAt) >= queueLockTimeout {
			return errors.New("Timed out waiting for Powerline queue store lock")
		}
		time.Sleep(queueLockRetry)
	}
	defer os.RemoveAll(lockPath)
	return fn()
}

func (s *queueStore) writeItems(items []queueItem) error {
	s.mu.Lock()
	s.snapshot = nil
	s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.inboxPath), 0o777); err != nil {
		return err
	}
	now := nowMs()
	var lines []string
	for _, item := range items {
		if item.Status != statusSent || now-item.UpdatedAt < queueSentRetained {
			lines = append(lines, marshalJSON(item.toJSON(), ""))
		}
	}
	content := ""
	if len(lines) > 0 {
		content = strings.Join(lines, "\n") + "\n"
	}
	return writeAtomic(s.inboxPath, content)
}

func (s *queueStore) writeJSON(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return err
	}
	return writeAtomic(path, content)
}

func writeAtomic(path, content string) error {
	temp := fmt.Sprintf("%s.%d.%d.tmp", path, os.Getpid(), clock())
	if err := os.WriteFile(temp, []byte(content), 0o666); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

// currentQueueContext is the context of a working directory and, when known, a session.
func currentQueueContext(cwd, sessionID string) queueContext {
	return queueContext{Cwd: normalizeQueueCwd(cwd), SessionID: jsTrim(sessionID)}
}

// isActiveForContext: an item is shown and delivered where its target says: everywhere (global), in one project, or in the
// session that queued it (in that session's directory when no session was known).
func isActiveForContext(item queueItem, context queueContext) bool {
	return isActive(&item, context.SessionID, normalizeQueueCwd(context.Cwd))
}

// isActive is isActiveForContext with the context's directory already normalized.
func isActive(item *queueItem, sessionID, currentCwd string) bool {
	switch item.Status {
	case statusQueued, statusBlocked, statusDelivering, statusFailed:
	default:
		return false
	}
	switch item.Target.Kind {
	case targetGlobal:
		return true
	case targetProject:
		return normalizeQueueCwd(item.Target.Cwd) == currentCwd
	}
	if item.Source.SessionID != "" {
		return sessionID == item.Source.SessionID
	}
	return item.Source.Cwd == currentCwd
}

func formatQueueDeliveryText(item queueItem) string { return item.Text }

// parseCompactQueuedPrompt reads `/compact <prompt>` (the /^\/compact\s+(.+)$/ of the original, with JavaScript's \s and a `.`
// that stops at a line terminator): the prompt, or false when the text is not that.
func parseCompactQueuedPrompt(text string) (string, bool) {
	rest, ok := strings.CutPrefix(jsTrim(text), "/compact")
	if !ok {
		return "", false
	}
	content := strings.TrimLeftFunc(rest, isJSSpace)
	if len(content) == len(rest) || content == "" || strings.ContainsAny(content, "\n\r\u2028\u2029") {
		return "", false
	}
	return jsTrim(content), true
}
