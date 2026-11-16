// Bridge to the Go AppUpdateService (updates of Ladilala itself). In a plain
// browser (bun run proto) a scenario-driven mock plays the same states, so the
// update sheet and the Settings screen keep working.
import { callService, inWails, onEvent, ServiceError } from './api'

export type UpdateChannel = 'stable' | 'beta' | 'nightly'
export type AutoCheck = 'daily' | 'weekly' | 'off'
export type Phase = 'idle' | 'checking' | 'available' | 'downloading' | 'ready' | 'failed'
export interface AppRelease {
  version: string; channel: string; name: string; notes: string; news: string[]; fixed: string[]
  publishedAt: string; sizeBytes: number; minEngine: number
}
export interface UpdateStage { key: string; state: 'pending' | 'active' | 'done' | 'fail'; code?: string; params?: Record<string, unknown> }
export interface UpdateFailure { code: string; message?: string; details?: Record<string, unknown> }
export interface AppUpdateState {
  current: string; channel: UpdateChannel; autoCheck: AutoCheck; installOnQuit: boolean; skipped: string; lastChecked: string
  configured: boolean; feedUrl: string; feedFromEnv: boolean
  phase: Phase; release: AppRelease | null; stages: UpdateStage[]
  bytesDone: number; bytesTotal: number; bytesPerSec: number; failure?: UpdateFailure
  whenIdle: boolean; activeRuns: number; engineMajor: number; engineCompatible: boolean
}
export interface AppUpdateSettings { channel: UpdateChannel; autoCheck: AutoCheck; installOnQuit: boolean; feedUrl: string }

export interface AppUpdateApi {
  state(): Promise<AppUpdateState>
  setSettings(s: AppUpdateSettings): Promise<AppUpdateState>
  check(force: boolean): Promise<AppUpdateState>
  download(): Promise<AppUpdateState>
  cancel(): Promise<AppUpdateState>
  applyFix(fixId: string): Promise<AppUpdateState>
  restartNow(): Promise<void>
  restartWhenIdle(): Promise<AppUpdateState>
  cancelRestartWhenIdle(): Promise<AppUpdateState>
  skip(version: string): Promise<AppUpdateState>
  onState(cb: (s: AppUpdateState) => void): () => void
}

const S = 'AppUpdateService'
const real: AppUpdateApi = {
  state: () => callService(S, 'GetAppUpdateState'),
  setSettings: (s) => callService(S, 'SetAppUpdateSettings', s),
  check: (force) => callService(S, 'CheckAppUpdate', force),
  download: () => callService(S, 'DownloadAppUpdate'),
  cancel: () => callService(S, 'CancelAppUpdate'),
  applyFix: (f) => callService(S, 'ApplyUpdateFix', f),
  restartNow: () => callService(S, 'RestartNow'),
  restartWhenIdle: () => callService(S, 'RestartWhenIdle'),
  cancelRestartWhenIdle: () => callService(S, 'CancelRestartWhenIdle'),
  skip: (v) => callService(S, 'SkipVersion', v),
  onState: (cb) => onEvent<AppUpdateState>('appupdate:state', cb),
}

// ---------------------------------------------------------------------------------
// Mock (browser prototype only). The simulate panel in App.vue picks a scenario.
// ---------------------------------------------------------------------------------

export type Scenario = 'none' | 'stable' | 'beta' | 'netfail' | 'sigfail' | 'disk' | 'engine' | 'ready' | 'unconfigured'
export const SCENARIOS: Scenario[] = ['stable', 'beta', 'netfail', 'sigfail', 'disk', 'engine', 'ready', 'none', 'unconfigured']

const STAGES = ['check', 'download', 'verify', 'prepare', 'ready']
const newStages = (): UpdateStage[] => STAGES.map((key) => ({ key, state: 'pending' as const }))
const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms))
const MB = 1024 * 1024
const mockRunsActive = 2

const ms: AppUpdateState = {
  current: '0.4.2', channel: 'stable', autoCheck: 'daily', installOnQuit: true, skipped: '', lastChecked: new Date(Date.now() - 3 * 3600_000).toISOString(),
  configured: true, feedUrl: 'https://updates.example.com/stable.json', feedFromEnv: false,
  phase: 'idle', release: null, stages: newStages(), bytesDone: 0, bytesTotal: 0, bytesPerSec: 0,
  whenIdle: false, activeRuns: mockRunsActive, engineMajor: 157, engineCompatible: true,
}
let scenario: Scenario = 'none'
const fails = { net: false, sig: false, disk: false }
const listeners = new Set<(s: AppUpdateState) => void>()
let cancelled = false
const snap = () => { ms.engineCompatible = !ms.release || ms.release.minEngine <= ms.engineMajor; return structuredClone(ms) }
const emit = () => { const s = snap(); listeners.forEach((f) => f(s)) }
const stage = (k: string, state: UpdateStage['state'], code?: string) => { const x = ms.stages.find((s) => s.key === k)!; x.state = state; x.code = code }

const release = (sc: Scenario): AppRelease | null => {
  if (sc === 'none' || sc === 'unconfigured') return null
  const base: AppRelease = { version: '0.5.0', channel: 'stable', name: 'Ladilala 0.5.0', notes: '', news: ['Macros can loop over a list of profiles in one run.', 'Proxy health is checked in the background every 10 minutes.', 'The profiles list remembers your sort order.'], fixed: ['Runs no longer stay paused after the computer wakes from sleep.', 'Importing cookies from a file with a byte order mark works.'], publishedAt: '2026-11-14T00:00:00Z', sizeBytes: 86 * MB, minEngine: 150 }
  if (sc === 'beta') return { ...base, version: '0.5.0-beta.2', channel: 'beta', publishedAt: '2026-11-15T00:00:00Z', news: [...base.news, 'New fingerprint consistency checks (preview).'] }
  if (sc === 'engine') return { ...base, minEngine: 165 }
  return base
}

export function mockSetScenario(sc: Scenario) {
  cancelled = true; scenario = sc
  Object.assign(ms, { skipped: '', stages: newStages(), failure: undefined, bytesDone: 0, bytesTotal: 0, bytesPerSec: 0, whenIdle: false, configured: sc !== 'unconfigured', lastChecked: new Date().toISOString() })
  fails.net = sc === 'netfail'; fails.sig = sc === 'sigfail'; fails.disk = sc === 'disk'
  ms.release = release(sc)
  if (sc === 'beta') ms.channel = 'beta'
  if (sc === 'ready') { ms.stages.forEach((s) => (s.state = 'done')); ms.phase = 'ready' } else ms.phase = ms.release ? 'available' : 'idle'
  emit()
}

async function mockDownload() {
  cancelled = false
  const r = ms.release!, total = r.sizeBytes
  Object.assign(ms, { phase: 'downloading', failure: undefined, stages: newStages(), bytesDone: 0, bytesTotal: total, bytesPerSec: 0 })
  stage('check', 'done', 'latest'); stage('download', 'active'); emit()
  const fail = (k: string, code: string, fixId: string) => { stage(k, 'fail', code); ms.phase = 'failed'; ms.failure = { code, details: { fixId } }; emit() }
  if (fails.disk) { await sleep(500); return fail('download', 'update.disk', 'retry') }
  while (ms.bytesDone < total) {
    await sleep(100); if (cancelled) { Object.assign(ms, { phase: 'available', stages: newStages(), bytesDone: 0 }); emit(); return }
    ms.bytesPerSec = 6.4 * MB; ms.bytesDone = Math.min(total, ms.bytesDone + ms.bytesPerSec * 0.1 * 6); emit()
    if (fails.net && ms.bytesDone / total >= 0.38) return fail('download', 'update.network', 'retry')
  }
  stage('download', 'done', 'downloaded'); stage('verify', 'active'); emit(); await sleep(1100)
  if (fails.sig) { ms.bytesDone = 0; return fail('verify', 'update.signature', 'redownload') }
  stage('verify', 'done', 'signed'); stage('prepare', 'active'); emit(); await sleep(1200)
  stage('prepare', 'done', 'staged'); stage('ready', 'done'); ms.phase = 'ready'; emit()
}

const finishRestart = () => {
  const r = ms.release!
  Object.assign(ms, { current: r.version, release: null, phase: 'idle', whenIdle: false, stages: newStages() }); scenario = 'none'; emit()
}

const mock: AppUpdateApi = {
  state: async () => snap(),
  setSettings: async (s) => { Object.assign(ms, s); if (s.channel === 'beta' && scenario === 'stable') ms.release = null; emit(); return snap() },
  check: async (force) => {
    if (!ms.configured) throw new ServiceError('update.notConfigured')
    if (['checking', 'downloading', 'ready', 'failed'].includes(ms.phase)) return snap()
    if (force) ms.skipped = ''
    ms.phase = 'checking'; emit(); await sleep(1200); ms.lastChecked = new Date().toISOString()
    ms.phase = ms.release ? 'available' : 'idle'; emit(); return snap()
  },
  download: async () => { void mockDownload(); return snap() },
  cancel: async () => { cancelled = true; return snap() },
  applyFix: async (fixId) => {
    if (fixId === 'retry') { fails.net = false; fails.disk = false }
    if (fixId === 'redownload') fails.sig = false
    void mockDownload(); return snap()
  },
  restartNow: async () => { if (ms.activeRuns > 0) throw new ServiceError('conflict', { reason: 'runActive', runs: ms.activeRuns }); await sleep(1200); finishRestart() },
  restartWhenIdle: async () => { ms.whenIdle = true; emit(); setTimeout(() => { ms.activeRuns = 0; if (ms.whenIdle) finishRestart(); ms.activeRuns = mockRunsActive }, 4000); return snap() },
  cancelRestartWhenIdle: async () => { ms.whenIdle = false; emit(); return snap() },
  skip: async (v) => { ms.skipped = v; if (ms.release?.version === v) { ms.release = null; ms.phase = 'idle'; scenario = 'none' } emit(); return snap() },
  onState: (cb) => { listeners.add(cb); return () => listeners.delete(cb) },
}

export const appUpdateApi: AppUpdateApi = inWails ? real : mock
