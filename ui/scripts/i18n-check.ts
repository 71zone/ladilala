// bun run i18n:check
// 1. vi.json and en.json must have exactly the same keys.
// 2. every static t('key') / $t('key') / lz('key') used in src must exist, and dynamic prefixes must resolve to a group.
// 3. best-effort: no hard-coded English text in .vue templates (text nodes, placeholder/title/aria-label/alt) or in notify('...') calls.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'

const root = new URL('../src', import.meta.url).pathname
const load = (l: string) => JSON.parse(readFileSync(join(root, 'locales', l + '.json'), 'utf8')) as Record<string, unknown>
const flat = (o: Record<string, unknown>, p = ''): string[] => Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? flat(v as Record<string, unknown>, p + k + '.') : [p + k]))
const vi = load('vi'), en = load('en')
const kv = new Set(flat(vi)), ke = new Set(flat(en))
const errors: string[] = []
for (const k of kv) if (!ke.has(k)) errors.push(`key "${k}" is in vi.json but not in en.json`)
for (const k of ke) if (!kv.has(k)) errors.push(`key "${k}" is in en.json but not in vi.json`)
const group = (path: string) => path.split('.').reduce<unknown>((o, k) => (o && typeof o === 'object' ? (o as Record<string, unknown>)[k] : undefined), vi)

const files: string[] = []
const walk = (d: string) => { for (const f of readdirSync(d)) { const p = join(d, f); if (statSync(p).isDirectory()) walk(p); else if (/\.(vue|ts)$/.test(f)) files.push(p) } }
walk(root)

// Words that are names, units or mock-page content and stay as they are in every language.
const ALLOW = new Set(['Ladilala', 'GPU', 'Ctrl', 'K', 'VI', 'EN', 'OK', 'WebRTC', 'WebGL', 'Canvas', 'Chrome', 'AudioContext', 'CSV', 'Excel', 'JSON', 'REST', 'Google Sheets', 'User-Agent', 'Seed', 'Proxy', 'Macro', 'Engine', 'Doctor', 'Profile', 'Beta', 'Stable', 'Nightly'])
const hasEnglish = (s: string) => { const t = s.replace(/&[a-z]+;/g, ' ').trim(); return /[A-Za-z]{3,}/.test(t) && !ALLOW.has(t) && !/^[\w./:#@\-\[\]=*>()]+$/.test(t) }
const isMockPage = (rel: string) => rel.endsWith('PageField.vue')

for (const f of files) {
  const rel = relative(root, f); const src = readFileSync(f, 'utf8')
  for (const m of src.matchAll(/(?:\$t|\bt|lz|lzn)\(\s*(['"`])([A-Za-z0-9_.\-]+)(\1|\$\{|['"`]\s*\+)/g)) {
    const key = m[2], dynamic = key.endsWith('.')
    if (dynamic) { if (!group(key.replace(/\.$/, ''))) errors.push(`${rel}: dynamic key prefix "${key}" has no group`) }
    else if (!kv.has(key)) errors.push(`${rel}: key "${key}" is not defined`)
  }
  for (const m of src.matchAll(/\bnotify\(\s*(['"`])([^'"`]*)\1/g)) if (hasEnglish(m[2])) errors.push(`${rel}: hard-coded notify text "${m[2]}"`)
  if (!f.endsWith('.vue') || isMockPage(rel)) continue
  const tpl = /<template>([\s\S]*)<\/template>\s*$/.exec(src.replace(/<script[\s\S]*?<\/script>/g, ''))?.[1] ?? ''
  for (const m of tpl.matchAll(/>([^<>{}]+)</g)) if (hasEnglish(m[1])) errors.push(`${rel}: hard-coded text "${m[1].trim()}"`)
  for (const m of tpl.matchAll(/(?<![:@\w-])(placeholder|title|aria-label|alt|label|description|sub|help|heading)="([^"]*)"/g)) if (hasEnglish(m[2])) errors.push(`${rel}: hard-coded ${m[1]} "${m[2]}"`)
}
if (errors.length) { console.error(errors.join('\n')); console.error(`\ni18n:check failed (${errors.length})`); process.exit(1) }
console.log(`i18n:check ok: ${kv.size} keys in vi and en, ${files.length} files scanned`)
