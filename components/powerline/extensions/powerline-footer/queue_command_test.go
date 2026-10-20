package powerline_footer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// /queue and the queue picker (index.ts 1750-1829, 2487-2580) on the scripted host.

func (r *queueRig) run(args string, choices ...string) []string {
	r.t.Helper()
	r.host.notes = nil
	var titles []string
	show := func(title, hint string, items []selectItem, maxVisible int) (*selectItem, error) {
		titles = append(titles, title)
		if len(choices) == 0 {
			return nil, nil
		}
		choice := choices[0]
		choices = choices[1:]
		if choice == "" {
			return nil, nil
		}
		for _, item := range items {
			if item.Value == choice {
				return &item, nil
			}
		}
		r.t.Fatalf("%q is not offered: %+v", choice, items)
		return nil, nil
	}
	if err := r.q.command(args, show); err != nil {
		r.t.Fatalf("/queue %s: %v", args, err)
	}
	return r.host.notices()
}

func TestQueueAlias(t *testing.T) {
	r := newQueueRig(t)
	dir := filepath.Join(r.root, "proj")
	eq(t, r.run("alias"), []string{"info: Usage: /queue alias <name> [path]"}, "usage")
	eq(t, r.run("alias work "+dir), []string{"info: Alias @work → " + dir}, "set")
	eq(t, r.run("alias   home"), []string{"info: Alias @home → " + r.root}, "default path is the working directory")
	eq(t, r.run("alias bad! "+dir), []string{"error: Alias must contain only letters, numbers, dashes, or underscores"}, "invalid")
	eq(t, r.run("alias spaced "+dir+"  with   gaps"), []string{"info: Alias @spaced → " + dir + " with gaps"}, "path words are joined by single spaces")
	aliases, err := r.store.readAliases()
	if err != nil {
		t.Fatal(err)
	}
	eq(t, aliases.vals["work"], dir, "stored")
	data, _ := os.ReadFile(filepath.Join(r.root, "powerline-footer", "projects.json"))
	eq(t, strings.HasPrefix(string(data), "{\n  \"work\": "), true, "indented JSON")
	eq(t, strings.HasSuffix(string(data), "}\n"), true, "newline at the end")
}

func TestQueueSendAndRetry(t *testing.T) {
	r := newQueueRig(t)
	r.host.idle = true
	eq(t, r.run("send"), []string{"info: No queued item to send"}, "nothing queued")
	a := r.item("first", intentFollowUp, queueTarget{Kind: targetGlobal})
	b := r.item("second", intentFollowUp, queueTarget{Kind: targetGlobal})
	eq(t, r.run("send"), []string(nil), "send without an id takes the first queued item")
	eq(t, r.host.sends, []sentMessage{{"first", ""}}, "sent")
	eq(t, r.get(a.ID).Status, statusDelivering, "delivering")
	if _, err := r.store.update(a.ID, queueUpdates{Status: statusFailed, SetError: true, Error: "old"}); err != nil {
		t.Fatal(err)
	}
	eq(t, r.run("retry "+a.ID[:4]), []string(nil), "retry by id prefix")
	eq(t, r.host.texts(), []string{"first", "first"}, "sent again")
	eq(t, r.get(a.ID).Error, "", "the error is cleared")
	eq(t, r.run("send zzzz"), []string{"warning: No unique queue item matches zzzz"}, "no match")
	eq(t, r.run("send "+b.ID), []string(nil), "send by id")
	eq(t, r.host.texts(), []string{"first", "first", "second"}, "second")

	r.host.idle = false
	c := r.item("third", intentSteer, queueTarget{Kind: targetGlobal})
	r.run("send " + c.ID)
	eq(t, r.host.sends[len(r.host.sends)-1], sentMessage{"third", "steer"}, "a busy session takes a steer item as steering")
}

func TestQueueSendIgnoresAnAmbiguousPrefix(t *testing.T) {
	r := newQueueRig(t)
	for _, id := range []string{"abcd0001", "abcd0002"} {
		item := queueItem{ID: id, Text: id, CreatedAt: 1, UpdatedAt: float64(clock()), Source: queueSource{Cwd: r.root}, Target: queueTarget{Kind: targetGlobal}, Intent: intentFollowUp, Status: statusQueued}
		appendInbox(t, r, item)
	}
	eq(t, r.run("send abcd"), []string{"warning: No unique queue item matches abcd"}, "two matches")
	eq(t, r.run("clear abcd"), []string{"warning: No unique queued item matches abcd"}, "clear names it differently")
	eq(t, r.run("target abcd global"), []string{"warning: No unique queue item matches abcd"}, "target")
}

func appendInbox(t *testing.T, r *queueRig, item queueItem) {
	t.Helper()
	path := filepath.Join(r.root, "powerline-footer", "inbox.jsonl")
	os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.WriteString(marshalJSON(item.toJSON(), "") + "\n")
}

func TestQueueClear(t *testing.T) {
	r := newQueueRig(t)
	eq(t, r.run("clear"), []string{"info: Usage: /queue clear <id|all>"}, "usage")
	eq(t, r.run("clear all"), []string{"info: Cleared 0 queued items"}, "none")
	a := r.item("one", intentFollowUp, queueTarget{Kind: targetGlobal})
	b := r.item("two", intentFollowUp, queueTarget{Kind: targetGlobal})
	eq(t, r.run("clear "+a.ID), []string{"info: Cleared " + a.ID}, "one")
	eq(t, r.get(a.ID).Status, statusSent, "marked done")
	eq(t, r.run("clear all"), []string{"info: Cleared 1 queued item"}, "singular")
	eq(t, r.get(b.ID).Status, statusSent, "all")
	eq(t, r.run("clear nosuch"), []string{"warning: No unique queued item matches nosuch"}, "unknown")
}

func TestQueueTarget(t *testing.T) {
	r := newQueueRig(t)
	item := r.item("move me", intentFollowUp, queueTarget{Kind: targetCurrentSession})
	eq(t, r.run("target"), []string{"info: Usage: /queue target <id> @alias|global|current"}, "usage")
	eq(t, r.run("target "+item.ID), []string{"info: Usage: /queue target <id> @alias|global|current"}, "usage with an id")
	eq(t, r.run("target "+item.ID+" global"), []string{"info: Retargeted " + item.ID}, "global")
	eq(t, r.get(item.ID).Target, queueTarget{Kind: targetGlobal}, "global target")
	eq(t, r.run("target "+item.ID+" @current"), []string{"info: Retargeted " + item.ID}, "current")
	eq(t, r.get(item.ID).Target, queueTarget{Kind: targetCurrentSession}, "current target")
	eq(t, r.run("target "+item.ID+" @nowhere"), []string{"error: Unknown project alias @nowhere. Use /queue alias nowhere <path> first."}, "unknown alias")
	dir := filepath.Join(r.root, "proj")
	r.run("alias work " + dir)
	eq(t, r.run("target "+item.ID+" @work"), []string{"info: Retargeted " + item.ID}, "alias")
	eq(t, r.get(item.ID).Target, queueTarget{Kind: targetProject, Cwd: dir, Alias: "work"}, "project target")
	eq(t, queueItemDescription(r.get(item.ID)), "@work", "description")
	eq(t, r.run("target nosuch global"), []string{"warning: No unique queue item matches nosuch"}, "unknown item")
}

func TestQueueUsageAndPicker(t *testing.T) {
	r := newQueueRig(t)
	eq(t, r.run("frobnicate"), []string{"info: Usage: /queue [send|retry|clear|target|alias]"}, "usage")
	eq(t, r.run(""), []string{"info: No queued items"}, "an empty picker says so")
	eq(t, r.run("   "), []string{"info: No queued items"}, "blank arguments open the picker")
}

func TestQueuePickerActions(t *testing.T) {
	r := newQueueRig(t)
	r.host.idle = true
	item := r.item("the   prompt\ntext", intentFollowUp, queueTarget{Kind: targetCurrentSession})
	other := r.item("other", intentSteer, queueTarget{Kind: targetGlobal})
	if _, err := r.store.update(other.ID, queueUpdates{Status: statusBlocked}); err != nil {
		t.Fatal(err)
	}

	// The list shows labels and targets.
	var offered []selectItem
	var hint string
	var visible int
	if err := r.q.command("", func(title, h string, items []selectItem, m int) (*selectItem, error) {
		offered, hint, visible = items, h, m
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	eq(t, hint, "↑↓ navigate • enter manage • esc cancel", "hint")
	eq(t, visible, 2, "visible rows")
	eq(t, offered, []selectItem{
		{Value: item.ID, Label: item.ID + " follow-up the prompt text", Description: "current session"},
		{Value: other.ID, Label: other.ID + " steer/blocked other", Description: "global"},
	}, "items")

	eq(t, r.run("", ""), []string(nil), "cancelling the list does nothing")
	eq(t, r.run("", item.ID, "cancel"), []string(nil), "cancel")
	eq(t, r.run("", item.ID, ""), []string(nil), "escape in the action list")
	eq(t, len(r.host.sends), 0, "nothing sent")

	r.run("", item.ID, "send")
	eq(t, r.host.sends, []sentMessage{{"the   prompt\ntext", ""}}, "send")
	eq(t, r.get(item.ID).Status, statusDelivering, "delivering")
	r.run("", other.ID, "retry")
	eq(t, r.host.texts(), []string{"the   prompt\ntext", "other"}, "retry marks queued and delivers")
	eq(t, r.get(other.ID).Status, statusDelivering, "retried")

	r.emit("before_agent_start", map[string]any{"prompt": "the prompt text"})
	eq(t, r.get(item.ID).Status, statusSent, "started")
	third := r.item("edit me", intentFollowUp, queueTarget{Kind: targetGlobal})
	r.run("", third.ID, "edit")
	eq(t, r.host.editor, "edit me", "the text moves into the editor")
	eq(t, r.get(third.ID).Status, statusSent, "and the item is done")
	fourth := r.item("drop me", intentFollowUp, queueTarget{Kind: targetGlobal})
	eq(t, r.run("", fourth.ID, "clear"), []string{"info: Cleared " + fourth.ID}, "clear")
	eq(t, r.get(fourth.ID).Status, statusSent, "cleared")
}

func TestQueuePickerOffersAtMostTwelveRowsAndTheActionTitles(t *testing.T) {
	r := newQueueRig(t)
	for i := 0; i < 15; i++ {
		r.item("n", intentFollowUp, queueTarget{Kind: targetGlobal})
	}
	var visible int
	var titles, actions []string
	r.q.command("", func(title, hint string, items []selectItem, m int) (*selectItem, error) {
		titles = append(titles, title)
		if len(titles) == 1 {
			visible = m
			return &items[0], nil
		}
		for _, it := range items {
			actions = append(actions, it.Label)
		}
		eq(t, hint, "n", "the action list shows the prompt")
		eq(t, m, 5, "five actions")
		return nil, nil
	})
	eq(t, visible, 12, "at most twelve rows")
	eq(t, titles[0], "Powerline queue", "list title")
	eq(t, strings.HasPrefix(titles[1], "Queue item "), true, "action title")
	eq(t, actions, []string{"Send to current session", "Edit in prompt", "Retry", "Clear", "Cancel"}, "actions")
}

func TestQueueAliasWithABrokenFileIsReportedNotSwallowed(t *testing.T) {
	r := newQueueRig(t)
	os.MkdirAll(filepath.Join(r.root, "powerline-footer"), 0o755)
	os.WriteFile(filepath.Join(r.root, "powerline-footer", "projects.json"), []byte("{nope"), 0o644)
	got := r.run("alias a " + r.root)
	eq(t, len(got), 1, "one notice")
	eq(t, strings.HasPrefix(got[0], "error: "), true, "an error")
	item := r.item("x", intentFollowUp, queueTarget{Kind: targetCurrentSession})
	got = r.run("target " + item.ID + " @a")
	eq(t, strings.HasPrefix(got[0], "error: "), true, "target reports it too")
}
