// Pure helpers for placeholders typed in a recorded page: name normalization,
// type-aware test tokens and the split of a field value into text and columns.
// They touch no DOM. In the page they run in the isolated world "ladilala"
// (globalThis.__ladiPh); under Bun or Node they are a CommonJS module, so the
// unit tests run without a browser.
(() => {
  const B36 = '0123456789abcdefghijklmnopqrstuvwxyz';
  const PREFIX = 'ladi';
  const ID_LEN = 6;

  // Placeholder text -> column name: no diacritics, lower case, spaces and
  // dashes as "_", only [a-z0-9_], never a leading digit, never empty.
  function normName(raw) {
    let s = String(raw).normalize('NFD').replace(/[\u0300-\u036f]/g, '').replace(/đ/g, 'd').replace(/Đ/g, 'd').toLowerCase();
    s = s.replace(/[\s-]+/g, '_').replace(/[^a-z0-9_]/g, '').replace(/_+/g, '_').replace(/^_+|_+$/g, '');
    if (s.length > 40) s = s.slice(0, 40).replace(/_+$/, '');
    if (!s) s = 'column';
    if (/^[0-9]/.test(s)) s = 'c_' + s;
    return s;
  }

  // Complete placeholders in text, left to right: {{ any text without braces }}.
  function find(text) {
    const out = [];
    const re = /\{\{([^{}]*)\}\}/g;
    let m;
    while ((m = re.exec(text))) {
      if (m[1].trim() === '') continue;
      out.push({ start: m.index, end: m.index + m[0].length, raw: m[1].trim(), name: normName(m[1]) });
    }
    return out;
  }

  const defaultRnd = () => {
    const a = new Uint32Array(1);
    globalThis.crypto.getRandomValues(a);
    return a[0] / 4294967296;
  };
  const rid = (rnd, n) => { let s = ''; for (let i = 0; i < n; i++) s += B36[Math.floor(rnd() * 36)]; return s; };
  const digits = (rnd, n, first) => { let s = first || ''; while (s.length < n) s += Math.floor(rnd() * 10); return s; };
  const letters = (s) => s.replace(/[0-9]/g, (d) => 'abcdefghij'[+d]);
  const pad2 = (n) => String(n).padStart(2, '0');

  // The kind of column a field makes: text, email, phone, number, url, date or password.
  function kindOf(tag, type) {
    if (tag === 'textarea') return 'text';
    const t = (type || 'text').toLowerCase();
    if (t === 'email') return 'email';
    if (t === 'tel') return 'phone';
    if (t === 'number') return 'number';
    if (t === 'url') return 'url';
    if (t === 'password') return 'password';
    if (['date', 'time', 'datetime-local', 'month', 'week'].includes(t)) return 'date';
    return 'text';
  }

  function numOf(v) { const n = parseFloat(v); return Number.isFinite(n) ? n : null; }
  function reOf(p) {
    for (const f of ['v', 'u', '']) { try { return new RegExp('^(?:' + p + ')$', f); } catch (e) { /* next flag */ } }
    return null;
  }
  // Does s satisfy what the field declares? spec: {kind, type, minlength, maxlength, pattern, min, max, step}.
  function valid(s, spec) {
    if (spec.maxlength > 0 && s.length > spec.maxlength) return false;
    if (spec.minlength > 0 && s.length < spec.minlength) return false;
    if (spec.pattern) { const re = reOf(spec.pattern); if (re && !re.test(s)) return false; }
    switch (spec.kind) {
      case 'email': return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(s);
      case 'url': try { return !!new URL(s).host; } catch (e) { return false; }
      case 'number': {
        const n = Number(s), lo = numOf(spec.min), hi = numOf(spec.max), st = numOf(spec.step) || 1;
        if (!s || !Number.isFinite(n)) return false;
        if (lo !== null && n < lo) return false;
        if (hi !== null && n > hi) return false;
        return Math.abs((n - (lo || 0)) / st - Math.round((n - (lo || 0)) / st)) < 1e-9;
      }
      case 'date':
        if (spec.min && s < spec.min) return false;
        if (spec.max && s > spec.max) return false;
        return true;
      default: return true;
    }
  }

  function dateToken(spec, rnd) {
    const type = (spec.type || 'date').toLowerCase();
    const from = /^\d{4}-\d{2}-\d{2}/.test(spec.min || '') ? Date.parse(spec.min.slice(0, 10) + 'T00:00:00Z') : Date.UTC(2000, 0, 1);
    const to = /^\d{4}-\d{2}-\d{2}/.test(spec.max || '') ? Date.parse(spec.max.slice(0, 10) + 'T00:00:00Z') : from + 9000 * 864e5;
    const d = new Date(from + Math.floor(rnd() * Math.max(1, Math.floor((to - from) / 864e5) + 1)) * 864e5);
    const day = d.getUTCFullYear() + '-' + pad2(d.getUTCMonth() + 1) + '-' + pad2(d.getUTCDate());
    const time = pad2(Math.floor(rnd() * 24)) + ':' + pad2(Math.floor(rnd() * 60));
    if (type === 'time') return time;
    if (type === 'month') return day.slice(0, 7);
    if (type === 'datetime-local') return day + 'T' + time;
    if (type === 'week') return d.getUTCFullYear() + '-W' + pad2(1 + Math.floor(rnd() * 52));
    return day;
  }

  function numberToken(spec, rnd) {
    const lo = numOf(spec.min) ?? 0, st = numOf(spec.step) || 1;
    let hi = numOf(spec.max);
    if (hi === null || hi < lo) hi = lo + st * 999999;
    const n = lo + st * Math.floor(rnd() * Math.floor((hi - lo) / st + 1));
    return String(Number.isInteger(st) && Number.isInteger(lo) ? n : +n.toFixed(6));
  }

  // The token must fit the length rules: cut text to maxlength, pad it to minlength.
  function fit(s, spec) {
    if (spec.kind !== 'text' && spec.kind !== 'password') return s;
    if (spec.maxlength > 0 && s.length > spec.maxlength) s = s.slice(0, spec.maxlength);
    for (let i = 0; spec.minlength > 0 && s.length < spec.minlength; i++) s += B36[(i * 7) % 36];
    return s;
  }

  function candidates(spec, rnd) {
    const id = rid(rnd, ID_LEN);
    switch (spec.kind) {
      case 'email': return [PREFIX + id + '@example.com', id + '@example.com', id.slice(0, 3) + '@ex.co'];
      case 'phone': return [digits(rnd, 10, '09'), digits(rnd, 10, '03'), digits(rnd, 9, '0'), digits(rnd, 7)];
      case 'url': return ['https://' + PREFIX + id + '.example.com', 'http://' + id + '.io'];
      case 'number': return [numberToken(spec, rnd)];
      case 'date': return [dateToken(spec, rnd)];
      case 'password': return [PREFIX + id + '-Aa9#kLm2', 'Ladi' + id + '#9x', PREFIX + id];
      default: return [PREFIX + id, PREFIX + letters(id), digits(rnd, 10), id];
    }
  }

  // A unique test value for a field. used: Set of tokens in use. Options: rnd
  // (a function giving [0,1), for tests) and check (an extra test of one candidate,
  // the page's own validity check). ok is false when no candidate met the rules;
  // the token is still unique.
  function makeToken(spec, used, opts) {
    const rnd = (opts && opts.rnd) || defaultRnd, check = opts && opts.check;
    let first = '';
    for (let attempt = 0; attempt < 40; attempt++) {
      for (const c of candidates(spec, rnd)) {
        const s = fit(c, spec);
        if (used.has(s)) continue;
        if (!first) first = s;
        if (valid(s, spec) && (!check || check(s))) return { token: s, ok: true };
      }
    }
    return { token: first || PREFIX + rid(rnd, ID_LEN), ok: false };
  }

  // Tokens without the "ladi" prefix (phone, number, date) are real-looking
  // values: they count only when they are the whole field value.
  const exactOnly = (token) => !token.includes(PREFIX);

  // Split text into literal and column parts: [{l: 'Hello '}, {v: 'name'}].
  // tokens: [{name, token}]. null when the text holds no token.
  function split(text, tokens) {
    const exact = tokens.find((t) => exactOnly(t.token) && t.token === text);
    if (exact) return [{ v: exact.name }];
    const loose = tokens.filter((t) => !exactOnly(t.token)).sort((a, b) => b.token.length - a.token.length);
    const parts = [];
    let rest = text, hit = false;
    while (rest) {
      let at = -1, who = null;
      for (const t of loose) {
        const i = rest.indexOf(t.token);
        if (i >= 0 && (at < 0 || i < at)) { at = i; who = t; }
      }
      if (!who) break;
      if (at > 0) parts.push({ l: rest.slice(0, at) });
      parts.push({ v: who.name });
      rest = rest.slice(at + who.token.length);
      hit = true;
    }
    if (!hit) return null;
    if (rest) parts.push({ l: rest });
    return parts;
  }

  const api = { normName, find, kindOf, makeToken, split, valid, PREFIX, ID_LEN };
  if (typeof module === 'object' && module.exports) module.exports = api;
  else globalThis.__ladiPh = api;
})();
