// Unit tests of placeholder.js. Run: bun test core/recorder/placeholder.test.js
// (go test runs them too when bun is on PATH).
const { test, expect } = require('bun:test');
const ph = require('./placeholder.js');

// A deterministic source of numbers in [0,1).
const seeded = (seed) => () => { seed = (seed * 1664525 + 1013904223) % 4294967296; return seed / 4294967296; };

test('normName', () => {
  const cases = {
    'email': 'email', 'Họ tên': 'ho_ten', 'Email Address': 'email_address', 'first name': 'first_name',
    'E-mail  address': 'e_mail_address', 'Đường dẫn': 'duong_dan', '  Tên  ': 'ten', '2nd phone': 'c_2nd_phone',
    '': 'column', '???': 'column', 'a__b': 'a_b', 'Số điện thoại!': 'so_dien_thoai',
  };
  for (const [raw, want] of Object.entries(cases)) expect(ph.normName(raw)).toBe(want);
  expect(ph.normName('x'.repeat(80)).length).toBe(40);
  for (const raw of ['Họ tên', '9 lives', 'a b-c', 'ÀÁẢÃẠ']) expect(ph.normName(raw)).toMatch(/^[a-z_][a-z0-9_]*$/);
});

test('find placeholders', () => {
  const got = ph.find('Hello {{Họ tên}}, mail {{ email }} {{}} {{a');
  expect(got.map((g) => g.name)).toEqual(['ho_ten', 'email']);
  expect(got[0].start).toBe(6);
  expect(ph.find('no braces')).toEqual([]);
});

test('token per field kind', () => {
  const rnd = seeded(7);
  const used = new Set();
  const t = (spec) => { const r = ph.makeToken(spec, used, { rnd }); expect(r.ok).toBe(true); used.add(r.token); return r.token; };
  expect(t({ kind: 'text' })).toMatch(/^ladi[0-9a-z]{6}$/);
  expect(t({ kind: 'email' })).toMatch(/^ladi[0-9a-z]{6}@example\.com$/);
  expect(t({ kind: 'url' })).toMatch(/^https:\/\/ladi[0-9a-z]{6}\.example\.com$/);
  expect(t({ kind: 'phone' })).toMatch(/^0[0-9]{9}$/);
  expect(t({ kind: 'number' })).toMatch(/^[0-9]+$/);
  expect(t({ kind: 'date', type: 'date' })).toMatch(/^\d{4}-\d{2}-\d{2}$/);
  expect(t({ kind: 'date', type: 'time' })).toMatch(/^\d{2}:\d{2}$/);
  expect(t({ kind: 'password' })).toMatch(/^ladi[0-9a-z]{6}-Aa9#kLm2$/);
});

test('token follows the field rules', () => {
  const rnd = seeded(11);
  const ok = (spec) => { const r = ph.makeToken(spec, new Set(), { rnd }); expect(r.ok).toBe(true); expect(ph.valid(r.token, spec)).toBe(true); return r.token; };
  expect(ok({ kind: 'text', maxlength: 8 }).length).toBe(8);
  expect(ok({ kind: 'text', minlength: 14 }).length).toBe(14);
  expect(ok({ kind: 'text', pattern: '[0-9]{10}' })).toMatch(/^[0-9]{10}$/);
  expect(ok({ kind: 'text', pattern: '[a-z]+' })).toMatch(/^[a-z]+$/);
  const n = Number(ok({ kind: 'number', min: '18', max: '65', step: '1' }));
  expect(n >= 18 && n <= 65).toBe(true);
  expect(ok({ kind: 'date', type: 'date', min: '2024-05-01', max: '2024-05-31' })).toMatch(/^2024-05-\d{2}$/);
  expect(ok({ kind: 'email', maxlength: 14 }).length).toBeLessThanOrEqual(14);
  // A rule nobody can meet: the token is still unique, and ok says false.
  const r = ph.makeToken({ kind: 'text', pattern: '^$x' }, new Set(), { rnd });
  expect(r.ok).toBe(false);
  expect(r.token).toMatch(/^ladi/);
});

test('tokens are unique', () => {
  const rnd = seeded(3), used = new Set();
  for (let i = 0; i < 300; i++) {
    const r = ph.makeToken({ kind: 'text' }, used, { rnd });
    expect(used.has(r.token)).toBe(false);
    used.add(r.token);
  }
});

test('split a value into text and columns', () => {
  const tokens = [{ name: 'name', token: 'ladiabc123' }, { name: 'phone', token: '0912345678' }, { name: 'email', token: 'ladixyz789@example.com' }];
  expect(ph.split('ladiabc123', tokens)).toEqual([{ v: 'name' }]);
  expect(ph.split('Hello ladiabc123!', tokens)).toEqual([{ l: 'Hello ' }, { v: 'name' }, { l: '!' }]);
  expect(ph.split('ladiabc123 ladixyz789@example.com', tokens)).toEqual([{ v: 'name' }, { l: ' ' }, { v: 'email' }]);
  expect(ph.split('0912345678', tokens)).toEqual([{ v: 'phone' }]);
  // A phone token inside other text is plain text.
  expect(ph.split('call 0912345678', tokens)).toBeNull();
  // A damaged token is plain text.
  expect(ph.split('ladiabc12', tokens)).toBeNull();
  expect(ph.split('', tokens)).toBeNull();
});
