// In-memory stand-in for the Go DataService, used in a plain browser (bun run proto).
import { ServiceError } from './api'
import { getMacro } from './macros-mock'
import { refsOf, type Macro } from './macros-types'
import type { FileSourceInput, MappingDTO, MappingVariable, RESTSourceInput, RowIssue, SourceDTO, SourceKind, SourcePreview, ValidationResult } from './data-types'

const sheet = [
  { first_name: 'Linh', last_name: 'Tran', email: 'linh.tran@mail.test', phone: '+84 90 123 4567', address: '12 Nguyen Hue', city: 'Ho Chi Minh', zip: '700000', country: 'VN', card: '4242 4242 4242 4242' },
  { first_name: 'Marcus', last_name: 'Weber', email: 'm.weber@mail.test', phone: '+49 151 2345678', address: 'Hauptstr. 4', city: 'Berlin', zip: '10115', country: 'DE', card: '4000 0566 5566 5556' },
  { first_name: 'Ana', last_name: 'Silva', email: 'ana.silva@mail.test', phone: '+55 11 91234-5678', address: 'Rua Augusta 90', city: 'Sao Paulo', zip: '01304', country: 'BR', card: '5555 5555 5555 4444' },
  { first_name: 'Kenji', last_name: 'Sato', email: 'kenji@mail.test', phone: '', address: '1-2-3 Shibuya', city: 'Tokyo', zip: '150-0002', country: 'JP', card: '4242 4242 4242 4242' },
]
const names = ['Mai', 'Hung', 'Lan', 'Minh', 'Thu', 'Duc', 'Hoa', 'Nam']
const cols: Record<SourceKind, string[]> = {
  csv: Object.keys(sheet[0]),
  excel: ['address', 'city', 'sku', 'qty', 'notes'],
  json: ['email', 'first_name', 'last_name'],
  rest: ['email', 'first_name', 'last_name', 'sku'],
}
const TOTAL: Record<string, number> = { s1: 412, s2: 1280, s3: -1 }
const colsFor = (s: SourceDTO) => cols[s.kind]
const rowOf = (s: SourceDTO, i: number): Record<string, string> => genRow(s.kind, i)

function genRow(kind: SourceKind, i: number): Record<string, string> {
  if (kind === 'csv' && i < sheet.length) return { ...sheet[i] }
  const n = names[i % names.length]
  const all: Record<string, string> = {
    first_name: n, last_name: 'Pham ' + i, email: `${n.toLowerCase()}${i}@mail.test`, phone: i % 9 === 3 ? '' : `+84 90 ${100 + i} 4567`, address: `${i + 1} Le Loi`, city: 'Da Nang',
    zip: String(550000 + i), country: 'VN', card: '4242 4242 4242 4242', sku: `SKU-${1000 + i}`, qty: String(1 + (i % 4)), notes: i % 5 === 0 ? 'gift' : '',
  }
  return Object.fromEntries(cols[kind].map(c => [c, all[c] ?? '']))
}

const sources: SourceDTO[] = [
  src('s1', 'sheet-april.csv', 'csv', { path: 'C:\\data\\sheet-april.csv', encoding: 'utf-8', delimiter: ',' }),
  src('s2', 'orders.xlsx', 'excel', { path: 'C:\\data\\orders.xlsx', sheet: 'April' }),
  src('s3', 'REST /customers', 'rest', { url: 'https://api.shop.example/v1/customers', authKind: 'bearer', hasAuth: true, rowsPath: '$.data', paging: 'page', pageSize: 100 }),
]
function src(id: string, name: string, kind: SourceKind, p: Partial<SourceDTO>): SourceDTO {
  const t = new Date().toISOString()
  return { id, name, kind, path: '', encoding: '', delimiter: '', sheet: '', nested: '', url: '', rowsPath: '', authKind: 'none', basicUser: '', hasAuth: false, headerNames: [], paging: '', pageSize: 0, columns: [], rowCount: -1, createdAt: t, updatedAt: t, ...p }
}
const find = (id: string) => sources.find(s => s.id === id) ?? (() => { throw new ServiceError('notFound', { what: 'source' }, 'notFound') })()

/** One row of a mock source, keyed by column. Used by the macro mock's PreviewRow. */
export function mockRow(sourceId: string, index: number): Record<string, string> {
  const s = sources.find(x => x.id === sourceId)
  return s ? rowOf(s, index) : genRow('csv', index)
}

// ---- service -------------------------------------------------------------------------------------

const canned = ['C:\\data\\customers.csv', 'C:\\data\\orders-may.xlsx', 'C:\\data\\emails.json']
let picked = 0
export async function pickDataFile(): Promise<string> { return canned[picked++ % canned.length] }

const kindOf = (path: string): SourceKind => {
  const ext = path.toLowerCase().split('.').pop() ?? ''
  if (ext === 'xlsx' || ext === 'xlsm') return 'excel'
  if (ext === 'json') return 'json'
  if (['csv', 'tsv', 'txt'].includes(ext)) return 'csv'
  throw new ServiceError('data.format', { reason: 'extension' }, 'unsupported file type')
}
function preview(kind: SourceKind, extra: Partial<SourcePreview>, total: number, limit = 8): SourcePreview {
  const columns = cols[kind]
  return {
    kind, encoding: '', delimiter: '', sheet: '', sheets: [], columns, from: 0, total, duplicates: [], issues: [],
    rows: Array.from({ length: Math.min(limit, total < 0 ? limit : total) }, (_, i) => columns.map(c => genRow(kind, i)[c])), ...extra,
  }
}
export async function inspectFile(input: FileSourceInput): Promise<SourcePreview> {
  const kind = kindOf(input.path)
  if (kind === 'csv') return preview(kind, { encoding: input.encoding || 'utf-8', delimiter: input.delimiter || ',' }, 412)
  if (kind === 'excel') return preview(kind, { sheets: ['April', 'May', 'Summary'], sheet: input.sheet || 'April' }, input.sheet === 'Summary' ? 12 : 1280)
  return preview(kind, {}, 96)
}
export async function createSourceFromFile(input: FileSourceInput): Promise<SourceDTO> {
  const p = await inspectFile(input)
  const name = input.name.trim() || input.path.split(/[\\/]/).pop() || input.path
  const s = src('s' + (sources.length + 1) + Math.random().toString(16).slice(2, 5), name, p.kind, { path: input.path, encoding: p.encoding, delimiter: p.delimiter, sheet: p.sheet, nested: input.nested, columns: p.columns, rowCount: p.total })
  sources.push(s)
  return structuredClone(s)
}
export async function testRestSource(input: RESTSourceInput): Promise<SourcePreview> {
  if (!/^https?:\/\//.test(input.url)) throw new ServiceError('data.format', { reason: 'url' }, 'bad url')
  if (input.authKind === 'bearer' && input.bearerToken === 'wrong') throw new ServiceError('data.auth', {}, 'refused')
  return preview('rest', {}, -1)
}
export async function createRestSource(input: RESTSourceInput): Promise<SourceDTO> {
  await testRestSource(input)
  const s = src('s' + (sources.length + 1) + Math.random().toString(16).slice(2, 5), input.name.trim() || input.url, 'rest', {
    url: input.url, rowsPath: input.rowsPath, authKind: input.authKind, basicUser: input.basicUser, hasAuth: input.authKind !== 'none' || Object.keys(input.headers).length > 0,
    headerNames: Object.keys(input.headers), paging: input.paging, pageSize: input.pageSize, columns: cols.rest,
  })
  sources.push(s)
  return structuredClone(s)
}
export async function listSources(): Promise<SourceDTO[]> { return sources.map(s => ({ ...structuredClone(s), columns: [], rowCount: -1 })) }
export async function getSource(id: string): Promise<SourceDTO> {
  const s = find(id)
  return structuredClone({ ...s, columns: colsFor(s), rowCount: TOTAL[s.id] ?? s.rowCount })
}
export async function deleteSource(id: string): Promise<void> { sources.splice(sources.indexOf(find(id)), 1) }
export async function previewSource(id: string, from: number, limit: number): Promise<SourcePreview> {
  const s = find(id)
  const total = TOTAL[id] ?? s.rowCount
  const columns = colsFor(s)
  const n = Math.min(limit, total < 0 ? limit : Math.max(0, total - from))
  return { ...preview(s.kind, { sheet: s.sheet, encoding: s.encoding, delimiter: s.delimiter, sheets: s.kind === 'excel' ? ['April', 'May'] : [] }, total), columns, from, rows: Array.from({ length: n }, (_, i) => columns.map(c => rowOf(s, from + i)[c])) }
}

// ---- mapping ----------------------------------------------------------------------------------------

const overrides = new Map<string, Record<string, string>>()
const squash = (s: string) => s.toLowerCase().replace(/[^\p{L}\p{N}]/gu, '')
export async function getMapping(macroId: string, sourceId: string): Promise<MappingDTO> {
  const m = (await getMacro(macroId)).macro
  const columns = colsFor(find(sourceId))
  const ov = overrides.get(macroId + '|' + sourceId) ?? {}
  const norm = (s: string) => s.trim().toLowerCase()
  const variables: MappingVariable[] = (m.variables ?? []).map(v => {
    const want = norm(ov[v.name] ?? v.name)
    const column = columns.find(c => norm(c) === want) ?? ''
    const suggestion = column ? '' : columns.find(c => squash(c) === squash(v.name)) ?? ''
    return { name: v.name, type: v.type, required: !!v.required, column, suggestion }
  })
  const used = new Set(variables.map(v => v.column).filter(Boolean))
  return {
    macroId, sourceId, variables, columns, missing: variables.filter(v => !v.column).map(v => v.name), extra: columns.filter(c => !used.has(c)), duplicates: [], overrides: { ...ov },
    ok: variables.every(v => v.column),
  }
}
export async function setMapping(macroId: string, sourceId: string, next: Record<string, string>): Promise<MappingDTO> {
  const columns = colsFor(find(sourceId))
  const bad = Object.entries(next).filter(([, c]) => c && !columns.includes(c))
  if (bad.length) throw new ServiceError('invalid', { problems: bad.map(([v]) => ({ path: 'overrides.' + v, msg: 'no such column' })) }, 'invalid')
  overrides.set(macroId + '|' + sourceId, Object.fromEntries(Object.entries(next).filter(([, c]) => c)))
  return getMapping(macroId, sourceId)
}
export async function validateRows(macroId: string, sourceId: string, from: number, to: number): Promise<ValidationResult> {
  const mp = await getMapping(macroId, sourceId)
  const s = find(sourceId)
  const total = TOTAL[sourceId] ?? 96
  const end = to < 0 ? total : Math.min(to, total)
  const issues: RowIssue[] = []
  for (let i = from; i < end && issues.length < 20; i++) {
    const row = genRow(s.kind, i)
    for (const v of mp.variables) {
      if (!v.required) continue
      if (!v.column) { issues.push({ rowIndex: i, level: 'error', code: 'noColumn', params: { variable: v.name } }); break }
      if (!row[v.column]) issues.push({ rowIndex: i, level: 'error', code: 'emptyRequired', params: { variable: v.name, column: v.column } })
    }
  }
  return { checked: Math.max(0, end - from), issues, truncated: false }
}

/** The mock has no file system: it counts the columns and, for CSV, offers the header row as a real download. */
export async function exportTemplate(target: { macroId: string } | { macro: Macro }, fileName: string, format: 'xlsx' | 'csv'): Promise<{ path: string; columns: number }> {
  const m = 'macro' in target ? target.macro : (await getMacro(target.macroId)).macro
  const names = [...new Set(m.steps.flatMap(s => refsOf(s.value?.expr ?? '')))]
  if (!names.length) throw new ServiceError('invalid', { what: 'macro' }, 'no data columns')
  if (format === 'csv') {
    const a = document.createElement('a')
    a.href = URL.createObjectURL(new Blob([names.join(',') + '\n'], { type: 'text/csv' }))
    a.download = fileName
    a.click()
    URL.revokeObjectURL(a.href)
  }
  return { path: 'C:\\Users\\you\\Downloads\\' + fileName, columns: names.length }
}
