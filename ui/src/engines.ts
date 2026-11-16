import { reactive, ref, computed } from 'vue'
import { notify } from './store'
import { t, i18n } from './i18n'
import { lz, type Msg } from './lazy'
import { fmtNum, fmtSize, fmtPct } from './format'
import { errorText } from './errtext'
import { ServiceError } from './bridge/api'
import { engineApi, sim, SIMS, resetSim, type SimKey, type EngineDTO, type JobDTO, type JobStage, type Diag, type TestStep, type CfTBuild, type EngineUpdateSettings, type EnginePins } from './bridge/engines'

export { sim, SIMS, resetSim }
export type { SimKey, EngineDTO, JobDTO, JobStage, Diag, TestStep, CfTBuild }

// State of the Engines screen. The Go EngineService owns the truth: this file
// asks it, listens to the "engine:job" event and keeps what the screen shows.
// Three tiers (ADR 0003, 0004, 0005): stock Chrome and Edge, Chrome for
// Testing, own patched Chromium (not built).
const MB = 1024 * 1024
const te = (k: string) => i18n.global.te(k)

// ---- log ----
export const elogs = reactive<{ t: string; l: string; m: Msg }[]>([])
export const log = (m: Msg, l = '') => { elogs.push({ t: new Date().toTimeString().slice(0, 8), l, m }); if (elogs.length > 200) elogs.shift() }
const attempt = async <T>(fn: () => Promise<T>): Promise<T | undefined> => {
  try { return await fn() } catch (e) { const m = errorText(e); log(() => m, 'e'); notify(m); return undefined }
}

// ---- engines ----
export const engines = ref<EngineDTO[]>([])
export const loaded = ref(false)
export const builds = ref<CfTBuild[]>([])
export const upd = reactive<EngineUpdateSettings>({ channel: 'Stable', autoCheck: 'daily', keepOld: 1, bandwidthLimit: 0, lastChecked: '' })
export const pins = reactive<EnginePins>({ groups: {}, profiles: {} })
export const checking = ref(false)
export const eng = (id: string) => engines.value.find((e) => e.id === id)
export const defaultEngine = computed(() => engines.value.find((e) => e.default)?.id ?? '')
export const major = (v: string) => +v.split('.')[0]
export const canUpdate = (e: EngineDTO) => e.id === 'cft' && !!e.active && !!e.latest && e.latest !== e.active && major(e.latest) >= major(e.active)
export const canInstall = (e: EngineDTO) => e.id === 'cft' && !e.active
export const stateOf = (e: EngineDTO): 'checking' | 'ok' | 'warn' | 'fail' | 'idle' => e.jobId ? 'checking' : e.status === 'blocked' ? 'fail' : e.status === 'ready' ? (canUpdate(e) ? 'warn' : 'ok') : 'idle'
export const statusText = (e: EngineDTO) => e.jobId ? t('engines.status.busy') : canUpdate(e) ? t('engines.status.update') : t('engines.status.' + e.status)

export async function refresh() {
  const [list, u, p] = await Promise.all([engineApi.listEngines(), engineApi.getUpdateSettings(), engineApi.getPins()])
  engines.value = list; Object.assign(upd, u); pins.groups = p.groups; pins.profiles = p.profiles; loaded.value = true
}
export async function checkUpdates(quiet = false) {
  if (checking.value) return
  checking.value = true
  log(lz('engines.log.checking', { channel: t('engines.channel.' + upd.channel.toLowerCase()) }))
  const b = await attempt(() => engineApi.checkUpdates())
  checking.value = false
  if (!b) return
  builds.value = b
  await refresh()
  const c = eng('cft')
  log(lz('engines.log.checked', { version: c?.latest ?? '' }))
  if (!quiet) notify(t(c && canUpdate(c) ? 'toasts.engineUpdate' : 'toasts.engineUpToDate', { version: c?.latest ?? '' }))
}
export async function saveUpdateSettings(patch: Partial<EngineUpdateSettings>) {
  const next = { ...upd, ...patch }
  const r = await attempt(() => engineApi.setUpdateSettings(next))
  if (r) Object.assign(upd, r)
  else await refresh()
}
export async function setDefault(e: EngineDTO) {
  if (await attempt(() => engineApi.setDefault(e.id).then(() => true))) { await refresh(); notify(t('toasts.nowDefault', { name: e.name })) }
}
export async function setGroupPin(group: string, engineId: string) {
  if (await attempt(() => engineApi.setGroupPin(group, engineId).then(() => true))) { if (engineId) pins.groups[group] = engineId; else delete pins.groups[group] }
}
export async function rollback(e: EngineDTO) {
  if (await attempt(() => engineApi.rollback(e.id).then(() => true))) { log(lz('engines.log.rolledBack', { name: e.name })); await refresh(); notify(t('toasts.rolledBack', { version: eng(e.id)?.active ?? '' })) }
}
export async function removeEngine(e: EngineDTO, version = '') {
  if (await attempt(() => engineApi.remove(e.id, version).then(() => true))) { log(lz('engines.log.removed', { name: e.name }), 'w'); await refresh(); notify(t('toasts.removedEngine', { name: e.name })) }
}
export const openFolder = () => attempt(() => engineApi.openFolder())

// ---- install, update, redownload job ----
export const job = reactive({ open: false, dto: null as JobDTO | null })
export const jobRunning = computed(() => job.dto?.state === 'running' || job.dto?.state === 'paused')
const jobName = (d: JobDTO) => eng(d.engineId)?.name ?? d.engineId

function onJob(d: JobDTO) {
  if (job.dto && job.dto.id !== d.id && jobRunning.value) return
  const prev = job.dto?.id === d.id ? job.dto.state : ''
  job.dto = d
  if (d.state === prev) return
  if (d.state === 'done') { log(lz('engines.log.nowActive', { name: jobName(d), version: d.version })); notify(t('toasts.engineInstalled', { name: jobName(d), version: d.version })); void refresh() }
  else if (d.state === 'failed' && d.error) { const c = d.error.code; log(() => t('engines.fail.' + c.slice(7) + '.title'), 'e'); void refresh() }
  else if (d.state === 'cancelled') { log(lz('engines.log.cancelled'), 'w'); void refresh() }
}
engineApi.onJob(onJob)

async function startJob(start: () => Promise<JobDTO>) {
  if (jobRunning.value) { job.open = true; return }
  const d = await attempt(start)
  if (!d) return
  job.dto = d; job.open = true
  log(lz('engines.log.jobStarted', { name: jobName(d), version: d.version || '...' }))
  void refresh()
}
export const openInstall = (id: string, version = '') => startJob(() => engineApi.install(id, version))
export const openUpdate = (id: string) => startJob(() => engineApi.update(id))
export const openRedownload = (id: string, version: string) => startJob(() => engineApi.redownload(id, version))
export const pauseJob = () => job.dto && attempt(() => engineApi.pauseJob(job.dto!.id).then(onJob))
export const resumeJob = () => job.dto && attempt(() => engineApi.resumeJob(job.dto!.id).then(onJob))
export const cancelJob = () => job.dto && attempt(() => engineApi.cancelJob(job.dto!.id).then(onJob))
export async function applyJobFix(fixId: string) {
  const d = job.dto; if (!d) return
  log(() => t('engines.log.fix', { fix: fixLabel(fixId) }), 'w')
  const n = await attempt(() => engineApi.applyJobFix(d.id, fixId))
  if (n) { job.dto = n; void refresh() }
}
export const fixLabel = (id: string) => t('engines.fixes.' + id)

/** The fix a failed job offers, or "resume" for a cancelled one that kept its partial file. */
export const jobFix = computed(() => {
  const d = job.dto
  if (!d) return ''
  if (d.state === 'cancelled') return 'resume'
  const f = d.error?.details?.fixId
  return typeof f === 'string' ? f : 'retry'
})
export const jobFailText = (d: JobDTO) => {
  const code = d.error?.code ?? 'engine.internal'
  const base = 'engines.fail.' + (te('engines.fail.' + code.slice(7) + '.title') ? code.slice(7) : 'internal')
  const pct = d.bytesTotal > 0 ? fmtPct((d.bytesDone / d.bytesTotal) * 100) : fmtPct(0)
  const p = { pct, version: d.version, ...(d.error?.details ?? {}) }
  return { title: t(base + '.title', p), why: t(base + '.why', p) }
}
const fmtParams = (p: Record<string, unknown> = {}) => {
  const o: Record<string, unknown> = { ...p }
  if (typeof p.bytes === 'number') o.size = fmtSize(p.bytes / MB)
  if (typeof p.files === 'number') o.files = fmtNum(p.files)
  if (typeof p.channel === 'string') o.channel = t('engines.channel.' + p.channel.toLowerCase())
  if ('have' in p) o.have = p.have || t('engines.none')
  if ('previous' in p) o.prev = p.previous
  if (typeof p.sha256 === 'string') o.sha = p.sha256.slice(0, 4) + '...' + p.sha256.slice(-4)
  return o
}
export const stageDetail = (s: JobStage) => {
  const key = 'engines.job.stage.' + (s.code ?? '')
  return s.code && te(key) ? t(key, fmtParams(s.params), typeof s.params?.attempts === 'number' ? Number(s.params.attempts) : 1) : ''
}
export const stageErr = (d: JobDTO) => d.error ? jobFailText(d).title : ''
export const selfTests = ['start', 'cdp', 'canvas', 'fingerprint']

// ---- doctor ----
export type Level = 'ok' | 'warn' | 'fail'
export interface DiagRow { id: string; state: 'pending' | 'running' | Level; text: Msg; fixId: string }
export const DIAG_IDS = ['browser', 'policy', 'pipe', 'folder', 'job', 'relay']
const none: Msg = () => ''
export const diag = reactive({ running: false, ran: false, items: DIAG_IDS.map((id) => ({ id, state: 'pending', text: none, fixId: '' }) as DiagRow) })
const diagMsg = (d: Diag): Msg => () => {
  const key = `engines.diag.${d.id}.${d.code}`
  const p = d.params ?? {}
  const o: Record<string, unknown> = { ...p }
  if (typeof p.error === 'string') o.error = p.error
  if (typeof p.ms === 'number') o.s = (p.ms / 1000).toFixed(0)
  if (Array.isArray(p.brands)) o.brands = p.brands.join(', ')
  if (typeof p.engine === 'string') o.engine = eng(p.engine)?.name ?? p.engine
  return te(key) ? t(key, o, Number(p.n ?? 1)) : d.code
}
export async function runDiag() {
  if (diag.running) return
  diag.running = true; diag.ran = true
  diag.items.forEach((i) => { i.state = 'running'; i.text = none; i.fixId = '' })
  log(lz('engines.log.diagStarted'))
  const ds = await attempt(() => engineApi.runDiagnostics())
  if (ds) diag.items = DIAG_IDS.map((id) => { const d = ds.find((x) => x.id === id); return d ? { id, state: d.level, text: diagMsg(d), fixId: d.fixId ?? '' } : { id, state: 'pending' as const, text: none, fixId: '' } })
  else diag.items.forEach((i) => { i.state = 'pending' })
  diag.running = false
  const bad = diag.items.filter((i) => i.state === 'fail').length, w = diag.items.filter((i) => i.state === 'warn').length
  log(() => t('engines.log.diagDone', { bad, warn: w }, w), bad ? 'e' : '')
}
export const diagSummary = computed(() => ({ fail: diag.items.filter((i) => i.state === 'fail').length, warn: diag.items.filter((i) => i.state === 'warn').length, ok: diag.items.filter((i) => i.state === 'ok').length }))
export const diagLabel = (id: string) => t('engines.diag.' + id + '.label')
export async function copyReport() {
  const r = await attempt(() => engineApi.report())
  if (r === undefined) return
  navigator.clipboard?.writeText(r).catch(() => {})
  notify(t('toasts.reportCopied'))
}
/** Fix from the doctor: only Chrome for Testing is offered. It opens the install job. */
export async function fixDiag(fixId: string) {
  if (fixId === 'installCft') await openInstall('cft')
}

// ---- test launch ----
export const LAUNCH_STEPS = ['launch', 'cdp', 'fingerprint', 'proxy']
export interface LaunchRow { k: string; state: 'pending' | 'running' | 'ok' | 'fail' | 'skipped'; text: Msg; fix: string }
export const launch = reactive({ running: false, steps: LAUNCH_STEPS.map((k) => ({ k, state: 'pending', text: none, fix: '' }) as LaunchRow) })
const stepMsg = (s: TestStep): Msg => () => {
  const key = `engines.launch.${s.id}.${s.state === 'ok' ? 'ok' : s.code ?? 'failed'}`
  const o: Record<string, unknown> = { ...(s.params ?? {}) }
  if (typeof s.params?.ms === 'number') o.s = (s.params.ms / 1000).toFixed(1)
  return te(key) ? t(key, o) : s.code ?? ''
}
export async function testLaunch(engineId = '') {
  if (launch.running) return
  launch.running = true; launch.steps.forEach((s) => { s.state = 'pending'; s.text = none; s.fix = '' }); launch.steps[0].state = 'running'
  log(lz('engines.log.testStarted'))
  const steps = await attempt(() => engineApi.testLaunch(engineId))
  launch.running = false
  if (!steps) { launch.steps[0].state = 'pending'; return }
  launch.steps = LAUNCH_STEPS.map((k) => {
    const s = steps.find((x) => x.id === k)
    return s ? { k, state: s.state, text: s.state === 'skipped' && !s.code ? lz('engines.launch.skipped') : stepMsg(s), fix: s.code === 'blocked' ? 'installCft' : '' } : { k, state: 'pending' as const, text: none, fix: '' }
  })
}

export const problems = [
  { k: 'noOpen' }, { k: 'updateStops' }, { k: 'policy' }, { k: 'freeze' }, { k: 'smartscreen' }, { k: 'damaged' }, { k: 'diskFull' },
]
// What the sidebar badge counts: engines a policy blocks, no usable browser at all, a newer Chrome for Testing.
export const healthIssues = computed(() => {
  if (!loaded.value) return 0
  const list = engines.value
  return list.filter((e) => e.status === 'blocked').length + (list.some((e) => e.status === 'ready') ? 0 : 1) + list.filter(canUpdate).length
})

// First load. In the browser prototype this fills the mock; in Wails it asks Go.
void refresh().then(() => { if (upd.autoCheck !== 'off') void checkUpdates(true) }).catch((e) => { if (e instanceof ServiceError) log(() => errorText(e), 'e') })
