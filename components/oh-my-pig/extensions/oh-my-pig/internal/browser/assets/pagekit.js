(function() {
  if (globalThis.__ompBrowserKit) return globalThis.__ompBrowserKit;

  const norm = s => String(s || "").replace(/\s+/g, " ").trim();
  const normLower = s => norm(s).toLowerCase();

  function composedContains(ancestor, node) {
    let cur = node;
    while (cur) {
      if (cur === ancestor) return true;
      cur = cur.assignedSlot || cur.parentElement || (cur.getRootNode && cur.getRootNode().host) || null;
    }
    return false;
  }

  function allElements(root, pierce) {
    const out = [];
    const walk = node => {
      if (!node) return;
      if (node.nodeType === 1) {
        out.push(node);
        if (pierce && node.shadowRoot) walk(node.shadowRoot);
      }
      const children = node.children || (node.childNodes ? Array.from(node.childNodes).filter(n => n.nodeType === 1) : []);
      for (const ch of children) walk(ch);
    };
    if (root === document) {
      if (document.documentElement) walk(document.documentElement);
    } else if (root.nodeType === 11 || root.nodeType === 9) {
      walk(root);
    } else {
      const children = root.children || [];
      for (const ch of children) walk(ch);
    }
    return out;
  }

  function idrefText(el, attr) {
    const ids = el.getAttribute(attr);
    if (!ids) return "";
    const doc = el.ownerDocument || document;
    return norm(ids.split(/\s+/).map(id => (doc.getElementById(id)?.textContent || "")).join(" "));
  }

  function implicitRole(el) {
    const tag = el.tagName.toLowerCase();
    if ((tag === "a" || tag === "area") && el.hasAttribute("href")) return "link";
    if (tag === "button") return "button";
    if (tag === "textarea") return "textbox";
    if (tag === "select") return (el.multiple || el.size > 1) ? "listbox" : "combobox";
    if (tag === "option") return "option";
    if (tag === "img") return el.getAttribute("alt") === "" ? "none" : "img";
    if (tag === "ul" || tag === "ol" || tag === "menu") return "list";
    if (tag === "li") return "listitem";
    if (tag === "nav") return "navigation";
    if (tag === "main") return "main";
    if (tag === "form") return "form";
    if (tag === "dialog") return "dialog";
    if (tag === "table") return "table";
    if (tag === "tr") return "row";
    if (tag === "th") return "columnheader";
    if (tag === "td") return "cell";
    if (/^h[1-6]$/.test(tag)) return "heading";
    if (tag === "input") {
      const t = (el.getAttribute("type") || "text").toLowerCase();
      if (t === "button" || t === "submit" || t === "reset" || t === "image") return "button";
      if (t === "checkbox") return "checkbox";
      if (t === "radio") return "radio";
      if (t === "range") return "slider";
      if (t === "number") return "spinbutton";
      if (t === "search") return "searchbox";
      if (t === "hidden") return null;
      return "textbox";
    }
    if (tag === "summary") return "button";
    return null;
  }

  function roleOf(el) {
    const explicit = (el.getAttribute("role") || "").trim().split(/\s+/)[0]?.toLowerCase();
    if (explicit) return explicit === "presentation" ? "none" : explicit;
    return implicitRole(el);
  }

  function accessibleName(el) {
    const labelledBy = idrefText(el, "aria-labelledby");
    if (labelledBy) return labelledBy;
    const ariaLabel = norm(el.getAttribute("aria-label"));
    if (ariaLabel) return ariaLabel;
    const doc = el.ownerDocument || document;
    if (el.id) {
      const lbl = doc.querySelector('label[for="' + CSS.escape(el.id) + '"]');
      if (lbl) return norm(lbl.textContent);
    }
    const parentLabel = el.closest ? el.closest("label") : null;
    if (parentLabel && parentLabel !== el) {
      const clone = parentLabel.cloneNode(true);
      clone.querySelectorAll("input,select,textarea,button").forEach(n => n.remove());
      const t = norm(clone.textContent);
      if (t) return t;
    }
    const tag = el.tagName.toLowerCase();
    if (tag === "input") {
      const t = (el.getAttribute("type") || "text").toLowerCase();
      if ((t === "button" || t === "submit" || t === "reset") && el.value) return norm(el.value);
      if (t === "image") return norm(el.getAttribute("alt"));
    }
    if (tag === "img") return norm(el.getAttribute("alt"));
    const title = norm(el.getAttribute("title"));
    if (tag === "input" || tag === "textarea" || tag === "select") {
      return norm(el.getAttribute("placeholder")) || title;
    }
    return norm(el.innerText || el.textContent) || title;
  }

  function queryByLabel(root, q) {
    const wanted = normLower(q);
    const matches = v => normLower(v).includes(wanted);
    const res = [];
    const seen = new Set();
    const add = el => { if (el && !seen.has(el)) { seen.add(el); res.push(el); } };
    const doc = root.ownerDocument || document;
    for (const label of root.querySelectorAll("label")) {
      if (!matches(label.textContent)) continue;
      const htmlFor = label.getAttribute("for");
      if (htmlFor) {
        const target = doc.getElementById(htmlFor);
        if (target && (root === doc || root.contains(target))) add(target);
      } else {
        add(label.querySelector("button,input,meter,output,progress,select,textarea"));
      }
    }
    for (const el of root.querySelectorAll("*")) {
      if (matches(el.getAttribute("aria-label"))) add(el);
      if (el.hasAttribute("aria-labelledby") && matches(idrefText(el, "aria-labelledby"))) add(el);
    }
    return res;
  }

  function queryByRole(root, selector) {
    const m = /^\s*([a-z][a-z0-9-]*)\s*(?:\[\s*name\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\]\s]+))(?:\s+(exact))?\s*\])?\s*$/i.exec(selector);
    if (!m) throw new Error('Invalid role/ selector: ' + selector);
    const wantedRole = m[1].toLowerCase();
    let wantedName = m[2] ?? m[3] ?? m[4];
    let exact = Boolean(m[5]);
    if (wantedName !== undefined && /\s+exact$/i.test(wantedName)) {
      wantedName = wantedName.replace(/\s+exact$/i, "");
      exact = true;
    }
    const normName = wantedName === undefined ? undefined : normLower(wantedName);
    const roleMatch = r => {
      if (!r) return false;
      if (r === wantedRole) return true;
      if ((wantedRole === "img" || wantedRole === "image") && (r === "img" || r === "image")) return true;
      if ((wantedRole === "none" || wantedRole === "presentation") && (r === "none" || r === "presentation")) return true;
      return false;
    };
    return allElements(root, true).filter(el => {
      if (!roleMatch(roleOf(el))) return false;
      if (normName === undefined) return true;
      const name = normLower(accessibleName(el));
      return exact ? name === normName : name.includes(normName);
    });
  }

  function queryByPierce(root, css) {
    const res = [];
    const seen = new Set();
    const visit = r => {
      if (!r || typeof r.querySelectorAll !== "function") return;
      for (const el of r.querySelectorAll(css)) {
        if (!seen.has(el)) { seen.add(el); res.push(el); }
      }
      for (const el of r.querySelectorAll("*")) {
        if (el.shadowRoot) visit(el.shadowRoot);
      }
    };
    visit(root);
    return res;
  }

  function queryByText(root, q) {
    const wanted = normLower(q);
    if (!wanted) return [];
    const all = allElements(root, true);
    const matched = all.filter(el => {
      const tag = el.tagName.toLowerCase();
      if (tag === "script" || tag === "style" || tag === "noscript") return false;
      const t = normLower(el.innerText || el.textContent);
      return t.includes(wanted);
    });
    return matched.filter(el => !matched.some(other => other !== el && el.contains(other)));
  }

  function queryByAria(root, q) {
    const trimmed = q.trim();
    const roleMatch = /^([^\[]+)\[\s*role\s*=\s*["']?([^"'\]]+)["']?\s*\]$/i.exec(trimmed);
    const wantedName = normLower(roleMatch ? roleMatch[1] : trimmed);
    const wantedRole = roleMatch ? roleMatch[2].trim().toLowerCase() : null;
    return allElements(root, true).filter(el => {
      if (wantedRole && roleOf(el) !== wantedRole) return false;
      const name = normLower(accessibleName(el));
      return name === wantedName || name.includes(wantedName);
    });
  }

  function queryAll(selector, rootNode) {
    const root = rootNode || document;
    const s = String(selector || "").trim();
    if (!s) return [];

    let refId = null;
    for (const p of ["aria-ref=", "aria-ref/", "ariaref/"]) {
      if (s.startsWith(p)) { refId = s.slice(p.length).trim(); break; }
    }
    if (!refId && /^@?e\d+$/.test(s)) refId = s.replace(/^@/, "");
    if (refId && /^e\d+$/.test(refId)) {
      const el = resolveAriaRef(refId);
      return el ? [el] : [];
    }

    if (s.startsWith("css/")) return Array.from(root.querySelectorAll(s.slice(4)));
    if (s.startsWith("xpath/")) {
      const expr = s.slice(6);
      const doc = root.ownerDocument || document;
      const snap = doc.evaluate(expr, root, null, XPathResult.ORDERED_NODE_SNAPSHOT_TYPE, null);
      const out = [];
      for (let i = 0; i < snap.snapshotLength; i++) {
        const item = snap.snapshotItem(i);
        if (item && item.nodeType === 1) out.push(item);
      }
      return out;
    }
    if (s.startsWith("pierce/")) return queryByPierce(root, s.slice(7));
    if (s.startsWith("text/")) return queryByText(root, s.slice(5));
    if (s.startsWith("aria/")) return queryByAria(root, s.slice(5));
    if (s.startsWith("label/")) return queryByLabel(root, s.slice(6));
    if (s.startsWith("placeholder/")) {
      const w = normLower(s.slice(12));
      return Array.from(root.querySelectorAll("[placeholder]")).filter(el => normLower(el.getAttribute("placeholder")).includes(w));
    }
    if (s.startsWith("testid/")) {
      const w = s.slice(7).trim();
      return Array.from(root.querySelectorAll("[data-testid]")).filter(el => el.getAttribute("data-testid") === w);
    }
    if (s.startsWith("alt/")) {
      const w = normLower(s.slice(4));
      return Array.from(root.querySelectorAll("[alt]")).filter(el => normLower(el.getAttribute("alt")).includes(w));
    }
    if (s.startsWith("title/")) {
      const w = normLower(s.slice(6));
      return Array.from(root.querySelectorAll("[title]")).filter(el => normLower(el.getAttribute("title")).includes(w));
    }
    if (s.startsWith("role/")) return queryByRole(root, s.slice(5));
    return Array.from(root.querySelectorAll(s));
  }

  function isVisible(el) {
    if (!el || !el.isConnected) return false;
    const style = getComputedStyle(el);
    if (style.display === "none" || style.visibility === "hidden" || style.visibility === "collapse") return false;
    if (parseFloat(style.opacity) === 0) return false;
    const rect = el.getBoundingClientRect();
    return rect.width > 0 || rect.height > 0;
  }

  function isEnabled(el) {
    if (!el) return false;
    if (el.disabled === true) return false;
    if (el.getAttribute("aria-disabled") === "true") return false;
    if (el.closest && el.closest("fieldset[disabled]")) return false;
    return true;
  }

  function isChecked(el) {
    if (!el) return false;
    if (typeof el.checked === "boolean" && (el.tagName === "INPUT" && (el.type === "checkbox" || el.type === "radio"))) {
      return el.checked;
    }
    const aria = el.getAttribute("aria-checked");
    if (aria === "true") return true;
    return false;
  }

  // Click actionability check ported from omp's interactions.ts:256-339.
  function checkClickActionable(el, viaLabel) {
    if (!el || !el.isConnected) {
      return { ok: false, reason: "detached (the page replaced this element; look it up again)", detached: true };
    }
    const style = getComputedStyle(el);
    if (style.display === "none") return { ok: false, reason: "display:none" };
    if (style.visibility === "hidden" || style.visibility === "collapse") {
      return { ok: false, reason: "visibility:" + style.visibility };
    }
    if (style.pointerEvents === "none") return { ok: false, reason: "pointer-events:none" };
    if (
      Number(style.opacity) === 0 &&
      !el.matches('input[type="checkbox"], input[type="radio"], [role="checkbox" i], [role="radio" i], [role="switch" i]')
    ) {
      return { ok: false, reason: "opacity:0" };
    }
    const rect = el.getBoundingClientRect();
    if (rect.width < 1 || rect.height < 1) return { ok: false, reason: "zero-size" };
    const fragments = Array.from(el.getClientRects());
    const box = fragments.length === 0 ? rect : fragments.find(
      r => Math.min(window.innerWidth, r.right) - Math.max(0, r.left) >= 1 &&
           Math.min(window.innerHeight, r.bottom) - Math.max(0, r.top) >= 1
    );
    if (!box) return { ok: false, reason: "off-viewport" };
    const left = Math.max(0, Math.min(window.innerWidth, box.left));
    const right = Math.max(0, Math.min(window.innerWidth, box.right));
    const top = Math.max(0, Math.min(window.innerHeight, box.top));
    const bottom = Math.max(0, Math.min(window.innerHeight, box.bottom));
    if (right - left < 1 || bottom - top < 1) return { ok: false, reason: "off-viewport" };
    const x = Math.floor((left + right) / 2);
    const y = Math.floor((top + bottom) / 2);
    let topEl = document.elementFromPoint(x, y);
    for (let depth = 0; topEl && topEl.shadowRoot && depth < 16; depth++) {
      const nested = topEl.shadowRoot.elementFromPoint(x, y);
      if (!nested || nested === topEl) break;
      topEl = nested;
    }
    if (!topEl) return { ok: false, reason: "elementFromPoint-null" };
    const interactiveContent = 'a[href], area[href], button, details, embed, iframe, input:not([type="hidden"]), label, select, textarea, summary, audio[controls], video[controls], [contenteditable=""], [contenteditable="true"]';
    const forwardedBy = owner => {
      let cur = topEl;
      while (cur && cur !== owner) {
        if (cur.matches && cur.matches(interactiveContent)) return false;
        cur = cur.assignedSlot || cur.parentElement || (cur.getRootNode && cur.getRootNode().host) || null;
      }
      return cur === owner;
    };
    const onTarget =
      composedContains(el, topEl) ||
      composedContains(topEl, el) ||
      (viaLabel && Array.from(el.labels || []).some(forwardedBy));
    if (!onTarget) {
      const tag = topEl.tagName.toLowerCase();
      const id = topEl.id ? "#" + topEl.id : "";
      const classes = Array.from(topEl.classList || []).slice(0, 2).map(n => "." + n).join("");
      return { ok: false, reason: "covered", coveredBy: "<" + tag + id + classes + ">", rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height } };
    }
    return { ok: true, x, y, rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height } };
  }

  let ariaMod = null;
  function resolveAriaRef(ref) {
    if (!ariaMod || typeof ariaMod.resolveAriaRef !== "function") return null;
    const el = ariaMod.resolveAriaRef(ref);
    return (el && el.isConnected) ? el : null;
  }
  function setAriaModule(mod) { ariaMod = mod; }

  function selectOptions(el, choices) {
    if (el.tagName !== "SELECT") {
      throw new Error("select() requires a <select> element, got <" + el.tagName.toLowerCase() + ">");
    }
    const opts = Array.from(el.options);
    const chosen = [];
    for (const c of choices) {
      let match = null;
      if (c.kind === "index") {
        match = opts[c.value] || null;
      } else if (c.kind === "value") {
        match = opts.find(o => o.value === c.value) || null;
      } else if (c.kind === "label") {
        const w = norm(c.value);
        match = opts.find(o => o.text === c.value || norm(o.text) === w || norm(o.label) === w) || null;
      } else {
        const w = norm(c.value);
        match = opts.find(o => o.value === c.value) || opts.find(o => o.text === c.value || norm(o.text) === w || norm(o.label) === w) || null;
      }
      if (!match) throw new Error("select(): no option matched " + JSON.stringify(c.value));
      chosen.push(match);
    }
    if (!el.multiple) {
      for (const o of opts) o.selected = false;
      if (chosen.length > 0) chosen[0].selected = true;
    } else {
      for (const o of opts) o.selected = chosen.includes(o);
    }
    el.dispatchEvent(new Event("input", { bubbles: true }));
    el.dispatchEvent(new Event("change", { bubbles: true }));
    return Array.from(el.selectedOptions).map(o => o.value);
  }

  function highlight(el, duration) {
    const rect = el.getBoundingClientRect();
    const box = document.createElement("div");
    box.style.cssText = "position:fixed;pointer-events:none;z-index:2147483647;border:2px solid #ff3b30;background:rgba(255,59,48,0.12);left:" + rect.left + "px;top:" + rect.top + "px;width:" + rect.width + "px;height:" + rect.height + "px;box-sizing:border-box;";
    document.documentElement.appendChild(box);
    setTimeout(() => box.remove(), duration || 1500);
  }

  function htmlToMarkdown(root) {
    const SKIP = new Set(["SCRIPT", "STYLE", "NOSCRIPT", "TEMPLATE", "SVG"]);
    const out = [];
    function walk(node, inPre) {
      if (!node) return;
      if (node.nodeType === 3) {
        let t = node.textContent || "";
        if (!inPre) t = t.replace(/[ \t\r\n]+/g, " ");
        out.push(t);
        return;
      }
      if (node.nodeType !== 1) return;
      const tag = node.tagName;
      if (SKIP.has(tag)) return;
      if (tag === "BR") { out.push("\n"); return; }
      if (tag === "HR") { out.push("\n\n---\n\n"); return; }
      if (/^H[1-6]$/.test(tag)) {
        const level = Number(tag[1]);
        out.push("\n\n" + "#".repeat(level) + " ");
        for (const ch of node.childNodes) walk(ch, false);
        out.push("\n\n");
        return;
      }
      if (tag === "A" && node.getAttribute("href")) {
        const href = node.href || node.getAttribute("href");
        const start = out.length;
        for (const ch of node.childNodes) walk(ch, inPre);
        const label = out.splice(start).join("").trim();
        out.push(label ? ("[" + label + "](" + href + ")") : href);
        return;
      }
      if (tag === "PRE") {
        out.push("\n\n```\n" + (node.textContent || "") + "\n```\n\n");
        return;
      }
      if (tag === "CODE" && !inPre) {
        out.push("`" + (node.textContent || "") + "`");
        return;
      }
      if (tag === "LI") {
        out.push("\n- ");
        for (const ch of node.childNodes) walk(ch, false);
        return;
      }
      const block = new Set(["P", "DIV", "SECTION", "ARTICLE", "MAIN", "HEADER", "FOOTER", "BLOCKQUOTE", "UL", "OL", "TABLE", "TR"]);
      if (block.has(tag)) out.push("\n\n");
      for (const ch of node.childNodes) walk(ch, inPre || tag === "PRE");
      if (block.has(tag)) out.push("\n\n");
    }
    walk(root, false);
    return out.join("").replace(/[ \t]+\n/g, "\n").replace(/\n{3,}/g, "\n\n").trim();
  }

  globalThis.__ompBrowserKit = {
    queryAll,
    queryFirst: (sel, root) => queryAll(sel, root)[0] || null,
    isVisible,
    isEnabled,
    isChecked,
    checkClickActionable,
    resolveAriaRef,
    setAriaModule,
    selectOptions,
    highlight,
    accessibleName,
    roleOf,
    htmlToMarkdown
  };
  return globalThis.__ompBrowserKit;
})();
