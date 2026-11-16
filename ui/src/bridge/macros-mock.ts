// In-memory stand-in for the Go MacroService, used in a plain browser (bun run proto).
// It follows the same rules as the Go side where the screens depend on them
// (names are unique, validation paths, MapVariable changes one step) so the prototype shows real behaviour.
import { ServiceError } from './api'
import { mockRow } from './data-mock'
import {
  clone, emptyMacro, newStepId, originOf, refsOf,
  type ImportReview, type Macro, type MacroDTO, type MacroSummary, type Problem, type RecordStateEvent, type RecordStatus, type RecordStepEvent, type RecordWarningEvent,
  type RecordWarning, type RecordColumn, type RecordingDraft, type VarKind, type SaveMacroInput, type StartRecordingInput, type Step, type StepPreview, type Target, type VariableDecl,
} from './macros-types'

// ---- event bus for the mock recording ------------------------------------------------

type Handler = (data: never) => void
const handlers = new Map<string, Set<Handler>>()
export const bus = {
  on<T>(name: string, cb: (data: T) => void): () => void {
    const set = handlers.get(name) ?? new Set<Handler>()
    set.add(cb as Handler)
    handlers.set(name, set)
    return () => set.delete(cb as Handler)
  },
  emit(name: string, data: unknown) { handlers.get(name)?.forEach(h => (h as (d: unknown) => void)(data)) },
}

// ---- seed ------------------------------------------------------------------------------

interface Row { macro: Macro; version: number; hash: string; description: string; sourceId: string; warnings: RecordWarning[]; createdAt: string; updatedAt: string }
const label = (value: string) => ({ locators: [{ kind: 'label' as const, value }, { kind: 'css' as const, value: '#' + value.toLowerCase().replace(/\W+/g, '_') }] })
const css = (value: string, text?: string) => ({ locators: [{ kind: 'css' as const, value }, ...(text ? [{ kind: 'text' as const, value: text }] : [])] })
const vars = (...names: string[]): VariableDecl[] => names.map(name => ({ name, type: 'string', required: true }))
const fill = (id: string, field: string, v: string): Step => ({ id, action: 'fill', target: label(field), value: { expr: v } })

function seed(id: string, name: string, description: string, steps: Step[], variables: VariableDecl[], sourceId: string, ago: number): Row {
  const m: Macro = { schemaVersion: 1, id, name, allowedOrigins: ['https://shop.example'], variables, steps }
  const t = new Date(Date.now() - ago).toISOString()
  return { macro: m, version: 1, hash: hashOf(m), description, sourceId, warnings: [], createdAt: t, updatedAt: t }
}
const DAY = 86_400_000
const rows: Row[] = [
  seed('signup', 'Signup form', '@descSignup', [
    { id: 's001-navigate', action: 'navigate', value: { expr: 'https://shop.example/signup' } },
    fill('s002-fill', 'First name', '{{first_name}}'), fill('s003-fill', 'Last name', '{{last_name}}'), fill('s004-fill', 'Email', '{{email}}'),
    { id: 's005-select', action: 'select', target: label('Country'), value: { expr: '{{country}}' } },
    { id: 's006-fill', action: 'fill', target: { locators: [{ kind: 'css', value: '#cc' }], framePath: ['iframe#pay'] }, value: { expr: '{{card}}' } },
    { id: 's007-click', action: 'click', target: { locators: [{ kind: 'label', value: 'Accept terms' }, { kind: 'css', value: '#terms' }] } },
    { id: 's008-click', action: 'click', target: { locators: [{ kind: 'role', value: 'button', name: 'Place order' }, { kind: 'css', value: 'button[type=submit]' }] }, commitPoint: true,
      expect: [{ kind: 'textContains', target: css('h1'), value: { expr: 'Thank you' } }] },
    { id: 's009-wait', action: 'wait', target: css('.confirmation'), timeout: '10s' },
  ], vars('first_name', 'last_name', 'email', 'country', 'card'), 's1', 0),
  seed('checkout', 'Checkout flow', '@descCheckout', [
    { id: 's001-navigate', action: 'navigate', value: { expr: 'https://shop.example/cart' } },
    fill('s002-fill', 'Address', '{{address}}'), fill('s003-fill', 'City', '{{city}}'),
    { id: 's004-click', action: 'click', target: { locators: [{ kind: 'text', value: 'Pay now' }] }, commitPoint: true },
  ], vars('address', 'city'), 's2', DAY),
  seed('price', 'Price check', '@descPrice', [
    { id: 's001-navigate', action: 'navigate', value: { expr: 'https://shop.example/sku/{{sku}}' } },
    { id: 's002-assert', action: 'assert', expect: [{ kind: 'elementPresent', target: css('.price') }] },
  ], vars('sku'), 's3', 3 * DAY),
  seed('unsub', 'Newsletter unsub', '', [
    { id: 's001-navigate', action: 'navigate', value: { expr: 'https://shop.example/unsubscribe' } },
    fill('s002-fill', 'Email', '{{email}}'),
    { id: 's003-click', action: 'click', target: { locators: [{ kind: 'text', value: 'Unsubscribe' }] } },
  ], vars('email'), '', 14 * DAY),
]

function hashOf(m: Macro): string {
  const s = JSON.stringify(m)
  let h = 0
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0
  return h.toString(16).padStart(8, '0')
}

// ---- validation: the same paths the Go validator reports ------------------------------------

// Service Save and Validate declare every {{name}} a value uses (text, optional) before the strict check.
export function declareUsed(m: Macro): string[] {
  m.variables ??= []
  const have = new Set(m.variables.map(v => v.name))
  const added: string[] = []
  const scan = (s: string) => {
    for (const r of refsOf(s)) {
      if (have.has(r)) continue
      have.add(r)
      added.push(r)
      m.variables!.push({ name: r, type: 'string' })
    }
  }
  const target = (t?: Target) => { for (const l of t?.locators ?? []) { scan(l.value); if (l.kind === 'role') scan(l.name ?? '') } }
  for (const s of m.steps) {
    target(s.target)
    scan(s.value?.expr ?? '')
    for (const e of s.expect ?? []) { target(e.target); scan(e.value?.expr ?? '') }
  }
  for (const g of m.guards ?? []) if (g.locator) { scan(g.locator.value); if (g.locator.kind === 'role') scan(g.locator.name ?? '') }
  return added
}

export function validateLocal(m: Macro): Problem[] {
  const out: Problem[] = []
  const add = (path: string, msg: string) => out.push({ path, msg })
  if (!m.name.trim()) add('name', 'required')
  if (!m.allowedOrigins.length) add('allowedOrigins', 'at least one origin required')
  m.allowedOrigins.forEach((o, i) => { if (!originOf(o) || new URL(o).pathname !== '/') add(`allowedOrigins[${i}]`, 'must be scheme://host[:port]') })
  ;(m.variables ?? []).forEach((v, i) => { if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(v.name)) add(`variables[${i}].name`, 'must be an identifier') })
  if (!m.steps.length) add('steps', 'at least one step required')
  const seen = new Set<string>()
  m.steps.forEach((s, i) => {
    const p = `steps[${s.id || i}]`
    if (!s.id || seen.has(s.id)) add(`steps[${i}].id`, 'must be unique and not empty')
    seen.add(s.id)
    if (['click', 'dblclick', 'fill', 'select', 'check', 'uploadFile'].includes(s.action) && !s.target?.locators.length) add(p + '.target', 'required for ' + s.action)
    if (['fill', 'select', 'press', 'navigate', 'uploadFile', 'manual'].includes(s.action) && !s.value?.expr.trim()) add(p + '.value', 'required for ' + s.action)
    if (s.action === 'navigate' && s.value && !refsOf(s.value.expr).length) {
      const o = originOf(s.value.expr)
      if (!o || !m.allowedOrigins.includes(o)) add(p + '.value.expr', 'URL is not http(s) within allowedOrigins')
    }
    if (s.action === 'wait' && !s.target && !s.timeout) add(p, 'wait needs a target or a positive timeout')
    if (s.action === 'assert' && !s.expect?.length) add(p + '.expect', 'assert needs at least one expectation')
    s.target?.locators.forEach((l, j) => { if (!l.value.trim()) add(`${p}.target.locators[${j}].value`, 'required') })
  })
  return out
}

const invalid = (problems: Problem[]) => new ServiceError('invalid', { problems }, 'invalid')
const notFound = (what: string) => new ServiceError('notFound', { what }, 'notFound')

const find = (id: string) => rows.find(r => r.macro.id === id) ?? (() => { throw notFound('macro') })()
const toDTO = (r: Row): MacroDTO => clone({ macro: r.macro, version: r.version, hash: r.hash, description: r.description, sourceId: r.sourceId, warnings: r.warnings, createdAt: r.createdAt, updatedAt: r.updatedAt })
const nameTaken = (id: string, name: string) => rows.some(r => r.macro.id !== id && r.macro.name.trim().toLowerCase() === name.trim().toLowerCase())

// ---- service ------------------------------------------------------------------------------------

export async function listMacros(): Promise<MacroSummary[]> {
  return rows.map(r => ({
    id: r.macro.id, name: r.macro.name, description: r.description, stepCount: r.macro.steps.length, version: r.version, hash: r.hash,
    variables: (r.macro.variables ?? []).map(v => v.name), sourceId: r.sourceId, warningCount: r.warnings.length, updatedAt: r.updatedAt,
  })).sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
}
export async function getMacro(id: string): Promise<MacroDTO> { return toDTO(find(id)) }

export async function saveMacro(input: SaveMacroInput): Promise<MacroDTO> {
  const m = clone(input.macro)
  if (!m.id) m.id = 'm' + Math.random().toString(16).slice(2, 14)
  m.name = m.name.trim()
  declareUsed(m)
  const problems = validateLocal(m)
  if (problems.length) throw invalid(problems)
  if (nameTaken(m.id, m.name)) throw new ServiceError('conflict', { field: 'name' }, 'conflict')
  const now = new Date().toISOString()
  const have = rows.find(r => r.macro.id === m.id)
  const hash = hashOf(m)
  if (have) {
    if (hash !== have.hash) have.version++
    Object.assign(have, { macro: m, hash, description: input.description.trim(), updatedAt: now })
    if (input.warnings) have.warnings = clone(input.warnings)
    return toDTO(have)
  }
  const r: Row = { macro: m, version: 1, hash, description: input.description.trim(), sourceId: '', warnings: clone(input.warnings ?? []), createdAt: now, updatedAt: now }
  rows.push(r)
  return toDTO(r)
}
export async function validateMacro(m: Macro): Promise<Problem[]> {
  const c = clone(m)
  declareUsed(c)
  return validateLocal(c)
}
export async function duplicateMacro(id: string, name: string): Promise<MacroDTO> {
  const src = find(id)
  const base = name.trim() || src.macro.name
  let n = base
  for (let i = 2; nameTaken('', n); i++) n = `${base} (${i})`
  const m = clone(src.macro)
  m.id = 'm' + Math.random().toString(16).slice(2, 14)
  m.name = n
  const now = new Date().toISOString()
  const r: Row = { macro: m, version: 1, hash: hashOf(m), description: src.description, sourceId: src.sourceId, warnings: clone(src.warnings), createdAt: now, updatedAt: now }
  rows.push(r)
  return toDTO(r)
}
export async function deleteMacro(id: string): Promise<void> {
  const i = rows.findIndex(r => r.macro.id === id)
  if (i < 0) throw notFound('macro')
  rows.splice(i, 1)
}

const reviews = new Map<string, Macro>()
export async function importMacro(content: string): Promise<ImportReview> {
  const none = { reviewId: '', macro: null, allowedOrigins: [], commitPointSteps: [], variables: [], hash: '', stepCount: 0 }
  let m: Macro
  try { m = JSON.parse(content) as Macro } catch (e) { return { ...none, problems: [{ path: '$', msg: String(e) }] } }
  if (m.schemaVersion !== 1) return { ...none, problems: [{ path: 'schemaVersion', msg: 'must be 1' }] }
  m.allowedOrigins ??= []
  m.steps ??= []
  m.name ??= ''
  m.id ||= 'imported'
  const problems = validateLocal(m)
  if (problems.length) return { ...none, problems }
  const reviewId = Math.random().toString(16).slice(2)
  reviews.set(reviewId, m)
  return {
    reviewId, macro: clone(m), allowedOrigins: [...m.allowedOrigins], commitPointSteps: m.steps.filter(s => s.commitPoint).map(s => s.id),
    variables: (m.variables ?? []).map(v => v.name), problems: [], hash: hashOf(m), stepCount: m.steps.length,
  }
}
export async function confirmImport(reviewId: string): Promise<MacroDTO> {
  const m = reviews.get(reviewId)
  if (!m) throw notFound('review')
  reviews.delete(reviewId)
  const copy = clone(m)
  copy.id = 'm' + Math.random().toString(16).slice(2, 14)
  const base = copy.name.trim()
  for (let i = 2; nameTaken(copy.id, copy.name); i++) copy.name = `${base} (${i})`
  const now = new Date().toISOString()
  const r: Row = { macro: copy, version: 1, hash: hashOf(copy), description: '', sourceId: '', warnings: [], createdAt: now, updatedAt: now }
  rows.push(r)
  return toDTO(r)
}
export async function exportMacro(id: string, fileName: string): Promise<boolean> {
  const blob = new Blob([JSON.stringify(find(id).macro, null, 2) + '\n'], { type: 'application/json' })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = fileName
  a.click()
  URL.revokeObjectURL(a.href)
  return true
}

export async function mapVariable(macroId: string, stepId: string, column: string): Promise<MacroDTO> {
  const r = find(macroId)
  const col = column.trim()
  const s = r.macro.steps.find(x => x.id === stepId)
  if (!s) throw notFound('step')
  if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(col)) throw invalid([{ path: 'column', msg: 'must be an identifier' }])
  if ((s.action !== 'fill' && s.action !== 'select') || !s.value) throw invalid([{ path: `steps[${stepId}].action`, msg: 'only fill and select steps take a column' }])
  s.value = { expr: `{{${col}}}`, transforms: s.value.transforms }
  r.macro.variables ??= []
  if (!r.macro.variables.some(v => v.name === col)) r.macro.variables.push({ name: col, type: 'string', required: true })
  r.hash = hashOf(r.macro)
  r.version++
  r.updatedAt = new Date().toISOString()
  return toDTO(r)
}
export async function setMacroSource(macroId: string, sourceId: string): Promise<void> { find(macroId).sourceId = sourceId }

export async function previewRow(macroId: string, sourceId: string, rowIndex: number): Promise<StepPreview[]> {
  const r = find(macroId)
  const row = mockRow(sourceId, rowIndex)
  return r.macro.steps.map(s => {
    if (!s.value) return { stepId: s.id, action: s.action, value: '' }
    let missing = ''
    const value = s.value.expr.replace(/\{\{\s*([A-Za-z_]\w*)[^}]*\}\}/g, (_, n: string) => { if (!(n in row)) missing ||= n; return row[n] ?? '' })
    return missing ? { stepId: s.id, action: s.action, value: '', error: 'missingColumn' as const, column: missing } : { stepId: s.id, action: s.action, value }
  })
}

// ---- recording: a scripted session -----------------------------------------------------------------

interface Rec { id: string; m: Macro; warnings: RecordWarning[]; state: RecordStatus['state']; timers: number[] }
const recs = new Map<string, Rec>()
const emitState = (r: Rec) => bus.emit('record.state', { recordId: r.id, state: r.state } satisfies RecordStateEvent)

export async function startRecording(input: StartRecordingInput): Promise<string> {
  const origin = originOf(input.startUrl)
  if (!origin) throw invalid([{ path: 'startUrl', msg: 'must be an http or https URL' }])
  const m = emptyMacro()
  m.id = 'draft'
  m.name = input.name.trim()
  m.allowedOrigins = [origin]
  const r: Rec = { id: 'rec' + Math.random().toString(16).slice(2, 8), m, warnings: [], state: 'recording', timers: [] }
  recs.set(r.id, r)
  const addStep = (step: Omit<Step, 'id'>) => () => {
    if (r.state !== 'recording') return
    const full = { ...step, id: newStepId(m, step.action) } as Step
    m.steps.push(full)
    bus.emit('record.step', { recordId: r.id, stepIndex: m.steps.length - 1, step: clone(full) } satisfies RecordStepEvent)
  }
  const warn = (kind: RecordWarning['kind'], message: string) => () => {
    if (r.state !== 'recording') return
    const w: RecordWarning = { kind, stepIndex: m.steps.length, message }
    r.warnings.push(w)
    bus.emit('record.warning', { recordId: r.id, ...w } satisfies RecordWarningEvent)
  }
  // The scripted user types placeholders in the page: the recorder turns each into a data column.
  const col = (name: string, kind: VarKind) => () => { m.variables = [...(m.variables ?? []).filter(v => v.name !== name), { name, type: 'string', required: true, kind }] }
  const fillCol = (field: string, name: string, kind: VarKind, expr = '{{' + name + '}}') => () => { col(name, kind)(); addStep({ action: 'fill', target: label(field), value: { expr } })() }
  const script: [number, () => void][] = [
    [600, addStep({ action: 'navigate', value: { expr: input.startUrl } })],
    [1500, fillCol('Họ tên', 'ho_ten', 'text')],
    [2300, fillCol('Email', 'email', 'email')],
    [2700, fillCol('Mobile phone', 'phone', 'phone')],
    [3000, addStep({ action: 'fill', target: label('Password'), value: { expr: '{{password}}' } })],
    [3300, warn('canvas_unsupported', 'canvas')],
    [3700, addStep({ action: 'click', target: { locators: [{ kind: 'role', value: 'button', name: 'Submit' }, { kind: 'css', value: 'button[type=submit]' }] } })],
    [4400, warn('file_input_unsupported', 'file')],
  ]
  r.timers = script.map(([ms, f]) => window.setTimeout(f, ms))
  setTimeout(() => emitState(r), 0)
  return r.id
}
const rec = (id: string) => recs.get(id) ?? (() => { throw notFound('recording') })()
export async function pauseRecording(id: string): Promise<void> { const r = rec(id); r.state = 'paused'; emitState(r) }
export async function resumeRecording(id: string): Promise<void> { const r = rec(id); r.state = 'recording'; emitState(r) }
export async function getRecordingStatus(id: string): Promise<RecordStatus> { const r = rec(id); return { recordId: id, state: r.state, stepCount: r.m.steps.length, warningCount: r.warnings.length } }
export async function stopRecording(id: string): Promise<RecordingDraft> {
  const r = rec(id)
  r.timers.forEach(t => clearTimeout(t))
  recs.delete(id)
  const m = clone(r.m)
  m.id = ''
  if (m.steps.some(st => st.value?.expr === '{{password}}')) m.variables = [...(m.variables ?? []), { name: 'password', type: 'string', required: true, kind: 'password' }]
  const columns: RecordColumn[] = (m.variables ?? []).map(v => ({ name: v.name, kind: v.kind ?? 'text', protected: v.kind === 'password', steps: m.steps.filter(st => refsOf(st.value?.expr ?? '').includes(v.name)).length }))
  return { macro: m, warnings: clone(r.warnings), columns, problems: validateLocal({ ...m, id: 'draft', name: m.name || 'x' }).filter(p => p.path !== 'name') }
}
export async function discardRecording(id: string): Promise<void> {
  const r = rec(id)
  r.timers.forEach(t => clearTimeout(t))
  recs.delete(id)
}
