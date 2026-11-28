package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// upstream: packages/coding-agent/src/tools/browser/relay/bridge.ts:696-1157, 1990-2392

// ---- extension lifecycle ----------------------------------------------------

// ExtConnected registers a new extension socket; its hello assigns or reuses a browser instance.
// upstream: bridge.ts:712-715
func (b *RelayBridge) ExtConnected(socket RelaySocket, extensionID string) {
	b.mu.Lock()
	if extensionID != "" {
		b.socketExtensionID[socket] = extensionID
	}
	b.logFn("extension socket connected", nil)
	b.mu.Unlock()
}

// ExtClosed handles an extension socket closing.
// upstream: bridge.ts:717-746
func (b *RelayBridge) ExtClosed(socket RelaySocket) {
	b.mu.Lock()
	b.extClosedLocked(socket)
	b.mu.Unlock()
	b.flush()
}

func (b *RelayBridge) extClosedLocked(socket RelaySocket) {
	delete(b.socketExtensionID, socket)
	instanceID, ok := b.socketInstance[socket]
	if !ok {
		return
	}
	delete(b.socketInstance, socket)
	inst := b.instances[instanceID]
	if inst == nil || inst.socket != socket {
		return
	}
	inst.socket = nil
	inst.goneSince = b.now()
	inst.updating = false
	inst.busy = false
	b.rejectPendingExtensionRPCsLocked(instanceID, errors.New("relay extension disconnected"))
	for _, key := range b.tabOrder {
		tab := b.tabs[key]
		if tab == nil || tab.instanceID != instanceID {
			continue
		}
		tab.attached = false
		tab.attaching = nil
		b.resetRuntimeLocked(tab)
		tab.grouped = false
		tab.grouping = false
		tab.ompGroupID = nil
		tab.overlayMounted = false
		clear(tab.screencasts)
	}
	b.groupQueue = slices.DeleteFunc(b.groupQueue, func(t *tabState) bool {
		return t.instanceID == instanceID
	})
}

func (b *RelayBridge) rejectPendingExtensionRPCsLocked(instanceID string, err error) {
	prefix := instanceID + ":"
	for key, pending := range b.pendingRPC {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		pending.timer.Stop()
		delete(b.pendingRPC, key)
		if pending.ch.onSettleLocked != nil {
			pending.ch.onSettleLocked(nil, err)
		}
		pending.ch.reject(err)
	}
}

// ExtMessage processes one JSON message received from an extension socket.
// upstream: bridge.ts:748-812
func (b *RelayBridge) ExtMessage(socket RelaySocket, raw string) {
	var msg ExtToRelayMessage
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		b.mu.Lock()
		b.logFn("dropping malformed extension message", nil)
		b.mu.Unlock()
		return
	}
	if msg.T == "hello" {
		b.mu.Lock()
		b.onHelloLocked(socket, &msg)
		b.mu.Unlock()
		b.flush()
		return
	}
	b.mu.Lock()
	instanceID, ok := b.socketInstance[socket]
	if !ok {
		b.mu.Unlock()
		return
	}
	inst := b.instances[instanceID]
	if inst == nil || inst.socket != socket {
		b.mu.Unlock()
		return
	}
	switch msg.T {
	case "rpcResult":
		key := fmt.Sprintf("%s:%d", instanceID, msg.ID)
		if pending, exists := b.pendingRPC[key]; exists {
			delete(b.pendingRPC, key)
			pending.timer.Stop()
			var err error
			if !msg.OK {
				errMsg := msg.Error
				if errMsg == "" {
					errMsg = "extension rpc failed"
				}
				err = errors.New(errMsg)
			}
			if pending.ch.onSettleLocked != nil {
				pending.ch.onSettleLocked(msg.Result, err)
			}
			if msg.OK {
				pending.ch.resolve(msg.Result)
			} else {
				pending.ch.reject(err)
			}
		} else if late, exists := b.lateRPC[key]; exists {
			delete(b.lateRPC, key)
			late.timer.Stop()
			b.mu.Unlock()
			if msg.OK && late.onResult != nil {
				// Run late cleanup (e.g. closing an orphaned late-created tab) in a goroutine
				// so ExtMessage never deadlocks waiting for its own reply, and trigger
				// updateIdleExtensions only AFTER cleanup finishes.
				resCopy := msg.Result
				go func() {
					late.onResult(resCopy)
					b.mu.Lock()
					noConns := len(b.conns) == 0
					b.mu.Unlock()
					if noConns {
						b.updateIdleExtensions()
					}
				}()
			} else {
				b.mu.Lock()
				noConns := len(b.conns) == 0
				b.mu.Unlock()
				if noConns {
					go b.updateIdleExtensions()
				}
			}
			b.flush()
			return
		}
	case "cdpEvent":
		b.onCdpEventLocked(tabKeyOf(inst.code, msg.TabID), msg.SessionID, msg.Method, msg.Params)
	case "detached":
		b.onTabDetachedLocked(tabKeyOf(inst.code, msg.TabID), msg.Reason, msg.RelayInitiated)
	case "framesHidden":
		b.onFramesHiddenLocked(tabKeyOf(inst.code, msg.TabID), msg.Frames)
	case "tabCreated":
		if msg.Tab != nil {
			b.onTabUpsertLocked(*msg.Tab, instanceID, false)
		}
	case "tabUpdated":
		if msg.Tab != nil {
			b.onTabUpsertLocked(*msg.Tab, instanceID, false)
		}
	case "tabRemoved":
		b.onTabRemovedLocked(tabKeyOf(inst.code, msg.TabID))
	case "call":
		call := msg.Call
		id := msg.ID
		b.mu.Unlock()
		go b.answerCall(socket, id, call)
		return
	case "busy":
		wasBusy := inst.busy
		inst.busy = msg.Busy
		shouldUpdate := wasBusy && !inst.busy && len(b.conns) == 0
		b.mu.Unlock()
		if shouldUpdate {
			go b.updateIdleExtensions()
		}
		b.flush()
		return
	}
	b.mu.Unlock()
	b.flush()
}

// answerCall answers a side-panel call on the socket that made it.
// upstream: bridge.ts:815-847
func (b *RelayBridge) answerCall(socket RelaySocket, id int, call *PanelCall) {
	okVal := true
	result, err := b.runCall(call)
	var reply RelayToExtMessage
	if err != nil {
		f := false
		reply = RelayToExtMessage{
			T:     "callResult",
			ID:    id,
			OK:    &f,
			Error: err.Error(),
		}
	} else {
		reply = RelayToExtMessage{
			T:      "callResult",
			ID:     id,
			OK:     &okVal,
			Result: result,
		}
	}
	b.mu.Lock()
	b.enqueueSendLocked(socket, reply)
	b.mu.Unlock()
	b.flush()
}

func (b *RelayBridge) runCall(call *PanelCall) (any, error) {
	if call == nil {
		return nil, errors.New("unknown panel call null")
	}
	switch call.Op {
	case "collabHosts":
		return b.panelSources.ListHosts()
	case "collabLink":
		if call.InstanceID == "" {
			return nil, errors.New("collabLink requires the host's instanceId")
		}
		url, err := b.panelSources.ControlLink(call.InstanceID)
		if err != nil {
			return nil, err
		}
		return map[string]string{"url": url}, nil
	case "installs":
		return b.panelSources.Installs()
	default:
		raw, _ := json.Marshal(call)
		return nil, fmt.Errorf("unknown panel call %s", raw)
	}
}

// onHelloLocked handles an extension `hello` message.
// upstream: bridge.ts:849-986
func (b *RelayBridge) onHelloLocked(socket RelaySocket, msg *ExtToRelayMessage) {
	instanceID := msg.InstanceID
	if instanceID == "" {
		instanceID = "anon"
	}
	code := InstanceCode(instanceID, msg.BrowserSession)
	inst := b.instances[instanceID]
	if inst == nil {
		inst = &extInstance{
			instanceID: instanceID,
			code:       code,
		}
		b.addInstanceLocked(inst)
	} else if inst.socket != nil && inst.socket != socket {
		for _, key := range b.tabOrder {
			if tab := b.tabs[key]; tab != nil && tab.instanceID == instanceID {
				b.resetRuntimeLocked(tab)
			}
		}
		b.rejectPendingExtensionRPCsLocked(instanceID, extReplacedError{})
		oldSock := inst.socket
		delete(b.socketInstance, oldSock)
		b.enqueueCloseLocked(oldSock)
	}
	if inst.code != code {
		var toRemove []string
		for _, key := range b.tabOrder {
			if tab := b.tabs[key]; tab != nil && tab.instanceID == instanceID {
				toRemove = append(toRemove, key)
			}
		}
		for _, key := range toRemove {
			b.onTabRemovedLocked(key)
		}
		prefix := inst.code + ":"
		for key := range b.closeOnHello {
			if strings.HasPrefix(key, prefix) {
				delete(b.closeOnHello, key)
			}
		}
		inst.code = code
	}
	inst.socket = socket
	inst.goneSince = time.Time{}
	if inst.reconnected != nil {
		close(inst.reconnected)
		inst.reconnected = nil
	}
	b.socketInstance[socket] = instanceID
	inst.extensionID = b.socketExtensionID[socket]
	inst.updating = false
	inst.busy = false
	inst.info = &extHelloInfo{
		userAgent:             msg.UserAgent,
		browserVersion:        msg.BrowserVersion,
		discardedTabsProtocol: msg.DiscardedTabsProtocol,
		ompVersion:            msg.OmpVersion,
		ompBuild:              msg.OmpBuild,
		reloadProtocol:        msg.ReloadProtocol,
		downloadsProtocol:     msg.DownloadsProtocol,
		overlayProtocol:       msg.OverlayProtocol,
		panelProtocol:         msg.PanelProtocol,
	}
	b.lastHelloInstance = instanceID

	seen := make(map[int]struct{}, len(msg.Tabs))
	attachedNow := make(map[int]struct{}, len(msg.AttachedTabIDs))
	for _, tid := range msg.AttachedTabIDs {
		attachedNow[tid] = struct{}{}
	}
	known := make(map[string]struct{})
	for _, key := range b.tabOrder {
		if tab := b.tabs[key]; tab != nil && tab.instanceID == instanceID {
			known[tab.tabKey] = struct{}{}
		}
	}
	for _, snap := range msg.Tabs {
		seen[snap.TabID] = struct{}{}
		b.onTabUpsertLocked(snap, instanceID, true)
	}
	var staleKeys []string
	for _, key := range b.tabOrder {
		tab := b.tabs[key]
		if tab == nil || tab.instanceID != instanceID {
			continue
		}
		if _, ok := seen[tab.tabID]; !ok {
			staleKeys = append(staleKeys, key)
		}
	}
	for _, key := range staleKeys {
		b.onTabRemovedLocked(key)
	}

	// Close tabs whose close failed while this browser was offline.
	// Issue synchronously inside onHelloLocked so pendingRPC records the removeTab
	// BEFORE maybeUpdateExtensionLocked runs at the end of hello.
	prefix := inst.code + ":"
	for key, wantURL := range b.closeOnHello {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		delete(b.closeOnHello, key)
		if closed := b.tabs[key]; closed != nil && closed.url == wantURL {
			_ = b.startCloseTabLocked(key, closed.tabID, inst)
		}
	}
	for _, key := range b.tabOrder {
		tab := b.tabs[key]
		if tab == nil || tab.instanceID != instanceID {
			continue
		}
		wasAttached := tab.attached
		_, tab.attached = attachedNow[tab.tabID]
		tab.attaching = nil
		tab.cancelAttach = nil
		tab.attachCancelled = false
		tab.overlayMounted = false
		if !tab.attached {
			b.forgetChildrenLocked(tab)
		}
		if tab.discarded {
			b.retractTabLocked(tab)
			b.detachIfUnheldLocked(tab.tabKey)
			continue
		}
		if _, wasKnown := known[tab.tabKey]; !wasKnown {
			for _, m := range tabWideSwitches {
				tab.unsettledSwitches[m] = struct{}{}
			}
		}
		unsettled := make([]string, 0, len(tab.unsettledSwitches))
		for m := range tab.unsettledSwitches {
			unsettled = append(unsettled, m)
		}
		clear(tab.unsettledSwitches)
		if tab.attached {
			for _, m := range unsettled {
				b.sendSwitchLocked(tab.tabKey, m, b.switchWantedLocked(tab.tabKey, m))
			}
		}
		if wasAttached && !tab.attached && len(b.sessionHoldersLocked(tab.tabKey)) > 0 {
			t := tab
			go func() {
				ok := b.ensureAttached(t, false)
				b.mu.Lock()
				if !ok {
					b.onTabDetachedLocked(t.tabKey, "reattach_failed", false)
				} else {
					for _, m := range tabWideSwitches {
						if b.switchWantedLocked(t.tabKey, m) {
							b.sendSwitchLocked(t.tabKey, m, true)
						}
					}
				}
				b.mu.Unlock()
				b.flush()
			}()
		}
		if !tab.announced && b.eligibleLocked(tab) {
			b.announceTabLocked(tab)
		}
		if b.claimedLocked(tab.tabKey) {
			b.mountOverlayLocked(tab)
		}
	}
	b.syncGroupingLocked()
	ompVer := ""
	if inst.info.ompVersion != nil {
		ompVer = *inst.info.ompVersion
	}
	b.logFn("extension connected", map[string]any{
		"instanceId": instanceID,
		"tabs":       len(msg.Tabs),
		"version":    msg.BrowserVersion,
		"omp":        ompVer,
	})
	b.maybeUpdateExtensionLocked(inst)
}

// ---- extension self-update --------------------------------------------------

func (b *RelayBridge) outdatedReloadableLocked(inst *extInstance) string {
	if inst.info == nil || inst.info.reloadProtocol != ExtensionReloadProtocolVersion || inst.extensionID == "" {
		return ""
	}
	ompVer := ""
	if inst.info.ompVersion != nil {
		ompVer = *inst.info.ompVersion
	}
	if !IsOmpVersion(ompVer) || !IsOlderOmpVersion(ompVer, b.relayVersion) {
		return ""
	}
	if tried := b.updateTried[inst.instanceID]; tried != nil {
		if _, already := tried[ompVer]; already {
			return ""
		}
	}
	return ompVer
}

func (b *RelayBridge) reloadBlockerLocked(inst *extInstance) string {
	if inst.socket == nil {
		return "disconnected"
	}
	if inst.busy {
		return "extension busy"
	}
	if len(b.conns) > 0 {
		return "CDP clients connected"
	}
	for _, tab := range b.tabs {
		if tab.instanceID == inst.instanceID && (tab.attached || tab.attaching != nil || tab.detaching != nil) {
			return "tabs attached"
		}
	}
	prefix := inst.instanceID + ":"
	for key := range b.pendingRPC {
		if strings.HasPrefix(key, prefix) {
			return "extension calls pending"
		}
	}
	for key := range b.lateRPC {
		if strings.HasPrefix(key, prefix) {
			return "extension calls pending"
		}
	}
	codePrefix := inst.code + ":"
	for key := range b.closeOnHello {
		if strings.HasPrefix(key, codePrefix) {
			return "tab closes queued"
		}
	}
	return ""
}

// RecheckExtensions re-runs update/reload checks for every connected extension.
// upstream: bridge.ts:1033-1035
func (b *RelayBridge) RecheckExtensions() {
	b.mu.Lock()
	for _, id := range b.instanceOrder {
		if inst := b.instances[id]; inst != nil {
			b.maybeUpdateExtensionLocked(inst)
		}
	}
	b.mu.Unlock()
	b.flush()
}

func (b *RelayBridge) maybeUpdateExtensionLocked(inst *extInstance) {
	if inst.updating {
		return
	}
	updater := b.updateExtension
	from := ""
	if updater != nil {
		from = b.outdatedReloadableLocked(inst)
	}
	if updater == nil || from == "" {
		b.maybeReloadIntoInstalledLocked(inst)
		return
	}
	if busy := b.reloadBlockerLocked(inst); busy != "" {
		b.logFn("extension update deferred", map[string]any{
			"instanceId": inst.instanceID,
			"from":       from,
			"reason":     busy,
		})
		return
	}
	sock := inst.socket
	extID := inst.extensionID
	inst.updating = true
	go func() {
		updated, err := updater(extID)
		if err != nil {
			b.mu.Lock()
			b.logFn("extension update failed", map[string]any{"error": err.Error()})
			b.mu.Unlock()
			updated = false
		}
		b.mu.Lock()
		if inst.socket != sock || !inst.updating {
			b.mu.Unlock()
			return
		}
		busy := "files not rewritten"
		if updated {
			busy = b.reloadBlockerLocked(inst)
		}
		if busy != "" {
			inst.updating = false
			b.logFn("extension update stopped", map[string]any{
				"instanceId": inst.instanceID,
				"from":       from,
				"reason":     busy,
			})
			b.mu.Unlock()
			return
		}
		b.reloadLocked(inst, sock, from, b.relayVersion)
		b.mu.Unlock()
		b.flush()
	}()
}

func (b *RelayBridge) maybeReloadIntoInstalledLocked(inst *extInstance) {
	lookup := b.installedStamp
	if lookup == nil || inst.info == nil || inst.socket == nil || inst.extensionID == "" {
		return
	}
	if _, checking := b.stampChecks[inst.instanceID]; checking {
		return
	}
	if inst.info.reloadProtocol != ExtensionReloadProtocolVersion {
		return
	}
	verStr := "unstamped"
	if inst.info.ompVersion != nil {
		verStr = *inst.info.ompVersion
	}
	buildStr := "unbuilt"
	if inst.info.ompBuild != nil {
		buildStr = *inst.info.ompBuild
	}
	running := verStr + "+" + buildStr
	if tried := b.updateTried[inst.instanceID]; tried != nil {
		if _, already := tried[running]; already {
			return
		}
	}
	if b.reloadBlockerLocked(inst) != "" {
		return
	}
	sock := inst.socket
	extID := inst.extensionID
	info := inst.info
	b.stampChecks[inst.instanceID] = struct{}{}
	go func() {
		installed, err := lookup(extID)
		b.mu.Lock()
		delete(b.stampChecks, inst.instanceID)
		if err != nil {
			b.logFn("installed extension lookup failed", map[string]any{"error": err.Error()})
			b.mu.Unlock()
			return
		}
		if installed == nil || inst.socket != sock || inst.updating {
			b.mu.Unlock()
			return
		}
		infoVerMatch := info.ompVersion != nil && *info.ompVersion == installed.OmpVersion
		infoBuildMatch := (info.ompBuild == nil && installed.OmpBuild == nil) ||
			(info.ompBuild != nil && installed.OmpBuild != nil && *info.ompBuild == *installed.OmpBuild)
		if infoVerMatch && infoBuildMatch {
			b.mu.Unlock()
			return
		}
		if busy := b.reloadBlockerLocked(inst); busy != "" {
			b.logFn("extension reload deferred", map[string]any{
				"instanceId": inst.instanceID,
				"from":       running,
				"reason":     busy,
			})
			b.mu.Unlock()
			return
		}
		toBuild := "unbuilt"
		if installed.OmpBuild != nil {
			toBuild = *installed.OmpBuild
		}
		b.reloadLocked(inst, sock, running, installed.OmpVersion+"+"+toBuild)
		b.mu.Unlock()
		b.flush()
	}()
}

func (b *RelayBridge) reloadLocked(inst *extInstance, sock RelaySocket, from, to string) {
	tried := b.updateTried[inst.instanceID]
	if tried == nil {
		tried = make(map[string]struct{})
		b.updateTried[inst.instanceID] = tried
	}
	tried[from] = struct{}{}
	b.enqueueSendLocked(sock, RelayToExtMessage{T: "reload"})
	b.logFn("extension reloading into update", map[string]any{
		"instanceId": inst.instanceID,
		"from":       from,
		"to":         to,
	})
	b.enqueueCloseLocked(sock)
	b.extClosedLocked(sock)
}

func (b *RelayBridge) updateIdleExtensions() {
	awaited := make(map[any]struct{})
	for {
		var busy []chan struct{}
		b.mu.Lock()
		for _, tab := range b.tabs {
			if tab.attaching != nil {
				if _, seen := awaited[tab.attaching]; !seen {
					awaited[tab.attaching] = struct{}{}
					busy = append(busy, tab.attaching.done)
				}
			} else if tab.detaching != nil {
				if _, seen := awaited[tab.detaching]; !seen {
					awaited[tab.detaching] = struct{}{}
					busy = append(busy, tab.detaching.done)
				}
			}
		}
		b.mu.Unlock()
		if len(busy) == 0 {
			break
		}
		for _, ch := range busy {
			<-ch
		}
	}
	b.mu.Lock()
	if len(b.conns) > 0 {
		b.mu.Unlock()
		return
	}
	for _, id := range b.instanceOrder {
		if inst := b.instances[id]; inst != nil {
			b.maybeUpdateExtensionLocked(inst)
		}
	}
	b.mu.Unlock()
	b.flush()
}

// ---- extension events -------------------------------------------------------

func (b *RelayBridge) onCdpEventLocked(tabKey, sourceSessionID, method string, params map[string]any) {
	tab := b.tabs[tabKey]
	if tab == nil {
		return
	}
	if method == "Page.windowOpen" {
		b.keepFrontAppLocked(tab)
	}
	if method == "Page.frameNavigated" && sourceSessionID == "" && tab.overlayMounted && !isChildFrame(params["frame"]) {
		b.mountOverlayLocked(tab)
	}
	if method == "Target.attachedToTarget" {
		if child, ok := params["sessionId"].(string); ok && child != "" {
			tab.setRealSession(child, sourceSessionID, params)
			b.realSessionTabs[child] = tabKey
		}
	} else if method == "Target.detachedFromTarget" {
		if child, ok := params["sessionId"].(string); ok && child != "" {
			tab.deleteRealSession(child)
			delete(b.realSessionTabs, child)
		}
	}
	if sourceSessionID != "" {
		nested, _ := params["sessionId"].(string)
		payloadBytes, err := json.Marshal(map[string]any{
			"sessionId": sourceSessionID,
			"method":    method,
			"params":    params,
		})
		if err != nil {
			return
		}
		payload := string(payloadBytes)
		for _, conn := range b.conns {
			if _, ok := conn.childParents[sourceSessionID]; !ok {
				continue
			}
			b.enqueueSendLocked(conn.socket, payload)
			if nested != "" && method == "Target.attachedToTarget" {
				if _, exists := conn.childParents[nested]; !exists {
					conn.childParents[nested] = sourceSessionID
				}
			} else if nested != "" && method == "Target.detachedFromTarget" {
				delete(conn.childParents, nested)
				delete(conn.explicitChildren, nested)
			}
		}
		return
	}
	if strings.HasPrefix(method, "Runtime.") {
		var createdID int
		hasCreated := false
		if method == "Runtime.executionContextCreated" {
			if ctxObj, ok := params["context"].(map[string]any); ok {
				if idVal, ok := toFloat64(ctxObj["id"]); ok {
					createdID = int(idVal)
					hasCreated = true
				}
			}
		}
		var destroyedID int
		hasDestroyed := false
		if method == "Runtime.executionContextDestroyed" {
			if idVal, ok := toFloat64(params["executionContextId"]); ok {
				destroyedID = int(idVal)
				hasDestroyed = true
			}
		}
		if hasCreated && params != nil {
			tab.setRuntimeContext(createdID, params)
		}
		if hasDestroyed {
			tab.deleteRuntimeContext(destroyedID)
		}
		if method == "Runtime.executionContextsCleared" {
			tab.clearRuntimeContexts()
		}
		for _, conn := range b.conns {
			for _, pageSession := range conn.sessionOrder {
				ref := conn.sessions[pageSession]
				if ref == nil || ref.kind != "page" || ref.tabKey != tabKey {
					continue
				}
				if hasDestroyed {
					delete(ref.runtimeContexts, destroyedID)
				}
				if method == "Runtime.executionContextsCleared" {
					clear(ref.runtimeContexts)
				}
				if ref.runtimeState == runtimeDisabled {
					continue
				}
				if hasCreated {
					if _, already := ref.runtimeContexts[createdID]; already {
						continue
					}
					ref.runtimeContexts[createdID] = struct{}{}
				}
				b.emitLocked(conn, method, params, pageSession)
			}
		}
		return
	}
	if method == "Target.attachedToTarget" || method == "Target.detachedFromTarget" {
		child, ok := params["sessionId"].(string)
		if !ok || child == "" {
			return
		}
		for _, conn := range b.conns {
			var parent string
			if method == "Target.attachedToTarget" {
				parent = conn.childTargetSessionForTab(tabKey)
				if parent != "" {
					if _, exists := conn.childParents[child]; !exists {
						conn.childParents[child] = parent
					}
				}
			} else {
				parent = conn.childParents[child]
				delete(conn.childParents, child)
				delete(conn.explicitChildren, child)
			}
			if parent != "" {
				b.emitLocked(conn, method, params, parent)
			}
		}
		return
	}
	for _, conn := range b.conns {
		for _, pageSession := range conn.sessionsForTab(tabKey, "page") {
			b.emitLocked(conn, method, params, pageSession)
		}
	}
}

func (b *RelayBridge) onFramesHiddenLocked(tabKey string, frames []map[string]any) {
	tab := b.tabs[tabKey]
	if tab == nil || len(frames) == 0 {
		return
	}
	for _, raw := range frames {
		hf, ok := IsHiddenFrame(raw)
		if !ok {
			continue
		}
		clean := HiddenFrame{
			ExtensionID: singleLine(hf.ExtensionID, hiddenFrameFieldMax),
			Path:        singleLine(hf.Path, hiddenFrameFieldMax),
		}
		if hf.Title != "" {
			clean.Title = singleLine(hf.Title, hiddenFrameFieldMax)
		}
		for i, existing := range tab.hiddenFrames {
			if existing.ExtensionID == clean.ExtensionID && existing.Path == clean.Path {
				tab.hiddenFrames = slices.Delete(tab.hiddenFrames, i, i+1)
				break
			}
		}
		tab.hiddenFrames = append(tab.hiddenFrames, clean)
	}
	if len(tab.hiddenFrames) > maxHiddenFrames {
		tab.hiddenFrames = tab.hiddenFrames[len(tab.hiddenFrames)-maxHiddenFrames:]
	}
	for _, conn := range b.conns {
		if _, claimed := conn.claims[tabKey]; !claimed {
			continue
		}
		for _, pageSession := range conn.sessionsForTab(tabKey, "page") {
			b.emitLocked(conn, "OMP.foreignFramesHidden", map[string]any{}, pageSession)
		}
	}
}

func (b *RelayBridge) onTabDetachedLocked(tabKey, reason string, relayInitiated bool) {
	tab := b.tabs[tabKey]
	if tab == nil {
		return
	}
	if relayInitiated {
		if !tab.reattachedAfterDetach {
			tab.attached = false
		}
		return
	}
	b.logFn("tab detached", map[string]any{"tabKey": tabKey, "reason": reason})
	tab.attached = false
	tab.attaching = nil
	b.resetRuntimeLocked(tab)
	b.unmountOverlayLocked(tab)
	cancelledByUser := reason == "canceled_by_user"
	if cancelledByUser {
		tab.refusal = "the user cancelled Chrome's debugging bar"
	}
	b.syncTabGroupingLocked(tab)
	if !tab.closing {
		for _, conn := range b.conns {
			for _, pageSession := range conn.sessionsForTab(tabKey, "page") {
				b.emitLocked(conn, "OMP.debuggerDetached", map[string]any{"reason": reason}, pageSession)
			}
		}
	}
	close(tab.chromeDetach)
	tab.chromeDetach = make(chan struct{})
	b.retractTabLocked(tab)
	if !cancelledByUser && !tab.closing && b.eligibleLocked(tab) && !tab.discarded {
		b.announceTabLocked(tab)
	}
}

func (b *RelayBridge) onTabRemovedLocked(tabKey string) {
	tab := b.tabs[tabKey]
	if tab == nil {
		return
	}
	tab.overlayMounted = false
	b.retractTabLocked(tab)
	b.deleteTabLocked(tabKey)
	for _, conn := range b.conns {
		delete(conn.claims, tabKey)
	}
}

func (b *RelayBridge) onTabUpsertLocked(snap TabSnapshot, instanceID string, silent bool) {
	inst := b.instances[instanceID]
	if inst == nil {
		return
	}
	key := tabKeyOf(inst.code, snap.TabID)
	tab := b.tabs[key]
	becameActive := snap.Active && (tab == nil || !tab.active || tab.windowID != snap.WindowID)
	if tab == nil {
		tab = newTabState(instanceID, inst.code, snap.TabID, snap)
		b.addTabLocked(tab)
	} else {
		if tab.url != snap.URL {
			tab.refusal = ""
		}
		if tab.grouped && tab.ompGroupID != nil && snap.GroupID != *tab.ompGroupID {
			tab.grouped = false
			tab.groupOptOut = true
		}
		tab.update(snap)
		if tab.discarded {
			tab.overlayMounted = false
		}
		if tab.discarded && tab.attaching != nil && !tab.attachCancelled {
			tab.attachGeneration++
			tab.attachCancelled = true
			if tab.cancelAttach != nil {
				tab.cancelAttach()
			}
		}
	}
	if becameActive {
		for _, other := range b.tabs {
			if other != tab && other.instanceID == instanceID && other.windowID == snap.WindowID {
				other.active = false
			}
		}
	}
	if silent {
		return
	}
	eligible := b.eligibleLocked(tab)
	b.syncTabGroupingLocked(tab)
	if eligible && !tab.announced && !tab.discarded {
		b.announceTabLocked(tab)
		return
	}
	if (!eligible || tab.discarded) && tab.announced {
		b.retractTabLocked(tab)
		b.detachIfUnheldLocked(tab.tabKey)
		return
	}
	if eligible && tab.announced {
		for _, conn := range b.conns {
			if !conn.discover {
				continue
			}
			b.emitLocked(conn, "Target.targetInfoChanged", map[string]any{
				"targetInfo": b.tabInfo(tab, tab.attached),
			}, "")
			b.emitLocked(conn, "Target.targetInfoChanged", map[string]any{
				"targetInfo": b.pageInfo(tab, tab.attached),
			}, "")
		}
	}
}

func (b *RelayBridge) announceTabLocked(tab *tabState) {
	tab.announced = true
	for _, conn := range b.conns {
		if !conn.discover {
			continue
		}
		b.emitLocked(conn, "Target.targetCreated", map[string]any{
			"targetInfo": b.tabInfo(tab, tab.attached),
		}, "")
		b.emitLocked(conn, "Target.targetCreated", map[string]any{
			"targetInfo": b.pageInfo(tab, tab.attached),
		}, "")
	}
	b.attachForAutoAttachLocked(tab)
}

func (b *RelayBridge) attachForAutoAttachLocked(tab *tabState) {
	hasAutoAttach := false
	for _, conn := range b.conns {
		if conn.autoAttach {
			hasAutoAttach = true
			break
		}
	}
	if !hasAutoAttach {
		return
	}
	// Fast-path when we can start the attach RPC synchronously under b.mu so
	// callers (including CDPConnected / ExtMessage) observe tab.attaching and
	// the pending attach RPC immediately, matching JS microtask semantics.
	if !tab.discarded && tab.detaching == nil && !tab.attached && b.eligibleLocked(tab) && tab.attaching == nil {
		inst := b.instances[tab.instanceID]
		if inst != nil && inst.socket != nil {
			attempt := b.startAttachAttemptLocked(tab, inst)
			t := tab
			attempt.onSettleLocked = func(ok bool, _ error) {
				b.finishAutoAttachLocked(t, ok)
			}
			return
		}
	}
	t := tab
	go func() {
		ok := b.ensureAttached(t, true)
		b.mu.Lock()
		b.finishAutoAttachLocked(t, ok)
		b.mu.Unlock()
		b.flush()
	}()
}

func (b *RelayBridge) finishAutoAttachLocked(t *tabState, ok bool) {
	if !ok {
		if t.refusal != "" && t.announced && b.tabs[t.tabKey] == t {
			b.retractTabLocked(t)
		}
		return
	}
	if t.discarded || !b.eligibleLocked(t) || b.tabs[t.tabKey] != t {
		b.retractTabLocked(t)
		b.detachIfUnheldLocked(t.tabKey)
		return
	}
	for _, conn := range b.conns {
		if conn.autoAttach {
			b.emitTabAttachedLocked(conn, t)
		}
	}
}

// ---- tab grouping -----------------------------------------------------------

func (b *RelayBridge) groupWorthyLocked(tab *tabState) bool {
	if !b.claimedLocked(tab.tabKey) || !b.eligibleLocked(tab) || tab.pinned || tab.groupOptOut {
		return false
	}
	return tab.grouped || tab.groupID == -1
}

func (b *RelayBridge) syncGroupingLocked() {
	if b.group == nil {
		return
	}
	var worthy []*tabState
	for _, key := range b.tabOrder {
		tab := b.tabs[key]
		if tab != nil && b.groupWorthyLocked(tab) && !tab.grouped && !tab.grouping {
			worthy = append(worthy, tab)
		}
	}
	if len(worthy) > 0 {
		b.requestGroupLocked(worthy)
	}
}

func (b *RelayBridge) syncTabGroupingLocked(tab *tabState) {
	if b.group == nil {
		return
	}
	if b.groupWorthyLocked(tab) {
		if !tab.grouped && !tab.grouping {
			b.requestGroupLocked([]*tabState{tab})
		}
		return
	}
	if tab.grouped {
		tab.grouped = false
		tab.ompGroupID = nil
		inst := b.instances[tab.instanceID]
		if inst != nil {
			req := RelayRpcRequest{Op: "ungroup", TabIDs: []int{tab.tabID}}
			_ = b.startRPCLocked(req, inst, b.rpcTimeout, nil)
		}
	}
}

func (b *RelayBridge) requestGroupLocked(tabs []*tabState) {
	if b.group == nil {
		return
	}
	for _, tab := range tabs {
		tab.grouping = true
		b.groupQueue = append(b.groupQueue, tab)
	}
	if !b.groupDraining {
		b.groupDraining = true
		go b.drainGroupQueue()
	}
}

func (b *RelayBridge) drainGroupQueue() {
	for {
		b.mu.Lock()
		group := b.group
		if group == nil || len(b.groupQueue) == 0 {
			b.groupDraining = false
			b.mu.Unlock()
			b.flush()
			return
		}
		batch := b.groupQueue
		b.groupQueue = nil
		// Split per browser instance while preserving order.
		var instOrder []string
		byInstance := make(map[string][]*tabState)
		for _, tab := range batch {
			if _, ok := byInstance[tab.instanceID]; !ok {
				instOrder = append(instOrder, tab.instanceID)
			}
			byInstance[tab.instanceID] = append(byInstance[tab.instanceID], tab)
		}
		b.mu.Unlock()

		for _, instID := range instOrder {
			tabs := byInstance[instID]
			b.mu.Lock()
			inst := b.instances[instID]
			if inst == nil || inst.socket == nil {
				for _, tab := range tabs {
					tab.grouping = false
				}
				b.mu.Unlock()
				continue
			}
			tabIDs := make([]int, len(tabs))
			for i, t := range tabs {
				tabIDs[i] = t.tabID
			}
			req := RelayRpcRequest{
				Op:     "group",
				TabIDs: tabIDs,
				Title:  group.Title,
				Color:  group.Color,
			}
			p := b.startRPCLocked(req, inst, b.rpcTimeout, nil)
			b.mu.Unlock()
			b.flush()

			raw, err := p.wait()
			b.mu.Lock()
			if err != nil {
				b.logFn("tab grouping failed", map[string]any{"error": err.Error()})
			} else {
				var parsed struct {
					Grouped map[string]any `json:"grouped"`
				}
				_ = json.Unmarshal(raw, &parsed)
				for _, tab := range tabs {
					if parsed.Grouped != nil {
						if gVal, ok := toFloat64(parsed.Grouped[fmt.Sprintf("%d", tab.tabID)]); ok {
							gid := int(gVal)
							tab.grouped = true
							tab.ompGroupID = &gid
						}
					}
				}
				b.logFn("grouped tabs", map[string]any{
					"instanceId": instID,
					"tabIds":     tabIDs,
					"grouped":    parsed.Grouped,
				})
			}
			for _, tab := range tabs {
				tab.grouping = false
				if tab.grouped && !b.groupWorthyLocked(tab) {
					b.syncTabGroupingLocked(tab)
				}
			}
			b.mu.Unlock()
			b.flush()
		}
	}
}
