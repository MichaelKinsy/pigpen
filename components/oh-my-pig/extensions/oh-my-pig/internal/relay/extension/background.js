(() => {
  // omp@v19.1.1/packages/browser-relay/extension/worker/groups.ts
  var relayGroupTitle = null;
  var groupOps = Promise.resolve();
  function isNonEmpty(ids) {
    return ids.length > 0;
  }
  function enqueueGroupOp(fn) {
    const result = groupOps.then(fn, fn);
    groupOps = result.catch(() => {});
    return result;
  }
  async function loadRelayGroupTitle() {
    if (relayGroupTitle)
      return relayGroupTitle;
    const stored = await chrome.storage.session.get({ ompGroupTitle: "" }).catch(() => ({ ompGroupTitle: "" }));
    relayGroupTitle = typeof stored.ompGroupTitle === "string" && stored.ompGroupTitle ? stored.ompGroupTitle : null;
    return relayGroupTitle;
  }
  async function groupableByWindow(tabIds) {
    const byWindow = new Map;
    for (const tabId of tabIds) {
      try {
        const tab = await chrome.tabs.get(tabId);
        if (tab.pinned || tab.id === undefined)
          continue;
        const bucket = byWindow.get(tab.windowId);
        if (bucket)
          bucket.push(tab.id);
        else
          byWindow.set(tab.windowId, [tab.id]);
      } catch {}
    }
    return byWindow;
  }
  var GROUP_COLORS = ["grey", "blue", "red", "yellow", "green", "pink", "purple", "cyan", "orange"];
  function isGroupColor(color) {
    return GROUP_COLORS.some((known) => known === color);
  }
  async function groupRelayTabs(tabIds, title, color) {
    if (!isGroupColor(color))
      throw new Error(`unknown tab group color: ${color}`);
    relayGroupTitle = title;
    chrome.storage.session.set({ ompGroupTitle: title });
    const grouped = {};
    for (const [windowId, ids] of await groupableByWindow(tabIds)) {
      const existing = await chrome.tabGroups.query({ title, windowId });
      let groupId;
      if (existing[0]) {
        groupId = existing[0].id;
        for (const dupe of existing.slice(1)) {
          const dupeTabs = await chrome.tabs.query({ groupId: dupe.id });
          const dupeIds = dupeTabs.map((tab) => tab.id).filter((id) => id !== undefined);
          if (isNonEmpty(dupeIds))
            await chrome.tabs.group({ tabIds: dupeIds, groupId });
        }
        await chrome.tabs.group({ tabIds: ids, groupId });
      } else {
        groupId = await chrome.tabs.group({ tabIds: ids, createProperties: { windowId } });
      }
      await chrome.tabGroups.update(groupId, { title, color });
      for (const id of ids)
        grouped[String(id)] = groupId;
    }
    return { grouped };
  }
  async function restoreRelayGroups() {
    const title = await loadRelayGroupTitle();
    if (!title)
      return;
    const groups = await chrome.tabGroups.query({ title }).catch(() => []);
    for (const group of groups) {
      const tabs = await chrome.tabs.query({ groupId: group.id }).catch(() => []);
      const ids = tabs.map((tab) => tab.id).filter((id) => id !== undefined);
      if (isNonEmpty(ids))
        await chrome.tabs.ungroup(ids).catch(() => {});
    }
  }

  // omp@v19.1.1/packages/coding-agent/src/tools/browser/relay/protocol.ts
  var OVERLAY_PROTOCOL_VERSION = 1;
  var EXTENSION_RELOAD_PROTOCOL_VERSION = 1;
  var FOREIGN_FRAME_REFUSAL = /chrome-extension:\/\/ URL of different extension/i;
  var DOWNLOADS_PROTOCOL_VERSION = 1;

  // omp@v19.1.1/packages/browser-relay/extension/debugger-ownership.ts
  async function ownedDebuggerTabs(targets, probe) {
    const owned = await Promise.all(targets.map(async (target) => {
      if (!target.attached || target.tabId === undefined)
        return;
      try {
        await probe(target.tabId);
        return target.tabId;
      } catch {
        return;
      }
    }));
    return owned.filter((tabId) => tabId !== undefined);
  }

  // omp@v19.1.1/packages/browser-relay/extension/messages.ts
  var NATIVE_HOST = "dev.pi_in_go.oh_my_pig.browser";

  // omp@v19.1.1/packages/browser-relay/extension/worker/calls.ts
  var RELAY_CALL_TIMEOUT_MS = 15000;

  class RelayCalls {
    #pending = new Map;
    #timeoutMs;
    #nextId = 1;
    constructor(timeoutMs = RELAY_CALL_TIMEOUT_MS) {
      this.#timeoutMs = timeoutMs;
    }
    start(call, send) {
      const id = this.#nextId++;
      const { promise, resolve, reject } = Promise.withResolvers();
      if (!send({ t: "call", id, call })) {
        reject(new Error("omp relay is not connected"));
        return promise;
      }
      const timer = setTimeout(() => {
        this.#pending.delete(id);
        reject(new Error(`omp relay did not answer ${call.op} within ${this.#timeoutMs / 1000}s`));
      }, this.#timeoutMs);
      this.#pending.set(id, { resolve, reject, timer });
      return promise;
    }
    settle(message) {
      const pending = this.#pending.get(message.id);
      if (!pending)
        return;
      this.#pending.delete(message.id);
      clearTimeout(pending.timer);
      if (message.ok)
        pending.resolve(message.result);
      else
        pending.reject(new Error(message.error ?? "omp relay call failed"));
    }
    failAll(reason) {
      for (const pending of this.#pending.values()) {
        clearTimeout(pending.timer);
        pending.reject(new Error(reason));
      }
      this.#pending.clear();
    }
  }

  // omp@v19.1.1/packages/browser-relay/extension/worker/identity.ts
  var INSTANCE_KEY = "relayInstanceId";
  var BROWSER_SESSION_KEY = "relayBrowserSession";
  var instanceId = null;
  function ensureInstanceId() {
    instanceId ??= (async () => {
      const stored = await chrome.storage.local.get({ [INSTANCE_KEY]: "" });
      const existing = stored[INSTANCE_KEY];
      if (typeof existing === "string" && existing.length > 0)
        return existing;
      const created = crypto.randomUUID();
      await chrome.storage.local.set({ [INSTANCE_KEY]: created });
      return created;
    })();
    return instanceId;
  }
  var browserSession = null;
  function ensureBrowserSession() {
    browserSession ??= (async () => {
      const stored = await chrome.storage.session.get({ [BROWSER_SESSION_KEY]: "" });
      const existing = stored[BROWSER_SESSION_KEY];
      if (typeof existing === "string" && existing.length > 0)
        return existing;
      const created = crypto.randomUUID();
      await chrome.storage.session.set({ [BROWSER_SESSION_KEY]: created });
      return created;
    })();
    return browserSession;
  }

  // omp@v19.1.1/packages/browser-relay/extension/worker/tabs.ts
  var REMOVE_TAB_ATTEMPTS = 3;
  var REMOVE_TAB_RETRY_MS = 500;
  function snapshot(tab) {
    if (tab.id === undefined)
      return null;
    return {
      tabId: tab.id,
      url: tab.url ?? tab.pendingUrl ?? "",
      title: tab.title ?? "",
      active: tab.active,
      discarded: tab.discarded === true,
      windowId: tab.windowId,
      pinned: tab.pinned,
      groupId: tab.groupId
    };
  }
  async function removeTab(tabId) {
    for (let attempt = 1;; attempt++) {
      try {
        await chrome.tabs.remove(tabId);
        return;
      } catch (error) {
        if (/No tab with id/.test(String(error)))
          return;
        if (attempt >= REMOVE_TAB_ATTEMPTS)
          throw error;
        const pause = Promise.withResolvers();
        setTimeout(pause.resolve, REMOVE_TAB_RETRY_MS);
        await pause.promise;
      }
    }
  }
  async function focusTab(tabId) {
    const tab = await chrome.tabs.get(tabId);
    await chrome.windows.update(tab.windowId, { focused: true });
    await chrome.tabs.update(tabId, { active: true });
  }

  // omp@v19.1.1/packages/browser-relay/extension/worker/relay.ts
  var RECONNECT_MIN_MS = 1000;
  var RECONNECT_MAX_MS = 1e4;
  var LINK_DEADLINE_MS = 15000;

  class Link {
    calls = new RelayCalls;
    #open = true;
    get open() {
      return this.#open;
    }
    close(reason) {
      this.#open = false;
      this.calls.failAll(reason);
    }
  }
  var handlers = null;
  var hostPort = null;
  var link = null;
  var hostError;
  var reconnectDelay = RECONNECT_MIN_MS;
  var reconnectTimer = null;
  var deadlineTimer = null;
  var busyReasons = new Set;
  function post(message) {
    if (!link || !hostPort)
      return false;
    const frame = { t: "relay", line: JSON.stringify(message) };
    try {
      hostPort.postMessage(frame);
      return true;
    } catch {
      return false;
    }
  }
  function setBusy(reason, busy) {
    const before = busyReasons.size > 0;
    if (busy)
      busyReasons.add(reason);
    else
      busyReasons.delete(reason);
    const after = busyReasons.size > 0;
    if (after !== before)
      post({ t: "busy", busy: after });
  }
  function relayStatus() {
    return {
      connected: link !== null,
      extensionVersion: typeof OMP_INSTALL_VERSION === "string" ? OMP_INSTALL_VERSION : undefined,
      hostError
    };
  }
  async function setBadge(connected) {
    try {
      await chrome.action.setBadgeText({ text: connected ? "on" : "" });
      await chrome.action.setTitle({ title: connected ? "oh-my-pig · Browser relay connected" : "oh-my-pig · Browser relay disconnected" });
      await chrome.action.setBadgeBackgroundColor({
        color: connected ? "#1a7f37" : "#8b8b8b"
      });
    } catch {}
  }
  async function buildHello() {
    const [tabs, targets] = await Promise.all([
      chrome.tabs.query({}),
      chrome.debugger.getTargets()
    ]);
    const snapshots = [];
    for (const tab of tabs) {
      const snap = snapshot(tab);
      if (snap)
        snapshots.push(snap);
    }
    const attachedTabIds = await ownedDebuggerTabs(targets, (tabId) => chrome.debugger.sendCommand({ tabId }, "Target.getTargetInfo"));
    const versionMatch = /Chrome\/[\d.]+/.exec(navigator.userAgent);
    return {
      t: "hello",
      instanceId: await ensureInstanceId(),
      browserSession: await ensureBrowserSession(),
      userAgent: navigator.userAgent,
      browserVersion: versionMatch?.[0] ?? "Chrome/unknown",
      discardedTabsProtocol: 1,
      ompVersion: typeof OMP_INSTALL_VERSION === "string" ? OMP_INSTALL_VERSION : undefined,
      ompBuild: typeof OMP_INSTALL_BUILD === "string" ? OMP_INSTALL_BUILD : undefined,
      reloadProtocol: EXTENSION_RELOAD_PROTOCOL_VERSION,
      downloadsProtocol: chrome.downloads === undefined ? undefined : DOWNLOADS_PROTOCOL_VERSION,
      overlayProtocol: OVERLAY_PROTOCOL_VERSION,
      tabs: snapshots,
      attachedTabIds
    };
  }
  function handleRelayLine(raw, origin, sendReply) {
    let msg;
    try {
      msg = JSON.parse(raw);
    } catch {
      return;
    }
    switch (msg.t) {
      case "reload":
        chrome.runtime.reload();
        return;
      case "input":
        handlers?.input(msg.tabId, msg.input);
        return;
      case "callResult":
        origin.calls.settle(msg);
        return;
      case "rpc": {
        const answer = (reply) => {
          if (origin.open)
            sendReply(reply);
        };
        const { id } = msg;
        const run = handlers ? handlers.rpc(msg, origin) : Promise.reject(new Error("worker not ready"));
        run.then((result) => answer({ t: "rpcResult", id, ok: true, result })).catch((err) => {
          answer({
            t: "rpcResult",
            id,
            ok: false,
            error: err instanceof Error ? err.message : String(err)
          });
        });
        return;
      }
    }
  }
  var chatLinks = new Set;
  function broadcastRelayEvent(message) {
    post(message);
    for (const send of chatLinks)
      send(message);
  }
  function linked() {
    if (link)
      unlinked("omp relay changed");
    clearLinkDeadline();
    reconnectDelay = RECONNECT_MIN_MS;
    hostError = undefined;
    const epoch = new Link;
    link = epoch;
    setBadge(true);
    buildHello().then((hello) => {
      if (link !== epoch || !post(hello))
        return;
      if (busyReasons.size > 0)
        post({ t: "busy", busy: true });
    });
    handlers?.status(relayStatus());
  }
  function unlinked(reason) {
    clearLinkDeadline();
    const epoch = link;
    link = null;
    setBadge(false);
    if (!epoch)
      return;
    epoch.close(reason);
    handlers?.disconnected();
    handlers?.status(relayStatus());
  }
  function scheduleReconnect() {
    const delay = reconnectDelay;
    reconnectDelay = Math.min(reconnectDelay * 2, RECONNECT_MAX_MS);
    clearTimeout(reconnectTimer ?? undefined);
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null;
      connectRelay();
    }, delay);
  }
  function clearLinkDeadline() {
    clearTimeout(deadlineTimer ?? undefined);
    deadlineTimer = null;
  }
  function enforceLinkDeadline(port) {
    clearLinkDeadline();
    deadlineTimer = setTimeout(() => {
      deadlineTimer = null;
      if (link || hostPort !== port)
        return;
      hostPort = null;
      port.disconnect();
      scheduleReconnect();
    }, LINK_DEADLINE_MS);
  }
  function connectRelay() {
    if (hostPort)
      return;
    const port = chrome.runtime.connectNative(NATIVE_HOST);
    hostPort = port;
    enforceLinkDeadline(port);
    const chunks = new Map;
    port.onMessage.addListener((message) => {
      if (hostPort !== port)
        return;
      switch (message.t) {
        case "linked":
          linked();
          return;
        case "unlinked":
          unlinked(message.reason);
          return;
        case "relay":
          if (link)
            handleRelayLine(message.line, link, post);
          return;
        case "chunk": {
          const parts = chunks.get(message.id) ?? [];
          parts[message.index] = message.data;
          if (!message.last) {
            chunks.set(message.id, parts);
            return;
          }
          chunks.delete(message.id);
          if (link)
            handleRelayLine(parts.join(""), link, post);
          return;
        }
      }
    });
    port.onDisconnect.addListener(() => {
      const detail = chrome.runtime.lastError?.message;
      if (hostPort !== port)
        return;
      hostPort = null;
      clearLinkDeadline();
      const hadLink = link !== null;
      unlinked(detail ? `omp native host disconnected: ${detail}` : "omp native host disconnected");
      if (!hadLink && detail !== hostError) {
        hostError = detail;
        handlers?.status(relayStatus());
      }
      scheduleReconnect();
    });
    const request = { t: "link", extensionId: chrome.runtime.id };
    port.postMessage(request);
  }
  function setRelayHandlers(relayHandlers) {
    handlers = relayHandlers;
  }

  // omp@v19.1.1/packages/browser-relay/extension/worker/session-store.ts
  class SessionValue {
    #key;
    #initial;
    #value = null;
    constructor(key, initial) {
      this.#key = key;
      this.#initial = initial;
    }
    get() {
      this.#value ??= chrome.storage.session.get(this.#key).then((stored) => stored[this.#key] ?? this.#initial()).catch(() => this.#initial());
      return this.#value;
    }
    async save() {
      const value = await this.get();
      await chrome.storage.session.set({ [this.#key]: value }).catch(() => {});
    }
  }

  // omp@v19.1.1/packages/browser-relay/extension/worker/overlay-host.ts
  var OVERLAY_SCRIPT = "overlay.js";
  var mounted = new SessionValue("omp.overlayMounted", () => []);
  var activities = new SessionValue("omp.tabActivity", () => ({}));
  var dismissedGroups = new SessionValue("omp.dismissedGroups", () => []);
  function reportWorking(stored) {
    let working = false;
    for (const tabId in stored) {
      if (stored[tabId]?.activity.state !== "working")
        continue;
      working = true;
      break;
    }
    setBusy("activity", working);
  }
  function isNoReceiver(error) {
    return /Receiving end does not exist/.test(String(error));
  }
  function send(tabId, command) {
    return chrome.tabs.sendMessage(tabId, command, { frameId: 0 });
  }
  async function overlay(tabId, command) {
    try {
      return await send(tabId, command);
    } catch (error) {
      if (!isNoReceiver(error))
        throw error;
    }
    await chrome.scripting.executeScript({ target: { tabId, frameIds: [0] }, files: [OVERLAY_SCRIPT] });
    return await send(tabId, command);
  }
  async function overlayIfPresent(tabId, command, absent) {
    try {
      return await send(tabId, command);
    } catch (error) {
      if (isNoReceiver(error))
        return absent;
      throw error;
    }
  }
  async function relayOverlay(tabId, action) {
    const tabs = await mounted.get();
    switch (action) {
      case "mount":
        await overlay(tabId, { omp: "overlay", op: "mount" });
        if (!tabs.includes(tabId)) {
          tabs.push(tabId);
          mounted.save();
        }
        break;
      case "unmount": {
        const index = tabs.indexOf(tabId);
        if (index >= 0) {
          tabs.splice(index, 1);
          mounted.save();
        }
        await overlayIfPresent(tabId, { omp: "overlay", op: "unmount" }, true);
        break;
      }
      case "hide":
      case "show":
        await overlayIfPresent(tabId, { omp: "overlay", op: action }, true);
        break;
    }
    return {};
  }
  function relayInput(tabId, input) {
    mounted.get().then((tabs) => {
      if (!tabs.includes(tabId))
        return;
      overlay(tabId, { omp: "overlay", op: "input", input }).catch(() => {});
    });
  }
  async function unmountAll() {
    const tabs = await mounted.get();
    const released = tabs.splice(0);
    mounted.save();
    await Promise.allSettled(released.map((tabId) => overlayIfPresent(tabId, { omp: "overlay", op: "unmount" }, true)));
  }
  async function restoreAfterNavigation(tabId, groupId) {
    const [tabs, stored, dismissed] = await Promise.all([mounted.get(), activities.get(), dismissedGroups.get()]);
    const restores = [];
    if (tabs.includes(tabId))
      restores.push(overlay(tabId, { omp: "overlay", op: "mount" }));
    const shown = stored[tabId];
    if (shown && !(shown.role === "secondary" && dismissed.includes(groupId))) {
      restores.push(overlay(tabId, { omp: "overlay", op: "activity", activity: shown.activity, role: shown.role }));
    }
    await Promise.allSettled(restores);
  }
  async function forgetTab(tabId) {
    const [tabs, stored] = await Promise.all([mounted.get(), activities.get()]);
    const index = tabs.indexOf(tabId);
    if (index >= 0) {
      tabs.splice(index, 1);
      mounted.save();
    }
    if (stored[tabId]) {
      delete stored[tabId];
      activities.save();
      reportWorking(stored);
    }
  }

  // omp@v19.1.1/packages/browser-relay/extension/download-pairing.ts
  var DOWNLOAD_PAIRING_MS = 5000;
  var DOWNLOAD_LOOKUP_MS = 60000;

  class DownloadPairing {
    #now;
    #pairs = new Map;
    #reports = [];
    constructor(now = Date.now) {
      this.#now = now;
    }
    report(guid, url) {
      if (this.#pairs.has(guid) || this.#reports.some((report) => report.guid === guid))
        return;
      this.#dropAgedReports();
      this.#reports.push({ guid, url, at: this.#now() });
    }
    created(id, url) {
      this.#dropAgedReports();
      const index = this.#reports.findIndex((report) => report.url === url);
      if (index < 0)
        return;
      this.#pairs.set(this.#reports[index].guid, { id });
      this.#reports.splice(index, 1);
    }
    #dropAgedReports() {
      const oldest = this.#now() - DOWNLOAD_PAIRING_MS;
      while (this.#reports.length > 0 && this.#reports[0].at < oldest)
        this.#reports.shift();
    }
    finished(id, state) {
      const now = this.#now();
      for (const [guid, pair] of this.#pairs) {
        if (pair.id === id) {
          if (state === "interrupted")
            this.#pairs.delete(guid);
          else
            pair.completedAt = now;
        } else if (pair.completedAt !== undefined && pair.completedAt < now - DOWNLOAD_LOOKUP_MS) {
          this.#pairs.delete(guid);
        }
      }
    }
    idFor(guid) {
      return this.#pairs.get(guid)?.id;
    }
    forget(guid) {
      this.#pairs.delete(guid);
    }
  }

  // omp@v19.1.1/packages/browser-relay/extension/foreign-frames.ts
  var RETRY_DELAYS_MS = [50, 100, 200, 400];
  var sweeps = new Map;
  async function pastForeignFrames(tabId, request) {
    for (let attempt = 0;; attempt++) {
      try {
        return await request();
      } catch (error) {
        if (attempt === RETRY_DELAYS_MS.length || !FOREIGN_FRAME_REFUSAL.test(String(error)) || await sweep(tabId) === 0)
          throw error;
      }
      const { promise, resolve } = Promise.withResolvers();
      setTimeout(resolve, RETRY_DELAYS_MS[attempt]);
      await promise;
    }
  }
  function sweep(tabId) {
    let pending = sweeps.get(tabId);
    if (!pending) {
      pending = neutralizeForeignFrames(tabId).finally(() => sweeps.delete(tabId));
      sweeps.set(tabId, pending);
    }
    return pending;
  }
  var reportHidden = () => {};
  function onForeignFramesHidden(report) {
    reportHidden = report;
  }
  var MAX_PATH_LENGTH = 100;
  var MAX_TITLE_LENGTH = 60;
  var PROBE_TIMEOUT_MS = 500;
  var PROBE_MAX_BYTES = 64 * 1024;
  async function reportEmptied(tabId, srcsByFrame) {
    const probes = await Promise.allSettled(Array.from(srcsByFrame, ([frameId, srcs]) => chrome.scripting.executeScript({
      target: { tabId, frameIds: [frameId] },
      world: "ISOLATED",
      injectImmediately: true,
      args: [srcs, PROBE_TIMEOUT_MS, PROBE_MAX_BYTES],
      func: async (urls, timeoutMs, maxBytes) => await Promise.all(urls.map(async (url) => {
        try {
          const response = await fetch(url, { signal: AbortSignal.timeout(timeoutMs) });
          if (!response.ok)
            return null;
          const reader = response.body?.getReader();
          const decoder = new TextDecoder;
          let head = "";
          for (let read = 0;reader && read < maxBytes && !/<\/title>/i.test(head); ) {
            const chunk = await reader.read();
            if (chunk.done)
              break;
            read += chunk.value.byteLength;
            head += decoder.decode(chunk.value, { stream: true });
          }
          reader?.cancel().catch(() => {});
          const title = /<title[^>]*>([^<]*)<\/title>/i.exec(head.slice(0, maxBytes))?.[1]?.trim();
          return { url: response.url, title };
        } catch {
          return null;
        }
      }))
    })));
    const frames = new Map;
    for (const probe of probes) {
      if (probe.status === "rejected")
        continue;
      for (const loaded of probe.value.flatMap(({ result }) => result ?? [])) {
        if (!loaded?.url.startsWith("chrome-extension://"))
          continue;
        const { host: extensionId, pathname } = new URL(loaded.url);
        if (!/^[a-p]{32}$/.test(extensionId) || extensionId === chrome.runtime.id)
          continue;
        const path = pathname.slice(1, 1 + MAX_PATH_LENGTH);
        const title = loaded.title?.slice(0, MAX_TITLE_LENGTH);
        frames.set(`${extensionId}/${path}`, { extensionId, path, ...title ? { title } : {} });
      }
    }
    if (frames.size > 0)
      reportHidden(tabId, [...frames.values()]);
  }
  async function neutralizeForeignFrames(tabId) {
    let results;
    try {
      results = await chrome.scripting.executeScript({
        target: { tabId, allFrames: true },
        injectImmediately: true,
        args: [chrome.runtime.id],
        func: (self) => {
          const swept = { found: 0, emptied: [] };
          const documentKey = "document";
          const page = Reflect.get(globalThis, documentKey);
          const dom = chrome.dom;
          const visit = (root) => {
            const walker = page.createTreeWalker(root, 1);
            for (let element = walker.nextNode();element; element = walker.nextNode()) {
              const tag = element.tagName.toUpperCase();
              if (tag === "IFRAME" || tag === "FRAME") {
                const src = element.getAttribute("src") ?? "";
                if (src.startsWith("chrome-extension://") && !src.startsWith(`chrome-extension://${self}/`)) {
                  if (tag === "FRAME") {
                    element.setAttribute("src", "about:blank");
                    swept.emptied.push(src);
                  } else if (element.getAttribute("srcdoc") === null) {
                    element.setAttribute("srcdoc", "");
                    swept.emptied.push(src);
                  }
                  element.style.setProperty("display", "none", "important");
                  swept.found++;
                }
              }
              let shadow = null;
              try {
                shadow = dom.openOrClosedShadowRoot(element);
              } catch {}
              if (shadow)
                visit(shadow);
            }
          };
          visit(page);
          return swept;
        }
      });
    } catch {
      return 0;
    }
    let found = 0;
    const srcsByFrame = new Map;
    for (const { frameId, result } of results) {
      found += result?.found ?? 0;
      if (result && result.emptied.length > 0)
        srcsByFrame.set(frameId, result.emptied);
    }
    if (srcsByFrame.size > 0)
      reportEmptied(tabId, srcsByFrame).catch(() => {});
    return found;
  }

  // omp@v19.1.1/packages/browser-relay/extension/worker/relay-events.ts
  var relayInitiatedDetachTabs = new Set;
  var downloads = new DownloadPairing;
  async function findDownload(guid) {
    if (chrome.downloads === undefined)
      return null;
    const id = downloads.idFor(guid);
    const [item] = id === undefined ? [] : await chrome.downloads.search({ id });
    if (item && item.state !== "in_progress")
      downloads.forget(guid);
    return item ? { filename: item.filename, state: item.state } : null;
  }
  function installRelayEvents() {
    chrome.downloads?.onCreated.addListener((item) => downloads.created(item.id, item.finalUrl));
    chrome.downloads?.onChanged.addListener((delta) => {
      const state = delta.state?.current;
      if (state === "complete" || state === "interrupted")
        downloads.finished(delta.id, state);
    });
    chrome.debugger.onEvent.addListener((source, method, params) => {
      if (source.tabId === undefined)
        return;
      if (method === "Page.downloadWillBegin" && typeof params?.guid === "string" && typeof params.url === "string") {
        downloads.report(params.guid, params.url);
      }
      broadcastRelayEvent({ t: "cdpEvent", tabId: source.tabId, sessionId: source.sessionId, method, params });
    });
    chrome.debugger.onDetach.addListener((source, reason) => {
      if (source.tabId === undefined)
        return;
      const relayInitiated = relayInitiatedDetachTabs.delete(source.tabId);
      broadcastRelayEvent({ t: "detached", tabId: source.tabId, reason, relayInitiated });
    });
    onForeignFramesHidden((tabId, frames) => broadcastRelayEvent({ t: "framesHidden", tabId, frames }));
    chrome.tabs.onCreated.addListener((tab) => {
      const snap = snapshot(tab);
      if (snap)
        broadcastRelayEvent({ t: "tabCreated", tab: snap });
    });
    chrome.tabs.onUpdated.addListener((_tabId, _changeInfo, tab) => {
      const snap = snapshot(tab);
      if (snap)
        broadcastRelayEvent({ t: "tabUpdated", tab: snap });
    });
    chrome.tabs.onActivated.addListener(({ tabId }) => {
      chrome.tabs.get(tabId).then((tab) => {
        const snap = snapshot(tab);
        if (snap)
          broadcastRelayEvent({ t: "tabUpdated", tab: snap });
      }).catch(() => {});
    });
    chrome.tabs.onRemoved.addListener((tabId) => {
      broadcastRelayEvent({ t: "tabRemoved", tabId });
    });
  }

  // omp@v19.1.1/packages/browser-relay/extension/worker/relay-rpc.ts
  async function runRelayRpc(request, origin) {
    switch (request.op) {
      case "attach": {
        const attach = () => chrome.debugger.attach({ tabId: request.tabId }, "1.3");
        await (request.clearForeignFrames ? pastForeignFrames(request.tabId, attach) : attach());
        return {};
      }
      case "detach":
        relayInitiatedDetachTabs.add(request.tabId);
        try {
          await chrome.debugger.detach({ tabId: request.tabId });
          return {};
        } catch (error) {
          relayInitiatedDetachTabs.delete(request.tabId);
          throw error;
        }
      case "send": {
        const send = () => chrome.debugger.sendCommand(request.sessionId ? { tabId: request.tabId, sessionId: request.sessionId } : { tabId: request.tabId }, request.method, request.params);
        return await (request.clearForeignFrames ? pastForeignFrames(request.tabId, send) : send());
      }
      case "createTab": {
        const tab = await chrome.tabs.create({ url: request.url, active: request.active ?? true });
        const snap = snapshot(tab);
        if (!snap)
          throw new Error("created tab has no id");
        if (!origin.open)
          removeTab(snap.tabId).catch(() => {});
        return { tab: snap };
      }
      case "removeTab":
        await removeTab(request.tabId);
        return {};
      case "activateTab":
        await focusTab(request.tabId);
        return {};
      case "group":
        return await enqueueGroupOp(() => groupRelayTabs(request.tabIds, request.title, request.color));
      case "ungroup": {
        const { tabIds } = request;
        if (isNonEmpty(tabIds))
          await enqueueGroupOp(() => chrome.tabs.ungroup(tabIds).catch(() => {}));
        return {};
      }
      case "findDownload":
        return await findDownload(request.guid);
      case "overlay":
        return await relayOverlay(request.tabId, request.action);
    }
  }

  // omp@v19.1.1/packages/browser-relay/extension/background.ts
  setRelayHandlers({
    rpc: runRelayRpc,
    input: relayInput,
    status: () => {},
    disconnected: () => {
      restoreRelayGroups();
      unmountAll();
    }
  });
  installRelayEvents();
  chrome.tabs.onUpdated.addListener((tabId, change, tab) => {
    if (change.status === "complete")
      restoreAfterNavigation(tabId, tab.groupId);
  });
  chrome.tabs.onRemoved.addListener((tabId) => void forgetTab(tabId));
  chrome.alarms.create("omp-relay-keepalive", { periodInMinutes: 0.5 });
  chrome.alarms.onAlarm.addListener((alarm) => {
    if (alarm.name === "omp-relay-keepalive")
      connectRelay();
  });
  connectRelay();
})();
