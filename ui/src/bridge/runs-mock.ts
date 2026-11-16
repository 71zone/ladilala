// In-memory run engine for the browser prototype. It follows the state rules of
// docs/run-engine.md closely enough to show every screen state: live progress,
// failed and uncertain rows, pause, takeover with a guard refusal, retry, crash recovery.
import { ServiceError } from './api'
import { listMacros } from './macros'
import type { CreateRunRequest, LedgerRow, LogLine, Recovery, Run, RowFilter, RowPage, RowState, RunFilter, RunState } from './runs'

type Handler = (e: unknown) => void
const handlers = new Map<string, Set<Handler>>()
export const bus = {
  on(name: string, cb: Handler) {
    if (!handlers.has(name)) handlers.set(name, new Set())
    handlers.get(name)!.add(cb)
    return () => { handlers.get(name)?.delete(cb) }
  },
}
const emit = (name: string, data: unknown) => handlers.get(name)?.forEach(h => h(data))

const STEPS = ['open', 'first_name', 'last_name', 'email', 'country', 'card', 'terms', 'submit', 'confirm']
const FIRST = ['Linh', 'Marcus', 'Ana', 'Kenji', 'Mai', 'Tom', 'Sofia', 'Hung', 'Eva', 'Noah']
const LAST = ['Tran', 'Weber', 'Silva', 'Sato', 'Le', 'Brown', 'Rossi', 'Pham', 'Meyer', 'Kim']

interface MockRun { run: Run; rows: LedgerRow[]; timer?: number; releaseRefused: boolean; pendingCursor: number }
const runs = new Map<string, MockRun>()
let seq = 0
let recoveredList: Recovery[] = []

const iso = (ms = Date.now()) => new Date(ms).toISOString()
const rowData = (i: number): Record<string, string> => ({
  first_name: FIRST[i % FIRST.length], last_name: LAST[(i * 7) % LAST.length], email: `${FIRST[i % FIRST.length].toLowerCase()}.${i}@mail.test`, password: 'hunter2-' + i,
})
const makeRow = (runId: string, index: number): LedgerRow => ({
  runId, index, idempotencyKey: `${runId}:${index}`, state: 'pending', commitState: 'none', profileId: '', attempts: 0,
  data: rowData(index), errorKind: '', errorMsg: '', stepId: '', 
})
const counts = (rows: LedgerRow[]) => {
  const c: Partial<Record<RowState, number>> = {}
  for (const r of rows) c[r.state] = (c[r.state] ?? 0) + 1
  return c
}
const sync = (m: MockRun) => { m.run.counts = counts(m.rows) }

function outcome(i: number): { state: RowState; kind: string; msg: string; step: string; commit: 'none' | 'submitted' | 'submitting' } {
  if (i % 37 === 9) return { state: 'failed', kind: 'locator', msg: 'Field not found on the page', step: 'email', commit: 'none' }
  if (i % 29 === 5) return { state: 'skipped', kind: 'data', msg: 'Column "email" is empty', step: '', commit: 'none' }
  if (i % 53 === 20) return { state: 'uncertain', kind: 'crash', msg: 'The browser closed after the commit point', step: 'submit', commit: 'submitting' }
  return { state: 'done', kind: '', msg: '', step: 'confirm', commit: 'submitted' }
}

function seedRuns() {
  const id = 'run-0'
  const rows = Array.from({ length: 412 }, (_, i) => makeRow(id, i))
  rows.forEach((r, i) => {
    if (i < 120) {
      const o = outcome(i)
      Object.assign(r, { state: o.state, errorKind: o.kind, errorMsg: o.msg, stepId: o.step, commitState: o.commit, attempts: 1, profileId: 'p' + ((i % 3) + 1), startedAt: iso(Date.now() - 3.6e6 + i * 20000), finishedAt: iso(Date.now() - 3.6e6 + i * 20000 + 15000) })
    }
  })
  const run: Run = {
    id, macroId: 'm1', macroName: 'Signup form', macroVersion: 3, macroHash: 'a1b2c3', sourceId: 's1', state: 'crashed_recovered', reason: 'the app closed while the run was active',
    options: { concurrency: 3, delayMs: 0, stopAfterErrors: 5, maxRestarts: 3, dryRun: false, testOne: false }, profileIds: ['p1', 'p2', 'p3'], rowFrom: 0, rowTo: 412, total: 412, counts: {},
    createdAt: iso(Date.now() - 3.7e6), startedAt: iso(Date.now() - 3.6e6),
  }
  const m: MockRun = { run, rows, releaseRefused: false, pendingCursor: 120 }
  sync(m)
  runs.set(id, m)
  recoveredList = [{ runId: id, backToQueue: 2, uncertain: m.run.counts.uncertain ?? 0 }]
}
seedRuns()

const get = (id: string) => {
  const m = runs.get(id)
  if (!m) throw new ServiceError('run.notFound')
  return m
}
const bad = (m: MockRun, op: string): never => { throw new ServiceError('run.state', { state: m.run.state, op }) }
const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v))

function setState(m: MockRun, to: RunState, reason = '') {
  m.run.state = to
  m.run.reason = reason
  if (to === 'running' && !m.run.startedAt) m.run.startedAt = iso()
  if (to === 'finished' || to === 'failed') m.run.finishedAt = iso()
  emit('run.state', { runId: m.run.id, state: to, reason, at: Date.now() })
  progress(m)
}
function progress(m: MockRun, resync = false) {
  sync(m)
  emit('run.progress', { runId: m.run.id, state: m.run.state, total: m.run.total, counts: { ...m.run.counts }, resync })
}
const log = (m: MockRun, lines: LogLine[]) => emit('run.log', { runId: m.run.id, lines })

function tick(m: MockRun) {
  if (m.run.state !== 'running') return
  const lines: LogLine[] = []
  // finish the rows in flight
  for (const r of m.rows.filter(x => x.state === 'running')) {
    const o = m.run.options.dryRun && outcome(r.index).state === 'uncertain' ? { state: 'done' as RowState, kind: '', msg: '', step: 'confirm', commit: 'none' as const } : outcome(r.index)
    const retried = r.attempts > 1 && o.state === 'failed'
    const final = retried ? { ...o, state: 'done' as RowState, kind: '', msg: '', step: 'confirm', commit: 'submitted' as const } : o
    Object.assign(r, { state: final.state, errorKind: final.kind, errorMsg: final.msg, stepId: final.step || r.stepId, commitState: m.run.options.dryRun ? 'none' : final.commit, finishedAt: iso() })
    lines.push({ at: Date.now(), kind: 'row.' + final.state, rowIndex: r.index, profileId: r.profileId, message: final.msg })
    if (final.state === 'uncertain') emit('run.uncertain', { runId: m.run.id, rowIndex: r.index, profileId: r.profileId, message: final.msg, at: Date.now() })
  }
  // claim new rows
  const free = m.run.options.concurrency - m.rows.filter(x => x.state === 'running').length
  const pending = m.rows.filter(x => x.state === 'pending').slice(0, free)
  pending.forEach((r, k) => {
    Object.assign(r, { state: 'running', attempts: r.attempts + 1, profileId: m.run.profileIds[(r.index + k) % m.run.profileIds.length], stepId: STEPS[(r.index + k) % 6 + 1], startedAt: iso(), errorKind: '', errorMsg: '' })
    lines.push({ at: Date.now(), kind: 'row.started', rowIndex: r.index, profileId: r.profileId, message: '' })
  })
  if (!m.rows.some(x => x.state === 'running' || x.state === 'pending')) {
    window.clearInterval(m.timer)
    log(m, lines)
    setState(m, 'finished')
    return
  }
  log(m, lines)
  progress(m)
}
function loop(m: MockRun) {
  window.clearInterval(m.timer)
  m.timer = window.setInterval(() => tick(m), 600)
}

export async function createRun(req: CreateRunRequest): Promise<Run> {
  if (!req.profileIds.length) throw new ServiceError('run.noProfiles')
  if (req.options.concurrency > req.profileIds.length) throw new ServiceError('run.tooFewProfiles')
  const sourceRows = 412
  const to = req.to < 0 ? sourceRows : Math.min(req.to, sourceRows)
  const idxs = Array.from({ length: Math.max(0, to - req.from) }, (_, i) => req.from + i)
  if (req.options.testOne) idxs.splice(1)
  if (!idxs.length) throw new ServiceError('run.noRows')
  const id = 'run-' + ++seq
  const run: Run = {
    id, macroId: req.macroId, macroName: (await listMacros()).find(m => m.id === req.macroId)?.name ?? req.macroId, macroVersion: 1, macroHash: 'f00dbeef', sourceId: req.sourceId, state: 'created', reason: '',
    options: { ...req.options }, profileIds: [...req.profileIds], rowFrom: req.from, rowTo: req.to, total: idxs.length, counts: {}, createdAt: iso(),
  }
  const m: MockRun = { run, rows: idxs.map(i => makeRow(id, i)), releaseRefused: false, pendingCursor: 0 }
  sync(m)
  runs.set(id, m)
  return clone(run)
}
export async function startRun(id: string) {
  const m = get(id)
  if (m.run.state !== 'created') bad(m, 'start')
  setState(m, 'running')
  log(m, [{ at: Date.now(), kind: 'run.state', rowIndex: -1, profileId: '', message: '' }])
  loop(m)
}
export async function pauseRun(id: string) {
  const m = get(id)
  if (m.run.state !== 'running') bad(m, 'pause')
  window.clearInterval(m.timer)
  setState(m, 'paused')
}
export async function resumeRun(id: string) {
  const m = get(id)
  if (!['paused', 'failed', 'crashed_recovered'].includes(m.run.state)) bad(m, 'resume')
  setState(m, 'running')
  loop(m)
}
export async function stopRun(id: string) {
  const m = get(id)
  if (['finished', 'stopping'].includes(m.run.state)) bad(m, 'stop')
  window.clearInterval(m.timer)
  for (const r of m.rows.filter(x => x.state === 'running')) Object.assign(r, r.commitState === 'submitting' ? { state: 'uncertain' } : { state: 'pending', profileId: '' })
  setState(m, 'finished', 'stopped by the user')
}
export async function takeover(id: string) {
  const m = get(id)
  if (!['running', 'paused'].includes(m.run.state)) bad(m, 'takeover')
  window.clearInterval(m.timer)
  setState(m, 'takeover')
}
export async function releaseTakeover(id: string) {
  const m = get(id)
  if (m.run.state !== 'takeover') bad(m, 'release')
  if (!m.releaseRefused) { // the first hand back shows the guard refusal
    m.releaseRefused = true
    throw new ServiceError('run.guard')
  }
  setState(m, 'running')
  loop(m)
}
export async function retryRows(id: string, f: RowFilter) {
  const m = get(id)
  const wanted = (r: LedgerRow) => (r.state === 'failed' || r.state === 'skipped') && (!f.states?.length || f.states.includes(r.state)) && (!f.indexes?.length || f.indexes.includes(r.index))
  const hit = m.rows.filter(wanted)
  hit.forEach(r => Object.assign(r, { state: 'pending', errorKind: '', errorMsg: '', profileId: '' }))
  if (hit.length && ['finished', 'failed', 'crashed_recovered'].includes(m.run.state)) setState(m, 'paused')
  else progress(m)
  return hit.length
}
export async function markUncertainRow(id: string, index: number, res: 'done' | 'failed') {
  const m = get(id)
  const r = m.rows.find(x => x.index === index)
  if (!r || r.state !== 'uncertain') throw new ServiceError('run.notUncertain')
  if (res === 'done') Object.assign(r, { state: 'done', commitState: 'submitted', errorKind: 'resolved', errorMsg: '' })
  else Object.assign(r, { state: 'failed', errorKind: 'resolved', errorMsg: '' })
  progress(m)
}
export async function listRuns(f: RunFilter): Promise<Run[]> {
  const all = [...runs.values()].map(m => m.run).filter(r => !f.states?.length || f.states.includes(r.state)).sort((a, b) => b.createdAt.localeCompare(a.createdAt))
  return clone(all.slice(f.offset ?? 0, (f.offset ?? 0) + (f.limit || 100)))
}
export async function getRun(id: string): Promise<Run> { const m = get(id); sync(m); return clone(m.run) }
export async function listRows(id: string, f: RowFilter): Promise<RowPage> {
  const m = get(id)
  const hit = m.rows.filter(r => (!f.states?.length || f.states.includes(r.state)) && (!f.indexes?.length || f.indexes.includes(r.index)))
  const off = f.offset ?? 0
  return { rows: clone(hit.slice(off, off + (f.limit || 100))), total: hit.length }
}
export async function recovered(): Promise<Recovery[]> { return clone(recoveredList) }
export async function dismissRecovered() { recoveredList = [] }
export async function exportLedger(id: string): Promise<string> {
  const m = get(id)
  const head = 'row,state,commit,profile,attempts,step,errorKind,errorMessage'
  const body = m.rows.map(r => [r.index + 1, r.state, r.commitState, r.profileId, r.attempts, r.stepId, r.errorKind, JSON.stringify(r.errorMsg)].join(','))
  const url = URL.createObjectURL(new Blob([[head, ...body].join('\n')], { type: 'text/csv' }))
  const a = document.createElement('a')
  a.href = url
  a.download = `run-${id}.csv`
  a.click()
  URL.revokeObjectURL(url)
  return a.download
}
