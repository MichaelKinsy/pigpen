package ternbrowser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/browser"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ternwire"
)

func openFakeTab(t *testing.T, fake *fakeDaemon, opts browser.OpenOptions) *Tab {
	t.Helper()
	b := NewBackend()
	opts.Endpoint = fake.socketPath
	if opts.Pane == 0 {
		opts.Pane = 3
	}
	if opts.Name == "" {
		opts.Name = "main"
	}
	if opts.Viewport == nil {
		opts.Viewport = &browser.Viewport{Width: 800, Height: 600, DeviceScaleFactor: 1}
	}
	if opts.TimeoutMs == 0 {
		opts.TimeoutMs = 5000
	}
	raw, err := b.Open(context.Background(), opts)
	if err != nil {
		t.Fatalf("Backend.Open: %v", err)
	}
	return raw.(*Tab)
}

// ─── Ported upstream browser-tern-tab.test.ts ─────────────────────────

// upstream: browser-tern-tab.test.ts: "configures the PiP before its first navigation and resolves on that navigation's own load"
func TestUpstreamTabConfigureBeforeFirstNavigation(t *testing.T) {
	page := &fakePageSpec{
		blankLoadPoll: 2,
		eventBatches: [][]map[string]any{
			{},
			{},
			{},
			{},
			{{"type": "committed", "url": "https://example.test/"}},
			{{"type": "loaded", "url": "https://example.test/"}},
		},
		kit: map[string]func([]any) any{},
	}
	fake := startPage(t, page)
	tab := openFakeTab(t, fake, browser.OpenOptions{
		URL:            "https://example.test/",
		Dialogs:        "accept",
		AllowedDomains: []string{"example.test"},
	})
	reqs := fake.Requests()
	if reqs[0].Op["op"] != "open" || reqs[0].Op["url"] != "about:blank" || reqs[0].Op["owner"] != float64(3) || reqs[0].Op["width"] != float64(800) || reqs[0].Op["height"] != float64(600) {
		t.Fatalf("unexpected open op: %+v", reqs[0].Op)
	}
	// Upstream assertions (N7): dialogs, allow, scripts before goto, all eventBatches drained.
	var foundDialogs, foundAllow bool
	for _, r := range reqs {
		if r.Op["op"] == "dialogs" && r.Op["block"] == float64(7) && r.Op["policy"] == "accept" {
			foundDialogs = true
		}
		if r.Op["op"] == "allow" {
			if hosts, ok := r.Op["hosts"].([]any); ok && len(hosts) == 1 && hosts[0] == "example.test" {
				foundAllow = true
			}
		}
	}
	if !foundDialogs {
		t.Fatalf("expected dialogs op with block=7 policy=accept; reqs=%+v", reqs)
	}
	if !foundAllow {
		t.Fatalf("expected allow op with hosts=[example.test]; reqs=%+v", reqs)
	}
	ops := fake.Ops()
	scriptsIdx := slices.Index(ops, "scripts")
	gotoIdx := slices.Index(ops, "goto")
	if scriptsIdx <= 0 || gotoIdx <= scriptsIdx {
		t.Fatalf("expected scripts before goto; ops=%v", ops)
	}
	scriptsOp := reqs[scriptsIdx].Op["scripts"].([]any)
	if len(scriptsOp) < 2 {
		t.Fatalf("expected capture + kit scripts, got %d", len(scriptsOp))
	}
	firstScript := scriptsOp[0].(map[string]any)
	if firstScript["world"] != "page" || firstScript["frames"] != "all" || firstScript["at"] != "start" {
		t.Fatalf("unexpected script spec: %+v", firstScript)
	}
	if len(page.eventBatches) != 0 {
		t.Fatalf("expected all eventBatches drained before open resolved; remaining=%d", len(page.eventBatches))
	}
	if tab.URL() != "https://example.test/" {
		t.Fatalf("expected URL https://example.test/, got %q", tab.URL())
	}
}

// upstream: browser-tern-tab.test.ts: "does not take an about:blank load reported after the first goto for that navigation"
func TestUpstreamTabIgnoresPostGotoBlankLoad(t *testing.T) {
	for _, stateURL := range []string{"about:blank", "https://example.test/"} {
		t.Run(stateURL, func(t *testing.T) {
			var mu sync.Mutex
			seq := 0
			gotGoto := false
			afterGoto := 0
			fake := startFakeDaemon(t, func(op map[string]any, _ int64) *fakeAnswer {
				mu.Lock()
				defer mu.Unlock()
				switch op["op"] {
				case "open":
					return okAns(map[string]any{"block": 7, "url": "about:blank"})
				case "goto":
					gotGoto = true
					return okAns(map[string]any{})
				case "state":
					return okAns(map[string]any{"url": stateURL, "loading": false, "width": 800, "height": 600})
				case "events":
					var batch []map[string]any
					if seq == 0 || (gotGoto && afterGoto == 0) {
						if gotGoto {
							afterGoto++
						}
						batch = []map[string]any{
							{"type": "committed", "url": "about:blank"},
							{"type": "loaded", "url": "about:blank"},
						}
					}
					stamped := make([]map[string]any, 0, len(batch))
					for _, ev := range batch {
						seq++
						cp := map[string]any{"seq": seq}
						for k, v := range ev {
							cp[k] = v
						}
						stamped = append(stamped, cp)
					}
					return okAns(map[string]any{"events": stamped, "next": seq, "dropped": 0})
				default:
					return okAns(map[string]any{})
				}
			}, fakeDaemonOpts{})

			b := NewBackend()
			_, err := b.Open(context.Background(), browser.OpenOptions{
				Endpoint:  fake.socketPath,
				Pane:      3,
				Name:      "main",
				Viewport:  &browser.Viewport{Width: 800, Height: 600},
				TimeoutMs: 400,
				URL:       "https://example.test/",
			})
			if err == nil || !strings.Contains(err.Error(), `tab.goto("https://example.test/") timed out`) || !strings.Contains(err.Error(), "about:blank load since the goto") {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.Contains(fake.Ops(), "close") {
				t.Fatalf("expected failed open to close PiP; ops=%v", fake.Ops())
			}
		})
	}
}

// upstream: browser-tern-tab.test.ts: "opens at about:blank without a goto, which a Tern web view would never load"
func TestUpstreamTabOpensAtBlankWithoutGoto(t *testing.T) {
	fake := startPage(t, &fakePageSpec{kit: map[string]func([]any) any{}})
	_ = openFakeTab(t, fake, browser.OpenOptions{URL: "about:blank"})
	if slices.Contains(fake.Ops(), "goto") {
		t.Fatalf("expected no goto op for about:blank; ops=%v", fake.Ops())
	}
}

// upstream: browser-tern-tab.test.ts: "reloads for a goto to the address the page shows, which an older Tern never navigates"
func TestUpstreamTabReloadsOnSameAddressGoto(t *testing.T) {
	fake := startPage(t, &fakePageSpec{
		eventBatches: [][]map[string]any{
			{},
			{},
			{{"type": "committed", "url": "https://example.test/"}, {"type": "loaded"}},
		},
		kit: map[string]func([]any) any{},
	})
	tab := openFakeTab(t, fake, browser.OpenOptions{})
	if _, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "goto", Args: jsonArgs("https://example.test")}}); err != nil {
		t.Fatalf("goto same address: %v", err)
	}
	foundReload := false
	for _, r := range fake.Requests() {
		if r.Op["op"] == "nav" && r.Op["go"] == "reload" && r.Op["block"] == float64(7) {
			foundReload = true
		}
		if r.Op["op"] == "goto" {
			t.Fatalf("expected reload instead of goto; ops=%v", fake.Ops())
		}
	}
	if !foundReload {
		t.Fatalf("expected nav reload; ops=%v", fake.Ops())
	}
}

// upstream: browser-tern-tab.test.ts: "fails a navigation Tern reports as failed"
func TestUpstreamTabFailsOnFailedNavigationEvent(t *testing.T) {
	fake := startPage(t, &fakePageSpec{
		eventBatches: [][]map[string]any{
			{},
			{},
			{{"type": "failed", "message": "A server with the specified hostname could not be found."}},
		},
		kit: map[string]func([]any) any{},
	})
	tab := openFakeTab(t, fake, browser.OpenOptions{})
	_, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "goto", Args: jsonArgs("https://nowhere.test/")}})
	if err == nil || !strings.Contains(err.Error(), "hostname could not be found") {
		t.Fatalf("expected hostname failure, got %v", err)
	}
}

// upstream: browser-tern-tab.test.ts: "clicks with trusted mouse events at the actionable element centre"
// & "presses the requested button for a selector click"
func TestUpstreamTabTrustedClickAndButton(t *testing.T) {
	var targets [][]any
	fake := startPage(t, &fakePageSpec{
		kit: map[string]func([]any) any{
			"target": func(args []any) any {
				targets = append(targets, args)
				return map[string]any{"ok": true, "x": 40, "y": 20, "width": 30, "height": 12, "count": 1}
			},
		},
	})
	tab := openFakeTab(t, fake, browser.OpenOptions{})
	if _, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "click", Args: jsonArgs("text/Sign in")}}); err != nil {
		t.Fatalf("click: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 target call, got %d", len(targets))
	}
	sel := targets[0][0].(map[string]any)
	if sel["engine"] != "text" || sel["query"] != "Sign in" || targets[0][1] != "click" {
		t.Fatalf("unexpected target args: %+v", targets[0])
	}
	if _, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "click", Args: jsonArgs("#menu", map[string]any{"button": "right"})}}); err != nil {
		t.Fatalf("right click: %v", err)
	}
	var inputs []any
	for _, r := range fake.Requests() {
		if r.Op["op"] == "input" {
			inputs = append(inputs, r.Op["events"])
		}
	}
	if len(inputs) != 2 {
		t.Fatalf("expected 2 input ops, got %d", len(inputs))
	}
	rightEvents := inputs[1].([]any)
	if len(rightEvents) != 3 || rightEvents[0].(map[string]any)["button"] != "right" || rightEvents[0].(map[string]any)["clicks"] != float64(0) || rightEvents[1].(map[string]any)["clicks"] != float64(1) {
		t.Fatalf("unexpected right click events: %+v", rightEvents)
	}
}

// upstream: browser-tern-tab.test.ts: "fills text controls with trusted text input after the kit selects their content"
func TestUpstreamTabFillViaKitPrepareFill(t *testing.T) {
	fake := startPage(t, &fakePageSpec{
		kit: map[string]func([]any) any{
			"target": func([]any) any {
				return map[string]any{"ok": true, "x": 5, "y": 5, "width": 100, "height": 20, "count": 1}
			},
			"prepareFill": func([]any) any { return map[string]any{"mode": "insert"} },
		},
	})
	tab := openFakeTab(t, fake, browser.OpenOptions{})
	if _, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "fill", Args: jsonArgs("#email", "a@b.test")}}); err != nil {
		t.Fatalf("fill: %v", err)
	}
	for _, r := range fake.Requests() {
		if r.Op["op"] == "input" {
			evs := r.Op["events"].([]any)
			if len(evs) == 1 && evs[0].(map[string]any)["type"] == "text" && evs[0].(map[string]any)["text"] == "a@b.test" {
				return
			}
		}
	}
	t.Fatalf("expected trusted text input event for fill; ops=%v", fake.Ops())
}

// upstream: browser-tern-tab.test.ts: "sends one key press to an uncovered element and none to an element a dialog covers"
func TestUpstreamTabPressCoveredElementRefused(t *testing.T) {
	covered := false
	fake := startPage(t, &fakePageSpec{
		kit: map[string]func([]any) any{
			"target": func([]any) any {
				if covered {
					return map[string]any{"ok": false, "reason": "covered", "count": 1, "detail": "<dialog#modal>"}
				}
				return map[string]any{"ok": true, "x": 5, "y": 5, "width": 100, "height": 20, "count": 1}
			},
			"focus": func([]any) any { return nil },
		},
	})
	tab := openFakeTab(t, fake, browser.OpenOptions{})
	tab.actionBudgetMs = 150
	if _, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "press", Args: jsonArgs("Enter", map[string]any{"selector": "#name"})}}); err != nil {
		t.Fatalf("uncovered press: %v", err)
	}
	countInputs := func() int {
		n := 0
		for _, r := range fake.Requests() {
			if r.Op["op"] == "input" {
				n++
			}
		}
		return n
	}
	if countInputs() != 1 {
		t.Fatalf("expected 1 input op, got %d", countInputs())
	}
	covered = true
	_, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "press", Args: jsonArgs("Enter", map[string]any{"selector": "#name"})}})
	if err == nil || !strings.Contains(err.Error(), "covered by <dialog#modal>") {
		t.Fatalf("expected covered error, got %v", err)
	}
	if countInputs() != 1 {
		t.Fatalf("expected no input op when covered, got %d", countInputs())
	}
}

// upstream: browser-tern-tab.test.ts: "reports a held dialog and answers it through the dialog op"
// Also verifies S3: handleDialog({accept: false}) sends accept: false (never true).
func TestUpstreamTabDialogAndDismissS3(t *testing.T) {
	fake := startPage(t, &fakePageSpec{
		eventBatches: [][]map[string]any{
			{},
			{{
				"type":    "dialog",
				"kind":    "prompt",
				"message": "Name?",
				"default": "x",
				"url":     "https://example.test/",
				"handled": nil,
			}},
		},
		kit: map[string]func([]any) any{},
	})
	tab := openFakeTab(t, fake, browser.OpenOptions{})
	res, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "dialog"}})
	if err != nil {
		t.Fatalf("dialog: %v", err)
	}
	ds := res.Value.(DialogState)
	if !ds.Open || ds.Type != "prompt" || ds.Message != "Name?" || ds.DefaultValue != "x" {
		t.Fatalf("unexpected DialogState: %+v", ds)
	}

	// Page ops must be blocked while dialog is open.
	if _, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "evaluate", Args: jsonArgs("1+1")}}); err == nil || !strings.Contains(err.Error(), "blocks the page") {
		t.Fatalf("expected dialogBlocksPageError, got %v", err)
	}

	// S3: dismiss with {accept: false}
	if _, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "handleDialog", Args: jsonArgs(map[string]any{"accept": false, "text": "no"})}}); err != nil {
		t.Fatalf("handleDialog dismiss: %v", err)
	}
	var dialogOp map[string]any
	for _, r := range fake.Requests() {
		if r.Op["op"] == "dialog" {
			dialogOp = r.Op
		}
	}
	if dialogOp == nil || dialogOp["accept"] != false || dialogOp["text"] != "no" {
		t.Fatalf("expected dialog op with accept=false text=no, got %+v", dialogOp)
	}
	res, err = tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "dialog"}})
	if err != nil || res.Value.(DialogState).Open {
		t.Fatalf("expected dialog closed after handleDialog, got %+v err=%v", res, err)
	}
}

// upstream: browser-tern-tab.test.ts: "resolves waitForResponse by response arrival, including requests already in flight"
func TestUpstreamTabWaitForResponseAndEventsCursor(t *testing.T) {
	fake := startPage(t, &fakePageSpec{
		eventBatches: [][]map[string]any{
			{},
			{
				captureMsg(map[string]any{"kind": "request", "id": "d:1", "method": "GET", "url": "https://example.test/slow", "resourceType": "fetch", "headers": map[string]any{}}),
				captureMsg(map[string]any{"kind": "request", "id": "d:2", "method": "GET", "url": "https://example.test/fast", "resourceType": "fetch", "headers": map[string]any{}}),
			},
			{captureMsg(map[string]any{"kind": "response", "id": "d:2", "url": "https://example.test/fast", "status": 200, "headers": map[string]any{}})},
			{captureMsg(map[string]any{"kind": "response", "id": "d:1", "url": "https://example.test/slow", "status": 201, "headers": map[string]any{}})},
			{{"type": "response", "url": "https://example.test/", "status": 200, "mime": "text/html", "headers": map[string]any{"Content-Type": "text/html"}, "main": true}},
		},
		kit: map[string]func([]any) any{},
	})
	tab := openFakeTab(t, fake, browser.OpenOptions{})
	resp, err := tab.WaitForResponse(context.Background(), "/slow", 5*time.Second)
	if err != nil || resp == nil || resp.Status == nil || *resp.Status != 201 {
		t.Fatalf("WaitForResponse: err=%v resp=%+v", err, resp)
	}
	// Verify events cursor actually advanced (A/events).
	var afters []float64
	for _, r := range fake.Requests() {
		if r.Op["op"] == "events" {
			afters = append(afters, r.Op["after"].(float64))
		}
	}
	if len(afters) < 3 || afters[len(afters)-1] == 0 {
		t.Fatalf("expected advancing events after cursor, got %v", afters)
	}
}

// upstream: browser-tern-tab.test.ts: "runs a string evaluate exactly once, as an expression or as statements"
func TestUpstreamTabEvaluateExpressionAndStatements(t *testing.T) {
	if !strings.Contains(UserSourceFunction("1 + 1"), "return (\n1 + 1\n);") {
		t.Fatalf("unexpected expression wrap: %s", UserSourceFunction("1 + 1"))
	}
	if UserSourceFunction("const a = 1; return a;") != "function () {\nconst a = 1; return a;\n}" {
		t.Fatalf("unexpected statement wrap: %s", UserSourceFunction("const a = 1; return a;"))
	}
	// Upstream source ends with JSON.parse('{') to ensure no retry-on-error runs code twice (N7).
	const upstreamSrc = "const response = await fetch('/api', { method: 'POST' }); return JSON.parse('{')"
	fake := startPage(t, &fakePageSpec{kit: map[string]func([]any) any{}})
	tab := openFakeTab(t, fake, browser.OpenOptions{})
	before := len(fake.Requests())
	if _, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "evaluate", Args: jsonArgs(upstreamSrc)}}); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	evalCount := 0
	for _, r := range fake.Requests()[before:] {
		if r.Op["op"] == "eval" {
			evalCount++
		}
	}
	if evalCount != 1 {
		t.Fatalf("expected 1 eval op, got %d", evalCount)
	}
}

// upstream: browser-tern-tab.test.ts: "reads the page body when text or html gets no selector"
func TestUpstreamTabTextAndHtmlDefaultBody(t *testing.T) {
	var reads [][]any
	fake := startPage(t, &fakePageSpec{
		kit: map[string]func([]any) any{
			"read": func(args []any) any {
				reads = append(reads, args)
				return "page"
			},
		},
	})
	tab := openFakeTab(t, fake, browser.OpenOptions{})
	r1, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "text"}})
	if err != nil || r1.Value != "page" {
		t.Fatalf("text(): err=%v res=%+v", err, r1)
	}
	r2, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "html"}})
	if err != nil || r2.Value != "page" {
		t.Fatalf("html(): err=%v res=%+v", err, r2)
	}
	if len(reads) != 2 || reads[0][0].(map[string]any)["query"] != "body" || reads[0][1] != "text" || reads[1][1] != "html" {
		t.Fatalf("unexpected reads: %+v", reads)
	}
}

// upstream: browser-tern-tab.test.ts: "closes the PiP of an open that was abandoned before Tern answered"
func TestUpstreamTabAbandonedOpenClosesLateBlock(t *testing.T) {
	openedCh := make(chan int64, 1)
	closedCh := make(chan float64, 1)
	fake := startFakeDaemon(t, func(op map[string]any, id int64) *fakeAnswer {
		if op["op"] == "open" {
			openedCh <- id
			return nil
		}
		if op["op"] == "close" {
			if blk, ok := op["block"].(float64); ok {
				closedCh <- blk
			}
			return okAns(map[string]any{})
		}
		return okAns(map[string]any{})
	}, fakeDaemonOpts{})

	b := NewBackend()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := b.Open(ctx, browser.OpenOptions{
			Endpoint:  fake.socketPath,
			Pane:      3,
			Name:      "main",
			Viewport:  &browser.Viewport{Width: 800, Height: 600},
			TimeoutMs: 5000,
		})
		errCh <- err
	}()
	id := <-openedCh
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	fake.Answer(id, okAns(map[string]any{"block": 44, "url": "about:blank"}))
	select {
	case blk := <-closedCh:
		if blk != 44 {
			t.Fatalf("expected close block 44, got %v", blk)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for late close of block 44")
	}
}

// upstream: browser-tern-tab.test.ts: "reports a block the window no longer lists as gone, with how to reopen, and sends nothing more"
// & "keeps a not_found for a block the window still lists as that op's own failure" (S8)
func TestUpstreamTabGoneBlockLatchAndStaleRef(t *testing.T) {
	gone := false
	fake := startFakeDaemon(t, func(op map[string]any, _ int64) *fakeAnswer {
		switch op["op"] {
		case "open":
			return okAns(map[string]any{"block": 7, "url": "about:blank"})
		case "events":
			return okAns(map[string]any{"events": []map[string]any{{"seq": 1, "type": "loaded", "url": "about:blank"}}, "next": 1})
		case "blocks":
			if gone {
				return okAns(map[string]any{"blocks": []any{}})
			}
			return okAns(map[string]any{"blocks": []map[string]any{{"block": 7, "url": "about:blank", "title": "", "owner": 3}}})
		case "state":
			if gone {
				return errAns("not_found", "no browser block 7 in this window")
			}
			return okAns(map[string]any{"url": "about:blank", "width": 800, "height": 600})
		default:
			return okAns(map[string]any{})
		}
	}, fakeDaemonOpts{})

	tab := openFakeTab(t, fake, browser.OpenOptions{})
	gone = true
	_, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "title"}})
	if err == nil || !strings.Contains(err.Error(), `Tab "main" is gone: its Tern browser block 7 is no longer in this Tern window`) || !strings.Contains(err.Error(), `browser.targets({ app: "tern" })`) {
		t.Fatalf("unexpected gone error: %v", err)
	}
	if tab.Alive() {
		t.Fatal("expected Alive() == false after block gone")
	}
	before := len(fake.Requests())
	if _, err2 := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "title"}}); err2 != err {
		t.Fatalf("expected same latched gone error, got %v", err2)
	}
	if len(fake.Requests()) != before {
		t.Fatalf("expected zero ops sent after gone latch, sent %d", len(fake.Requests())-before)
	}
}

// upstream: browser-tern-tab.test.ts: "says how to get a fixed viewport when the block was docked"
func TestUpstreamTabDockedViewportError(t *testing.T) {
	fake := startFakeDaemon(t, func(op map[string]any, _ int64) *fakeAnswer {
		switch op["op"] {
		case "open":
			return okAns(map[string]any{"block": 7, "url": "about:blank"})
		case "events":
			return okAns(map[string]any{"events": []map[string]any{{"seq": 1, "type": "loaded", "url": "about:blank"}}, "next": 1})
		case "viewport":
			return errAns("failed", "the block is docked: its page takes the block's size")
		case "blocks":
			return okAns(map[string]any{"blocks": []map[string]any{{"block": 7, "url": "https://example.test/", "title": "", "owner": nil}}})
		case "state":
			return okAns(map[string]any{"url": "https://example.test/", "width": 843, "height": 632})
		default:
			return okAns(map[string]any{})
		}
	}, fakeDaemonOpts{})

	tab := openFakeTab(t, fake, browser.OpenOptions{})
	err := tab.SetViewport(context.Background(), browser.Viewport{Width: 1000, Height: 900})
	if err == nil || !strings.Contains(err.Error(), "is docked, so its page takes the block's size (843x632 now)") || !strings.Contains(err.Error(), `app: "own"`) {
		t.Fatalf("unexpected docked viewport error: %v", err)
	}
}

// upstream: browser-tern-tab.test.ts: "names the Tern backend and the reason for helpers it cannot provide"
func TestUpstreamTabRefusalMessagesAndNoSideEffects(t *testing.T) {
	fake := startPage(t, &fakePageSpec{kit: map[string]func([]any) any{}})
	tab := openFakeTab(t, fake, browser.OpenOptions{})
	before := len(fake.Requests())

	cases := []struct {
		step browser.CallStep
		want string
	}{
		{browser.CallStep{Method: "traceStart"}, "tab.traceStart() is not supported on the Tern browser backend: the web view exposes no performance-trace protocol"},
		{browser.CallStep{Method: "profileStart"}, "tab.profileStart() is not supported on the Tern browser backend: the web view exposes no CPU-profiler protocol"},
		{browser.CallStep{Method: "webmcpList"}, `the Tern web view has no native WebMCP; open the tab with ` + "`app: \"chromium\"`"},
		{browser.CallStep{Method: "pdf", Args: jsonArgs(map[string]any{"format": "a4", "margin": map[string]any{"top": 1}})}, "options format, margin are unavailable"},
		{browser.CallStep{Method: "emulate", Args: jsonArgs(map[string]any{"timezone": "Europe/Paris", "viewport": map[string]any{"width": 1, "height": 1}})}, "timezone cannot be overridden"},
		{browser.CallStep{Method: "emulate", Args: jsonArgs(map[string]any{"reducedMotion": false})}, "prefers-reduced-motion"},
		{browser.CallStep{Method: "emulate", Args: jsonArgs(map[string]any{"headers": map[string]any{"X-Test": "1"}})}, "extra HTTP headers cannot be added"},
		{browser.CallStep{Method: "route", Args: jsonArgs("**/*.png", map[string]any{"resourceType": "image", "abort": true})}, "resourceType image: only the page's fetch and xhr requests can be routed"},
	}
	for _, tc := range cases {
		_, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{tc.step})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: expected %q, got %v", tc.step.Method, tc.want, err)
		}
	}
	if len(fake.Requests()) != before {
		t.Fatalf("refused helpers sent %d daemon ops", len(fake.Requests())-before)
	}
}

// ─── Ported upstream browser-tern-adopt.test.ts ───────────────────────

func startAdoptWindow(t *testing.T, shown bool, releaseAns *fakeAnswer) *fakeDaemon {
	t.Helper()
	var mu sync.Mutex
	seq := 0
	blocks := []map[string]any{
		{"block": 5, "url": "https://docs.example/a", "title": "Docs A", "owner": nil, "shown": false, "driven": false},
		{"block": 6, "url": "https://docs.example/b", "title": "Docs B", "owner": 3, "shown": true, "driven": true},
		{"block": 7, "url": "https://docs.example/c", "title": "Docs C", "owner": nil, "shown": true, "driven": false},
	}
	return startFakeDaemon(t, func(op map[string]any, _ int64) *fakeAnswer {
		mu.Lock()
		defer mu.Unlock()
		switch op["op"] {
		case "blocks":
			return okAns(map[string]any{"blocks": blocks})
		case "open":
			return okAns(map[string]any{"block": 9, "url": "about:blank"})
		case "events":
			if op["block"] == float64(9) && op["after"] == float64(0) {
				seq++
				return okAns(map[string]any{"events": []map[string]any{{"seq": seq, "type": "loaded", "url": "about:blank"}}, "next": seq, "dropped": 0})
			}
			return okAns(map[string]any{"events": []any{}, "next": seq + 40, "dropped": 0})
		case "state":
			for _, b := range blocks {
				if float64(b["block"].(int)) == op["block"] {
					return okAns(map[string]any{"url": b["url"], "title": b["title"], "loading": false, "shown": shown, "width": 800, "height": 600})
				}
			}
			return okAns(map[string]any{"url": "about:blank", "title": "", "loading": false, "shown": shown, "width": 800, "height": 600})
		case "eval":
			return okAns(map[string]any{"value": map[string]any{"value": []any{}}})
		case "release":
			if releaseAns != nil {
				return releaseAns
			}
			return okAns(map[string]any{})
		default:
			return okAns(map[string]any{})
		}
	}, fakeDaemonOpts{})
}

// upstream: browser-tern-adopt.test.ts: "drives the matching block without opening one, and releases it on close without closing it"
func TestUpstreamAdoptAndRelease(t *testing.T) {
	fake := startAdoptWindow(t, true, nil)
	tab := openFakeTab(t, fake, browser.OpenOptions{Name: "adopt-release", Target: "docs"})
	info := tab.Info()
	if info.TargetID != "7" || info.URL != "https://docs.example/c" || !info.Adopted {
		t.Fatalf("unexpected adopted TabInfo: %+v", info)
	}
	if !strings.Contains(info.Description, "the user's Tern browser block 7 (docked)") {
		t.Fatalf("unexpected adopted description: %q", info.Description)
	}
	if err := tab.Close(context.Background(), false); err != nil {
		t.Fatalf("Close: %v", err)
	}
	ops := fake.Ops()
	if slices.Contains(ops, "open") || slices.Contains(ops, "close") {
		t.Fatalf("adopt must never open or close; ops=%v", ops)
	}
	if ops[len(ops)-1] != "release" {
		t.Fatalf("expected final op release, got %v", ops)
	}
}

// upstream: browser-tern-adopt.test.ts: "clears its scripts instead on a Tern without release" (S5)
func TestUpstreamAdoptReleaseFallbackClearsScripts(t *testing.T) {
	fake := startAdoptWindow(t, true, errAns("invalid", `unknown web call "release"`))
	tab := openFakeTab(t, fake, browser.OpenOptions{Name: "adopt-old", Target: "6"})
	if err := tab.Close(context.Background(), false); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reqs := fake.Requests()
	last := reqs[len(reqs)-1].Op
	if last["op"] != "scripts" || last["block"] != float64(6) || len(last["scripts"].([]any)) != 0 {
		t.Fatalf("expected scripts:[] fallback on block 6, got %+v", last)
	}
	if slices.Contains(fake.Ops(), "close") {
		t.Fatalf("must never close adopted block; ops=%v", fake.Ops())
	}
}

// upstream: browser-tern-adopt.test.ts: "fails a target no block matches, listing the window's blocks, and touches none"
func TestUpstreamAdoptNoMatchTouchesNothing(t *testing.T) {
	fake := startAdoptWindow(t, true, nil)
	b := NewBackend()
	_, err := b.Open(context.Background(), browser.OpenOptions{Endpoint: fake.socketPath, Pane: 3, Name: "none", Target: "nowhere"})
	if err == nil || !strings.Contains(err.Error(), `5 "Docs A" https://docs.example/a (docked, not on screen)`) {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(fake.Ops(), []string{"blocks"}) {
		t.Fatalf("expected only [blocks], got %v", fake.Ops())
	}
}

// ─── Ported upstream browser-tern-dom.test.ts ─────────────────────────

func nativeDomTab(t *testing.T, domFn func(op map[string]any) any, kit map[string]func([]any) any) (*fakeDaemon, *Tab) {
	t.Helper()
	fake := startFakeDaemon(t, func(op map[string]any, _ int64) *fakeAnswer {
		if op["op"] == "dom" {
			return okAns(domFn(op))
		}
		if op["op"] == "eval" {
			args, _ := op["args"].([]any)
			if len(args) > 0 {
				method, _ := args[0].(string)
				if stub := kit[method]; stub != nil {
					var margs []any
					if len(args) > 1 {
						margs, _ = args[1].([]any)
					}
					return okAns(map[string]any{"value": map[string]any{"value": stub(margs)}})
				}
			}
			return errAns("js", "kit not stubbed")
		}
		return okAns(map[string]any{})
	}, fakeDaemonOpts{Welcome: map[string]any{"ops": []string{"dom"}}})

	client, err := ternwire.Dial(context.Background(), fake.socketPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	tab := newTab(NewBackend(), client, 3, 7, "main", browser.Viewport{Width: 800, Height: 600, DeviceScaleFactor: 1}, nil, nil)
	return fake, tab
}

// upstream: browser-tern-dom.test.ts: "maps a snapshot to an observation and resolves its ids to the nodes' refs"
// & "refuses an id the last observe did not list, and one whose element left the page"
func TestUpstreamDomObserveAndIdResolution(t *testing.T) {
	button := map[string]any{"ref": "e1-5", "tag": "button", "role": "button", "name": "Sign in", "states": []string{}, "rect": []float64{20, 10, 40, 20}}
	okRes := map[string]any{"state": "ok", "point": []float64{40, 20}, "rect": []float64{20, 10, 40, 20}, "node": button}
	snap := map[string]any{
		"url": "https://example.test/login", "title": "Login",
		"viewport": map[string]any{"width": 800, "height": 600, "scale": 2},
		"scroll":   map[string]any{"x": 0, "y": 120, "width": 800, "height": 600, "scrollWidth": 800, "scrollHeight": 2400},
		"nodes": []map[string]any{
			{
				"ref": "e1-1", "tag": "form", "role": "form", "name": "Login", "states": []string{}, "rect": []float64{0, 0, 400, 300},
				"children": []map[string]any{
					{"ref": "e1-2", "tag": "input", "role": "checkbox", "name": "Remember me", "states": []string{"checked", "focused"}, "rect": []float64{10, 10, 16, 16}},
					{"role": "text", "name": "or", "rect": []float64{30, 10, 20, 16}},
					{"ref": "e1-3", "tag": "input", "role": "slider", "name": "Volume", "value": "40", "states": []string{"disabled"}, "rect": []float64{10, 40, 100, 16}},
				},
			},
			{"ref": "e1-4", "tag": "button", "role": "button", "name": "Menu", "states": []string{"collapsed", "haspopup", "clickable"}, "rect": []float64{10, 320, 80, 24}},
		},
	}

	fake, tab := nativeDomTab(t, func(req map[string]any) any {
		switch req["read"] {
		case "snapshot":
			return snap
		case "query":
			if req["value"] == "e1-2" {
				return map[string]any{"nodes": []any{}, "total": 0}
			}
			return map[string]any{"nodes": []any{map[string]any{"ref": req["value"], "role": "button", "rect": []float64{0, 0, 10, 10}}}, "total": 1}
		default:
			return okRes
		}
	}, nil)

	res, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "observe", Args: jsonArgs(map[string]any{"includeAll": true})}})
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	obs := res.Value.(browser.Observation)
	if len(obs.Elements) != 4 || obs.Elements[1].States[0] != "checked=true" || obs.Elements[2].Value != float64(40) || obs.Elements[3].States[0] != "expanded=false" {
		t.Fatalf("unexpected observation elements: %+v", obs.Elements)
	}

	// id(4).click()
	if _, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "id", Args: jsonArgs(4)}, {Method: "click"}}); err != nil {
		t.Fatalf("id(4).click: %v", err)
	}
	if slices.Contains(fake.Ops(), "eval") {
		t.Fatalf("native dom click should not call eval; ops=%v", fake.Ops())
	}

	// Unknown id & stale id
	if _, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "id", Args: jsonArgs(9)}, {Method: "click"}}); err == nil || !strings.Contains(err.Error(), "Unknown element id 9") {
		t.Fatalf("expected unknown id 9 error, got %v", err)
	}
	if _, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "id", Args: jsonArgs(2)}, {Method: "click"}}); err == nil || !strings.Contains(err.Error(), "Element id 2 is stale") {
		t.Fatalf("expected stale id 2 error, got %v", err)
	}
}

// upstream: browser-tern-dom.test.ts: "spends one re-centring wheel on a covered element, then names what covers it"
// Also verifies S7: covered element is NOT clicked.
func TestUpstreamDomCoveredElementNotClickedS7(t *testing.T) {
	button := map[string]any{"ref": "e1-5", "tag": "button", "role": "button", "name": "Sign in", "states": []string{}, "rect": []float64{20, 10, 40, 20}}
	fake, tab := nativeDomTab(t, func(req map[string]any) any {
		if req["read"] == "query" {
			return map[string]any{"nodes": []any{button}, "total": 1}
		}
		return map[string]any{
			"state":     "covered",
			"node":      button,
			"point":     []float64{40, 20},
			"scroll":    map[string]any{"at": []float64{400, 300}, "dx": 0, "dy": -80},
			"coveredBy": map[string]any{"ref": "e1-8", "tag": "div", "role": "dialog", "name": "Cookies", "states": []string{}, "rect": []float64{0, 0, 800, 60}},
		}
	}, nil)
	tab.actionBudgetMs = 120
	_, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "click", Args: jsonArgs("#under")}})
	if err == nil || !strings.Contains(err.Error(), `the element is covered by div[role=dialog] "Cookies" (1 match(es))`) {
		t.Fatalf("unexpected covered error: %v", err)
	}
	// Only the single wheel event should have been sent — NEVER a mouse click!
	for _, r := range fake.Requests() {
		if r.Op["op"] == "input" {
			for _, ev := range r.Op["events"].([]any) {
				if ev.(map[string]any)["type"] == "mouse" {
					t.Fatalf("S7 violation: mouse input sent for covered element: %+v", ev)
				}
			}
		}
	}
}

// upstream: browser-tern-dom.test.ts: "reads text trimmed and inner HTML out of the node's outer HTML" & "surfaces the engine's own message for a read it refuses"
func TestUpstreamDomTextHtmlAndEngineError(t *testing.T) {
	_, tab := nativeDomTab(t, func(req map[string]any) any {
		if req["value"] == ":bad(" {
			return map[string]any{"error": map[string]any{"kind": "invalid", "message": "':bad(' is not a valid selector"}}
		}
		return map[string]any{
			"nodes": []any{map[string]any{"ref": "e1-1", "tag": "div", "role": "generic", "text": "  hi\n", "html": `<div title="a>b" hidden><b>hi</b></div>`, "rect": []float64{0, 0, 10, 10}}},
			"total": 1,
		}
	}, nil)

	r1, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "text", Args: jsonArgs("#a")}})
	if err != nil || r1.Value != "hi" {
		t.Fatalf("text: err=%v val=%v", err, r1)
	}
	r2, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "html", Args: jsonArgs("#a")}})
	if err != nil || r2.Value != "<b>hi</b>" {
		t.Fatalf("html: err=%v val=%v", err, r2)
	}
	if _, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "count", Args: jsonArgs(":bad(")}}); err == nil || !strings.Contains(err.Error(), "':bad(' is not a valid selector") {
		t.Fatalf("expected engine error, got %v", err)
	}
}

// upstream: browser-tern-dom.test.ts: domQueryFor cases
func TestUpstreamDomQueryFor(t *testing.T) {
	cases := []struct {
		in   string
		want DomQuery
	}{
		{"pierce/.card", DomQuery{By: "css", Value: ".card"}},
		{"xpath///main//a", DomQuery{By: "xpath", Value: "//main//a"}},
		{"aria/Save changes", DomQuery{By: "role", Name: "Save changes", Exact: true}},
		{`aria/Save[role="button"]`, DomQuery{By: "role", Value: "button", Name: "Save", Exact: true}},
		{`role/button[name="Save" exact]`, DomQuery{By: "role", Value: "button", Name: "save", Exact: true}},
		{"role/link", DomQuery{By: "role", Value: "link"}},
		{"label/Email", DomQuery{By: "label", Value: "Email"}},
		{"placeholder/Search", DomQuery{By: "attribute", Attribute: "placeholder", Value: "Search"}},
		{"testid/submit", DomQuery{By: "attribute", Attribute: "data-testid", Value: "submit", Exact: true}},
	}
	for _, tc := range cases {
		sel, err := ParseSelector(tc.in)
		if err != nil {
			t.Fatalf("ParseSelector(%q): %v", tc.in, err)
		}
		got, err := DomQueryFor(sel)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("DomQueryFor(%q) = %+v (err=%v), want %+v", tc.in, got, err, tc.want)
		}
	}
}

// ─── Safety invariants (S1, S2, S5, S6, lifecycle) ────────────────────

// Safety S1: model-supplied op/block keys in setCookies or emulate.viewport can NEVER override op or block.
func TestSafetyS1TypedOpsPreventBlockOverride(t *testing.T) {
	fake := startPage(t, &fakePageSpec{kit: map[string]func([]any) any{}})
	tab := openFakeTab(t, fake, browser.OpenOptions{})

	// Malicious setCookies payload attempting op:"close", block:12
	if _, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{
		Method: "setCookies",
		Args:   jsonArgs([]map[string]any{{"name": "a", "value": "1", "domain": "example.test", "op": "close", "block": 12}}),
	}}); err != nil {
		t.Fatalf("setCookies: %v", err)
	}
	// Malicious emulate.viewport payload attempting op:"close", block:5
	if _, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{
		Method: "emulate",
		Args:   jsonArgs(map[string]any{"viewport": map[string]any{"width": 900, "height": 700, "op": "close", "block": 5}}),
	}}); err != nil {
		t.Fatalf("emulate viewport: %v", err)
	}

	for _, r := range fake.Requests() {
		if r.Op["op"] == "close" {
			t.Fatalf("S1 violation: unexpected close op sent: %+v", r.Op)
		}
		if blk, ok := r.Op["block"].(float64); ok && blk != 7 {
			t.Fatalf("S1 violation: op sent to foreign block %v: %+v", blk, r.Op)
		}
		if r.Op["op"] == "setCookie" {
			if _, hasNested := r.Op["cookie"].(map[string]any); !hasNested {
				t.Fatalf("A/setCookie: expected nested cookie object, got %+v", r.Op)
			}
		}
	}
}

// Safety S2: cookies() and clearCookies() filter strictly to the current page URL;
// saveState() writes to a file and returns the path (never inline cookies).
func TestSafetyS2CookieFilteringAndSaveStateFile(t *testing.T) {
	var deleted []map[string]any
	fake := startFakeDaemon(t, func(op map[string]any, _ int64) *fakeAnswer {
		switch op["op"] {
		case "open":
			return okAns(map[string]any{"block": 7, "url": "about:blank"})
		case "events":
			return okAns(map[string]any{"events": []map[string]any{{"seq": 1, "type": "loaded", "url": "about:blank"}}, "next": 1})
		case "state":
			return okAns(map[string]any{"url": "https://app.example.test/dashboard", "title": "App", "width": 800, "height": 600})
		case "cookies":
			return okAns(map[string]any{"cookies": []map[string]any{
				{"name": "app_sid", "value": "s1", "domain": "app.example.test", "path": "/", "secure": true},
				{"name": "bank_secret", "value": "s2", "domain": "bank.other.test", "path": "/", "secure": true},
			}})
		case "deleteCookie":
			deleted = append(deleted, op)
			return okAns(map[string]any{})
		default:
			return okAns(map[string]any{})
		}
	}, fakeDaemonOpts{})

	tab := openFakeTab(t, fake, browser.OpenOptions{})
	res, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "cookies"}})
	if err != nil {
		t.Fatalf("cookies: %v", err)
	}
	list := res.Value.([]BrowserCookie)
	if len(list) != 1 || list[0].Name != "app_sid" {
		t.Fatalf("S2 violation: expected only app_sid, got %+v", list)
	}

	if _, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "clearCookies"}}); err != nil {
		t.Fatalf("clearCookies: %v", err)
	}
	if len(deleted) != 1 || deleted[0]["name"] != "app_sid" || deleted[0]["domain"] != "app.example.test" {
		t.Fatalf("S2 violation: clearCookies deleted wrong set: %+v", deleted)
	}

	tmp := t.TempDir()
	stateFile := filepath.Join(tmp, "state.json")
	res, err = tab.Call(context.Background(), browser.CallContext{Cwd: tmp}, []browser.CallStep{{Method: "saveState", Args: jsonArgs("state.json")}})
	if err != nil {
		t.Fatalf("saveState: %v", err)
	}
	if res.Value != stateFile {
		t.Fatalf("expected saveState to return path %q, got %v", stateFile, res.Value)
	}
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("saveState did not write file: %v", err)
	}
}

// Safety S5: Close uses a detached context so a cancelled caller context still closes the PiP.
func TestSafetyS5CloseDetachedContext(t *testing.T) {
	closed := false
	fake := startFakeDaemon(t, func(op map[string]any, _ int64) *fakeAnswer {
		switch op["op"] {
		case "open":
			return okAns(map[string]any{"block": 7, "url": "about:blank"})
		case "events":
			return okAns(map[string]any{"events": []map[string]any{{"seq": 1, "type": "loaded", "url": "about:blank"}}, "next": 1})
		case "close":
			closed = true
			return okAns(map[string]any{})
		default:
			return okAns(map[string]any{})
		}
	}, fakeDaemonOpts{})

	tab := openFakeTab(t, fake, browser.OpenOptions{})
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := tab.Close(cancelled, false); err != nil {
		t.Fatalf("Close with cancelled ctx failed: %v", err)
	}
	if !closed {
		t.Fatal("S5 violation: close op was not sent when caller ctx was cancelled")
	}
}

// N6: Register called twice returns the same SharedBackend without resetting live tabs.
func TestDoubleRegisterPreservesSharedBackend(t *testing.T) {
	Register(nil)
	b1, ok1 := browser.LookupBackend(string(browser.KindTern))
	Register(nil)
	b2, ok2 := browser.LookupBackend(string(browser.KindTern))
	if !ok1 || !ok2 || b1 != b2 {
		t.Fatalf("expected Register twice to keep same SharedBackend, got ok=%v,%v same=%v", ok1, ok2, b1 == b2)
	}
}

// ─── Regression tests for N1, N2, N3, N4, N8 ──────────────────────────

// N1: decode {__omp_fn: src} and {__omp_re: {source, flags}} in evaluate,
// element evaluate, waitForUrl, and route/unroute.
func TestPreludeFnAndRegExpDecodingN1(t *testing.T) {
	var lastEvalFn string
	var lastEvalArgs []any
	fake := startFakeDaemon(t, func(op map[string]any, _ int64) *fakeAnswer {
		switch op["op"] {
		case "open":
			return okAns(map[string]any{"block": 7, "url": "about:blank"})
		case "events":
			return okAns(map[string]any{"events": []map[string]any{{"seq": 1, "type": "loaded", "url": "about:blank"}}, "next": 1})
		case "state":
			return okAns(map[string]any{"url": "https://example.test/items/42?a=1", "title": "Items", "width": 800, "height": 600})
		case "eval":
			fn, _ := op["function"].(string)
			args, _ := op["args"].([]any)
			if strings.Contains(fn, "__ompTernKit") && len(args) > 0 {
				switch args[0] {
				case "count":
					return okAns(map[string]any{"value": map[string]any{"value": 1}})
				case "mark":
					return okAns(map[string]any{"value": map[string]any{"value": "tok1"}})
				case "unmark":
					return okAns(map[string]any{"value": map[string]any{"value": nil}})
				case "frames":
					return okAns(map[string]any{"value": map[string]any{"value": []any{}}})
				}
			}
			lastEvalFn = fn
			lastEvalArgs = args
			return okAns(map[string]any{"value": "Items"})
		default:
			return okAns(map[string]any{})
		}
	}, fakeDaemonOpts{})

	tab := openFakeTab(t, fake, browser.OpenOptions{})
	ctx := context.Background()

	// tab.evaluate(() => document.title)
	res, err := tab.Call(ctx, browser.CallContext{}, []browser.CallStep{{
		Method: "evaluate",
		Args:   jsonArgs(map[string]any{"__omp_fn": "(x) => document.title + x"}, "!"),
	}})
	if err != nil || res.Value != "Items" {
		t.Fatalf("tab.evaluate(__omp_fn): err=%v res=%+v", err, res)
	}
	if lastEvalFn != "(x) => document.title + x" || len(lastEvalArgs) != 1 || lastEvalArgs[0] != "!" {
		t.Fatalf("unexpected eval fn/args: fn=%q args=%+v", lastEvalFn, lastEvalArgs)
	}

	// id(1).evaluate(el => el.tagName)
	res, err = tab.Call(ctx, browser.CallContext{}, []browser.CallStep{
		{Method: "id", Args: jsonArgs(1)},
		{Method: "evaluate", Args: jsonArgs(map[string]any{"__omp_fn": "el => el.tagName"})},
	})
	if err != nil || res.Value != "Items" || !strings.Contains(lastEvalFn, "el => el.tagName") {
		t.Fatalf("id(1).evaluate(__omp_fn): err=%v fn=%q", err, lastEvalFn)
	}

	// waitForUrl(/items\/\d+/i)
	res, err = tab.Call(ctx, browser.CallContext{}, []browser.CallStep{{
		Method: "waitForUrl",
		Args:   jsonArgs(map[string]any{"__omp_re": map[string]any{"source": `ITEMS/\d+`, "flags": "i"}}, map[string]any{"timeout": 500}),
	}})
	if err != nil || res.Value != "https://example.test/items/42?a=1" {
		t.Fatalf("waitForUrl(__omp_re): err=%v res=%+v", err, res)
	}

	// route(/api\/v\d+/gi, {abort: true}) and unroute
	reArg := map[string]any{"__omp_re": map[string]any{"source": `api/v\d+`, "flags": "gi"}}
	if _, err := tab.Call(ctx, browser.CallContext{}, []browser.CallStep{{
		Method: "route",
		Args:   jsonArgs(reArg, map[string]any{"abort": true}),
	}}); err != nil {
		t.Fatalf("route(__omp_re): %v", err)
	}
	rules := tab.netLog.Rules()
	if len(rules) != 1 || rules[0].Source != `api/v\d+` || rules[0].Flags != "i" {
		t.Fatalf("unexpected RegExp route rule: %+v", rules)
	}
	if _, err := tab.Call(ctx, browser.CallContext{}, []browser.CallStep{{Method: "unroute", Args: jsonArgs(reArg)}}); err != nil {
		t.Fatalf("unroute(__omp_re): %v", err)
	}
	if len(tab.netLog.Rules()) != 0 {
		t.Fatalf("expected route removed after unroute(__omp_re), got %+v", tab.netLog.Rules())
	}
}

// N2: String evaluate wraps expressions containing `=>` or `;` inside strings as
// `return (...)`, never treating a string as a function or splitting on string `;`.
func TestStringEvaluateTokenAwareN2(t *testing.T) {
	exprCases := []string{
		`[1, 2].map(x => x * 2)`,
		`document.cookie.split(";").length`,
		"`a;b${1 + 2}`",
		`/foo;bar/.test("foo;bar")`,
		`/* ; */ 42`,
		`(() => { const x = 1; return x; })()`,
	}
	for _, src := range exprCases {
		got := UserSourceFunction(src)
		want := "function () {\nreturn (\n" + src + "\n);\n}"
		if got != want {
			t.Fatalf("N2 expression %q:\n got: %q\nwant: %q", src, got, want)
		}
	}

	stmtCases := []string{
		`const a = 1; return a;`,
		`let x = 2; return x`,
		`const response = await fetch('/api', { method: 'POST' }); return JSON.parse('{')`,
		`foo(); bar()`,
		`return 123`,
	}
	for _, src := range stmtCases {
		got := UserSourceFunction(src)
		want := "function () {\n" + src + "\n}"
		if got != want {
			t.Fatalf("N2 statement %q:\n got: %q\nwant: %q", src, got, want)
		}
	}

	// Verify tab.evaluate("[1,2].map(x => x * 2)") sends wrapped expression, not raw string as function.
	var sentFn string
	fake := startFakeDaemon(t, func(op map[string]any, _ int64) *fakeAnswer {
		switch op["op"] {
		case "open":
			return okAns(map[string]any{"block": 7, "url": "about:blank"})
		case "events":
			return okAns(map[string]any{"events": []map[string]any{{"seq": 1, "type": "loaded", "url": "about:blank"}}, "next": 1})
		case "eval":
			sentFn, _ = op["function"].(string)
			return okAns(map[string]any{"value": []int{2, 4}})
		default:
			return okAns(map[string]any{})
		}
	}, fakeDaemonOpts{})
	tab := openFakeTab(t, fake, browser.OpenOptions{})
	if _, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{
		Method: "evaluate",
		Args:   jsonArgs(`[1, 2].map(x => x * 2)`),
	}}); err != nil {
		t.Fatalf("evaluate string with arrow: %v", err)
	}
	if !strings.HasPrefix(sentFn, "function () {\nreturn (\n") {
		t.Fatalf("N2 violation: arrow expression string was not wrapped in return (...): %q", sentFn)
	}
}

// N3: saveState() without a path sanitises the tab name (`n/../../zz` cannot escape)
// and writes under `<AgentDir>/browser-state/<safe>.json`.
func TestStorageStatePathSanitisationN3(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)

	p := storageStatePath("n/../../zz", "", "/tmp")
	wantDir := filepath.Join(agentDir, "browser-state")
	if filepath.Dir(p) != wantDir {
		t.Fatalf("N3 violation: path escaped browser-state dir: got %q, want dir %q", p, wantDir)
	}
	if filepath.Base(p) != "n_.._.._zz.json" {
		t.Fatalf("unexpected sanitised filename: %q", filepath.Base(p))
	}
	if got := filepath.Base(storageStatePath("..", "", "/tmp")); got != "_.json" {
		t.Fatalf("expected _.json for '..', got %q", got)
	}
	if got := filepath.Base(storageStatePath("", "", "/tmp")); got != "main.json" {
		t.Fatalf("expected main.json for '', got %q", got)
	}
}

// N4: urlPattern widens single leading/trailing `*` to `**` and treats `?` as literal.
func TestURLPatternAndLiteralQuestionMarkN4(t *testing.T) {
	m, err := compileURLMatcher("*example*")
	if err != nil {
		t.Fatalf("compileURLMatcher(*example*): %v", err)
	}
	if !m("https://example.test/") {
		t.Fatal("N4 violation: *example* should match https://example.test/")
	}

	mQ, err := compileURLMatcher("*search?q=1*")
	if err != nil {
		t.Fatalf("compileURLMatcher(*search?q=1*): %v", err)
	}
	if !mQ("https://example.test/search?q=1&p=2") {
		t.Fatal("N4: literal ? should match ?")
	}
	if mQ("https://example.test/searchXq=1") {
		t.Fatal("N4 violation: ? acted as regex wildcard instead of literal")
	}
}

// N8: pdf & harStop create missing parent dirs; Open with Headless=true (or OpenWithShow false) sends show:false.
func TestNestedParentDirsAndParkedOpenN8(t *testing.T) {
	var openReq map[string]any
	fake := startFakeDaemon(t, func(op map[string]any, _ int64) *fakeAnswer {
		switch op["op"] {
		case "open":
			openReq = op
			return okAns(map[string]any{"block": 7, "url": "about:blank"})
		case "events":
			return okAns(map[string]any{"events": []map[string]any{{"seq": 1, "type": "loaded", "url": "about:blank"}}, "next": 1})
		case "pdf":
			return okAns(map[string]any{"data": base64.StdEncoding.EncodeToString([]byte("%PDF-1.4"))})
		default:
			return okAns(map[string]any{})
		}
	}, fakeDaemonOpts{})

	tab := openFakeTab(t, fake, browser.OpenOptions{Show: new(false)})
	if openReq["show"] != false {
		t.Fatalf("N8: expected open request to include show:false when opts.Show=false, got %+v", openReq)
	}

	tmp := t.TempDir()
	pdfRel := filepath.Join("nested", "dir", "out.pdf")
	res, err := tab.Call(context.Background(), browser.CallContext{Cwd: tmp}, []browser.CallStep{{
		Method: "pdf", Args: jsonArgs(map[string]any{"path": pdfRel}),
	}})
	if err != nil {
		t.Fatalf("pdf nested dir: %v", err)
	}
	if _, err := os.Stat(res.Value.(string)); err != nil {
		t.Fatalf("pdf file missing: %v", err)
	}

	if _, err := tab.Call(context.Background(), browser.CallContext{Cwd: tmp}, []browser.CallStep{{Method: "harStart"}}); err != nil {
		t.Fatalf("harStart: %v", err)
	}
	harRel := filepath.Join("nested", "har", "trace.har")
	res, err = tab.Call(context.Background(), browser.CallContext{Cwd: tmp}, []browser.CallStep{{
		Method: "harStop", Args: jsonArgs(map[string]any{"path": harRel}),
	}})
	if err != nil {
		t.Fatalf("harStop nested dir: %v", err)
	}
	if _, err := os.Stat(res.Value.(string)); err != nil {
		t.Fatalf("har file missing: %v", err)
	}
}

// ─── Additional ported upstream cases (N7) ────────────────────────────

// upstream: browser-tern-adopt.test.ts: "matches an id exactly, else a URL/title substring in any case",
// "prefers a block on screen, then the user's own, then the first",
// "never matches a block another omp tab drives"
func TestUpstreamPickTernBlockRanking(t *testing.T) {
	blocks := []BlockEntry{
		{Block: 12, URL: "https://a.example/12"},
		{Block: 4, URL: "https://x.example/", Title: "GitHub Issues"},
	}
	if b := PickTernBlock(blocks, "4", nil); b == nil || b.Block != 4 {
		t.Fatalf("exact id: got %+v", b)
	}
	if b := PickTernBlock(blocks, "github", nil); b == nil || b.Block != 4 {
		t.Fatalf("title substring: got %+v", b)
	}
	if b := PickTernBlock(blocks, "A.EXAMPLE", nil); b == nil || b.Block != 12 {
		t.Fatalf("url substring: got %+v", b)
	}
	if b := PickTernBlock(blocks, "missing", nil); b != nil {
		t.Fatalf("expected nil for missing, got %+v", b)
	}

	hidden := BlockEntry{Block: 1, URL: "https://x.example/", Shown: new(false), Driven: new(false)}
	shownDriven := BlockEntry{Block: 2, URL: "https://x.example/", Shown: new(true), Driven: new(true)}
	shownOwn := BlockEntry{Block: 3, URL: "https://x.example/", Shown: new(true), Driven: new(false)}
	if b := PickTernBlock([]BlockEntry{hidden, shownDriven, shownOwn}, "x.example", nil); b == nil || b.Block != 3 {
		t.Fatalf("expected shownOwn(3), got %+v", b)
	}
	if b := PickTernBlock([]BlockEntry{hidden, shownDriven}, "x.example", nil); b == nil || b.Block != 2 {
		t.Fatalf("expected shownDriven(2), got %+v", b)
	}
	if b := PickTernBlock([]BlockEntry{{Block: 8, URL: "https://x.example/"}, {Block: 9, URL: "https://x.example/"}}, "x.example", nil); b == nil || b.Block != 8 {
		t.Fatalf("expected first(8) for old Tern, got %+v", b)
	}
	if b := PickTernBlock([]BlockEntry{{Block: 1, URL: "https://x.example/", Shown: new(true)}, {Block: 2, URL: "https://x.example/", Shown: new(false)}}, "x.example", map[int]string{1: "docs"}); b == nil || b.Block != 2 {
		t.Fatalf("expected untaken(2), got %+v", b)
	}
	if b := PickTernBlock([]BlockEntry{{Block: 1, URL: "https://x.example/", Shown: new(true)}}, "1", map[int]string{1: "docs"}); b != nil {
		t.Fatalf("expected taken id 1 skipped, got %+v", b)
	}
}

// upstream: browser-tern-adopt.test.ts: "tells the agent a PiP is not on screen and where it shows"
// & "names the user's adopted block and that closing releases it"
func TestUpstreamOpenResultNotes(t *testing.T) {
	fakeHidden := startAdoptWindow(t, false, nil)
	tab := openFakeTab(t, fakeHidden, browser.OpenOptions{Name: "hidden-pip"})
	info := tab.Info()
	if info.Description != "Tern browser picture-in-picture (pane 3)" {
		t.Fatalf("unexpected description: %q", info.Description)
	}
	joined := strings.Join(info.Notes, "\n")
	if !strings.Contains(joined, "Not on screen") || !strings.Contains(joined, "this omp pane (block 3)") {
		t.Fatalf("expected off-screen PiP note, got %q", joined)
	}

	fakeShown := startAdoptWindow(t, true, nil)
	adopted := openFakeTab(t, fakeShown, browser.OpenOptions{Name: "adopted", Target: "Docs C"})
	aInfo := adopted.Info()
	if aInfo.Description != "the user's Tern browser block 7 (docked)" {
		t.Fatalf("unexpected adopted desc: %q", aInfo.Description)
	}
	aNotes := strings.Join(aInfo.Notes, "\n")
	if !strings.Contains(aNotes, "user's own Tern browser block 7") || !strings.Contains(aNotes, "releases it") || strings.Contains(aNotes, "Not on screen") {
		t.Fatalf("unexpected adopted notes: %q", aNotes)
	}
}

// upstream: browser-tern-dom.test.ts: "clicks by resolving the selector natively and pressing the resolved point, with no page script",
// "wheels an element that is out of view into it before resolving again",
// "fails an action whose element stays hidden/disabled/notEditable/unstable/offViewport/detached without sending input",
// "waits natively", "rejects a wait the engine reports as timed out",
// "fills by clicking the resolved point, selecting all through the edit op, then inserting text",
// "marks the engine's node for a helper only the kit provides, and unmarks every candidate"
func TestUpstreamDomClickWheelStatesWaitFillMarkRef(t *testing.T) {
	btn := map[string]any{"ref": "e1-5", "tag": "button", "role": "button", "name": "Sign in", "states": []string{}, "rect": []float64{20, 10, 40, 20}}
	okRes := map[string]any{"state": "ok", "point": []float64{40, 20}, "rect": []float64{20, 10, 40, 20}, "node": btn}

	// 1. Native click + wheel offViewport
	resolves := 0
	fake, tab := nativeDomTab(t, func(req map[string]any) any {
		if req["read"] == "query" {
			return map[string]any{"nodes": []any{btn}, "total": 1}
		}
		resolves++
		if resolves == 1 {
			return map[string]any{"state": "offViewport", "node": btn, "scroll": map[string]any{"at": []float64{400, 300}, "dx": 0, "dy": 900}}
		}
		return okRes
	}, nil)
	if _, err := tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "click", Args: jsonArgs("text/Sign in")}}); err != nil {
		t.Fatalf("native click with offViewport wheel: %v", err)
	}
	var inputs []any
	for _, r := range fake.Requests() {
		if r.Op["op"] == "input" {
			inputs = append(inputs, r.Op["events"])
		}
	}
	if len(inputs) != 2 || inputs[0].([]any)[0].(map[string]any)["type"] != "wheel" || inputs[1].([]any)[0].(map[string]any)["type"] != "mouse" {
		t.Fatalf("expected wheel then mouse input, got %+v", inputs)
	}

	// 2. Non-actionable states never send input
	for _, tc := range []struct{ state, want string }{
		{"hidden", "the element is not visible (1 match(es))"},
		{"disabled", "the element is disabled (1 match(es))"},
		{"notEditable", "the element is not editable (1 match(es))"},
		{"unstable", "the element keeps moving (1 match(es))"},
		{"offViewport", "the element is outside the viewport (1 match(es))"},
		{"detached", "no element matches (wrong page or selector?) (0 match(es))"},
	} {
		f2, t2 := nativeDomTab(t, func(req map[string]any) any {
			if req["read"] == "query" {
				return map[string]any{"nodes": []any{btn}, "total": 1}
			}
			return map[string]any{"state": tc.state, "node": btn}
		}, nil)
		t2.actionBudgetMs = 80
		_, err := t2.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "click", Args: jsonArgs("#x")}})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("state %s: expected %q, got %v", tc.state, tc.want, err)
		}
		if slices.Contains(f2.Ops(), "input") {
			t.Fatalf("state %s sent input unexpectedly", tc.state)
		}
	}

	// 3. Native wait matched & timeout
	_, tWait := nativeDomTab(t, func(req map[string]any) any {
		if req["for"].(map[string]any)["value"] == "#late" {
			return map[string]any{"matched": true, "nodes": []any{btn}}
		}
		return map[string]any{"matched": false}
	}, nil)
	if res, err := tWait.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "waitFor", Args: jsonArgs("#late", map[string]any{"timeout": 500})}}); err != nil || res.Value != true {
		t.Fatalf("native waitFor #late: err=%v res=%+v", err, res)
	}
	if _, err := tWait.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "waitFor", Args: jsonArgs("#never", map[string]any{"timeout": 50})}}); err == nil || !strings.Contains(err.Error(), `tab.waitFor("#never") timed out after 50ms`) {
		t.Fatalf("native waitFor #never: got %v", err)
	}

	// 4. Native fill: click -> edit selectAll -> text input
	inputNode := map[string]any{"ref": "e1-6", "tag": "input", "role": "textbox", "states": []string{"editable"}, "rect": []float64{20, 10, 40, 20}, "attributes": map[string]any{"type": "email"}}
	fFill, tFill := nativeDomTab(t, func(req map[string]any) any {
		if req["read"] == "query" {
			return map[string]any{"nodes": []any{inputNode}, "total": 1}
		}
		return map[string]any{"state": "ok", "point": []float64{40, 20}, "rect": []float64{20, 10, 40, 20}, "node": inputNode}
	}, nil)
	if _, err := tFill.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "fill", Args: jsonArgs("#email", "a@b.test")}}); err != nil {
		t.Fatalf("native fill: %v", err)
	}
	var actionOps []string
	for _, r := range fFill.Requests() {
		if r.Op["op"] != "dom" && r.Op["op"] != "events" {
			actionOps = append(actionOps, r.Op["op"].(string))
		}
	}
	if !reflect.DeepEqual(actionOps, []string{"input", "edit", "input"}) {
		t.Fatalf("expected [input edit input] for native fill, got %v", actionOps)
	}

	// 5. markRef bridge for kit-only select() on native id(1)
	selNode := map[string]any{"ref": "e1-2", "tag": "select", "role": "combobox", "states": []string{}, "rect": []float64{10, 10, 80, 20}}
	marked := false
	var kitOrder []string
	_, tMark := nativeDomTab(t, func(req map[string]any) any {
		switch req["read"] {
		case "snapshot":
			return map[string]any{
				"url": "https://example.test", "title": "", "viewport": map[string]any{"width": 800, "height": 600, "scale": 1},
				"scroll": map[string]any{"x": 0, "y": 0, "width": 800, "height": 600, "scrollWidth": 800, "scrollHeight": 600},
				"nodes":  []any{selNode},
			}
		case "resolve":
			return map[string]any{"state": "ok", "point": []float64{50, 20}, "rect": []float64{10, 10, 80, 20}, "node": selNode}
		default:
			attrs := map[string]any{"name": "state", "class": `a "b"`}
			if marked {
				attrs[HandleAttribute] = "other t2"
			}
			return map[string]any{"nodes": []any{map[string]any{"ref": "e1-2", "tag": "select", "role": "combobox", "attributes": attrs, "rect": []float64{10, 10, 80, 20}}}, "total": 1}
		}
	}, map[string]func([]any) any{
		"markAll": func([]any) any { kitOrder = append(kitOrder, "markAll"); marked = true; return []string{"t1", "t2"} },
		"unmark":  func([]any) any { kitOrder = append(kitOrder, "unmark"); return nil },
		"select":  func([]any) any { kitOrder = append(kitOrder, "select"); return []string{"ca"} },
	})
	if _, err := tMark.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "observe"}}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	res, err := tMark.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "id", Args: jsonArgs(1)}, {Method: "select", Args: jsonArgs("ca")}})
	if err != nil || !reflect.DeepEqual(res.Value, []string{"ca"}) {
		t.Fatalf("id(1).select(ca): err=%v res=%+v", err, res)
	}
	if !reflect.DeepEqual(kitOrder, []string{"markAll", "unmark", "select", "unmark"}) {
		t.Fatalf("unexpected kit bridge order: %v", kitOrder)
	}
}

// upstream: browser-tern-tab.test.ts: "asks a host that watches responsiveness to fail a read at once, and names the way out"
// & "names an unresponsive page as the cause of a timed-out call when the host's state says so"
func TestUpstreamTabUnresponsivePage(t *testing.T) {
	fake := startFakeDaemon(t, func(op map[string]any, _ int64) *fakeAnswer {
		if op["op"] == "dom" {
			return errAns("unresponsive", "dom: the page is unresponsive: a script keeps its web process busy")
		}
		return okAns(map[string]any{})
	}, fakeDaemonOpts{Welcome: map[string]any{"ops": []string{"browser", "dom", "responsive"}}})
	c, err := ternwire.Dial(context.Background(), fake.socketPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	tab := newTab(NewBackend(), c, 3, 7, "main", browser.Viewport{Width: 800, Height: 600}, nil, nil)
	_, err = tab.Call(context.Background(), browser.CallContext{}, []browser.CallStep{{Method: "observe"}})
	var te *ternwire.Error
	if !errors.As(err, &te) || te.Kind != ternwire.ErrUnresponsive || !strings.Contains(te.Message, `In tab "main", tab.reload() or tab.goto() ends that web process`) {
		t.Fatalf("expected unresponsive error with recovery hint, got %v", err)
	}
	for _, r := range fake.Requests() {
		if r.Op["op"] == "dom" && r.Op["failUnresponsive"] != true {
			t.Fatalf("expected failUnresponsive:true on dom op, got %+v", r.Op)
		}
	}
}

// Verify wheel zero-dx serialization (A/input), capture full/scale (A/capture),
// and selector parsing (A13: :visible, bare e5, @e5, aria-ref= e5).
func TestWireShapesInputCaptureAndSelectors(t *testing.T) {
	// A/input: dx=0 must be present in JSON.
	step := InputStep{Type: "wheel", X: 400, Y: 300, DX: 0, DY: 300, Mods: []Modifier{}}
	raw, err := json.Marshal(step)
	if err != nil {
		t.Fatalf("marshal wheel: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if _, hasDX := m["dx"]; !hasDX {
		t.Fatalf("A/input: dx=0 was omitted from wheel step: %s", string(raw))
	}

	// A/capture: fullPage sends `full: true` and `scale`.
	pngMagic := base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	fake := startFakeDaemon(t, func(op map[string]any, _ int64) *fakeAnswer {
		switch op["op"] {
		case "open":
			return okAns(map[string]any{"block": 7, "url": "about:blank"})
		case "events":
			return okAns(map[string]any{"events": []map[string]any{{"seq": 1, "type": "loaded", "url": "about:blank"}}, "next": 1})
		case "capture":
			return okAns(map[string]any{"data": pngMagic})
		default:
			return okAns(map[string]any{})
		}
	}, fakeDaemonOpts{})
	tab := openFakeTab(t, fake, browser.OpenOptions{})
	res, err := tab.Call(context.Background(), browser.CallContext{ScreenshotDir: t.TempDir()}, []browser.CallStep{{Method: "screenshot", Args: jsonArgs(map[string]any{"fullPage": true})}})
	if err != nil || len(res.Images) != 1 {
		t.Fatalf("screenshot fullPage: err=%v res=%+v", err, res)
	}
	for _, r := range fake.Requests() {
		if r.Op["op"] == "capture" {
			if r.Op["full"] != true || r.Op["scale"] != float64(1) {
				t.Fatalf("A/capture: expected full:true and scale:1, got %+v", r.Op)
			}
		}
	}

	// A13 selectors: bare e5, @e5, aria-ref= e5, :visible rejection.
	for _, s := range []string{"e5", "@e5", "aria-ref=e5", "aria-ref= e5"} {
		sel, err := ParseSelector(s)
		if err != nil || sel.Engine != "ariaRef" || sel.Ref != "e5" {
			t.Fatalf("ParseSelector(%q) = %+v err=%v", s, sel, err)
		}
	}
	for _, bad := range []string{"div:visible", "span:hidden", "li:nth-match(1)", "button:near(.x)"} {
		if _, err := ParseSelector(bad); err == nil {
			t.Fatalf("expected Playwright-only selector %q to fail", bad)
		}
	}
}
