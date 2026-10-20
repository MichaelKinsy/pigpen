package warden

import (
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

// Standing preferences (src/prefs.ts): the grouping of the clauses the user repeated across sessions, the rules of
// evaluatePrefs that decide what reaches the agent, and the agent's own lessons. Code only, no judge request.

const (
	// maxSessions and maxAgeDays bound the scan: short automated sessions fill a project's directory quickly, so the
	// 30-day window is the real bound.
	prefsMaxSessions = 200
	prefsMaxAgeDays  = 30
	// minSessions: listed by `/warden prefs` from two sessions; injected only from injectSessions sessions on
	// injectDays days.
	prefsMinSessions = 2
	injectSessions   = 3
	injectDays       = 2
	// currentDays: a preference not seen for this long is not current, and an agent lesson not confirmed for this long expires.
	currentDays  = 30
	maxPrefs     = 10
	maxInjected  = 5
	lessonChars  = clauseChars
	messageChars = 400
	// maxLessons: stored lessons per project; the oldest go first.
	maxLessons      = 50
	prefsSimilarity = 0.6
	// rareShare and rareOverlap: a word in fewer than rareShare of the user's typed messages is a subject; two clauses
	// that share one, with at least rareOverlap of their other content words in common, are one preference.
	rareShare   = 0.05
	rareOverlap = 0.3
	// scanBudget: reading stops after this long; the newest sessions come first.
	prefsScanBudget = 250 * time.Millisecond
	dayMs           = 86_400_000
)

const (
	prefsHint     = "Add the ones you want to keep to pi-warden.md as rules; /warden prefs forget <n> drops one for good."
	prefsLead     = "Standing preferences for this project, quoted as said in earlier sessions:"
	prefsClosing  = "If the current request says otherwise, follow the current request."
	lessonMark    = "(agent lesson)"
	wasInjected   = "injected"
	notConfirmed  = "agent lesson, not yet confirmed"
	prefsCapped   = "not injected: over the 5-item or 400-character cap"
	prefsNotice   = "pi-warden: standing preferences scan failed:"
	prefsStoreDir = "prefs"
)

// PrefCandidate is one clause (or permission) the user said in one session.
type PrefCandidate struct {
	Clause  string
	Session string
	At      int64
	// Permit: "Do use subagents here", "feel free to push": a later permission that only contradicts, never a preference.
	Permit bool
}

// PrefClass says why a clause may never reach the agent however often it is repeated: rules 3 and 6.
type PrefClass string

const (
	classStanding  PrefClass = "standing"
	classTaskBound PrefClass = "task-bound"
	classWeakens   PrefClass = "weakens"
)

// StandingPref is a group of clauses that mean one preference.
type StandingPref struct {
	// Text is the newest wording, redacted, at most clauseChars units.
	Text     string
	Sessions int
	// Days is the number of distinct calendar days (UTC) it was said on.
	Days   int
	LastAt int64
	Kind   PrefClass
	// Contradicted: a later message of the opposite polarity matches it.
	Contradicted bool
	// Members are the content words of every wording: the group's key for forget and for matching a lesson.
	Members [][]string
	Negated bool
}

// WordCounts is how often each word appears in the user's typed messages: the yardstick for a rare word.
type WordCounts struct {
	Messages int
	Words    map[string]int
}

func emptyWordCounts() *WordCounts { return &WordCounts{Words: map[string]int{}} }

func (c *WordCounts) add(words []string) {
	c.Messages++
	seen := map[string]bool{}
	for _, word := range words {
		if !seen[word] {
			seen[word] = true
			c.Words[word]++
		}
	}
}

// PrefsScan is what the scan of earlier sessions found.
type PrefsScan struct {
	// Prefs are the groups seen in minSessions or more sessions, before the rules of evaluatePrefs.
	Prefs []StandingPref
	// Candidates is every clause and permission read, for matching the agent's lessons against what the user said.
	Candidates []PrefCandidate
	Counts     *WordCounts
	// Project is the repository's main worktree, or the working directory: the key of the project's lesson file.
	Project string
	// Scanned is the number of session files read; Directories the session directories searched.
	Scanned     int
	Directories int
	Ms          float64
}

var (
	reNegation     = prefsRE("i", `^(?:don`+apostrophe+`?t|do\s+not|never|stop|no|not)\b`)
	reNegationWord = prefsRE("", `^(?:dont|not|never|stop|no)$`)
)

var stopWords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`a an the and or but so to of in on at for with by from as into
		i me my we us our you your it its this that these those there here
		is are was were be been am do does did have has had will would can
		could should please pls just also then again ok okay any some all more
		very really always ever next time now anymore yourself itself them they
		use run let make get
		stuff thing anything everything something nothing though first actually even`) {
		m[w] = true
	}
	return m
}()

// tokens: lowercased content words with a light plural strip. A clause that starts with a negation gets a `not`
// token, so "never use X" and "always use X" do not merge. The original splits on everything that is not
// [a-z0-9] once the apostrophes are gone, so a word is a run of those characters.
func tokens(clause string) []string {
	negated := reNegation.MatchString(jsTrim(clause))
	lower := jsLower(clause)
	lower = strings.NewReplacer("'", "", "’", "").Replace(lower)
	var words []string
	seen := map[string]bool{}
	start := -1
	flush := func(end int) {
		if start < 0 {
			return
		}
		word := lower[start:end]
		start = -1
		if stopWords[word] || reNegationWord.MatchString(word) {
			return
		}
		if len(word) > 3 && strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss") {
			word = word[:len(word)-1]
		}
		if !seen[word] {
			seen[word] = true
			words = append(words, word)
		}
	}
	for i := 0; i < len(lower); i++ {
		c := lower[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			if start < 0 {
				start = i
			}
			continue
		}
		flush(i)
	}
	flush(len(lower))
	if negated && len(words) > 0 {
		words = append(words, "not")
	}
	return words
}

func jaccard(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	right := map[string]bool{}
	for _, w := range b {
		right[w] = true
	}
	shared := 0
	for _, w := range a {
		if right[w] {
			shared++
		}
	}
	return float64(shared) / float64(len(a)+len(right)-shared)
}

func wordsWithout(words []string, drop string) []string {
	out := make([]string, 0, len(words))
	for _, w := range words {
		if w != drop {
			out = append(out, w)
		}
	}
	return out
}

func hasWord(words []string, word string) bool { return slices.Contains(words, word) }

// Rarity says whether a word names a subject.
type Rarity func(word string) bool

// rarity: a word in fewer than rareShare of the typed messages is a subject word. Without counts, the clauses are
// the yardstick.
func rarity(candidates []PrefCandidate, counts *WordCounts) Rarity {
	yard := counts
	if counts == nil || counts.Messages == 0 {
		yard = emptyWordCounts()
		for _, c := range candidates {
			yard.add(tokens(c.Clause))
		}
	}
	return func(word string) bool { return float64(yard.Words[word]) < rareShare*float64(yard.Messages) }
}

// samePreference: two clauses of the same polarity are one preference when their words mostly match (Jaccard at or
// above prefsSimilarity), or when they share a rare word and rareOverlap of their content words.
func samePreference(a, b []string, rare Rarity) bool {
	if jaccard(a, b) >= prefsSimilarity {
		return true
	}
	left, right := wordsWithout(a, "not"), wordsWithout(b, "not")
	shared := false
	for _, w := range left {
		if rare(w) && hasWord(right, w) {
			shared = true
			break
		}
	}
	return shared && jaccard(left, right) >= rareOverlap
}

// sameSubject: the same subject with the polarity set aside.
func sameSubject(a, b []string, rare Rarity) bool {
	return samePreference(wordsWithout(a, "not"), wordsWithout(b, "not"), rare)
}

func prefDay(at int64) string { return time.UnixMilli(at).UTC().Format("2006-01-02") }

type prefGroup struct {
	members  [][]string
	negated  bool
	kind     PrefClass
	text     string
	sessions map[string]bool
	days     map[string]bool
	lastAt   int64
}

// groupPreferences: clauses that are the same preference as any member of a group join it; opposite polarity never
// does, and neither does a clause of another class. A group seen in minSessions or more distinct sessions is listed,
// ranked by session count, then recency. counts may be nil.
func groupPreferences(candidates []PrefCandidate, counts *WordCounts, minSessions, max int) []StandingPref {
	rare := rarity(candidates, counts)
	var groups []*prefGroup
	type opposite struct {
		words   []string
		negated bool
		at      int64
	}
	var opposites []opposite
	sorted := append([]PrefCandidate(nil), candidates...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At > sorted[j].At })
	for _, c := range sorted {
		words := tokens(c.Clause)
		if len(words) == 0 {
			continue
		}
		negated := !c.Permit && hasWord(words, "not")
		opposites = append(opposites, opposite{words, negated, c.At})
		if c.Permit {
			continue
		}
		kind := classifyClause(c.Clause)
		var group *prefGroup
		for _, g := range groups {
			if g.negated != negated || g.kind != kind {
				continue
			}
			for _, member := range g.members {
				if samePreference(member, words, rare) {
					group = g
					break
				}
			}
			if group != nil {
				break
			}
		}
		if group != nil {
			group.members = append(group.members, words)
			group.sessions[c.Session] = true
			group.days[prefDay(c.At)] = true
			if c.At > group.lastAt {
				group.lastAt = c.At
			}
		} else {
			groups = append(groups, &prefGroup{members: [][]string{words}, negated: negated, kind: kind, text: c.Clause,
				sessions: map[string]bool{c.Session: true}, days: map[string]bool{prefDay(c.At): true}, lastAt: c.At})
		}
	}
	var kept []*prefGroup
	for _, g := range groups {
		if len(g.sessions) >= minSessions {
			kept = append(kept, g)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if len(kept[i].sessions) != len(kept[j].sessions) {
			return len(kept[i].sessions) > len(kept[j].sessions)
		}
		return kept[i].lastAt > kept[j].lastAt
	})
	if len(kept) > max {
		kept = kept[:max]
	}
	out := make([]StandingPref, 0, len(kept))
	for _, g := range kept {
		contradicted := false
		for _, other := range opposites {
			if other.at > g.lastAt && other.negated != g.negated {
				for _, member := range g.members {
					if sameSubject(member, other.words, rare) {
						contradicted = true
						break
					}
				}
			}
			if contradicted {
				break
			}
		}
		out = append(out, StandingPref{Text: g.text, Sessions: len(g.sessions), Days: len(g.days), LastAt: g.lastAt, Kind: g.kind,
			Contradicted: contradicted, Members: g.members, Negated: g.negated})
	}
	return out
}

func plural(count int, word string) string {
	if count == 1 {
		return itoaInt(count) + " " + word
	}
	return itoaInt(count) + " " + word + "s"
}

// PrefItem is one numbered line of `/warden prefs`.
type PrefItem struct {
	Source   string // "user" or "agent"
	Text     string
	Sessions int
	// Days is the number of distinct days, for the user's preferences.
	Days     int
	LastAt   int64
	Injected bool
	// Status is wasInjected, or the rule that kept it out.
	Status  string
	Members [][]string
	// AgentConfirmations is the number of sessions in which the agent's own lesson repeated this user preference.
	AgentConfirmations int
}

// matches: same polarity, and the same preference as one of the wordings.
func matches(members [][]string, words []string, rare Rarity) bool {
	negated := hasWord(words, "not")
	for _, member := range members {
		if hasWord(member, "not") == negated && samePreference(member, words, rare) {
			return true
		}
	}
	return false
}

func isForgotten(store PrefsStore, members [][]string, rare Rarity) bool {
	for _, entry := range store.Forgotten {
		for _, words := range members {
			if matches(entry.Members, words, rare) {
				return true
			}
		}
	}
	return false
}

func lessonLastAt(l Lesson) int64 {
	last := int64(math.MinInt64)
	for _, r := range l.Records {
		if r.At > last {
			last = r.At
		}
	}
	return last
}

func lessonSessions(l Lesson) int {
	seen := map[string]bool{}
	for _, r := range l.Records {
		seen[r.Session] = true
	}
	return len(seen)
}

func messageLine(item PrefItem) string {
	line := "\n- \"" + item.Text + "\" (" + plural(item.Sessions, "session") + ")"
	if item.Source == "agent" {
		line += " " + lessonMark
	}
	return line
}

// evaluatePrefs applies the injection rules to the listed groups and the agent's lessons (prefs.ts evaluatePrefs).
// A user preference is injected only when it is a standing, not task-bound instruction that keeps every check
// (rules 2, 3, 6), was said in injectSessions or more sessions on injectDays or more days (rule 4), was last said
// within currentDays days and no later message lifts it (rule 5). An agent lesson must also be confirmed. User
// preferences fill the maxInjected and messageChars budget first (rule 7). Forgotten groups are not returned.
func evaluatePrefs(scan *PrefsScan, store PrefsStore, now int64) []PrefItem {
	rare := rarity(scan.Candidates, scan.Counts)
	current := now - currentDays*dayMs
	var items []PrefItem
	for _, pref := range scan.Prefs {
		if isForgotten(store, pref.Members, rare) {
			continue
		}
		var status string
		switch {
		case pref.Kind == classWeakens:
			status = "not injected: weakens a check"
		case pref.Kind == classTaskBound:
			status = "not injected: task-bound"
		case pref.Contradicted:
			status = "not injected: contradicted by a later message"
		case pref.Sessions < injectSessions:
			status = "not injected: seen in " + plural(pref.Sessions, "session")
		case pref.Days < injectDays:
			status = "not injected: seen on " + plural(pref.Days, "day")
		case pref.LastAt < current:
			status = "not injected: last seen " + prefDay(pref.LastAt) + ", over " + itoaInt(currentDays) + " days ago"
		default:
			status = wasInjected
		}
		confirmedBy := map[string]bool{}
		for _, entry := range store.Confirmations {
			hit := false
			for _, member := range pref.Members {
				if matches([][]string{tokens(entry.Text)}, member, rare) {
					hit = true
					break
				}
			}
			if hit {
				for _, r := range entry.Records {
					confirmedBy[r.Session] = true
				}
			}
		}
		items = append(items, PrefItem{Source: "user", Text: pref.Text, Sessions: pref.Sessions, Days: pref.Days, LastAt: pref.LastAt,
			Injected: status == wasInjected, Status: status, Members: pref.Members, AgentConfirmations: len(confirmedBy)})
	}
	type said struct {
		clause string
		words  []string
	}
	var spoken []said
	type lift struct {
		words   []string
		negated bool
		at      int64
	}
	var lifted []lift
	for _, c := range scan.Candidates {
		if !c.Permit {
			spoken = append(spoken, said{c.Clause, tokens(c.Clause)})
		}
		lifted = append(lifted, lift{tokens(c.Clause), !c.Permit && reNegation.MatchString(c.Clause), c.At})
	}
	for _, lesson := range store.Lessons {
		words := tokens(lesson.Text)
		if len(words) == 0 || isForgotten(store, [][]string{words}, rare) {
			continue
		}
		last := lessonLastAt(lesson)
		sessions := lessonSessions(lesson)
		negated := hasWord(words, "not")
		kind := classifyClause(lesson.Text)
		var duplicate *PrefItem
		for i := range items {
			if items[i].Source == "user" && items[i].Injected && matches(items[i].Members, words, rare) {
				duplicate = &items[i]
				break
			}
		}
		confirmed := sessions >= 2
		if !confirmed {
			for _, s := range spoken {
				if classifyClause(s.clause) == classStanding && matches([][]string{s.words}, words, rare) {
					confirmed = true
					break
				}
			}
		}
		liftedLater := false
		for _, other := range lifted {
			if other.at > last && other.negated != negated && sameSubject(words, other.words, rare) {
				liftedLater = true
				break
			}
		}
		var status string
		switch {
		case last < current:
			status = "not injected: agent lesson expired, not confirmed in " + itoaInt(currentDays) + " days"
		case kind == classWeakens:
			status = "not injected: weakens a check"
		case kind == classTaskBound:
			status = "not injected: task-bound"
		case liftedLater:
			status = "not injected: contradicted by a later message"
		case duplicate != nil:
			status = "not injected: same as the user's preference \"" + duplicate.Text + "\""
		case !confirmed:
			status = notConfirmed
		default:
			status = wasInjected
		}
		items = append(items, PrefItem{Source: "agent", Text: lesson.Text, Sessions: sessions, LastAt: last, Injected: status == wasInjected, Status: status, Members: [][]string{words}})
	}
	length := utf16Len(prefsLead) + 1 + utf16Len(prefsClosing)
	injected := 0
	for i := range items {
		item := &items[i]
		if !item.Injected {
			continue
		}
		line := utf16Len(messageLine(*item))
		if injected < maxInjected && length+line <= messageChars {
			length += line
			injected++
		} else {
			item.Injected = false
			item.Status = prefsCapped
		}
	}
	return items
}

// formatPrefs is the `/warden prefs` output: every listed group and lesson, numbered for forget, with its status.
func formatPrefs(scanned, directories int, items []PrefItem) string {
	if len(items) == 0 {
		return "No standing preferences: nothing was repeated in " + itoaInt(prefsMinSessions) + " or more of the last " + plural(scanned, "session") + " of this project, and the agent recorded no lesson."
	}
	worktrees := ""
	if directories > 1 {
		worktrees = " and its worktrees"
	}
	lines := []string{"Standing preferences (repeated in " + itoaInt(prefsMinSessions) + "+ of the last " + plural(scanned, "session") + " of this project" + worktrees +
		"; injected from " + itoaInt(injectSessions) + " sessions on " + itoaInt(injectDays) + " days):"}
	for index, item := range items {
		var seen string
		if item.Source == "user" {
			days := item.Days
			if days == 0 {
				days = 1
			}
			seen = plural(item.Sessions, "session") + " on " + plural(days, "day") + ", last " + prefDay(item.LastAt)
			if item.AgentConfirmations > 0 {
				seen += "; the agent noted it in " + plural(item.AgentConfirmations, "session")
			}
		} else {
			seen = "agent lesson, recorded in " + plural(item.Sessions, "session") + ", last " + prefDay(item.LastAt)
		}
		lines = append(lines, itoaInt(index+1)+". "+item.Text+" ("+seen+"): "+item.Status)
	}
	lines = append(lines, prefsHint)
	return strings.Join(lines, "\n")
}

// prefsMessage is the session-start context message: the injected items quoted as said, each with its session
// count, and the closing sentence. "" when nothing is injected.
func prefsMessage(items []PrefItem) string {
	var lines []string
	for _, item := range items {
		if item.Injected {
			lines = append(lines, messageLine(item))
			if len(lines) == maxInjected {
				break
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return prefsLead + strings.Join(lines, "") + "\n" + prefsClosing
}

// forgetPref drops the item for good: its wordings join the forgotten list, and an agent lesson leaves the file.
func forgetPref(store PrefsStore, item PrefItem, now int64) PrefsStore {
	out := store
	out.Forgotten = append(append([]Forgotten(nil), store.Forgotten...), Forgotten{Text: item.Text, Members: item.Members, At: now})
	if item.Source == "agent" {
		out.Lessons = nil
		for _, l := range store.Lessons {
			if l.Text != item.Text {
				out.Lessons = append(out.Lessons, l)
			}
		}
	}
	return out
}

// LessonInput is one warden_remember call.
type LessonInput struct {
	Lesson  string
	Session string
	Now     int64
	Scan    *PrefsScan
	Store   PrefsStore
}

// recordLesson is the warden_remember gate. A lesson is recorded only in a standing form, not task-bound, keeping
// every check, and within lessonChars. One that repeats a stored lesson confirms it; one that repeats a listed user
// preference is counted for that preference. The store is nil when nothing changes.
func recordLesson(in LessonInput) (string, *PrefsStore) {
	raw := jsTrim(in.Lesson)
	if utf16Len(raw) > lessonChars {
		return "not recorded: longer than " + itoaInt(lessonChars) + " characters", nil
	}
	clauses := extractPreferences(raw)
	if len(clauses) != 1 {
		return "not recorded: not one standing instruction; state it once, with don't, never, always, stop, from now on, or next time, and no temporary words such as yet, now, until, or this PR", nil
	}
	text := clauses[0]
	switch classifyClause(text) {
	case classWeakens:
		return "not recorded: weakens a check", nil
	case classTaskBound:
		return "not recorded: task-bound; name the standing rule, not a pronoun, ticket, branch, PR number, or hash", nil
	}
	words := tokens(text)
	rare := rarity(in.Scan.Candidates, in.Scan.Counts)
	if isForgotten(in.Store, [][]string{words}, rare) {
		return "not recorded: the user forgot this preference", nil
	}
	record := LessonRecord{Session: in.Session, At: in.Now}
	// confirm adds the record to entries[index] unless that session has one already.
	confirm := func(entries []Lesson, index int) []Lesson {
		out := make([]Lesson, len(entries))
		copy(out, entries)
		old := out[index]
		for _, r := range old.Records {
			if r.Session == record.Session {
				return out
			}
		}
		out[index] = Lesson{Text: old.Text, Records: append(append([]LessonRecord(nil), old.Records...), record)}
		return out
	}
	for i, entry := range in.Store.Lessons {
		if !matches([][]string{tokens(entry.Text)}, words, rare) {
			continue
		}
		lessons := confirm(in.Store.Lessons, i)
		var sessions int
		for _, l := range lessons {
			if l.Text == entry.Text {
				sessions = lessonSessions(l)
				break
			}
		}
		store := pruned(PrefsStore{Lessons: lessons, Confirmations: in.Store.Confirmations, Forgotten: in.Store.Forgotten}, in.Now)
		return "recorded as a confirmation of the agent lesson \"" + entry.Text + "\" (" + plural(sessions, "session") + ")", &store
	}
	for _, group := range in.Scan.Prefs {
		if !matches(group.Members, words, rare) {
			continue
		}
		confirmations := in.Store.Confirmations
		found := -1
		for i, entry := range confirmations {
			if entry.Text == group.Text {
				found = i
				break
			}
		}
		if found >= 0 {
			confirmations = confirm(confirmations, found)
		} else {
			confirmations = append(append([]Lesson(nil), confirmations...), Lesson{Text: group.Text, Records: []LessonRecord{record}})
		}
		store := pruned(PrefsStore{Lessons: in.Store.Lessons, Confirmations: confirmations, Forgotten: in.Store.Forgotten}, in.Now)
		return "recorded as a confirmation of the user's preference \"" + group.Text + "\"", &store
	}
	lessons := append(append([]Lesson(nil), in.Store.Lessons...), Lesson{Text: text, Records: []LessonRecord{record}})
	store := pruned(PrefsStore{Lessons: lessons, Confirmations: in.Store.Confirmations, Forgotten: in.Store.Forgotten}, in.Now)
	return "recorded: \"" + text + "\". It reaches later sessions only after it is confirmed: recorded again in a later session, or said by the user.", &store
}

// pruned drops lessons and confirmations with no record in the last currentDays days when the file is written.
func pruned(store PrefsStore, now int64) PrefsStore {
	keep := func(entries []Lesson) []Lesson {
		var out []Lesson
		for _, l := range entries {
			if lessonLastAt(l) >= now-currentDays*dayMs {
				out = append(out, l)
			}
		}
		if len(out) > maxLessons {
			out = out[len(out)-maxLessons:]
		}
		return out
	}
	store.Lessons, store.Confirmations = keep(store.Lessons), keep(store.Confirmations)
	return store
}

// Classification (rules 3 and 6).

var (
	// PRONOUN_OBJECT: a pronoun as the whole object points at something in the task at hand. Its `(?!let\s)` lookahead
	// becomes a word class that is any word but "let"; the demonstrative's `(?=…)` lookahead is consumed, which only
	// changes where the match ends, and this pattern is only tested.
	rePronounObject = prefsRE("i", `^(?:(?:don`+apostrophe+`?t|do\s+not|never|stop|always)\s+)?(?:[\w-]+\s+(?:or|and)\s+)?`+
		`(?:[\w-]{1,2}|[\w-]{4,}|[A-KM-Za-km-z0-9_-][\w-]{2}|[lL][A-DF-Za-df-z0-9_-][\w-]|[lL][eE][A-SU-Za-su-z0-9_-])\s+`+
		`(?:it\b|them\b|(?:this|that|these|those)\b(?:\s*$|[,.;!]|\s+(?:again|anymore|ever|please|yourself|here|back|to|in|on|into|for|with|or|and)\b))`)
	// SPECIFIC_REFERENCE without its commit-hash alternative, which needs two lookaheads (see hasCommitHash).
	reSpecificReference = prefsRE("", strings.Join([]string{
		`\b[A-Z][A-Z0-9]{1,9}-\d+\b`,
		`(?:^|\s)#\d+\b`,
		`\b(?:PR|MR|pull\s+request|issue|ticket)\s*#?\d+\b`,
		`\bbranch\s+["'\x60]?[\w.-]+\/[\w./-]+`,
		`\b(?:feat|feature|fix|bugfix|hotfix|chore|release)\/[\w./-]+`,
	}, "|"))
)

// hasCommitHash is `\b(?=[0-9a-f]*\d)(?=[0-9a-f]*[a-f])[0-9a-f]{7,40}\b`: a whole word of 7 to 40 lowercase hex
// characters with at least one digit and one letter a-f.
func hasCommitHash(text string) bool {
	for start := 0; start < len(text); {
		if !wordAt(text, start) {
			start++
			continue
		}
		end := start
		for wordAt(text, end) {
			end++
		}
		word := text[start:end]
		start = end
		if len(word) < 7 || len(word) > 40 {
			continue
		}
		digit, letter, hex := false, false, true
		for i := 0; i < len(word); i++ {
			switch c := word[i]; {
			case c >= '0' && c <= '9':
				digit = true
			case c >= 'a' && c <= 'f':
				letter = true
			default:
				hex = false
			}
		}
		if hex && digit && letter {
			return true
		}
	}
	return false
}

// weakensChecks are the markers of a preference that weakens a safeguard (WEAKENS_CHECK): skipping tests, checks,
// reviews, or confirmations, turning warden off, or pushing, deploying, or deleting without asking. `asked`
// weakens when the user asks for it ("skip the tests"); `refused` weakens when the user forbids the verification
// itself ("don't run the tests", "never ask before pushing"). The third `refused` marker's lookahead is consumed:
// the pattern is only tested.
var weakensChecks = struct{ asked, refused []*lazyRegexp }{
	asked: []*lazyRegexp{
		prefsRE("i", `\b(?:skip\w*|bypass\w*|disabl\w*|ignor\w*|omit\w*|turn(?:ing)?\s+off|silenc\w*|mut(?:e|ing))\b.*\b(?:tests?|checks?|lint\w*|reviews?|verif\w*|confirm\w*|hooks?|ci|typecheck\w*|warden|guards?)\b`),
		prefsRE("i", `--no-verify\b`),
		prefsRE("i", `\bwithout\s+(?:asking|confirm\w*|approval|permission|(?:a\s+)?review\w*|testing|tests?|checks?|checking|verif\w*)\b`),
	},
	refused: []*lazyRegexp{
		prefsRE("i", `^(?:don`+apostrophe+`?t|do\s+not|never|stop)\s+(?:(?:ever|even|bother(?:ing)?|need|have|to)\s+){0,2}(?:run\w*|writ\w*|add\w*|wait\w*(?:\s+for)?|do\w*|us(?:e|ing)|call\w*|request\w*)\s+(?:(?:the|any|a)\s+)?(?:[\w-]+\s+)?(?:tests?|checks?|lint\w*|reviews?|verif\w*|typecheck\w*|ci|warden)\b`),
		prefsRE("i", `^(?:don`+apostrophe+`?t|do\s+not|never|stop)\s+(?:(?:ever|even|bother(?:ing)?|need|have|to)\s+){0,2}(?:ask\w*|confirm\w*)\b`),
		prefsRE("i", `^(?:don`+apostrophe+`?t|do\s+not|never|stop)\s+(?:(?:ever|even|bother(?:ing)?|need|have|to)\s+){0,2}(?:check|verify|review|test)(?:s|ed|es|ied|ing)?(?:\s*$|\s+(?:before|after|first|anything|again|it|them|that|this|my|your|each|every|(?:the\s+)?(?:code|build|changes?|output|results?|diffs?|work|pr|pull\s+requests?|commits?))\b)`),
	},
}

// classifyClause is rules 3 and 6: a clause that names one task, or that weakens a check, is never a standing
// preference to inject.
func classifyClause(clause string) PrefClass {
	text := jsTrim(clause)
	markers := weakensChecks.asked
	if reNegation.MatchString(text) {
		markers = weakensChecks.refused
	}
	for _, m := range markers {
		if m.MatchString(text) {
			return classWeakens
		}
	}
	if rePronounObject.MatchString(text) || reSpecificReference.MatchString(text) || hasCommitHash(text) {
		return classTaskBound
	}
	return classStanding
}
