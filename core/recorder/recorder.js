// Recorder and overlay. Runs only in the isolated world "ladilala" (one per
// frame), after locator.js. It adds nothing to the page's main world: no
// window property, no patched function. Events leave through the binding
// ladiEmit, which Go adds to this context only. Until Go has added it, events
// wait in a queue that Go flushes through __ladiFlush().
(() => {
  if (globalThis.__ladiRec) return;
  const L = globalThis.__ladiLoc;
  const q = [];
  let seq = 0, paused = false, stopped = false, pending = null, timer = 0, host = null, ui = null;

  function flush() {
    if (typeof ladiEmit !== 'function') return;
    while (q.length) ladiEmit(q.shift());
  }
  function emit(msg) {
    msg.seq = ++seq; msg.ts = Date.now(); msg.url = location.href; msg.origin = location.origin;
    msg.top = window === window.top;
    q.push(JSON.stringify(msg));
    flush();
  }


  // --- placeholders: {{name}} typed in a field ------------------------------------
  // Go owns the list of columns (it survives a page load); this frame keeps a copy,
  // pushed by sync(). A new column is announced with a "var" event.
  const Ph = globalThis.__ladiPh;
  let vars = [], counts = {}, dead = [], lang = 'en', hintOn = false, composing = false, applying = false, firstVar = true;
  const unacked = new Set();
  const TXT = {
    en: {
      cols: 'Data columns', empty: 'Type {{name}} in any field.', warn: 'The page gets test values while you record. Do not submit on a live site you care about.',
      hint: 'Type {{email}} in a field to make a data column.', copy: 'Click to copy', copied: 'Copied', never: 'Never show again', close: 'Close',
      steps: (n) => n + (n === 1 ? ' step' : ' steps'), rename: 'Rename', del: 'Delete', yes: 'Delete', no: 'Cancel', exists: 'This name is used already.',
      confirm: (n) => 'Delete this column and its ' + (n === 1 ? '1 step' : n + ' steps') + '?', protectedTag: 'protected', recording: 'Recording', stopped: 'Stopped', paused: 'Paused',
      pause: 'Pause', resume: 'Resume', stop: 'Stop', badTest: 'The test value may not pass this field\'s rules.',
      kinds: { text: 'text', email: 'email', phone: 'phone', number: 'number', url: 'url', date: 'date', password: 'password' },
    },
    vi: {
      cols: 'Cột dữ liệu', empty: 'Gõ {{tên}} vào bất kỳ ô nào.', warn: 'Trang nhận giá trị thử khi bạn ghi. Đừng gửi biểu mẫu trên trang thật quan trọng.',
      hint: 'Gõ {{email}} vào một ô để tạo cột dữ liệu.', copy: 'Bấm để sao chép', copied: 'Đã sao chép', never: 'Không hiện nữa', close: 'Đóng',
      steps: (n) => n + ' bước', rename: 'Đổi tên', del: 'Xóa', yes: 'Xóa', no: 'Hủy', exists: 'Tên này đã có.',
      confirm: (n) => 'Xóa cột này và ' + n + ' bước dùng nó?', protectedTag: 'được bảo vệ', recording: 'Đang ghi', stopped: 'Đã dừng', paused: 'Tạm dừng',
      pause: 'Tạm dừng', resume: 'Tiếp tục', stop: 'Dừng', badTest: 'Giá trị thử có thể không qua luật của ô này.',
      kinds: { text: 'văn bản', email: 'email', phone: 'điện thoại', number: 'số', url: 'url', date: 'ngày', password: 'mật khẩu' },
    },
  };
  const tx = (k, a) => { const t = (TXT[lang] || TXT.en)[k]; return typeof t === 'function' ? t(a) : t; };
  const kindName = (k) => (TXT[lang] || TXT.en).kinds[k] || k;

  const SET = {
    textarea: Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set,
    input: Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set,
  };
  // The player's fill path: the native setter of this isolated world, then input and change.
  function writeValue(el, v) {
    applying = true;
    try {
      const set = SET[el.localName];
      if (set) set.call(el, v); else el.textContent = v;
      for (const t of ['input', 'change']) el.dispatchEvent(new Event(t, { bubbles: true, composed: true }));
    } finally { applying = false; }
  }
  function specOf(el) {
    const a = (n) => el.getAttribute && el.getAttribute(n);
    const type = (a('type') || 'text').toLowerCase();
    return {
      kind: Ph.kindOf(el.localName, type), type, minlength: parseInt(a('minlength'), 10) || 0, maxlength: parseInt(a('maxlength'), 10) || 0,
      pattern: a('pattern') || '', min: a('min') || '', max: a('max') || '', step: a('step') || '',
    };
  }
  // The page's own check of a candidate: a detached copy of the field takes the value.
  function pageCheck(el) {
    return (s) => {
      if (el.localName !== 'input' && el.localName !== 'textarea') return true;
      const c = el.cloneNode(false);
      c.removeAttribute('required');
      SET[c.localName].call(c, s);
      return c.checkValidity();
    };
  }
  const used = () => new Set(vars.map((v) => v.token).concat(dead));

  function ensureVar(name, el) {
    let v = vars.find((x) => x.name === name);
    if (v) return v;
    const spec = specOf(el);
    const r = Ph.makeToken(spec, used(), { check: pageCheck(el) });
    v = { name, kind: spec.kind, token: r.token, protected: spec.kind === 'password' };
    vars.push(v);
    unacked.add(name);
    emit({ t: 'var', op: 'add', var: name, kind: v.kind, token: v.token, protected: v.protected });
    if (!r.ok) notify(tx('badTest'));
    if (firstVar) { firstVar = false; closeHint(false); if (ui && ui.panel) { ui.panel.style.display = 'block'; } }
    renderPanel();
    return v;
  }
  let badgeTimer = 0;
  function showBadge(el, names) {
    if (!ui) return;
    const r = el.getBoundingClientRect();
    ui.badge.textContent = names.map((n) => { const v = vars.find((x) => x.name === n); return n + ' ' + (v && v.protected ? '••••••' : v ? v.token : ''); }).join('  ');
    ui.badge.style.background = '#16a34a';
    Object.assign(ui.badge.style, { display: 'block', left: Math.max(4, r.left) + 'px', top: Math.max(4, r.top - 22) + 'px' });
    clearTimeout(badgeTimer); badgeTimer = setTimeout(() => { ui.badge.style.display = 'none'; }, 2000);
  }
  // Replace each complete {{placeholder}} in the field by its token. Returns true when it did.
  function applyPlaceholders(el) {
    if (applying || stopped || paused || !isText(el)) return false;
    const text = el.isContentEditable && !('value' in el) ? el.textContent : el.value;
    const found = Ph.find(text);
    if (!found.length) return false;
    let caret = null;
    try { caret = el.selectionStart; } catch (e) { /* email and number inputs have no selection */ }
    let out = '', at = 0, shift = 0;
    const names = [];
    for (const f of found) {
      const v = ensureVar(f.name, el);
      names.push(v.name);
      out += text.slice(at, f.start) + v.token;
      if (caret !== null && f.end <= caret) shift += v.token.length - (f.end - f.start);
      at = f.end;
    }
    out += text.slice(at);
    writeValue(el, out);
    if (caret !== null) { try { el.setSelectionRange(caret + shift, caret + shift); } catch (e) { /* no selection support */ } }
    showBadge(el, names);
    return true;
  }
  const dropEmpty = () => { for (const k of Object.keys(counts)) if (!counts[k]) delete counts[k]; };
  function sync(s) {
    if (s.lang) lang = s.lang;
    if (s.vars) {
      for (const v of s.vars) unacked.delete(v.name);
      vars = s.vars.concat(vars.filter((v) => unacked.has(v.name) && !s.vars.some((x) => x.name === v.name)));
      if (vars.length) firstVar = false;
    }
    if (s.counts) { counts = s.counts; dropEmpty(); }
    if (s.dead) dead = s.dead;
    if (typeof s.hint === 'boolean') hintOn = s.hint;
    renderAll();
  }
  function closeHint(tell, never) {
    hintOn = false;
    if (tell !== false) emit({ t: 'hint', op: never ? 'off' : 'close' });
    renderAll();
  }

  const BUTTONISH = ['button', 'submit', 'reset', 'image', 'checkbox', 'radio', 'file', 'range', 'color', 'hidden'];
  const isText = (el) => !!el && ((el.localName === 'input' && !BUTTONISH.includes((el.getAttribute('type') || 'text').toLowerCase())) ||
    el.localName === 'textarea' || el.isContentEditable === true);
  const isToggle = (el) => !!el && el.localName === 'input' && ['checkbox', 'radio'].includes((el.getAttribute('type') || '').toLowerCase());
  const DATELIKE = ['date', 'time', 'datetime-local', 'month', 'week'];
  const isDate = (el) => !!el && el.localName === 'input' && DATELIKE.includes((el.getAttribute('type') || '').toLowerCase());
  const MSG = {
    canvas_unsupported: 'Canvas is not supported: clicks and drags on a canvas are not recorded',
    file_input_unsupported: 'File upload is not supported: choosing a file is not recorded',
  };
  // The kind of unsupported thing the event path touches, with its element.
  function unsupported(p, e) {
    for (const n of p) if (n.nodeType === 1 && n.localName === 'canvas') return { kind: 'canvas_unsupported', el: n };
    const isFile = (n) => n && n.localName === 'input' && n.type === 'file';
    for (const n of p) {
      if (n.nodeType !== 1) continue;
      if (isFile(n)) return { kind: 'file_input_unsupported', el: n };
      if (n.localName === 'label' && isFile(n.control)) return { kind: 'file_input_unsupported', el: n.control };
    }
    // A drop zone: a few levels up there is a container whose only control is one file input.
    let c = p.find((n) => n.nodeType === 1);
    for (let i = 0; c && i < 4 && c !== document.body && c !== document.documentElement; i++, c = c.parentElement) {
      const f = c.querySelectorAll ? c.querySelectorAll('input[type=file]') : [];
      if (f.length === 1 && c.querySelectorAll('input,select,textarea,button').length === 1) return { kind: 'file_input_unsupported', el: f[0] };
    }
    return null;
  }
  const warned = { el: null, at: 0 };
  function warn(u) {
    const now = Date.now();
    if (warned.el === u.el && now - warned.at < 800) return;
    warned.el = u.el; warned.at = now;
    const b = L.build(u.el);
    emit({ t: 'warn', kind: u.kind, msg: MSG[u.kind], target: { locators: b.locators, shadowPath: b.shadowPath, framePath: [], crossFrame: false } });
    notify(MSG[u.kind]);
  }
  const ACTION = 'button,a[href],input,select,textarea,summary,label,[role=button],[role=link],[role=menuitem],[role=tab],[role=option],[role=checkbox],[role=radio],[role=switch]';
  const path = (e) => (e.composedPath ? e.composedPath() : [e.target]);
  const ours = (e) => host && path(e).includes(host);
  function actionable(e) {
    const p = path(e);
    for (const n of p) if (n.nodeType === 1 && n.matches && n.matches(ACTION)) return n;
    return p[0];
  }

  function step(action, el, extra) {
    let target = null;
    if (el && el.nodeType === 1) {
      const b = L.build(el), fc = L.frameChain();
      target = { locators: b.locators, shadowPath: b.shadowPath, framePath: fc.path, crossFrame: fc.cross };
    }
    emit(Object.assign({ t: 'step', action, tag: el && el.localName, type: el && el.getAttribute && el.getAttribute('type'), target }, extra));
  }

  // A field that holds test tokens is recorded with its columns: "Hello {{name}}".
  // Text that holds no token is a fixed value. A token of a deleted column is not
  // recorded at all.
  function flushFill() {
    if (!pending) return;
    const el = pending; pending = null; clearTimeout(timer);
    if (!el.isConnected) return;
    applyPlaceholders(el);
    const sensitive = el.type === 'password';
    const raw = el.isContentEditable && !('value' in el) ? el.textContent : el.value;
    const name = el.getAttribute('name') || el.id || '';
    const parts = Ph.split(raw, vars.map((v) => ({ name: v.name, token: v.token })));
    if (parts) return step('fill', el, { value: raw, sensitive: false, name, parts });
    if (dead.some((t) => raw.includes(t))) return;
    step('fill', el, { value: sensitive ? '' : raw, sensitive, name });
  }

  const on = (type, fn) => window.addEventListener(type, (e) => {
    if (stopped || paused || !e.isTrusted || ours(e)) return;
    try { fn(e); } catch (err) { emit({ t: 'error', msg: String(err && err.message || err) }); }
  }, { capture: true, passive: true });

  on('mousedown', (e) => { const u = unsupported(path(e), e); if (u) { flushFill(); warn(u); } });
  on('drop', (e) => {
    if (!e.dataTransfer || !e.dataTransfer.files || !e.dataTransfer.files.length) return;
    const u = unsupported(path(e), e);
    if (u && u.kind === 'file_input_unsupported') warn(u);
  });
  on('click', (e) => {
    const un = unsupported(path(e), e);
    if (un) { flushFill(); return warn(un); }
    if (e.detail >= 2) return; // the second click of a double click; dblclick records it
    const el = actionable(e);
    if (!el || isText(el) || isToggle(el) || el.localName === 'select' || el.localName === 'option') return;
    if (el.localName === 'label' && el.control && (isToggle(el.control) || isText(el.control) || el.control.localName === 'select')) return;
    flushFill();
    const submit = (el.localName === 'button' && (el.type || 'submit') === 'submit' && !!el.form) || (el.localName === 'input' && el.type === 'submit');
    step('click', el, { commit: submit });
  });
  on('dblclick', (e) => {
    if (unsupported(path(e), e)) return;
    const el = actionable(e);
    if (!el || isText(el) || isToggle(el) || el.localName === 'select' || el.localName === 'option') return;
    flushFill();
    step('dblclick', el, {});
  });
  on('dragstart', () => emit({ t: 'gap', what: 'drag-and-drop' }));
  on('input', (e) => {
    const el = path(e)[0];
    if (!isText(el)) return;
    if (!e.isComposing && !composing) applyPlaceholders(el);
    if (pending && pending !== el) flushFill();
    pending = el;
    clearTimeout(timer);
    timer = setTimeout(flushFill, 700);
  });
  on('change', (e) => {
    const el = path(e)[0];
    // A date input fires change for every valid edit (each digit of the year);
    // the value is read when the user leaves it, clicks away or pauses.
    if (isDate(el)) return;
    if (isText(el)) return flushFill();
    flushFill();
    if (el.localName === 'select') {
      if (el.multiple) return emit({ t: 'gap', what: 'multi-select' });
      const o = el.selectedOptions[0];
      return step('select', el, { value: o ? (o.value || o.textContent.trim()) : '' });
    }
    if (isToggle(el)) {
      if (el.type === 'radio' && !el.checked) return;
      step('check', el, { value: el.checked ? 'true' : 'false' });
    }
  });
  window.addEventListener('focusout', (e) => { if (!stopped && !paused && e.isTrusted && !ours(e)) flushFill(); }, true);
  // A search box or combobox with no form: the Enter may or may not submit. Go
  // keeps the step only when a navigation follows.
  const mayRun = (el) => !!el && (el.localName === 'textarea' || el.isContentEditable === true ||
    (el.getAttribute && (el.getAttribute('role') === 'combobox' || el.hasAttribute('aria-autocomplete'))));
  on('keydown', (e) => {
    if (e.isComposing || e.ctrlKey || e.metaKey || e.altKey) return;
    if (e.key !== 'Enter' && e.key !== 'Tab') return;
    const el = path(e)[0];
    if (e.key === 'Tab' && isDate(el)) return; // moves between the segments of a date field
    if (e.key === 'Enter' && (!el || (el.matches && el.matches('button,a,summary,[role=button],[role=link]')))) return;
    const multi = el && (el.localName === 'textarea' || el.isContentEditable === true);
    if (e.key === 'Enter' && multi && e.shiftKey) return; // a new line
    flushFill();
    const isEnter = e.key === 'Enter';
    step('press', el, { value: e.shiftKey && e.key === 'Tab' ? 'Shift+Tab' : e.key, commit: isEnter && !multi && !!(el && el.form), maybe: isEnter && (multi || (!el.form && mayRun(el))) });
  });
  // Vietnamese Telex and other IMEs: nothing is replaced while a composition runs.
  window.addEventListener('compositionstart', (e) => { if (e.isTrusted) composing = true; }, true);
  window.addEventListener('compositionend', (e) => {
    composing = false;
    if (stopped || paused || !e.isTrusted || ours(e)) return;
    const el = path(e)[0];
    if (!isText(el)) return;
    applyPlaceholders(el);
    if (pending && pending !== el) flushFill();
    pending = el;
    clearTimeout(timer);
    timer = setTimeout(flushFill, 700);
  }, true);
  window.addEventListener('pagehide', () => { if (!stopped) flushFill(); }, true);


  // --- overlay: one host element in a closed shadow root ---------------------
  const CSS = ':host{all:initial}.hl{position:fixed;pointer-events:none;border:2px solid #2563eb;background:rgba(37,99,235,.12);z-index:2147483647;display:none;box-sizing:border-box}' +
    '.bar{position:fixed;top:8px;right:8px;z-index:2147483647;font:12px system-ui,sans-serif;background:#111;color:#fff;border-radius:16px;padding:4px 8px;display:flex;gap:8px;align-items:center;box-shadow:0 2px 8px rgba(0,0,0,.4)}' +
    '.hl.warn{border-color:#f59e0b;background:rgba(245,158,11,.22)}.note{position:fixed;bottom:12px;right:8px;z-index:2147483647;max-width:320px;font:12px system-ui,sans-serif;background:#f59e0b;color:#111;border-radius:8px;padding:6px 10px;display:none;box-shadow:0 2px 8px rgba(0,0,0,.4)}' +
    '.badge{position:fixed;z-index:2147483647;pointer-events:none;display:none;font:600 11px ui-monospace,Menlo,monospace;background:#16a34a;color:#fff;border-radius:6px;padding:2px 6px;box-shadow:0 1px 4px rgba(0,0,0,.4);white-space:nowrap}' +
    '.dot{width:8px;height:8px;border-radius:50%;background:#ef4444}.bar button,.card button,.panel button{all:unset;cursor:pointer;padding:2px 8px;border-radius:10px;background:#333;color:#fff}' +
    '.panel{position:fixed;top:44px;right:8px;z-index:2147483647;width:300px;font:12px system-ui,sans-serif;background:#111;color:#fff;border-radius:12px;padding:8px;box-shadow:0 2px 8px rgba(0,0,0,.4);display:none}' +
    '.panel .warn{margin:0 0 6px;padding:4px 6px;border-radius:6px;background:#78350f;color:#fde68a;font-size:11px;line-height:1.35}.panel .empty{margin:0;color:#9ca3af}' +
    '.list{display:grid;gap:4px;max-height:50vh;overflow:auto}.row{padding:5px 8px;border-radius:8px;background:#1f2937;border:1px solid #374151}' +
    '.row .l1{display:flex;gap:6px;align-items:center}.row b{font:600 12px ui-monospace,Menlo,monospace;flex:1;overflow:hidden;text-overflow:ellipsis}.row .tag{color:#9ca3af;font-size:11px}' +
    '.row .tok{font:11px ui-monospace,Menlo,monospace;color:#86efac;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.row .l2{display:flex;gap:6px;align-items:center;margin-top:2px;color:#9ca3af;font-size:11px}' +
    '.row .l2 .tok{flex:1}.row button{padding:0 6px;line-height:18px}.row .ask{margin-top:4px;display:flex;gap:6px;align-items:center;flex-wrap:wrap}.row .ask button.y{background:#b91c1c}' +
    '.row input{all:unset;flex:1;min-width:0;background:#000;border:1px solid #4b5563;border-radius:6px;padding:1px 6px;font:12px ui-monospace,Menlo,monospace}.row .err{color:#fca5a5;font-size:11px}' +
    '.card{position:fixed;bottom:12px;left:12px;z-index:2147483647;width:300px;font:12px/1.4 system-ui,sans-serif;background:#111;color:#fff;border-radius:12px;padding:10px 12px;box-shadow:0 2px 10px rgba(0,0,0,.45);display:none}' +
    '.card .top{display:flex;gap:8px;align-items:flex-start}.card .top span{flex:1}.card .x{padding:0 7px;line-height:18px}.card .chip{display:inline-block;margin-top:6px;padding:3px 10px;border-radius:8px;background:#14532d;border:1px solid #22c55e;font:600 12px ui-monospace,Menlo,monospace}' +
    '.card .foot{margin-top:6px;display:flex;gap:8px;align-items:center;color:#9ca3af;font-size:11px}.card .foot button{background:none;padding:0;text-decoration:underline;color:#9ca3af}';
  const h = (tag, cls, text) => { const n = document.createElement(tag); if (cls) n.className = cls; if (text !== undefined) n.textContent = text; return n; };

  function mount() {
    if (host) return true;
    const root = document.documentElement;
    if (!root) return false;
    host = document.createElement('ladi-' + Math.random().toString(36).slice(2, 8));
    const sh = host.attachShadow({ mode: 'closed' });
    // A constructed stylesheet is not subject to the page's Content-Security-Policy; a <style>
    // element is, and a page with a strict style-src would leave the overlay unstyled.
    try {
      const sheet = new CSSStyleSheet();
      sheet.replaceSync(CSS);
      sh.adoptedStyleSheets = [sheet];
    } catch (e) {
      const css = document.createElement('style');
      css.textContent = CSS;
      sh.append(css);
    }
    const hl = h('div', 'hl'), badge = h('div', 'badge');
    sh.append(hl, badge);
    ui = { hl, badge };
    // A click on the overlay must not take the focus from the field the user is typing in.
    sh.addEventListener('mousedown', (e) => { if (!(e.target && e.target.localName === 'input')) e.preventDefault(); });
    if (window === window.top) {
      const bar = h('div', 'bar'), dot = h('span', 'dot'), label = h('span');
      const cb = h('button'), pb = h('button'), sb = h('button');
      cb.addEventListener('click', () => { ui.panel.style.display = ui.panel.style.display === 'block' ? 'none' : 'block'; });
      pb.addEventListener('click', () => { api.pause(!paused); emit({ t: 'pause', paused }); });
      sb.addEventListener('click', () => { api.stop(); emit({ t: 'stop' }); });
      bar.append(dot, label, cb, pb, sb);
      const note = h('div', 'note');
      ui.panel = h('div', 'panel'); ui.card = h('div', 'card');
      sh.append(bar, note, ui.panel, ui.card);
      Object.assign(ui, { bar, note, label, dot, pb, cb, sb });
    }
    root.appendChild(host);
    renderAll();
    emit({ t: 'overlay', ms: performance.now() });
    return true;
  }
  let noteTimer = 0;
  function notify(msg) {
    if (ui && ui.note) {
      ui.note.textContent = msg; ui.note.style.display = 'block';
      clearTimeout(noteTimer); noteTimer = setTimeout(() => { ui.note.style.display = 'none'; }, 5000);
    } else emit({ t: 'notice', msg });
  }
  function renderAll() { paint(); renderPanel(); renderCard(); }
  function paint() {
    if (!ui || !ui.label) return;
    ui.label.textContent = stopped ? tx('stopped') : paused ? tx('paused') : tx('recording');
    ui.dot.style.background = stopped || paused ? '#9ca3af' : '#ef4444';
    ui.pb.textContent = paused ? tx('resume') : tx('pause');
    ui.sb.textContent = tx('stop');
    ui.cb.textContent = tx('cols') + (vars.length ? ' (' + vars.length + ')' : '');
    ui.panel.style.opacity = paused ? '.5' : '1';
    if (stopped) { ui.panel.style.display = 'none'; ui.card.style.display = 'none'; }
  }

  let asking = '', renaming = '', renameErr = '';
  function renderPanel() {
    if (!ui || !ui.panel) return;
    const p = ui.panel;
    p.textContent = '';
    if (vars.length) p.append(h('p', 'warn', tx('warn')));
    else { p.append(h('p', 'empty', tx('empty'))); }
    const list = h('div', 'list');
    for (const v of vars) {
      const n = counts[v.name] || 0, row = h('div', 'row');
      const l1 = h('div', 'l1');
      if (renaming === v.name) {
        const inp = h('input'); inp.value = v.name; inp.setAttribute('data-rename', v.name);
        const from = v.name;
        const commit = () => {
          const to = Ph.normName(inp.value);
          if (to === from) { renaming = ''; renameErr = ''; return renderPanel(); }
          if (vars.some((x) => x.name === to)) { renameErr = tx('exists'); return renderPanel(); }
          renaming = ''; renameErr = '';
          v.name = to; renderPanel();
          emit({ t: 'varop', op: 'rename', var: from, to });
        };
        inp.addEventListener('keydown', (e) => { e.stopPropagation(); if (e.key === 'Enter') commit(); else if (e.key === 'Escape') { renaming = ''; renameErr = ''; renderPanel(); } });
        const ok = h('button', '', '✓'); ok.addEventListener('click', commit);
        l1.append(inp, ok);
        row.append(l1);
        if (renameErr) row.append(h('div', 'err', renameErr));
        setTimeout(() => { inp.focus(); inp.select(); }, 0);
      } else {
        const ed = h('button', '', '✎'); ed.title = tx('rename'); ed.setAttribute('data-act', 'rename:' + v.name);
        ed.addEventListener('click', () => { renaming = v.name; renameErr = ''; asking = ''; renderPanel(); });
        const rm = h('button', '', '✕'); rm.title = tx('del'); rm.setAttribute('data-act', 'delete:' + v.name);
        rm.addEventListener('click', () => { asking = v.name; renaming = ''; renderPanel(); });
        l1.append(h('b', '', v.name), h('span', 'tag', kindName(v.kind) + (v.protected && v.kind !== 'password' ? ' · ' + tx('protectedTag') : '')), ed, rm);
        row.append(l1);
        const l2 = h('div', 'l2');
        l2.append(h('span', 'tok', v.protected ? '••••••••' : v.token), h('span', '', tx('steps', n)));
        row.append(l2);
        if (asking === v.name) {
          const ask = h('div', 'ask', tx('confirm', n));
          const y = h('button', 'y', tx('yes')), no = h('button', '', tx('no'));
          y.setAttribute('data-act', 'confirm');
          y.addEventListener('click', () => { asking = ''; emit({ t: 'varop', op: 'delete', var: v.name }); vars = vars.filter((x) => x.name !== v.name); renderAll(); });
          no.addEventListener('click', () => { asking = ''; renderPanel(); });
          ask.append(y, no);
          row.append(ask);
        }
      }
      list.append(row);
    }
    p.append(list);
    paint();
  }

  // The first-use hint: one card, with an example the user can copy.
  function copyText(s) {
    const done = () => { if (ui && ui.copy) { ui.copy.textContent = tx('copied'); setTimeout(() => { if (ui.copy) ui.copy.textContent = tx('copy'); }, 1500); } };
    const fallback = () => {
      const t = document.createElement('textarea');
      t.value = s; t.style.cssText = 'position:fixed;opacity:0';
      document.documentElement.appendChild(t); t.select();
      try { document.execCommand('copy'); } catch (e) { /* no clipboard */ }
      t.remove(); done();
    };
    if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(s).then(done, fallback); else fallback();
  }
  function renderCard() {
    if (!ui || !ui.card) return;
    const c = ui.card;
    c.textContent = '';
    if (!hintOn || stopped) { c.style.display = 'none'; return; }
    c.style.display = 'block';
    const top = h('div', 'top');
    const x = h('button', 'x', '✕'); x.title = tx('close'); x.setAttribute('data-act', 'hint-close');
    x.addEventListener('click', () => closeHint(true, false));
    top.append(h('span', '', tx('hint')), x);
    const chip = h('div', 'chip', '{{email}}'); chip.setAttribute('data-act', 'hint-chip'); chip.style.cursor = 'pointer';
    chip.addEventListener('click', () => copyText('{{email}}'));
    const foot = h('div', 'foot');
    ui.copy = h('span', '', tx('copy'));
    const never = h('button', '', tx('never')); never.setAttribute('data-act', 'hint-never');
    never.addEventListener('click', () => closeHint(true, true));
    foot.append(ui.copy, never);
    c.append(top, chip, foot);
  }

  let raf = 0, lastMove = null, hovered = null;
  window.addEventListener('mousemove', (e) => {
    if (!ui || stopped || paused) return;
    lastMove = e;
    if (raf) return;
    raf = requestAnimationFrame(() => {
      raf = 0;
      const el = lastMove && !ours(lastMove) ? path(lastMove)[0] : null;
      if (!el || el.nodeType !== 1) { ui.hl.style.display = 'none'; return; }
      const un = unsupported(path(lastMove), lastMove);
      ui.hl.classList.toggle('warn', !!un);
      if (un && hovered !== un.el) notify(MSG[un.kind]);
      hovered = un ? un.el : null;
      const r = el.getBoundingClientRect();
      Object.assign(ui.hl.style, { display: 'block', left: r.left + 'px', top: r.top + 'px', width: r.width + 'px', height: r.height + 'px' });
    });
  }, { capture: true, passive: true });

  const api = {
    pause(v) { paused = !!v; if (ui) ui.hl.style.display = 'none'; paint(); },
    stop() { flushFill(); stopped = true; if (ui) ui.hl.style.display = 'none'; renderAll(); },
    notice: (m) => { if (ui && ui.note) notify(m); },
    ready: () => !!host,
    sync,
    // Computed position and box of the overlay parts, for the layout test.
    layout() {
      if (!ui || !ui.panel) return null;
      const out = { vw: innerWidth, vh: innerHeight };
      for (const [k, el] of [['bar', ui.bar], ['panel', ui.panel], ['card', ui.card], ['note', ui.note]]) {
        const cs = getComputedStyle(el), r = el.getBoundingClientRect();
        out[k] = { position: cs.position, display: cs.display, x: r.left, y: r.top, w: r.width, h: r.height };
      }
      return out;
    },
    // Center of an overlay control in viewport pixels, for tests that drive it with real mouse events.
    rect(what) {
      if (!ui || !ui.panel) return null;
      let el = null;
      if (what === 'cols') el = ui.cb;
      else if (what === 'stop') el = ui.sb;
      else el = (what === 'hint-chip' || what === 'hint-close' || what === 'hint-never' ? ui.card : ui.panel).querySelector('[data-act="' + what + '"]');
      if (!el) return null;
      const r = el.getBoundingClientRect();
      return r.width ? [r.left + r.width / 2, r.top + r.height / 2] : null;
    },
  };
  globalThis.__ladiRec = api;
  globalThis.__ladiFlush = flush;

  if (!mount()) {
    const mo = new MutationObserver(() => { if (mount()) mo.disconnect(); });
    mo.observe(document, { childList: true });
  }
})();
