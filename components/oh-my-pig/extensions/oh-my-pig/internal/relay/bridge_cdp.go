package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// upstream: packages/coding-agent/src/tools/browser/relay/bridge.ts:1158-1989, 2394-2839

// ---- downstream (CDP client) lifecycle --------------------------------------

// CDPConnected registers a downstream CDP WebSocket and returns its connection ID.
// claim is the optional target ID from `/cdp?claim=`.
// upstream: bridge.ts:1164-1179
func (b *RelayBridge) CDPConnected(socket RelaySocket, claim string) int {
	b.mu.Lock()
	b.connSeq++
	conn := newCdpConnection(b.connSeq, socket)
	b.conns[conn.id] = conn
	b.logFn("cdp client connected", map[string]any{"conn": conn.id})
	if claim != "" {
		if key, _, ok := parseTargetID(claim); ok {
			if tab := b.tabs[key]; tab != nil {
				foreignRefusal := tab.refusal != "" && ForeignFrameRefusal.MatchString(tab.refusal)
				if foreignRefusal {
					tab.refusal = ""
				}
				b.claimTabLocked(conn, tab.tabKey)
				if foreignRefusal && !tab.announced && b.eligibleLocked(tab) && !tab.discarded {
					b.announceTabLocked(tab)
				}
			}
		}
	}
	id := conn.id
	b.mu.Unlock()
	b.flush()
	return id
}

// CDPClosed cleans up a disconnected downstream CDP client.
// upstream: bridge.ts:1181-1210
func (b *RelayBridge) CDPClosed(connID int) {
	b.mu.Lock()
	conn := b.conns[connID]
	if conn == nil {
		b.mu.Unlock()
		return
	}
	delete(b.conns, connID)
	touched := make(map[string]struct{})
	departed := make([]*sessionRef, 0, len(conn.sessions))
	for _, sid := range conn.sessionOrder {
		if ref := conn.sessions[sid]; ref != nil {
			touched[ref.tabKey] = struct{}{}
			departed = append(departed, ref)
		}
	}
	clear(conn.sessions)
	conn.sessionOrder = nil
	armedTabs := make(map[string]struct{})
	for _, ref := range departed {
		b.releaseSwitchesLocked(ref)
		if ref.autoAttach {
			armedTabs[ref.tabKey] = struct{}{}
		}
	}
	for tabKey := range armedTabs {
		b.releaseAutoAttachLocked(tabKey)
	}
	for tabKey := range conn.claims {
		delete(conn.claims, tabKey)
		if tab := b.tabs[tabKey]; tab != nil {
			b.syncTabGroupingLocked(tab)
			if !b.claimedLocked(tab.tabKey) {
				b.unmountOverlayLocked(tab)
			}
		}
	}
	prefix := fmt.Sprintf("%d ", conn.id)
	for _, tab := range b.tabs {
		b.endScreencastsLocked(tab, func(k string) bool { return strings.HasPrefix(k, prefix) })
	}
	for tabKey := range touched {
		b.detachIfUnheldLocked(tabKey)
	}
	b.logFn("cdp client closed", map[string]any{"conn": connID})
	noConns := len(b.conns) == 0
	b.mu.Unlock()
	b.flush()
	if noConns {
		go b.updateIdleExtensions()
	}
}

// CDPMessage processes one JSON CDP command from downstream connection connID.
// Synchronous commands (discovery, tab pseudo-sessions, Browser.getVersion, etc.)
// complete before returning; RPC-backed commands launch asynchronously as in omp.
// upstream: bridge.ts:1212-1225
func (b *RelayBridge) CDPMessage(connID int, raw string) {
	var parsed struct {
		ID        *float64       `json:"id"`
		Method    *string        `json:"method"`
		Params    map[string]any `json:"params"`
		SessionID string         `json:"sessionId"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return
	}
	if parsed.ID == nil || parsed.Method == nil {
		return
	}
	msg := cdpCommand{
		ID:        int(*parsed.ID),
		Method:    *parsed.Method,
		Params:    parsed.Params,
		SessionID: parsed.SessionID,
	}
	b.mu.Lock()
	conn := b.conns[connID]
	if conn == nil {
		b.mu.Unlock()
		return
	}
	b.dispatchCDPCommandLocked(conn, msg)
	b.mu.Unlock()
	b.flush()
}

// ---- command routing --------------------------------------------------------

func (b *RelayBridge) dispatchCDPCommandLocked(conn *cdpConnection, msg cdpCommand) {
	sessionID := msg.SessionID
	if sessionID == "" {
		b.handleBrowserCommandLocked(conn, msg, nil)
		return
	}
	if ref := conn.sessions[sessionID]; ref != nil {
		if ref.kind == "tab" {
			b.handleTabSessionCommandLocked(conn, msg, ref)
			return
		}
		if ref.kind == "page" {
			b.handlePageSessionCommandLocked(conn, msg, sessionID, ref)
			return
		}
	}
	if realTabKey, ok := b.realSessionTabs[sessionID]; ok {
		if _, known := conn.childParents[sessionID]; known {
			var beforeReply func()
			if msg.Method == "Target.setAutoAttach" && paramBool(msg.Params, "autoAttach") {
				sid := sessionID
				tk := realTabKey
				beforeReply = func() {
					b.announceChildrenLocked(conn, tk, sid, sid)
				}
			}
			b.forwardToTabLocked(conn, msg, realTabKey, sessionID, beforeReply, nil)
			return
		}
	}
	b.replyErrorLocked(conn, msg, fmt.Sprintf("Unknown session id %s", sessionID), cdpErrorServer)
}

func paramBool(params map[string]any, key string) bool {
	if params == nil {
		return false
	}
	v, _ := params[key].(bool)
	return v
}

func paramString(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	v, _ := params[key].(string)
	return v
}

// handleTabSessionCommandLocked handles commands on a minted ST* tab pseudo-session.
// upstream: bridge.ts:1739-1775
func (b *RelayBridge) handleTabSessionCommandLocked(conn *cdpConnection, msg cdpCommand, ref *sessionRef) {
	switch msg.Method {
	case "Target.setAutoAttach":
		tab := b.tabs[ref.tabKey]
		if tab == nil {
			b.replyErrorLocked(conn, msg, fmt.Sprintf("Tab %s is gone", ref.tabKey), cdpErrorServer)
			return
		}
		pageSession := b.mintSessionLocked(conn, "page", tab)
		b.emitLocked(conn, "Target.attachedToTarget", map[string]any{
			"sessionId":          pageSession,
			"targetInfo":         b.pageInfo(tab, true),
			"waitingForDebugger": false,
		}, msg.SessionID)
		b.replyLocked(conn, msg, map[string]any{})
	case "Runtime.runIfWaitingForDebugger":
		b.replyLocked(conn, msg, map[string]any{})
	case "Target.detachFromTarget":
		if child := paramString(msg.Params, "sessionId"); child != "" {
			b.releaseSessionLocked(conn, child, msg.SessionID)
		}
		b.replyLocked(conn, msg, map[string]any{})
	default:
		b.replyErrorLocked(conn, msg, fmt.Sprintf("'%s' is not supported on a tab target", msg.Method), cdpErrorMethodNotFound)
	}
}

// handlePageSessionCommandLocked handles commands on a minted SP* page pseudo-session.
// upstream: bridge.ts:1256-1341
func (b *RelayBridge) handlePageSessionCommandLocked(conn *cdpConnection, msg cdpCommand, sessionID string, ref *sessionRef) {
	if msg.Method == "Runtime.disable" {
		ref.runtimeState = runtimeDisabled
		ref.runtimeEpoch++
		clear(ref.runtimeContexts)
		ref.runtimeEnabling = nil
		b.replyLocked(conn, msg, map[string]any{})
		return
	}
	if msg.Method != "Runtime.enable" {
		if msg.Method == "Target.setAutoAttach" {
			tabKey := ref.tabKey
			b.serializeSwitchesLocked(tabKey, "Target.setAutoAttach", func() {
				b.mu.Lock()
				if conn.sessions[sessionID] != ref {
					b.replyErrorLocked(conn, msg, fmt.Sprintf("Unknown session id %s", sessionID), cdpErrorServer)
					b.mu.Unlock()
					b.flush()
					return
				}
				previous := ref.autoAttach
				ref.autoAttach = paramBool(msg.Params, "autoAttach")
				var announce func()
				if ref.autoAttach {
					announce = func() {
						if parent := conn.childTargetSessionForTab(tabKey); parent != "" {
							b.announceChildrenLocked(conn, tabKey, "", parent)
						}
					}
				}
				if !ref.autoAttach && b.autoAttachesLocked(tabKey) {
					b.replyLocked(conn, msg, map[string]any{})
					if tab := b.tabs[tabKey]; tab != nil && conn.childTargetSessionForTab(tabKey) == "" {
						b.forgetConnectionChildrenLocked(conn, tab, true)
					}
					b.mu.Unlock()
					b.flush()
					return
				}
				done := make(chan bool, 1)
				b.forwardToTabLocked(conn, msg, tabKey, "", announce, func(ok bool) {
					done <- ok
				})
				b.mu.Unlock()
				b.flush()

				ok := <-done
				b.mu.Lock()
				if !ok {
					ref.autoAttach = previous
					if previous && conn.sessions[sessionID] != ref {
						b.releaseAutoAttachLocked(tabKey)
					}
					b.mu.Unlock()
					b.flush()
					return
				}
				if tab := b.tabs[tabKey]; tab != nil && conn.childTargetSessionForTab(tabKey) == "" {
					b.forgetConnectionChildrenLocked(conn, tab, true)
				}
				b.mu.Unlock()
				b.flush()
			})
			return
		}
		if isTabWideSwitch(msg.Method) {
			method := msg.Method
			tabKey := ref.tabKey
			b.serializeSwitchesLocked(tabKey, method, func() {
				b.mu.Lock()
				_, previous := ref.switches[method]
				if paramBool(msg.Params, "enabled") {
					ref.switches[method] = struct{}{}
				} else {
					delete(ref.switches, method)
				}
				paramsCopy := make(map[string]any, len(msg.Params)+1)
				for k, v := range msg.Params {
					paramsCopy[k] = v
				}
				paramsCopy["enabled"] = b.switchWantedLocked(tabKey, method)
				fwdMsg := msg
				fwdMsg.Params = paramsCopy
				done := make(chan bool, 1)
				b.forwardToTabLocked(conn, fwdMsg, tabKey, "", nil, func(ok bool) {
					done <- ok
				})
				b.mu.Unlock()
				b.flush()

				ok := <-done
				if !ok {
					b.mu.Lock()
					if previous {
						ref.switches[method] = struct{}{}
					} else {
						delete(ref.switches, method)
					}
					if tab := b.tabs[tabKey]; tab != nil {
						tab.unsettledSwitches[method] = struct{}{}
					}
					b.mu.Unlock()
					b.flush()
				}
			})
			return
		}
		b.forwardToTabLocked(conn, msg, ref.tabKey, "", nil, nil)
		return
	}

	// Runtime.enable
	if ref.runtimeEnabling != nil {
		enabling := ref.runtimeEnabling
		go b.awaitEnable(conn, msg, enabling)
		return
	}
	if ref.runtimeState == runtimeEnabled {
		b.replyLocked(conn, msg, map[string]any{})
		return
	}
	prev := ref.runtimeState
	ref.runtimeEpoch++
	epoch := ref.runtimeEpoch
	ref.runtimeState = runtimeEnabled
	enabling := newPromise[struct{}]()
	ref.runtimeEnabling = enabling
	tab := b.tabs[ref.tabKey]
	if tab == nil {
		ref.runtimeState = prev
		ref.runtimeEnabling = nil
		b.replyErrorLocked(conn, msg, fmt.Sprintf("No tab %s", ref.tabKey), cdpErrorServer)
		return
	}
	go func() {
		err := b.ensureRuntimeEnabled(tab)
		b.mu.Lock()
		if err != nil {
			if ref.runtimeEpoch == epoch {
				ref.runtimeState = prev
				clear(ref.runtimeContexts)
			}
			enabling.reject(err)
			b.replyErrorLocked(conn, msg, err.Error(), cdpErrorServer)
		} else {
			if conn.sessions[sessionID] == ref && ref.runtimeEpoch == epoch && ref.runtimeState == runtimeEnabled {
				b.replayRuntimeContextsLocked(conn, sessionID, ref, tab)
			}
			enabling.resolve(struct{}{})
			b.replyLocked(conn, msg, map[string]any{})
		}
		if ref.runtimeEnabling == enabling {
			ref.runtimeEnabling = nil
		}
		b.mu.Unlock()
		b.flush()
	}()
}

func (b *RelayBridge) awaitEnable(conn *cdpConnection, msg cdpCommand, enabling *promise[struct{}]) {
	_, err := enabling.wait()
	b.mu.Lock()
	if err != nil {
		b.replyErrorLocked(conn, msg, err.Error(), cdpErrorServer)
	} else {
		b.replyLocked(conn, msg, map[string]any{})
	}
	b.mu.Unlock()
	b.flush()
}

func (b *RelayBridge) ensureRuntimeEnabled(tab *tabState) error {
	b.mu.Lock()
	if tab.rootRuntimeEnabled {
		b.mu.Unlock()
		return nil
	}
	if tab.rootRuntimeEnabling != nil {
		p := tab.rootRuntimeEnabling
		b.mu.Unlock()
		_, err := p.wait()
		return err
	}
	p := newPromise[struct{}]()
	tab.rootRuntimeEnabling = p
	gen := tab.runtimeGeneration
	inst := b.instances[tab.instanceID]
	b.mu.Unlock()

	if inst == nil {
		err := errors.New("relay extension is not connected")
		b.mu.Lock()
		if tab.rootRuntimeEnabling == p {
			tab.rootRuntimeEnabling = nil
		}
		b.mu.Unlock()
		p.reject(err)
		return err
	}

	_, err := b.sendTabCommand(tab, inst, "Runtime.disable", nil, "")
	if err == nil {
		_, err = b.sendTabCommand(tab, inst, "Runtime.enable", nil, "")
	}

	b.mu.Lock()
	if err == nil && tab.runtimeGeneration == gen {
		tab.rootRuntimeEnabled = true
	}
	if tab.rootRuntimeEnabling == p {
		tab.rootRuntimeEnabling = nil
	}
	b.mu.Unlock()
	if err != nil {
		p.reject(err)
	} else {
		p.resolve(struct{}{})
	}
	return err
}

func (b *RelayBridge) replayRuntimeContextsLocked(conn *cdpConnection, sessionID string, ref *sessionRef, tab *tabState) {
	for _, ctxID := range tab.runtimeContextOrder {
		if _, already := ref.runtimeContexts[ctxID]; already {
			continue
		}
		ref.runtimeContexts[ctxID] = struct{}{}
		b.emitLocked(conn, "Runtime.executionContextCreated", tab.runtimeContexts[ctxID], sessionID)
	}
}

func (b *RelayBridge) announceChildrenLocked(conn *cdpConnection, tabKey, parent, sessionID string) {
	tab := b.tabs[tabKey]
	if tab == nil {
		return
	}
	for _, child := range tab.realSessionOrder {
		known := tab.realSessions[child]
		if known == nil || known.parent != parent {
			continue
		}
		if _, exists := conn.childParents[child]; exists {
			continue
		}
		conn.childParents[child] = sessionID
		b.emitLocked(conn, "Target.attachedToTarget", known.event, sessionID)
	}
}

// ---- browser-level command emulation ----------------------------------------

func (b *RelayBridge) affineInstanceLocked(conn *cdpConnection, nearTargetID string) *extInstance {
	if nearTargetID != "" {
		if key, _, ok := parseTargetID(nearTargetID); ok {
			if idx := strings.IndexByte(key, ':'); idx > 0 {
				code := key[:idx]
				for _, id := range b.instanceOrder {
					if inst := b.instances[id]; inst != nil && inst.code == code {
						return inst
					}
				}
			}
		}
	}
	for key := range conn.claims {
		if tab := b.tabs[key]; tab != nil {
			if inst := b.instances[tab.instanceID]; inst != nil {
				return inst
			}
		}
	}
	return nil
}

func (b *RelayBridge) awaitReconnect(inst *extInstance) {
	b.mu.Lock()
	if inst.socket != nil || inst.goneSince.IsZero() {
		b.mu.Unlock()
		return
	}
	left := time.Until(inst.goneSince.Add(b.affineReconnectWait))
	if left <= 0 {
		b.mu.Unlock()
		return
	}
	if inst.reconnected == nil {
		inst.reconnected = make(chan struct{})
	}
	ch := inst.reconnected
	b.mu.Unlock()

	timer := time.NewTimer(left)
	defer timer.Stop()
	select {
	case <-ch:
	case <-timer.C:
	}
}

func (b *RelayBridge) instanceForNewTabLocked(affine *extInstance) *extInstance {
	if affine != nil && affine.socket != nil {
		return affine
	}
	for _, key := range b.tabOrder {
		tab := b.tabs[key]
		if tab == nil || !tab.active || tab.discarded || !b.eligibleLocked(tab) {
			continue
		}
		if inst := b.instances[tab.instanceID]; inst != nil && inst.socket != nil {
			return inst
		}
	}
	return b.lastHelloLocked()
}

// handleBrowserCommandLocked emulates browser-level CDP commands (`Target.*`, `Browser.*`).
// upstream: bridge.ts:1778-1988
func (b *RelayBridge) handleBrowserCommandLocked(conn *cdpConnection, msg cdpCommand, near *tabState) {
	switch msg.Method {
	case "Browser.getVersion":
		lh := b.lastHelloLocked()
		product := "Chrome/unknown"
		ua := ""
		if lh != nil && lh.info != nil {
			if lh.info.browserVersion != "" {
				product = lh.info.browserVersion
			}
			ua = lh.info.userAgent
		}
		b.replyLocked(conn, msg, map[string]any{
			"protocolVersion": "1.3",
			"product":         product,
			"revision":        "",
			"userAgent":       ua,
			"jsVersion":       "",
		})

	case "Target.getBrowserContexts":
		b.replyLocked(conn, msg, map[string]any{"browserContextIds": []string{}})

	case "Target.getTargets":
		infos := make([]targetInfo, 0, len(b.tabOrder))
		for _, key := range b.tabOrder {
			if tab := b.tabs[key]; tab != nil && b.eligibleLocked(tab) {
				infos = append(infos, b.pageInfo(tab, tab.attached))
			}
		}
		b.replyLocked(conn, msg, map[string]any{"targetInfos": infos})

	case "Target.setDiscoverTargets":
		conn.discover = true
		for _, key := range b.tabOrder {
			tab := b.tabs[key]
			if tab == nil || !b.eligibleLocked(tab) {
				continue
			}
			tab.announced = true
			b.emitLocked(conn, "Target.targetCreated", map[string]any{
				"targetInfo": b.tabInfo(tab, tab.attached),
			}, "")
			b.emitLocked(conn, "Target.targetCreated", map[string]any{
				"targetInfo": b.pageInfo(tab, tab.attached),
			}, "")
		}
		b.replyLocked(conn, msg, map[string]any{})

	case "Target.setAutoAttach":
		conn.autoAttach = true
		var tabs []*tabState
		for _, key := range b.tabOrder {
			tab := b.tabs[key]
			if tab != nil && b.eligibleLocked(tab) && !tab.discarded {
				tabs = append(tabs, tab)
			}
		}
		go func() {
			var wg sync.WaitGroup
			for _, tab := range tabs {
				wg.Add(1)
				go func(t *tabState) {
					defer wg.Done()
					_ = b.ensureAttached(t, true)
				}(tab)
			}
			wg.Wait()
			b.mu.Lock()
			for _, tab := range tabs {
				if !tab.attached || tab.discarded || !b.eligibleLocked(tab) {
					b.retractTabLocked(tab)
					b.detachIfUnheldLocked(tab.tabKey)
					continue
				}
				b.emitTabAttachedLocked(conn, tab)
			}
			for _, key := range b.tabOrder {
				tab := b.tabs[key]
				if tab != nil && tab.announced && tab.discarded && b.eligibleLocked(tab) {
					b.retractTabLocked(tab)
					b.detachIfUnheldLocked(tab.tabKey)
				}
			}
			b.replyLocked(conn, msg, map[string]any{})
			b.mu.Unlock()
			b.flush()
		}()

	case "Target.attachToTarget":
		rawID := paramString(msg.Params, "targetId")
		key, kind, ok := parseTargetID(rawID)
		tab := b.tabs[key]
		if !ok || tab == nil {
			b.replyErrorLocked(conn, msg, fmt.Sprintf("No target with id %v", msg.Params["targetId"]), cdpErrorServer)
			return
		}
		if tab.discarded {
			b.replyErrorLocked(conn, msg, fmt.Sprintf("Cannot attach to discarded tab %s", tab.tabKey), cdpErrorServer)
			return
		}
		go func() {
			attached := b.ensureAttached(tab, false)
			b.mu.Lock()
			if !attached {
				reason := ""
				if tab.refusal != "" {
					reason = ": " + tab.refusal
				}
				b.replyErrorLocked(conn, msg, fmt.Sprintf("Cannot attach to tab %s (%s)%s", tab.tabKey, tab.url, reason), cdpErrorServer)
				b.mu.Unlock()
				b.flush()
				return
			}
			sessionID := b.mintSessionLocked(conn, kind, tab)
			var info targetInfo
			if kind == "tab" {
				info = b.tabInfo(tab, true)
			} else {
				info = b.pageInfo(tab, true)
			}
			b.emitLocked(conn, "Target.attachedToTarget", map[string]any{
				"sessionId":          sessionID,
				"targetInfo":         info,
				"waitingForDebugger": false,
			}, "")
			b.replyLocked(conn, msg, map[string]any{"sessionId": sessionID})
			b.mu.Unlock()
			b.flush()
		}()

	case "Target.detachFromTarget":
		if sid := paramString(msg.Params, "sessionId"); sid != "" {
			b.releaseSessionLocked(conn, sid, "")
		}
		b.replyLocked(conn, msg, map[string]any{})

	case "Target.createTarget":
		targetURL := paramString(msg.Params, "url")
		if targetURL == "" {
			targetURL = "about:blank"
		}
		var affine *extInstance
		if near == nil {
			affine = b.affineInstanceLocked(conn, paramString(msg.Params, "nearTargetId"))
		}
		var activePtr *bool
		if paramBool(msg.Params, "background") {
			f := false
			activePtr = &f
		}
		// If we don't need to await reconnect, issue the createTab RPC synchronously
		// so tests observing pending RPCs immediately after CDPMessage see it right away.
		if affine != nil && affine.socket == nil {
			go b.createTargetAsync(conn, msg, near, affine, targetURL, activePtr)
			return
		}
		var inst *extInstance
		if near != nil {
			inst = b.instances[near.instanceID]
		} else {
			inst = b.instanceForNewTabLocked(affine)
		}
		if inst == nil || inst.socket == nil {
			b.replyErrorLocked(conn, msg, "relay extension is not connected", cdpErrorServer)
			return
		}
		p := b.startCreateTabRPCLocked(inst, targetURL, activePtr)
		b.launchFinishCreateTargetLocked(conn, msg, inst, p)
	case "Target.closeTarget":
		rawID := paramString(msg.Params, "targetId")
		key, _, ok := parseTargetID(rawID)
		if !ok {
			b.replyErrorLocked(conn, msg, fmt.Sprintf("No target with id %v", msg.Params["targetId"]), cdpErrorServer)
			return
		}
		removedTab := b.tabs[key]
		if removedTab == nil {
			b.replyErrorLocked(conn, msg, fmt.Sprintf("No target with id %v", msg.Params["targetId"]), cdpErrorServer)
			return
		}
		inst := b.instances[removedTab.instanceID]
		if inst == nil {
			b.replyErrorLocked(conn, msg, "relay extension is not connected", cdpErrorServer)
			return
		}
		tabID := removedTab.tabID
		removedTab.closing = true
		if inst.socket == nil {
			removedTab.closing = false
			b.closeOnHello[key] = removedTab.url
			b.replyErrorLocked(conn, msg, "relay extension is not connected", cdpErrorServer)
			return
		}
		p := b.startRPCLocked(RelayRpcRequest{Op: "removeTab", TabID: tabID}, inst, b.rpcTimeout, nil)
		p.onSettleLocked = func(_ json.RawMessage, err error) {
			if err != nil {
				if t := b.tabs[key]; t != nil {
					t.closing = false
					b.closeOnHello[key] = t.url
				} else {
					b.closeOnHello[key] = ""
				}
				b.replyErrorLocked(conn, msg, err.Error(), cdpErrorServer)
			} else {
				b.replyLocked(conn, msg, map[string]any{"success": true})
			}
		}

	case "Target.activateTarget":
		rawID := paramString(msg.Params, "targetId")
		key, _, ok := parseTargetID(rawID)
		var activatedTab *tabState
		if ok {
			activatedTab = b.tabs[key]
		}
		if ok && activatedTab != nil {
			inst := b.instances[activatedTab.instanceID]
			if inst == nil {
				b.replyErrorLocked(conn, msg, "relay extension is not connected", cdpErrorServer)
				return
			}
			p := b.startRPCLocked(RelayRpcRequest{Op: "activateTab", TabID: activatedTab.tabID}, inst, b.rpcTimeout, nil)
			go func() {
				_, err := p.wait()
				b.mu.Lock()
				if err != nil {
					b.replyErrorLocked(conn, msg, err.Error(), cdpErrorServer)
				} else {
					b.replyLocked(conn, msg, map[string]any{})
				}
				b.mu.Unlock()
				b.flush()
			}()
			return
		}
		if near != nil {
			b.replyErrorLocked(conn, msg, fmt.Sprintf("No target with id %v", msg.Params["targetId"]), cdpErrorServer)
			return
		}
		b.replyLocked(conn, msg, map[string]any{})

	case "Target.getTargetInfo":
		rawID := paramString(msg.Params, "targetId")
		if rawID != "" {
			if key, kind, ok := parseTargetID(rawID); ok {
				if tab := b.tabs[key]; tab != nil {
					var info targetInfo
					if kind == "tab" {
						info = b.tabInfo(tab, tab.attached)
					} else {
						info = b.pageInfo(tab, tab.attached)
					}
					res := map[string]any{"targetInfo": info}
					if tab.refusal != "" {
						res["refusal"] = tab.refusal
					}
					b.replyLocked(conn, msg, res)
					return
				}
			}
		}
		b.replyLocked(conn, msg, map[string]any{
			"targetInfo": targetInfo{
				TargetID:        "relay-browser",
				Type:            "browser",
				Title:           "",
				URL:             "",
				Attached:        true,
				CanAccessOpener: false,
			},
		})

	case "Browser.close":
		b.logFn("refusing Browser.close from downstream client", map[string]any{"conn": conn.id})
		b.replyLocked(conn, msg, map[string]any{})

	case "Browser.setDownloadBehavior":
		b.replyLocked(conn, msg, map[string]any{})

	case "Target.createBrowserContext":
		b.replyErrorLocked(conn, msg, "Browser contexts are not supported by the omp browser relay", cdpErrorServer)

	default:
		b.replyErrorLocked(conn, msg, fmt.Sprintf("'%s' wasn't found", msg.Method), cdpErrorMethodNotFound)
	}
}

func (b *RelayBridge) startCreateTabRPCLocked(inst *extInstance, targetURL string, active *bool) *promise[json.RawMessage] {
	onLate := func(raw json.RawMessage) {
		var created struct {
			Tab TabSnapshot `json:"tab"`
		}
		if err := json.Unmarshal(raw, &created); err != nil {
			return
		}
		key := tabKeyOf(inst.code, created.Tab.TabID)
		if err := b.closeTab(key, created.Tab.TabID, inst); err != nil {
			b.mu.Lock()
			b.logFn("late relay tab close waits for the next hello", map[string]any{
				"tabId": created.Tab.TabID,
				"error": err.Error(),
			})
			b.mu.Unlock()
		}
	}
	return b.startRPCLocked(RelayRpcRequest{
		Op:     "createTab",
		URL:    targetURL,
		Active: active,
	}, inst, b.rpcTimeout, onLate)
}

func (b *RelayBridge) createTargetAsync(conn *cdpConnection, msg cdpCommand, near *tabState, affine *extInstance, targetURL string, active *bool) {
	b.awaitReconnect(affine)
	b.mu.Lock()
	if b.conns[conn.id] != conn {
		b.mu.Unlock()
		return
	}
	var inst *extInstance
	if near != nil {
		inst = b.instances[near.instanceID]
	} else {
		inst = b.instanceForNewTabLocked(affine)
	}
	if inst == nil || inst.socket == nil {
		b.replyErrorLocked(conn, msg, "relay extension is not connected", cdpErrorServer)
		b.mu.Unlock()
		b.flush()
		return
	}
	p := b.startCreateTabRPCLocked(inst, targetURL, active)
	b.launchFinishCreateTargetLocked(conn, msg, inst, p)
	b.mu.Unlock()
	b.flush()
}

func (b *RelayBridge) launchFinishCreateTargetLocked(conn *cdpConnection, msg cdpCommand, inst *extInstance, p *promise[json.RawMessage]) {
	// Run createTab completion directly in p.after so if the client disconnected
	// before createTab resolved, startCloseTabLocked registers the pending removeTab
	// RPC BEFORE startRPCLocked's goroutine runs updateIdleExtensions().
	p.setAfter(func(raw json.RawMessage, err error) {
		if err != nil {
			b.mu.Lock()
			b.replyErrorLocked(conn, msg, err.Error(), cdpErrorServer)
			b.mu.Unlock()
			b.flush()
			return
		}
		var res struct {
			Tab TabSnapshot `json:"tab"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			b.mu.Lock()
			b.replyErrorLocked(conn, msg, err.Error(), cdpErrorServer)
			b.mu.Unlock()
			b.flush()
			return
		}
		b.mu.Lock()
		b.onTabUpsertLocked(res.Tab, inst.instanceID, false)
		createdKey := tabKeyOf(inst.code, res.Tab.TabID)
		if b.conns[conn.id] != conn {
			b.startCloseTabLocked(createdKey, res.Tab.TabID, inst)
			b.mu.Unlock()
			b.flush()
			return
		}
		b.claimTabLocked(conn, createdKey)
		created := b.tabs[createdKey]
		autoAttach := conn.autoAttach
		b.mu.Unlock()
		b.flush()

		if autoAttach && created != nil {
			go func() {
				attached := b.ensureAttached(created, false)
				b.mu.Lock()
				refusal := created.refusal
				b.mu.Unlock()
				if !attached && refusal != "" {
					_ = b.closeTab(createdKey, created.tabID, inst)
					b.mu.Lock()
					b.replyErrorLocked(conn, msg, fmt.Sprintf("Chrome refused to attach to the new tab: %s", refusal), cdpErrorServer)
					b.mu.Unlock()
					b.flush()
					return
				}
				b.mu.Lock()
				b.replyLocked(conn, msg, map[string]any{"targetId": pageTargetIDFromKey(createdKey)})
				b.mu.Unlock()
				b.flush()
			}()
			return
		}
		b.mu.Lock()
		b.replyLocked(conn, msg, map[string]any{"targetId": pageTargetIDFromKey(createdKey)})
		b.mu.Unlock()
		b.flush()
	})
}

// ---- forwarding & overlay ---------------------------------------------------

// forwardToTabLocked routes a page/child-session CDP command to the extension.
// onDone (optional) is invoked once the command settles (true on success, false on error).
// upstream: bridge.ts:1466-1554
func (b *RelayBridge) forwardToTabLocked(
	conn *cdpConnection,
	msg cdpCommand,
	tabKey string,
	realSessionID string,
	beforeReply func(),
	onDone func(ok bool),
) {
	finish := func(ok bool) {
		if onDone != nil {
			onDone(ok)
		}
	}
	if msg.Method == "Browser.close" {
		b.replyLocked(conn, msg, map[string]any{})
		finish(true)
		return
	}
	if msg.Method == "OMP.claimTarget" {
		b.claimTabLocked(conn, tabKey)
		b.replyLocked(conn, msg, map[string]any{})
		finish(true)
		return
	}
	if msg.Method == "OMP.takeHiddenFrames" {
		frames := []HiddenFrame{}
		if tab := b.tabs[tabKey]; tab != nil && len(tab.hiddenFrames) > 0 {
			frames = append(frames, tab.hiddenFrames...)
			tab.hiddenFrames = nil
		}
		b.replyLocked(conn, msg, map[string]any{"frames": frames})
		finish(true)
		return
	}
	tab := b.tabs[tabKey]
	if tab == nil {
		b.replyErrorLocked(conn, msg, fmt.Sprintf("No tab with key %s", tabKey), cdpErrorServer)
		finish(false)
		return
	}
	inst := b.instances[tab.instanceID]
	if inst == nil || inst.socket == nil {
		b.replyErrorLocked(conn, msg, "relay extension is not connected", cdpErrorServer)
		finish(false)
		return
	}
	if msg.Method == "OMP.downloadPath" {
		b.replyDownloadPathLocked(conn, msg, inst, finish)
		return
	}
	if msg.Method == "Target.createTarget" || msg.Method == "Target.closeTarget" || msg.Method == "Target.activateTarget" {
		b.handleBrowserCommandLocked(conn, msg, tab)
		finish(true)
		return
	}
	if b.readFrontApp != nil && canOpenWindow(msg.Method, msg.Params) {
		if b.now().Before(b.keepingFrontUntil) {
			tab.frontAtInput = nil
		} else {
			tab.frontAtInput = b.readFrontAtInputLocked(b.readFrontApp)
		}
	}

	// Capture/screencast commands coordinate with the page overlay asynchronously;
	// regular commands dispatch their RPC immediately while holding the lock so
	// callers observe deterministic ordering.
	screencastKey := fmt.Sprintf("%d %s", conn.id, msg.SessionID)
	if (msg.Method == "Page.captureScreenshot" || msg.Method == "Page.startScreencast") && tab.overlayMounted {
		if tab.overlayCaptures == 0 && len(tab.screencasts) == 0 {
			tab.overlayHidden = b.overlayRPCLocked(tab, OverlayHide)
		}
		tab.overlayCaptures++
		hideCh := tab.overlayHidden
		hideWait := b.overlayHideWait
		go func() {
			waitSettledWithin(hideCh, hideWait)
			raw, err := b.sendTabCommand(tab, inst, msg.Method, msg.Params, realSessionID)
			b.mu.Lock()
			if err == nil && msg.Method == "Page.startScreencast" && tab.overlayMounted {
				tab.screencasts[screencastKey] = struct{}{}
			}
			tab.overlayCaptures--
			b.showOverlayIfUncapturedLocked(tab)
			b.mu.Unlock()
			b.completeForward(conn, msg, tab, inst, realSessionID, beforeReply, finish, raw, err)
		}()
		return
	}

	p := b.startSendTabCommandLocked(tab, inst, msg.Method, msg.Params, realSessionID)
	go func() {
		raw, err := p.wait()
		if msg.Method == "Page.stopScreencast" {
			b.mu.Lock()
			b.endScreencastsLocked(tab, func(k string) bool { return k == screencastKey })
			b.mu.Unlock()
			b.flush()
		}
		b.completeForward(conn, msg, tab, inst, realSessionID, beforeReply, finish, raw, err)
	}()
}

func (b *RelayBridge) completeForward(
	conn *cdpConnection,
	msg cdpCommand,
	tab *tabState,
	inst *extInstance,
	realSessionID string,
	beforeReply func(),
	finish func(bool),
	raw json.RawMessage,
	err error,
) {
	if err != nil {
		errMsg := err.Error()
		b.mu.Lock()
		waitDetach := realSessionID == "" && tab.attached && detachedCommandErrorRE.MatchString(errMsg)
		detachCh := tab.chromeDetach
		waitDur := b.chromeDetachReport
		b.mu.Unlock()
		if waitDetach {
			waitSettledWithin(detachCh, waitDur)
		}
		b.mu.Lock()
		b.replyErrorLocked(conn, msg, errMsg, cdpErrorServer)
		b.mu.Unlock()
		b.flush()
		finish(false)
		return
	}
	var resp map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &resp)
	}
	if resp == nil {
		resp = map[string]any{}
	}
	b.mu.Lock()
	if msg.Method == "Target.attachToTarget" && msg.SessionID != "" {
		if childSID, ok := resp["sessionId"].(string); ok && childSID != "" {
			conn.childParents[childSID] = msg.SessionID
			conn.explicitChildren[childSID] = struct{}{}
		}
	}
	if beforeReply != nil {
		beforeReply()
	}
	b.replyLocked(conn, msg, resp)
	if realSessionID == "" && tab.overlayMounted {
		if _, claimed := conn.claims[tab.tabKey]; claimed {
			if inp := overlayInputOf(msg.Method, msg.Params); inp != nil && inst.socket != nil {
				b.enqueueSendLocked(inst.socket, RelayToExtMessage{
					T:     "input",
					TabID: tab.tabID,
					Input: inp,
				})
			}
		}
	}
	b.mu.Unlock()
	b.flush()
	finish(true)
}

func waitSettledWithin(ch <-chan struct{}, d time.Duration) {
	select {
	case <-ch:
		return
	default:
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ch:
	case <-timer.C:
	}
}

func (b *RelayBridge) readFrontAtInputLocked(read func() (FrontApp, error)) *frontSnapshot {
	app, err := read()
	if err != nil {
		b.readFrontApp = nil
		b.logFn("could not read the app in front; not holding it in front", map[string]any{"error": err.Error()})
		return nil
	}
	if app == nil {
		return nil
	}
	return &frontSnapshot{app: app, at: b.now()}
}

func (b *RelayBridge) keepFrontAppLocked(tab *tabState) {
	front := tab.frontAtInput
	tab.frontAtInput = nil
	now := b.now()
	if front == nil || now.Before(b.keepingFrontUntil) || now.Sub(front.at) > popupAfterInput {
		return
	}
	until := now.Add(keepFrontDuration)
	b.keepingFrontUntil = until
	app := front.app
	go func() {
		times, err := app.KeepInFront(keepFrontDuration)
		b.mu.Lock()
		if err != nil {
			b.logFn("could not keep the user's app in front", map[string]any{"error": err.Error()})
		} else {
			if times > 0 {
				b.logFn("put the user's app back in front", map[string]any{"times": times})
			}
			if b.keepingFrontUntil.Equal(until) {
				b.keepingFrontUntil = time.Time{}
			}
		}
		b.mu.Unlock()
	}()
}

func (b *RelayBridge) claimTabLocked(conn *cdpConnection, tabKey string) {
	tab := b.tabs[tabKey]
	if tab == nil {
		return
	}
	if _, already := conn.claims[tabKey]; !already {
		conn.claims[tabKey] = struct{}{}
		b.logFn("tab claimed", map[string]any{"conn": conn.id, "tabKey": tabKey})
	}
	b.syncTabGroupingLocked(tab)
	if !tab.overlayMounted {
		b.mountOverlayLocked(tab)
	}
}

func (b *RelayBridge) claimedLocked(tabKey string) bool {
	for _, conn := range b.conns {
		if _, ok := conn.claims[tabKey]; ok {
			return true
		}
	}
	return false
}

// ---- page overlay -----------------------------------------------------------

func (b *RelayBridge) mountOverlayLocked(tab *tabState) {
	inst := b.instances[tab.instanceID]
	if inst == nil || inst.info == nil || inst.info.overlayProtocol != OverlayProtocolVersion {
		return
	}
	tab.overlayMounted = true
	_ = b.overlayRPCLocked(tab, OverlayMount)
	if tab.overlayCaptures > 0 || len(tab.screencasts) > 0 {
		tab.overlayHidden = b.overlayRPCLocked(tab, OverlayHide)
	}
}

func (b *RelayBridge) unmountOverlayLocked(tab *tabState) {
	if !tab.overlayMounted {
		return
	}
	tab.overlayMounted = false
	clear(tab.screencasts)
	_ = b.overlayRPCLocked(tab, OverlayUnmount)
}

func (b *RelayBridge) endScreencastsLocked(tab *tabState, match func(string) bool) {
	any := false
	for k := range tab.screencasts {
		if match(k) {
			delete(tab.screencasts, k)
			any = true
		}
	}
	if any {
		b.showOverlayIfUncapturedLocked(tab)
	}
}

func (b *RelayBridge) showOverlayIfUncapturedLocked(tab *tabState) {
	if tab.overlayMounted && tab.overlayCaptures == 0 && len(tab.screencasts) == 0 {
		_ = b.overlayRPCLocked(tab, OverlayShow)
	}
}

func (b *RelayBridge) overlayRPCLocked(tab *tabState, action OverlayAction) chan struct{} {
	prev := tab.overlayQueue
	done := make(chan struct{})
	tab.overlayQueue = done

	// Fast path: if previous overlay RPC is already done, issue this RPC synchronously
	// under b.mu so callers/tests see deterministic message ordering immediately.
	select {
	case <-prev:
		inst := b.instances[tab.instanceID]
		if inst == nil || inst.socket == nil || inst.info == nil || inst.info.overlayProtocol != OverlayProtocolVersion {
			close(done)
			return done
		}
		p := b.startRPCLocked(RelayRpcRequest{Op: "overlay", TabID: tab.tabID, Action: action}, inst, b.overlayRPCTimeout, nil)
		go func() {
			defer close(done)
			if _, err := p.wait(); err != nil {
				b.mu.Lock()
				b.logFn("overlay rpc failed", map[string]any{
					"tabKey": tab.tabKey,
					"action": string(action),
					"error":  err.Error(),
				})
				b.mu.Unlock()
			}
		}()
		return done
	default:
	}

	go func() {
		defer close(done)
		<-prev
		b.mu.Lock()
		inst := b.instances[tab.instanceID]
		if inst == nil || inst.socket == nil || inst.info == nil || inst.info.overlayProtocol != OverlayProtocolVersion {
			b.mu.Unlock()
			return
		}
		p := b.startRPCLocked(RelayRpcRequest{Op: "overlay", TabID: tab.tabID, Action: action}, inst, b.overlayRPCTimeout, nil)
		b.mu.Unlock()
		b.flush()
		if _, err := p.wait(); err != nil {
			b.mu.Lock()
			b.logFn("overlay rpc failed", map[string]any{
				"tabKey": tab.tabKey,
				"action": string(action),
				"error":  err.Error(),
			})
			b.mu.Unlock()
		}
	}()
	return done
}

// ---- session + attach bookkeeping ------------------------------------------

func (b *RelayBridge) retractTabLocked(tab *tabState) {
	b.forgetChildrenLocked(tab)
	b.endScreencastsLocked(tab, func(string) bool { return true })
	for _, conn := range b.conns {
		tabSessions := conn.sessionsForTab(tab.tabKey, "tab")
		var tabParent string
		if len(tabSessions) > 0 {
			tabParent = tabSessions[0]
		}
		for _, pageSession := range conn.sessionsForTab(tab.tabKey, "page") {
			conn.deleteSession(pageSession)
			b.emitLocked(conn, "Target.detachedFromTarget", map[string]any{
				"sessionId": pageSession,
				"targetId":  pageTargetIDFromKey(tab.tabKey),
			}, tabParent)
		}
		for _, tabSession := range tabSessions {
			conn.deleteSession(tabSession)
			b.emitLocked(conn, "Target.detachedFromTarget", map[string]any{
				"sessionId": tabSession,
				"targetId":  tabTargetIDFromKey(tab.tabKey),
			}, "")
		}
		if conn.discover && tab.announced {
			b.emitLocked(conn, "Target.targetDestroyed", map[string]any{
				"targetId": pageTargetIDFromKey(tab.tabKey),
			}, "")
			b.emitLocked(conn, "Target.targetDestroyed", map[string]any{
				"targetId": tabTargetIDFromKey(tab.tabKey),
			}, "")
		}
	}
	tab.announced = false
}

func (b *RelayBridge) mintSessionLocked(conn *cdpConnection, kind string, tab *tabState) string {
	b.sessionSeq++
	prefix := "SP"
	if kind == "tab" {
		prefix = "ST"
	}
	sessionID := fmt.Sprintf("%s%d.%d.%d", prefix, tab.tabID, conn.id, b.sessionSeq)
	conn.addSession(sessionID, &sessionRef{
		kind:            kind,
		tabKey:          tab.tabKey,
		tabID:           tab.tabID,
		runtimeState:    runtimeDefault,
		runtimeContexts: make(map[int]struct{}),
		switches:        make(map[string]struct{}),
	})
	return sessionID
}

func (b *RelayBridge) releaseSessionLocked(conn *cdpConnection, sessionID, parentSessionID string) {
	ref := conn.sessions[sessionID]
	if ref == nil {
		return
	}
	conn.deleteSession(sessionID)
	tab := b.tabs[ref.tabKey]
	if tab != nil {
		targetKey := fmt.Sprintf("%d %s", conn.id, sessionID)
		b.endScreencastsLocked(tab, func(k string) bool { return k == targetKey })
	}
	b.releaseSwitchesLocked(ref)
	if ref.autoAttach {
		b.releaseAutoAttachLocked(ref.tabKey)
	}
	if ref.kind == "page" && tab != nil && conn.childTargetSessionForTab(ref.tabKey) == "" {
		b.forgetConnectionChildrenLocked(conn, tab, true)
	}
	targetID := pageTargetIDFromKey(ref.tabKey)
	if ref.kind == "tab" {
		targetID = tabTargetIDFromKey(ref.tabKey)
	}
	b.emitLocked(conn, "Target.detachedFromTarget", map[string]any{
		"sessionId": sessionID,
		"targetId":  targetID,
	}, parentSessionID)
	b.detachIfUnheldLocked(ref.tabKey)
}

func (b *RelayBridge) detachIfUnheldLocked(tabKey string) {
	if len(b.sessionHoldersLocked(tabKey)) > 0 {
		return
	}
	tab := b.tabs[tabKey]
	if tab == nil || !tab.attached {
		return
	}
	tab.attached = false
	b.resetRuntimeLocked(tab)
	b.forgetChildrenLocked(tab)
	tab.reattachedAfterDetach = false

	var transitions []chan struct{}
	for _, method := range tabWideSwitches {
		if ch := b.switchChains[tabKey+" "+method]; ch != nil {
			transitions = append(transitions, ch)
		}
	}
	done := newPromise[struct{}]()
	tab.detaching = done

	// If no tab-wide switch transition is in flight, send the detach RPC synchronously
	// so callers/tests immediately see it without scheduling jitter.
	if len(transitions) == 0 {
		inst := b.instances[tab.instanceID]
		if inst == nil || inst.socket == nil {
			tab.detaching = nil
			done.resolve(struct{}{})
			return
		}
		p := b.startRPCLocked(RelayRpcRequest{Op: "detach", TabID: tab.tabID}, inst, b.rpcTimeout, nil)
		go func() {
			_, _ = p.wait()
			b.mu.Lock()
			if tab.detaching == done {
				tab.detaching = nil
			}
			b.mu.Unlock()
			done.resolve(struct{}{})
		}()
		return
	}

	go func() {
		for _, ch := range transitions {
			<-ch
		}
		b.mu.Lock()
		inst := b.instances[tab.instanceID]
		var p *promise[json.RawMessage]
		if inst != nil && inst.socket != nil {
			p = b.startRPCLocked(RelayRpcRequest{Op: "detach", TabID: tab.tabID}, inst, b.rpcTimeout, nil)
		}
		b.mu.Unlock()
		b.flush()
		if p != nil {
			_, _ = p.wait()
		}
		b.mu.Lock()
		if tab.detaching == done {
			tab.detaching = nil
		}
		b.mu.Unlock()
		done.resolve(struct{}{})
	}()
}

func (b *RelayBridge) resetRuntimeLocked(tab *tabState) {
	tab.clearRuntimeContexts()
	tab.rootRuntimeEnabled = false
	tab.rootRuntimeEnabling = nil
	tab.runtimeGeneration++
}

func (b *RelayBridge) forgetChildrenLocked(tab *tabState) {
	for _, conn := range b.conns {
		b.forgetConnectionChildrenLocked(conn, tab, false)
	}
	for _, child := range tab.realSessionOrder {
		delete(b.realSessionTabs, child)
	}
	clear(tab.realSessions)
	tab.realSessionOrder = nil
}

func (b *RelayBridge) sessionHoldersLocked(tabKey string) []*cdpConnection {
	var out []*cdpConnection
	for _, conn := range b.conns {
		if len(conn.sessionsForTab(tabKey, "")) > 0 {
			out = append(out, conn)
		}
	}
	return out
}

func (b *RelayBridge) autoAttachesLocked(tabKey string) bool {
	for _, conn := range b.conns {
		if conn.childTargetSessionForTab(tabKey) != "" {
			return true
		}
	}
	return false
}

func (b *RelayBridge) forgetConnectionChildrenLocked(conn *cdpConnection, tab *tabState, automaticOnly bool) {
	for i := len(tab.realSessionOrder) - 1; i >= 0; i-- {
		child := tab.realSessionOrder[i]
		parent, ok := conn.childParents[child]
		if !ok {
			continue
		}
		if automaticOnly {
			ancestor := child
			for {
				if _, explicit := conn.explicitChildren[ancestor]; explicit {
					break
				}
				next, hasParent := conn.childParents[ancestor]
				if !hasParent {
					break
				}
				ancestor = next
			}
			if _, explicit := conn.explicitChildren[ancestor]; explicit {
				continue
			}
		}
		delete(conn.childParents, child)
		delete(conn.explicitChildren, child)
		b.emitLocked(conn, "Target.detachedFromTarget", map[string]any{"sessionId": child}, parent)
	}
}

func (b *RelayBridge) releaseAutoAttachLocked(tabKey string) {
	b.serializeSwitchesLocked(tabKey, "Target.setAutoAttach", func() {
		b.mu.Lock()
		if b.autoAttachesLocked(tabKey) {
			b.mu.Unlock()
			return
		}
		tab := b.tabs[tabKey]
		var inst *extInstance
		if tab != nil && tab.attached {
			inst = b.instances[tab.instanceID]
		}
		if tab == nil || inst == nil || inst.socket == nil {
			b.mu.Unlock()
			return
		}
		p := b.startSendTabCommandLocked(tab, inst, "Target.setAutoAttach", map[string]any{
			"autoAttach":             false,
			"waitForDebuggerOnStart": false,
			"flatten":                true,
		}, "")
		b.mu.Unlock()
		b.flush()
		_, _ = p.wait()
	})
}

func (b *RelayBridge) switchWantedLocked(tabKey, method string) bool {
	for _, conn := range b.conns {
		for _, ref := range conn.sessions {
			if ref.tabKey == tabKey {
				if _, ok := ref.switches[method]; ok {
					return true
				}
			}
		}
	}
	return false
}

func (b *RelayBridge) releaseSwitchesLocked(ref *sessionRef) {
	for method := range ref.switches {
		b.sendSwitchLocked(ref.tabKey, method, false)
	}
}

func (b *RelayBridge) sendSwitchLocked(tabKey, method string, enabled bool) {
	b.serializeSwitchesLocked(tabKey, method, func() {
		b.mu.Lock()
		if b.switchWantedLocked(tabKey, method) != enabled {
			b.mu.Unlock()
			return
		}
		tab := b.tabs[tabKey]
		if tab == nil {
			b.mu.Unlock()
			return
		}
		// Allow sending while attached OR while detachIfUnheld is waiting on this switch transition.
		var inst *extInstance
		if tab.attached || tab.detaching != nil {
			inst = b.instances[tab.instanceID]
		}
		if inst == nil || inst.socket == nil {
			tab.unsettledSwitches[method] = struct{}{}
			b.mu.Unlock()
			return
		}
		p := b.startRPCLocked(RelayRpcRequest{
			Op:     "send",
			TabID:  tab.tabID,
			Method: method,
			Params: map[string]any{"enabled": enabled},
		}, inst, b.rpcTimeout, nil)
		b.mu.Unlock()
		b.flush()
		if _, err := p.wait(); err != nil {
			b.mu.Lock()
			tab.unsettledSwitches[method] = struct{}{}
			b.mu.Unlock()
		}
	})
}

func (b *RelayBridge) serializeSwitchesLocked(tabKey, method string, run func()) {
	key := tabKey + " " + method
	prev := b.switchChains[key]
	done := make(chan struct{})
	b.switchChains[key] = done
	go func() {
		if prev != nil {
			<-prev
		}
		run()
		b.mu.Lock()
		if b.switchChains[key] == done {
			delete(b.switchChains, key)
		}
		b.mu.Unlock()
		close(done)
	}()
}

func (b *RelayBridge) emitTabAttachedLocked(conn *cdpConnection, tab *tabState) {
	if len(conn.sessionsForTab(tab.tabKey, "tab")) > 0 {
		return
	}
	sessionID := b.mintSessionLocked(conn, "tab", tab)
	b.emitLocked(conn, "Target.attachedToTarget", map[string]any{
		"sessionId":          sessionID,
		"targetInfo":         b.tabInfo(tab, true),
		"waitingForDebugger": false,
	}, "")
}

// ensureAttached ensures chrome.debugger is attached to tab.
// upstream: bridge.ts:2630-2680
func (b *RelayBridge) ensureAttached(tab *tabState, retryAfterDiscard bool) bool {
	for {
		b.mu.Lock()
		if tab.discarded {
			b.mu.Unlock()
			return false
		}
		if tab.detaching != nil {
			d := tab.detaching
			b.mu.Unlock()
			_, _ = d.wait()
			continue
		}
		if tab.attached {
			b.mu.Unlock()
			return true
		}
		if !b.eligibleLocked(tab) {
			b.mu.Unlock()
			return false
		}
		inst := b.instances[tab.instanceID]
		if inst == nil || inst.socket == nil {
			b.mu.Unlock()
			return false
		}
		if tab.attaching != nil {
			if tab.attachCancelled && retryAfterDiscard {
				tab.retryAttachAfterDiscard = true
			}
			att := tab.attaching
			b.mu.Unlock()
			ok, _ := att.wait()
			return ok
		}

		attempt := b.startAttachAttemptLocked(tab, inst)
		b.mu.Unlock()
		b.flush()

		ok, _ := attempt.wait()
		return ok
	}
}

func (b *RelayBridge) startAttachAttemptLocked(tab *tabState, inst *extInstance) *promise[bool] {
	generation := tab.attachGeneration
	attempt := newPromise[bool]()
	tab.attaching = attempt
	tab.cancelAttach = func() {
		attempt.resolve(false)
		if attempt.onSettleLocked != nil {
			cb := attempt.onSettleLocked
			attempt.onSettleLocked = nil
			cb(false, nil)
		}
	}
	p := b.startRPCLocked(RelayRpcRequest{Op: "attach", TabID: tab.tabID}, inst, b.rpcTimeout, nil)
	p.onSettleLocked = func(_ json.RawMessage, err error) {
		var ok bool
		if err != nil {
			b.logFn("attach failed", map[string]any{
				"tabKey": tab.tabKey,
				"url":    tab.url,
				"error":  err.Error(),
			})
			var replaced extReplacedError
			if !errors.As(err, &replaced) && generation == tab.attachGeneration {
				msg := err.Error()
				if msg == "" {
					msg = "Chrome refused the attach"
				}
				tab.refusal = msg
			}
			ok = false
		} else {
			tab.attached = true
			tab.reattachedAfterDetach = true
			if tab.discarded {
				b.detachIfUnheldLocked(tab.tabKey)
			} else if !tab.overlayMounted && b.claimedLocked(tab.tabKey) {
				b.mountOverlayLocked(tab)
			}
			ok = !tab.discarded
		}
		if tab.attaching == attempt {
			tab.attaching = nil
			tab.cancelAttach = nil
			tab.attachCancelled = false
		}
		retry := tab.retryAttachAfterDiscard
		tab.retryAttachAfterDiscard = false
		if retry && !tab.discarded {
			b.attachForAutoAttachLocked(tab)
		}
		if len(b.conns) == 0 {
			b.detachIfUnheldLocked(tab.tabKey)
		}
		attempt.resolve(ok)
		if attempt.onSettleLocked != nil {
			cb := attempt.onSettleLocked
			attempt.onSettleLocked = nil
			cb(ok, nil)
		}
	}
	return attempt
}

// ---- download path & RPC plumbing -------------------------------------------

// replyDownloadPathLocked answers OMP.downloadPath.
// upstream: bridge.ts:2740-2775
func (b *RelayBridge) replyDownloadPathLocked(conn *cdpConnection, msg cdpCommand, inst *extInstance, finish func(bool)) {
	guid, ok := msg.Params["guid"].(string)
	if !ok {
		b.replyErrorLocked(conn, msg, "OMP.downloadPath requires the download's guid", cdpErrorServer)
		finish(false)
		return
	}
	if inst.info == nil || inst.info.downloadsProtocol != DownloadsProtocolVersion {
		b.replyErrorLocked(conn, msg, "the omp relay extension in this browser predates download tracking; run `omp browser-relay install` and reload the extension in chrome://extensions", cdpErrorServer)
		finish(false)
		return
	}
	p := b.startRPCLocked(RelayRpcRequest{Op: "findDownload", GUID: guid}, inst, b.rpcTimeout, nil)
	go func() {
		raw, err := p.wait()
		b.mu.Lock()
		if err != nil {
			b.replyErrorLocked(conn, msg, err.Error(), cdpErrorServer)
			b.mu.Unlock()
			b.flush()
			finish(false)
			return
		}
		var found FoundDownload
		if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &found) != nil || found.Filename == "" || found.State != "complete" {
			b.replyErrorLocked(conn, msg, "Chrome has no completed download for this report", cdpErrorServer)
			b.mu.Unlock()
			b.flush()
			finish(false)
			return
		}
		b.replyLocked(conn, msg, map[string]any{"filePath": found.Filename})
		b.mu.Unlock()
		b.flush()
		finish(true)
	}()
}

func (b *RelayBridge) startSendTabCommandLocked(tab *tabState, inst *extInstance, method string, params map[string]any, sessionID string) *promise[json.RawMessage] {
	pageBound := paramBool(params, "awaitPromise") || method == "Runtime.awaitPromise" || method == "Page.navigate"
	timeout := b.rpcTimeout
	if pageBound {
		timeout = b.pageBoundRPCTimeout
	}
	rawP := b.startRPCLocked(RelayRpcRequest{
		Op:        "send",
		TabID:     tab.tabID,
		SessionID: sessionID,
		Method:    method,
		Params:    params,
	}, inst, timeout, nil)
	out := newPromise[json.RawMessage]()
	go func() {
		res, err := rawP.wait()
		if err != nil {
			var te rpcTimeoutError
			if errors.As(err, &te) {
				out.reject(fmt.Errorf("%s on tab %d: Chrome sent no answer within %dms: %w", method, tab.tabID, timeout.Milliseconds(), err))
				return
			}
			out.reject(err)
			return
		}
		out.resolve(res)
	}()
	return out
}

func (b *RelayBridge) sendTabCommand(tab *tabState, inst *extInstance, method string, params map[string]any, sessionID string) (json.RawMessage, error) {
	b.mu.Lock()
	p := b.startSendTabCommandLocked(tab, inst, method, params, sessionID)
	b.mu.Unlock()
	b.flush()
	return p.wait()
}

func (b *RelayBridge) startRPCLocked(req RelayRpcRequest, inst *extInstance, timeout time.Duration, onLate func(json.RawMessage)) *promise[json.RawMessage] {
	p := newPromise[json.RawMessage]()
	if inst == nil || inst.socket == nil {
		p.reject(errors.New("relay extension is not connected"))
		return p
	}
	if timeout <= 0 {
		timeout = b.rpcTimeout
	}
	b.rpcSeq++
	id := b.rpcSeq
	key := fmt.Sprintf("%s:%d", inst.instanceID, id)

	timer := time.AfterFunc(timeout, func() {
		b.mu.Lock()
		pending, exists := b.pendingRPC[key]
		if !exists {
			b.mu.Unlock()
			return
		}
		delete(b.pendingRPC, key)
		if onLate != nil {
			expTimer := time.AfterFunc(b.lateRPCKeep, func() {
				b.mu.Lock()
				delete(b.lateRPC, key)
				noConns := len(b.conns) == 0
				b.mu.Unlock()
				if noConns {
					go b.updateIdleExtensions()
				}
			})
			b.lateRPC[key] = &lateRPC{onResult: onLate, timer: expTimer}
		}
		err := rpcTimeoutError{msg: fmt.Sprintf("extension rpc '%s' timed out after %dms", req.Op, timeout.Milliseconds())}
		if pending.ch.onSettleLocked != nil {
			pending.ch.onSettleLocked(nil, err)
		}
		b.mu.Unlock()
		b.flush()
		pending.ch.reject(err)
	})

	b.pendingRPC[key] = &pendingRPC{ch: p, timer: timer}

	var clearPtr *bool
	if req.Op == "attach" || req.Op == "send" {
		v := b.claimedLocked(tabKeyOf(inst.code, req.TabID))
		clearPtr = &v
	}

	wire := RelayToExtMessage{
		T:                  "rpc",
		ID:                 id,
		Op:                 req.Op,
		TabID:              req.TabID,
		SessionID:          req.SessionID,
		Method:             req.Method,
		Params:             req.Params,
		ClearForeignFrames: clearPtr,
		URL:                req.URL,
		Active:             req.Active,
		TabIDs:             req.TabIDs,
		Title:              req.Title,
		Color:              req.Color,
		GUID:               req.GUID,
		Action:             req.Action,
	}
	b.enqueueSendLocked(inst.socket, wire)

	go func() {
		_, _ = p.wait()
		p.runAfterIfReady()
		b.mu.Lock()
		noConns := len(b.conns) == 0
		b.mu.Unlock()
		if noConns {
			go b.updateIdleExtensions()
		}
	}()
	return p
}

func (b *RelayBridge) startCloseTabLocked(key string, tabID int, inst *extInstance) *promise[json.RawMessage] {
	tab := b.tabs[key]
	if tab != nil {
		tab.closing = true
	}
	if inst == nil || inst.socket == nil {
		if tab != nil {
			tab.closing = false
			b.closeOnHello[key] = tab.url
		} else {
			b.closeOnHello[key] = ""
		}
		p := newPromise[json.RawMessage]()
		p.reject(errors.New("relay extension is not connected"))
		return p
	}
	p := b.startRPCLocked(RelayRpcRequest{Op: "removeTab", TabID: tabID}, inst, b.rpcTimeout, nil)
	p.onSettleLocked = func(_ json.RawMessage, err error) {
		if err != nil {
			if t := b.tabs[key]; t != nil {
				t.closing = false
				b.closeOnHello[key] = t.url
			} else {
				b.closeOnHello[key] = ""
			}
		}
	}
	return p
}
func (b *RelayBridge) closeTab(key string, tabID int, inst *extInstance) error {
	b.mu.Lock()
	p := b.startCloseTabLocked(key, tabID, inst)
	b.mu.Unlock()
	b.flush()
	_, err := p.wait()
	return err
}
