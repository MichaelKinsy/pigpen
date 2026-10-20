package powerline_footer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The store beyond the upstream cases: the file format, validation of what is read, retention, ids and aliases.

func (s tstore) inboxText() string {
	b, _ := os.ReadFile(s.inboxPath)
	return string(b)
}

func TestQueueStoreFileFormat(t *testing.T) {
	withStore(t, func(store tstore, dir string) {
		item := store.add(createQueueItemInput{Text: "write me", Source: queueSource{Cwd: dir, SessionID: " s1 "}, Target: queueTarget{Kind: targetProject, Cwd: dir, Alias: "p"}, Intent: intentSteer, Now: new(5.0)})
		eq(t, len(item.ID), 8, "an 8 character id")
		for _, c := range item.ID {
			if !strings.ContainsRune("0123456789abcdef", c) {
				t.Errorf("id %q is not lower case hex", item.ID)
			}
		}
		want := `{"id":"` + item.ID + `","text":"write me","createdAt":5,"updatedAt":5,"source":{"cwd":"` + dir + `","sessionId":"s1"},"target":{"kind":"project","cwd":"` + dir + `","alias":"p"},"intent":"steer","status":"queued"}` + "\n"
		eq(t, store.inboxText(), want, "one compact JSON line per item, keys in the original's order")
		store.update(item.ID, queueUpdates{Status: statusFailed, SetError: true, Error: "boom"})
		eq(t, strings.HasSuffix(strings.TrimSpace(store.inboxText()), `"status":"failed","error":"boom"}`), true, "error is the last key")
		store.update(item.ID, queueUpdates{SetError: true})
		eq(t, strings.Contains(store.inboxText(), "error"), false, "an empty error is left out")
		ids := map[string]bool{}
		for i := 0; i < 50; i++ {
			ids[store.add(createQueueItemInput{Text: "x", Source: queueSource{Cwd: dir}, Target: queueTarget{Kind: targetGlobal}, Intent: intentFollowUp}).ID] = true
		}
		eq(t, len(ids), 50, "ids differ")
	})
}

func TestQueueStoreIgnoresWhatItCannotRead(t *testing.T) {
	withStore(t, func(store tstore, dir string) {
		good := `{"id":"good0001","text":"ok","createdAt":2,"updatedAt":2,"source":{"cwd":"/x"},"target":{"kind":"global"},"intent":"steer","status":"queued"}`
		older := `{"id":" older01 ","text":"older","createdAt":1,"updatedAt":1,"source":{"cwd":"/x","sessionId":"  "},"target":{"kind":"project","cwd":"/p","alias":"  a  "},"intent":"follow-up","status":"blocked","error":" why "}`
		bad := []string{
			"not json", `[]`, `{}`,
			`{"id":"","text":"t","createdAt":1,"updatedAt":1,"source":{"cwd":"/x"},"target":{"kind":"global"},"intent":"steer","status":"queued"}`,
			`{"id":"i","text":"  ","createdAt":1,"updatedAt":1,"source":{"cwd":"/x"},"target":{"kind":"global"},"intent":"steer","status":"queued"}`,
			`{"id":"i","text":"t","createdAt":"1","updatedAt":1,"source":{"cwd":"/x"},"target":{"kind":"global"},"intent":"steer","status":"queued"}`,
			`{"id":"i","text":"t","createdAt":1,"updatedAt":null,"source":{"cwd":"/x"},"target":{"kind":"global"},"intent":"steer","status":"queued"}`,
			`{"id":"i","text":"t","createdAt":1,"updatedAt":1,"source":{"cwd":" "},"target":{"kind":"global"},"intent":"steer","status":"queued"}`,
			`{"id":"i","text":"t","createdAt":1,"updatedAt":1,"source":"x","target":{"kind":"global"},"intent":"steer","status":"queued"}`,
			`{"id":"i","text":"t","createdAt":1,"updatedAt":1,"source":{"cwd":"/x"},"target":{"kind":"nowhere"},"intent":"steer","status":"queued"}`,
			`{"id":"i","text":"t","createdAt":1,"updatedAt":1,"source":{"cwd":"/x"},"target":{"kind":"project"},"intent":"steer","status":"queued"}`,
			`{"id":"i","text":"t","createdAt":1,"updatedAt":1,"source":{"cwd":"/x"},"target":{"kind":"global"},"intent":"later","status":"queued"}`,
			`{"id":"i","text":"t","createdAt":1,"updatedAt":1,"source":{"cwd":"/x"},"target":{"kind":"global"},"intent":"steer","status":"waiting"}`,
		}
		content := good + "\n\n  \n" + strings.Join(bad, "\n") + "\n" + older + "\n"
		if err := os.WriteFile(store.inboxPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		items := store.list()
		eq(t, len(items), 2, "only the two valid lines")
		eq(t, items[0], queueItem{ID: "older01", Text: "older", CreatedAt: 1, UpdatedAt: 1, Source: queueSource{Cwd: "/x"}, Target: queueTarget{Kind: targetProject, Cwd: "/p", Alias: "a"},
			Intent: intentFollowUp, Status: statusBlocked, Error: "why"}, "the older item first, trimmed")
		eq(t, items[1].ID, "good0001", "then the newer")
		// A write keeps the valid items and drops the rest.
		store.add(createQueueItemInput{Text: "new", Source: queueSource{Cwd: "/x"}, Target: queueTarget{Kind: targetGlobal}, Intent: intentFollowUp})
		eq(t, len(strings.Split(strings.TrimSpace(store.inboxText()), "\n")), 3, "three lines")
		eq(t, store.get("   ") == nil, true, "a blank prefix matches nothing")
	})
}

func TestQueueStoreKeepsSentItemsForADay(t *testing.T) {
	old := clock
	defer func() { clock = old }()
	now := int64(10_000_000_000)
	clock = func() int64 { return now }
	withStore(t, func(store tstore, dir string) {
		in := func(text string) createQueueItemInput {
			return createQueueItemInput{Text: text, Source: queueSource{Cwd: dir}, Target: queueTarget{Kind: targetGlobal}, Intent: intentFollowUp}
		}
		a, b, c := store.add(in("a")), store.add(in("b")), store.add(in("c"))
		store.clear(a.ID)
		now += 24*60*60*1000 - 1
		store.clear(b.ID)
		store.update(c.ID, queueUpdates{Status: statusBlocked})
		eq(t, len(store.list()), 3, "just under a day: all kept")
		now += 1
		store.update(c.ID, queueUpdates{Text: new("c again")})
		ids := []string{}
		for _, it := range store.list() {
			ids = append(ids, it.Text)
		}
		eq(t, ids, []string{"b", "c again"}, "a sent item older than a day is dropped at the next write, a sent younger one and an unsent one stay")
	})
}

func TestQueueStoreAliases(t *testing.T) {
	withStore(t, func(store tstore, dir string) {
		for _, bad := range []string{"", "  ", "a b", "a.b", "a/b", "bad!", "é"} {
			err := store.setAlias(bad, "/x")
			if err == nil || err.Error() != "Alias must contain only letters, numbers, dashes, or underscores" {
				t.Errorf("setAlias(%q) = %v", bad, err)
			}
		}
		if err := store.setAlias("  ok_Name-1 ", "/x/../y"); err != nil {
			t.Fatal(err)
		}
		cwd, ok, _ := store.resolveAlias("ok_Name-1")
		eq(t, []any{cwd, ok}, []any{"/y", true}, "trimmed name, resolved path")
		_, ok, _ = store.resolveAlias("missing")
		eq(t, ok, false, "unknown")
		// A hand-edited file: invalid names, non-strings and blank paths are ignored, order is JavaScript's.
		os.WriteFile(store.aliasesPath, []byte(`{"b":"/b","10":"/ten","2":"/two","bad name":"/x","c":5,"d":"  ","__proto__":"/p","e":"/e"}`), 0o644)
		aliases, err := store.readAliases()
		if err != nil {
			t.Fatal(err)
		}
		eq(t, aliases.order(), []string{"2", "10", "b", "e"}, "valid aliases in Object.keys order")
		if err := store.setAlias("a", "/a"); err != nil {
			t.Fatal(err)
		}
		eq(t, string(readOf(t, store.aliasesPath)), "{\n  \"2\": \"/two\",\n  \"10\": \"/ten\",\n  \"b\": \"/b\",\n  \"e\": \"/e\",\n  \"a\": \"/a\"\n}\n", "rewritten with the valid ones")
		os.WriteFile(store.aliasesPath, []byte(`[1]`), 0o644)
		aliases, _ = store.readAliases()
		eq(t, len(aliases.keys), 0, "not an object")
		os.WriteFile(store.aliasesPath, []byte(`{`), 0o644)
		if _, err := store.readAliases(); err == nil {
			t.Error("a broken aliases file is an error")
		}
		_ = filepath.Join
	})
}

func TestQueueContextAndActivity(t *testing.T) {
	ctx := currentQueueContext("/a/b/../c/", " s ")
	eq(t, ctx, queueContext{Cwd: "/a/c", SessionID: "s"}, "context")
	item := func(target queueTarget, sid string, status queueStatus) queueItem {
		return queueItem{Source: queueSource{Cwd: "/a/c", SessionID: sid}, Target: target, Status: status}
	}
	active := func(i queueItem, c queueContext) bool { return isActiveForContext(i, c) }
	global, current := queueTarget{Kind: targetGlobal}, queueTarget{Kind: targetCurrentSession}
	eq(t, active(item(global, "", statusQueued), queueContext{Cwd: "/elsewhere"}), true, "global is everywhere")
	for _, st := range []queueStatus{statusQueued, statusBlocked, statusDelivering, statusFailed} {
		eq(t, active(item(global, "", st), ctx), true, string(st))
	}
	eq(t, active(item(global, "", statusSent), ctx), false, "sent")
	proj := queueTarget{Kind: targetProject, Cwd: "/a/c/"}
	eq(t, active(item(proj, "other", statusQueued), ctx), true, "project item in its directory, whatever the session")
	eq(t, active(item(proj, "", statusQueued), queueContext{Cwd: "/a"}), false, "project item elsewhere")
	eq(t, active(item(current, "s", statusQueued), queueContext{Cwd: "/zzz", SessionID: "s"}), true, "the session wins over the directory")
	eq(t, active(item(current, "s", statusQueued), queueContext{Cwd: "/a/c", SessionID: "t"}), false, "another session")
	eq(t, active(item(current, "", statusQueued), queueContext{Cwd: "/a/c", SessionID: "t"}), true, "no session known: the directory")
	eq(t, active(item(current, "", statusQueued), queueContext{Cwd: "/a"}), false, "no session known, another directory")
}

func TestQueuedDeliveryItemsFilterByIntent(t *testing.T) {
	withStore(t, func(store tstore, dir string) {
		in := func(intent queueIntent) queueItem {
			return store.add(createQueueItemInput{Text: string(intent), Source: queueSource{Cwd: dir}, Target: queueTarget{Kind: targetGlobal}, Intent: intent})
		}
		in(intentSteer)
		in(intentPostCompact)
		blocked := in(intentPostCompact)
		store.update(blocked.ID, queueUpdates{Status: statusBlocked})
		ctx := currentQueueContext(dir, "")
		all, _ := store.queuedDeliveryItems(ctx, "")
		post, _ := store.queuedDeliveryItems(ctx, intentPostCompact)
		eq(t, len(all), 2, "queued of every intent, not the blocked one")
		eq(t, len(post), 1, "queued post-compaction")
	})
}
