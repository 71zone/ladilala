// Locator library shared by the recorder (builds locators for an element) and
// the player (resolves a target to one element). It runs in an isolated world
// only; it never touches the page's main world and adds no property to it.
// The file is one expression that evaluates to the library object.
(() => {
  if (globalThis.__ladiLoc) return globalThis.__ladiLoc;

  const norm = (s) => (s == null ? '' : String(s)).replace(/\s+/g, ' ').trim();
  const esc = (s) => (globalThis.CSS && CSS.escape ? CSS.escape(s) : String(s).replace(/[^\w-]/g, '\\$&'));
  const attrEsc = (s) => String(s).replace(/\\/g, '\\\\').replace(/"/g, '\\"');
  const rootOf = (el) => el.getRootNode();

  const INPUT_ROLES = {
    button: 'button', submit: 'button', reset: 'button', image: 'button', checkbox: 'checkbox',
    radio: 'radio', range: 'slider', number: 'spinbutton', search: 'searchbox', email: 'textbox',
    tel: 'textbox', url: 'textbox', text: 'textbox', '': 'textbox',
  };
  const TAG_ROLES = {
    button: 'button', textarea: 'textbox', h1: 'heading', h2: 'heading', h3: 'heading', h4: 'heading',
    h5: 'heading', h6: 'heading', img: 'img', ul: 'list', ol: 'list', li: 'listitem', nav: 'navigation',
    table: 'table', main: 'main', dialog: 'dialog', option: 'option', progress: 'progressbar',
    summary: 'button', fieldset: 'group',
  };

  function roleOf(el) {
    const explicit = el.getAttribute && el.getAttribute('role');
    if (explicit) return explicit.split(/\s+/)[0];
    const tag = el.localName;
    if (tag === 'a') return el.hasAttribute('href') ? 'link' : null;
    if (tag === 'input') return INPUT_ROLES[(el.getAttribute('type') || 'text').toLowerCase()] || null;
    if (tag === 'select') return el.multiple || el.size > 1 ? 'listbox' : 'combobox';
    return TAG_ROLES[tag] || null;
  }

  // Text of a label or heading without the form controls inside it.
  function plainText(node) {
    let out = '';
    for (const c of node.childNodes) {
      if (c.nodeType === 3) out += c.nodeValue;
      else if (c.nodeType === 1) {
        const t = c.localName;
        if (t === 'input' || t === 'select' || t === 'textarea' || t === 'script' || t === 'style') continue;
        if (c.getAttribute('aria-hidden') === 'true') continue;
        out += ' ' + plainText(c) + ' ';
      }
    }
    return out;
  }

  function labelTexts(el) {
    const out = [];
    if (el.labels) for (const l of el.labels) out.push(norm(plainText(l)));
    return out.filter(Boolean);
  }

  function accName(el) {
    const by = el.getAttribute('aria-labelledby');
    if (by) {
      const root = rootOf(el);
      const parts = by.split(/\s+/).map((id) => root.getElementById ? root.getElementById(id) : null).filter(Boolean);
      const t = norm(parts.map((p) => plainText(p) || p.textContent).join(' '));
      if (t) return t;
    }
    const al = norm(el.getAttribute('aria-label'));
    if (al) return al;
    const tag = el.localName;
    if (tag === 'input' || tag === 'select' || tag === 'textarea') {
      const type = (el.getAttribute('type') || 'text').toLowerCase();
      if (tag === 'input' && (type === 'submit' || type === 'reset' || type === 'button')) {
        return norm(el.value) || (type === 'submit' ? 'Submit' : type === 'reset' ? 'Reset' : '');
      }
      if (tag === 'input' && type === 'image') return norm(el.alt);
      const l = labelTexts(el);
      if (l.length) return l.join(' ');
      return norm(el.getAttribute('title')) || norm(el.getAttribute('placeholder'));
    }
    if (tag === 'img') return norm(el.getAttribute('alt'));
    if (tag === 'fieldset') {
      const lg = el.querySelector(':scope > legend');
      return lg ? norm(lg.textContent) : '';
    }
    const t = norm(plainText(el));
    return t || norm(el.getAttribute('title'));
  }

  const all = (root) => Array.from(root.querySelectorAll('*'));
  const isControl = (e) => ['input', 'select', 'textarea', 'button'].includes(e.localName);

  function queryAll(root, loc) {
    const v = loc.value;
    try {
      switch (loc.kind) {
        case 'css': return Array.from(root.querySelectorAll(v));
        case 'testid': return Array.from(root.querySelectorAll('[data-testid="' + attrEsc(v) + '"]'));
        case 'xpath': {
          const doc = root.ownerDocument || root;
          const snap = doc.evaluate(v, root.nodeType === 11 ? doc : root, null, 7, null);
          const out = [];
          for (let i = 0; i < snap.snapshotLength; i++) {
            const n = snap.snapshotItem(i);
            if (n.nodeType === 1 && (root.nodeType === 11 ? root.contains(n) : true)) out.push(n);
          }
          return out;
        }
        case 'role': {
          const want = norm(loc.name);
          return all(root).filter((e) => roleOf(e) === v && (loc.name == null || loc.name === '' || accName(e) === want));
        }
        case 'label': {
          const want = norm(v);
          return all(root).filter((e) => isControl(e) && (labelTexts(e).includes(want) || norm(e.getAttribute('aria-label')) === want));
        }
        case 'text': {
          const want = norm(v);
          const hits = all(root).filter((e) => !['script', 'style'].includes(e.localName) && norm(e.textContent) === want);
          return hits.filter((e) => !hits.some((o) => o !== e && e.contains(o)));
        }
      }
    } catch (_) { /* invalid selector or expression: nothing matches */ }
    return [];
  }

  // resolveRoot walks shadowPath (open shadow roots only) from document.
  function resolveRoot(shadowPath) {
    let root = document;
    for (const sel of shadowPath || []) {
      const hosts = Array.from(root.querySelectorAll(sel));
      if (hosts.length !== 1) return { status: hosts.length ? 'ambiguous' : 'missing', count: hosts.length, what: 'shadow host ' + sel };
      if (!hosts[0].shadowRoot) return { status: 'missing', count: 0, what: 'open shadow root on ' + sel };
      root = hosts[0].shadowRoot;
    }
    return { status: 'ok', root };
  }

  // resolve returns {status: ok|missing|ambiguous, el, index, count, tried}.
  // Locators are tried in order; the first matching exactly one element wins.
  function resolve(target) {
    const r = resolveRoot(target.shadowPath);
    if (r.status !== 'ok') return r;
    let ambiguous = null;
    const tried = [];
    (target.locators || []).forEach((loc, i) => {
      const found = queryAll(r.root, loc);
      tried.push({ kind: loc.kind, count: found.length });
      if (found.length === 1) { if (!tried.winner && tried.winner !== 0) tried.winner = i; }
      else if (found.length > 1 && !ambiguous) ambiguous = { index: i, count: found.length };
    });
    if (tried.winner !== undefined) {
      const loc = target.locators[tried.winner];
      return { status: 'ok', el: queryAll(r.root, loc)[0], index: tried.winner, tried };
    }
    if (ambiguous) return { status: 'ambiguous', index: ambiguous.index, count: ambiguous.count, tried };
    return { status: 'missing', tried };
  }

  const generated = (id) => /^(:r|ember|react-|ext-|gen|uid)/i.test(id) || /\d{4,}/.test(id) || /[0-9a-f]{8,}/i.test(id);

  function cssSeg(el) {
    const tag = el.localName;
    const p = el.parentNode;
    if (!p) return tag;
    const same = Array.from(p.children).filter((c) => c.localName === tag);
    return same.length === 1 ? tag : tag + ':nth-of-type(' + (same.indexOf(el) + 1) + ')';
  }

  // cssPath returns a short selector that is unique inside root.
  function cssPath(el, root) {
    const segs = [];
    let cur = el;
    while (cur && cur.nodeType === 1 && cur !== root) {
      if (cur.id && !generated(cur.id)) {
        segs.unshift('#' + esc(cur.id));
        const sel = segs.join(' > ');
        if (root.querySelectorAll(sel).length === 1) return sel;
      } else {
        segs.unshift(cssSeg(cur));
      }
      const sel = segs.join(' > ');
      if (segs.length >= 2 || cur.parentNode === root) {
        if (root.querySelectorAll(sel).length === 1) return sel;
      }
      cur = cur.parentNode;
      if (cur && cur.nodeType === 11) break;
    }
    return segs.join(' > ');
  }

  function xpathOf(el) {
    const segs = [];
    for (let cur = el; cur && cur.nodeType === 1; cur = cur.parentNode) {
      const same = cur.parentNode ? Array.from(cur.parentNode.children).filter((c) => c.localName === cur.localName) : [cur];
      segs.unshift(cur.localName + '[' + (same.indexOf(cur) + 1) + ']');
      if (cur === document.documentElement) break;
    }
    return '/' + segs.join('/');
  }

  const only = (root, loc, el) => { const f = queryAll(root, loc); return f.length === 1 && f[0] === el; };

  // build returns {locators, shadowPath} for el, most stable locator first:
  // role+name, label, id, data-testid, name, short CSS path, XPath.
  function build(el) {
    const root = rootOf(el);
    const locs = [];
    const add = (loc) => { if (only(root, loc, el)) locs.push(loc); };
    const role = roleOf(el), name = accName(el);
    if (role && name) add({ kind: 'role', value: role, name });
    const lt = labelTexts(el);
    if (lt.length === 1) add({ kind: 'label', value: lt[0] });
    if (el.id && !generated(el.id)) add({ kind: 'css', value: '#' + esc(el.id) });
    const tid = el.getAttribute('data-testid');
    if (tid) add({ kind: 'testid', value: tid });
    const nm = el.getAttribute('name');
    if (nm) add({ kind: 'css', value: el.localName + '[name="' + attrEsc(nm) + '"]' });
    const path = cssPath(el, root);
    if (path) add({ kind: 'css', value: path });
    if (root.nodeType === 9) {
      const xp = xpathOf(el);
      if (only(root, { kind: 'xpath', value: xp }, el)) locs.push({ kind: 'xpath', value: xp });
    }
    // Shadow hosts, outermost first.
    const shadowPath = [];
    for (let r = root; r && r.nodeType === 11 && r.host; r = rootOf(r.host)) {
      shadowPath.unshift(cssPath(r.host, rootOf(r.host)));
    }
    return { locators: locs, shadowPath };
  }

  // frameChain returns CSS selectors of the iframes around this window,
  // outermost first, and whether a frame could not be reached (cross-origin).
  function frameChain() {
    const path = [];
    let w = window;
    while (w !== w.top) {
      let fe = null;
      try { fe = w.frameElement; } catch (_) {}
      if (!fe) return { path, cross: true };
      path.unshift(cssPath(fe, rootOf(fe)));
      w = w.parent;
    }
    return { path, cross: false };
  }

  // Page-space centre of el after scrolling it into view, with the offset of
  // every same-process frame between el and the top viewport added. The hit
  // check reports whether another element covers the centre.
  function centre(el) {
    el.scrollIntoView({ block: 'center', inline: 'center' });
    const r = el.getBoundingClientRect();
    const x = r.left + r.width / 2, y = r.top + r.height / 2;
    const hit = rootOf(el).elementFromPoint ? rootOf(el).elementFromPoint(x, y) : null;
    let covered = '';
    if (hit && hit !== el && !el.contains(hit) && !(hit.shadowRoot && hit.shadowRoot.contains(el)) && !hit.contains(el)) {
      covered = hit.localName + (hit.id ? '#' + hit.id : '');
    }
    const f = frameOffset();
    return { x: x + f.x, y: y + f.y, w: r.width, h: r.height, covered, crossFrame: f.cross };
  }

  // Offset of this window's viewport inside the viewport of the top window of
  // its process; cross is true when the chain stops at a cross-origin frame.
  function frameOffset() {
    let ox = 0, oy = 0, w = window;
    while (w !== w.top) {
      let fe = null;
      try { fe = w.frameElement; } catch (_) {}
      if (!fe) break;
      const fr = fe.getBoundingClientRect(), cs = getComputedStyle(fe);
      ox += fr.left + fe.clientLeft + parseFloat(cs.paddingLeft || 0);
      oy += fr.top + fe.clientTop + parseFloat(cs.paddingTop || 0);
      w = w.parent;
    }
    return { x: ox, y: oy, cross: w !== w.top };
  }

  // Where the content of an iframe element starts, in the viewport of the top
  // window of this process.
  function iframeOrigin(fe) {
    const fr = fe.getBoundingClientRect(), cs = getComputedStyle(fe), f = frameOffset();
    return { x: f.x + fr.left + fe.clientLeft + parseFloat(cs.paddingLeft || 0), y: f.y + fr.top + fe.clientTop + parseFloat(cs.paddingTop || 0) };
  }

  const lib = { roleOf, accName, queryAll, resolve, resolveRoot, build, frameChain, centre, frameOffset, iframeOrigin, norm };
  globalThis.__ladiLoc = lib;
  return lib;
})()
