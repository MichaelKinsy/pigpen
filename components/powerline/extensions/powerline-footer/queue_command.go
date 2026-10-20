package powerline_footer

import (
	"strconv"
	"strings"
)

// The /queue command and the queue picker. upstream: index.ts 1523-1532 (labels), 1750-1829 (picker, actions, targets) and
// 2487-2580 (the command).

// selectFunc opens the select overlay (showSelectOverlay of one context) and returns the chosen item, nil when cancelled. It
// blocks until the user chooses, so the queue calls it without holding the lock.
type selectFunc func(title, hint string, items []selectItem, maxVisible int) (*selectItem, error)

func queueItemLabel(item queueItem) string {
	status := string(item.Intent)
	if item.Status != statusQueued {
		status += "/" + string(item.Status)
	}
	return item.ID + " " + status + " " + buildStashPreview(item.Text, 56)
}

func queueItemDescription(item queueItem) string {
	switch item.Target.Kind {
	case targetGlobal:
		return "global"
	case targetCurrentSession:
		return "current session"
	}
	if item.Target.Alias != "" {
		return "@" + item.Target.Alias
	}
	return item.Target.Cwd
}

// resolveCommandTarget reads `@alias`, `global` or `current`.
func (q *queueState) resolveCommandTarget(spec string) (queueTarget, error) {
	normalized := jsTrim(spec)
	normalized = strings.TrimPrefix(normalized, "@")
	switch normalized {
	case "current":
		return queueTarget{Kind: targetCurrentSession}, nil
	case "global":
		return queueTarget{Kind: targetGlobal}, nil
	}
	cwd, ok, err := q.store.resolveAlias(normalized)
	if err != nil {
		return queueTarget{}, err
	}
	if !ok {
		return queueTarget{}, queueError("Unknown project alias @" + normalized + ". Use /queue alias " + normalized + " <path> first.")
	}
	return queueTarget{Kind: targetProject, Cwd: cwd, Alias: normalized}, nil
}

// sendOrRetryQueueItem marks the item queued and delivers it.
func (q *queueState) sendOrRetryQueueItem(idPrefix string) error {
	item, err := q.store.get(idPrefix)
	if err != nil {
		return err
	}
	if item == nil {
		q.p.h.Notify("No unique queue item matches "+idPrefix, "warning")
		return nil
	}
	updated, err := q.store.update(item.ID, queueUpdates{Status: statusQueued, SetError: true})
	if err != nil || updated == nil {
		return err
	}
	_, err = q.deliverQueueItem(*updated)
	return err
}

// openQueuePicker lists the active items of the context in the select overlay and lets the user manage one. It takes the lock
// itself and drops it while the overlay is open.
func (q *queueState) openQueuePicker(show selectFunc) error {
	q.mu.Lock()
	active, err := q.store.activeItems(q.context())
	if err != nil || len(active) == 0 {
		if err == nil {
			q.p.h.Notify("No queued items", "info")
		}
		q.mu.Unlock()
		return err
	}
	items := make([]selectItem, len(active))
	for i, item := range active {
		items[i] = selectItem{Value: item.ID, Label: queueItemLabel(item), Description: queueItemDescription(item)}
	}
	q.mu.Unlock()

	selected, err := show("Powerline queue", "↑↓ navigate • enter manage • esc cancel", items, min(len(active), 12))
	if err != nil || selected == nil {
		return err
	}
	for _, item := range active {
		if item.ID == selected.Value {
			return q.chooseQueueAction(item, show)
		}
	}
	return nil
}

func (q *queueState) chooseQueueAction(item queueItem, show selectFunc) error {
	actions := []selectItem{
		{Value: "send", Label: "Send to current session", Description: "Deliver as prompt/follow-up"},
		{Value: "edit", Label: "Edit in prompt", Description: "Move text into the editor"},
		{Value: "retry", Label: "Retry", Description: "Mark queued and deliver"},
		{Value: "clear", Label: "Clear", Description: "Mark item done"},
		{Value: "cancel", Label: "Cancel"},
	}
	selected, err := show("Queue item "+item.ID, buildStashPreview(item.Text, 72), actions, len(actions))
	if err != nil || selected == nil || selected.Value == "cancel" {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	switch selected.Value {
	case "send":
		_, err = q.deliverQueueItem(item)
		return err
	case "retry":
		updated, err := q.store.update(item.ID, queueUpdates{Status: statusQueued, SetError: true})
		if err != nil || updated == nil {
			return err
		}
		_, err = q.deliverQueueItem(*updated)
		return err
	case "edit":
		q.host.SetEditorText(item.Text)
		if _, err := q.store.clear(item.ID); err != nil {
			return err
		}
		q.requestQueueRender()
	case "clear":
		if _, err := q.store.clear(item.ID); err != nil {
			return err
		}
		q.p.h.Notify("Cleared "+item.ID, "info")
		q.requestQueueRender()
	}
	return nil
}

// command is /queue [send|retry|clear|target|alias]. Without an argument it opens the picker. It takes the lock itself.
func (q *queueState) command(args string, show selectFunc) error {
	parts := strings.FieldsFunc(args, isJSSpace)
	q.mu.Lock()
	q.ctxLive = true
	q.mu.Unlock()
	if len(parts) == 0 {
		return q.openQueuePicker(show)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	notify := func(message, level string) { q.p.h.Notify(message, level) }

	switch parts[0] {
	case "alias":
		if len(parts) < 2 {
			notify("Usage: /queue alias <name> [path]", "info")
			return nil
		}
		alias := parts[1]
		aliasPath := strings.Join(parts[2:], " ")
		if aliasPath == "" {
			aliasPath = q.p.h.Cwd()
			if aliasPath == "" {
				aliasPath = "."
			}
		}
		if err := q.store.setAlias(alias, aliasPath); err != nil {
			notify(err.Error(), "error")
			return nil
		}
		notify("Alias @"+alias+" → "+aliasPath, "info")
		return nil

	case "send", "retry":
		if len(parts) < 2 {
			items, err := q.store.queuedDeliveryItems(q.context(), "")
			if err != nil {
				return err
			}
			if len(items) == 0 {
				notify("No queued item to send", "info")
				return nil
			}
			return q.sendOrRetryQueueItem(items[0].ID)
		}
		return q.sendOrRetryQueueItem(parts[1])

	case "clear":
		if len(parts) >= 2 && parts[1] == "all" {
			active, err := q.store.activeItems(q.context())
			if err != nil {
				return err
			}
			for _, item := range active {
				if _, err := q.store.clear(item.ID); err != nil {
					return err
				}
			}
			notify("Cleared "+strconv.Itoa(len(active))+" queued item"+queuePlural(len(active)), "info")
			q.requestQueueRender()
			return nil
		}
		if len(parts) < 2 {
			notify("Usage: /queue clear <id|all>", "info")
			return nil
		}
		item, err := q.store.get(parts[1])
		if err != nil {
			return err
		}
		if item == nil {
			notify("No unique queued item matches "+parts[1], "warning")
			return nil
		}
		if _, err := q.store.clear(item.ID); err != nil {
			return err
		}
		notify("Cleared "+item.ID, "info")
		q.requestQueueRender()
		return nil

	case "target":
		if len(parts) < 3 {
			notify("Usage: /queue target <id> @alias|global|current", "info")
			return nil
		}
		item, err := q.store.get(parts[1])
		if err != nil {
			return err
		}
		if item == nil {
			notify("No unique queue item matches "+parts[1], "warning")
			return nil
		}
		target, err := q.resolveCommandTarget(parts[2])
		if err == nil {
			_, err = q.store.update(item.ID, queueUpdates{Target: &target})
		}
		if err != nil {
			notify(err.Error(), "error")
			return nil
		}
		notify("Retargeted "+item.ID, "info")
		q.requestQueueRender()
		return nil
	}
	notify("Usage: /queue [send|retry|clear|target|alias]", "info")
	return nil
}
