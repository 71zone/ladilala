// Bridge to the Go EngineService. Inside the Wails window every function calls
// main.EngineService.<Method>. In a plain browser (bun run proto) the same
// functions answer from a small in-memory mock that plays the install job,
// the doctor and the test launch, so the Engines screen keeps working.
import { reactive } from 'vue'
import { callService, inWails, onEvent, ServiceError } from './api'

export type EngineId = 'chrome' | 'edge' | 'cft' | 'patched'
export interface EngineVersion { version: string; sizeBytes: number; path: string; sha256?: string; active: boolean }
export interface EngineDTO {
  id: EngineId; tier: 1 | 2 | 3; name: string
  status: 'ready' | 'missing' | 'blocked' | 'unavailable'
  system: boolean; path: string; installed: EngineVersion[]
  active: string; previous: string; latest: string; downloadBytes: number
  default: boolean; policyBlocked: boolean; notesUrl: string; jobId: string
}
export type StageState = 'pending' | 'active' | 'done' | 'fail'
export interface JobStage { key: string; state: StageState; code?: string; params?: Record<string, unknown> }
export interface JobError { code: string; message?: string; details?: Record<string, unknown> }
export type JobState = 'running' | 'paused' | 'done' | 'failed' | 'cancelled'
export interface JobDTO {
  id: string; engineId: EngineId; kind: 'install' | 'update' | 'redownload'; version: string
  state: JobState; stage: string; stages: JobStage[]
  bytesDone: number; bytesTotal: number; bytesPerSec: number; attempt: number; partialKept: boolean
  error?: JobError; startedAt: string
}
export interface CfTBuild { version: string; channel: string; revision: string; url: string; installed: boolean }
export interface EngineUpdateSettings { channel: 'Stable' | 'Beta'; autoCheck: 'daily' | 'weekly' | 'off'; keepOld: number; bandwidthLimit: number; lastChecked: string }
export interface Diag { id: string; level: 'ok' | 'warn' | 'fail'; code: string; params?: Record<string, unknown>; fixId?: string }
export interface TestStep { id: string; state: 'ok' | 'fail' | 'skipped'; code?: string; params?: Record<string, unknown> }
export interface EnginePins { groups: Record<string, string>; profiles: Record<string, string> }

export interface EngineApi {
  listEngines(): Promise<EngineDTO[]>
  checkUpdates(): Promise<CfTBuild[]>
  install(engineId: string, version?: string): Promise<JobDTO>
  update(engineId: string): Promise<JobDTO>
  redownload(engineId: string, version: string): Promise<JobDTO>
  pauseJob(id: string): Promise<JobDTO>
  resumeJob(id: string): Promise<JobDTO>
  cancelJob(id: string): Promise<JobDTO>
  applyJobFix(id: string, fixId: string): Promise<JobDTO>
  rollback(engineId: string): Promise<void>
  remove(engineId: string, version?: string): Promise<void>
  setDefault(engineId: string): Promise<void>
  getPins(): Promise<EnginePins>
  setGroupPin(group: string, engineId: string): Promise<void>
  getUpdateSettings(): Promise<EngineUpdateSettings>
  setUpdateSettings(s: EngineUpdateSettings): Promise<EngineUpdateSettings>
  runDiagnostics(): Promise<Diag[]>
  testLaunch(engineId?: string): Promise<TestStep[]>
  report(): Promise<string>
  openFolder(): Promise<void>
  onJob(cb: (j: JobDTO) => void): () => void
}

const S = 'EngineService'
const real: EngineApi = {
  listEngines: () => callService(S, 'ListEngines'),
  checkUpdates: () => callService(S, 'CheckEngineUpdates'),
  install: (engineId, version = '') => callService(S, 'InstallEngine', { engineId, version }),
  update: (id) => callService(S, 'UpdateEngine', id),
  redownload: (id, v) => callService(S, 'RedownloadEngine', id, v),
  pauseJob: (id) => callService(S, 'PauseJob', id),
  resumeJob: (id) => callService(S, 'ResumeJob', id),
  cancelJob: (id) => callService(S, 'CancelJob', id),
  applyJobFix: (id, fix) => callService(S, 'ApplyJobFix', id, fix),
  rollback: (id) => callService(S, 'RollbackEngine', id),
  remove: (id, v = '') => callService(S, 'RemoveEngine', id, v),
  setDefault: (id) => callService(S, 'SetDefaultEngine', id),
  getPins: () => callService(S, 'GetPins'),
  setGroupPin: (g, id) => callService(S, 'SetGroupPin', g, id),
  getUpdateSettings: () => callService(S, 'GetUpdateSettings'),
  setUpdateSettings: (s) => callService(S, 'SetUpdateSettings', s),
  runDiagnostics: () => callService(S, 'RunDiagnostics'),
  testLaunch: (id = '') => callService(S, 'TestLaunch', id),
  report: () => callService(S, 'DiagnosticsReport'),
  openFolder: () => callService(S, 'OpenEnginesFolder'),
  onJob: (cb) => onEvent<JobDTO>('engine:job', cb),
}

// ---------------------------------------------------------------------------------
// Mock (browser prototype only)
// ---------------------------------------------------------------------------------

export type SimKey = 'network' | 'checksum' | 'policy' | 'disk' | 'orphan' | 'lock' | 'hang'
export const SIMS: SimKey[] = ['network', 'checksum', 'policy', 'disk', 'orphan', 'lock', 'hang']
export const sim = reactive<Record<SimKey, boolean>>({ network: false, checksum: false, policy: false, disk: false, orphan: false, lock: false, hang: false })
export const resetSim = () => { (Object.keys(sim) as SimKey[]).forEach((k) => (sim[k] = false)) }

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms))
const MB = 1024 * 1024
const mockEngines: EngineDTO[] = [
  { id: 'chrome', tier: 1, name: 'Google Chrome', status: 'ready', system: true, path: 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe', installed: [{ version: '157.0.7955.60', sizeBytes: 0, path: '', active: true }], active: '157.0.7955.60', previous: '', latest: '', downloadBytes: 0, default: true, policyBlocked: false, notesUrl: 'https://chromereleases.googleblog.com/', jobId: '' },
  { id: 'edge', tier: 1, name: 'Microsoft Edge', status: 'ready', system: true, path: 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe', installed: [{ version: '157.0.3896.2', sizeBytes: 0, path: '', active: true }], active: '157.0.3896.2', previous: '', latest: '', downloadBytes: 0, default: false, policyBlocked: false, notesUrl: 'https://learn.microsoft.com/deployedge/microsoft-edge-relnote-stable-channel', jobId: '' },
  { id: 'cft', tier: 2, name: 'Chrome for Testing', status: 'missing', system: false, path: 'C:\\Users\\you\\AppData\\Local\\Ladilala\\engines\\chrome-for-testing', installed: [], active: '', previous: '', latest: '157.0.8103.78', downloadBytes: 156 * MB, default: false, policyBlocked: false, notesUrl: 'https://googlechromelabs.github.io/chrome-for-testing/', jobId: '' },
  { id: 'patched', tier: 3, name: 'Ladilala Chromium', status: 'unavailable', system: false, path: '', installed: [], active: '', previous: '', latest: '', downloadBytes: 0, default: false, policyBlocked: false, notesUrl: '', jobId: '' },
]
const mockSettings: EngineUpdateSettings = { channel: 'Stable', autoCheck: 'daily', keepOld: 1, bandwidthLimit: 0, lastChecked: '' }
const mockPins: EnginePins = { groups: {}, profiles: {} }
const jobListeners = new Set<(j: JobDTO) => void>()
const mockJobs = new Map<string, JobDTO>()
const ctl = new Map<string, { paused: boolean; cancelled: boolean }>()
let seq = 0
let mockDefaultId: EngineId = 'chrome'
const STAGES = ['check', 'download', 'verify', 'unpack', 'selftest', 'switch']
const emitJob = (j: JobDTO) => { const c = structuredClone(j); jobListeners.forEach((f) => f(c)) }
const err = (code: string, details?: Record<string, unknown>) => new ServiceError(code, details ?? {})
const cft = () => mockEngines[2]

class Stop extends Error {}
async function runMockJob(j: JobDTO, version: string) {
  const c = ctl.get(j.id)!
  const tick = async (ms: number) => { for (let w = 0; w < ms; w += 50) { if (c.cancelled) throw new Stop(); while (c.paused && !c.cancelled) await sleep(100); await sleep(50) } if (c.cancelled) throw new Stop() }
  const st = (k: string, state: StageState, code?: string, params?: Record<string, unknown>) => { const s = j.stages.find((x) => x.key === k)!; s.state = state; s.code = code; s.params = params; if (state === 'active') j.stage = k; emitJob(j) }
  const failAt = (k: string, code: string, fixId?: string) => { st(k, 'fail', code); j.state = 'failed'; j.error = { code, details: fixId ? { fixId } : {} }; emitJob(j) }
  try {
    st('check', 'active', 'contacting'); await tick(700)
    if (sim.network) return failAt('check', 'engine.network', 'retry')
    st('check', 'done', 'latest', { version, channel: 'Stable', have: cft().active })
    st('download', 'active'); j.attempt = 1; j.bytesTotal = 156 * MB
    while (j.bytesDone < j.bytesTotal) {
      await tick(150)
      const rate = (mockSettings.bandwidthLimit || 12 * MB) * (0.7 + Math.random() * 0.3)
      j.bytesPerSec = Math.round(rate); j.bytesDone = Math.min(j.bytesTotal, j.bytesDone + rate * 0.15 * 2.2); emitJob(j)
      if (sim.network && j.bytesDone / j.bytesTotal > 0.38) {
        if (j.attempt < 3) { j.attempt++; await tick(1200); continue }
        j.partialKept = true; return failAt('download', 'engine.download', 'retry')
      }
    }
    j.bytesPerSec = 0; st('download', 'done', 'downloaded', { bytes: j.bytesTotal, attempts: j.attempt })
    st('verify', 'active', 'computing'); await tick(900)
    if (sim.checksum) { j.bytesDone = 0; return failAt('verify', 'engine.checksum', 'redownload') }
    st('verify', 'done', 'shaRecorded', { sha256: '9f2c4d7a1be0c3a5d8e1f0a7b6c5d4e3f2a1b0c9d8e7f6a5b4c3d2e1f0a1e7a7' })
    st('unpack', 'active', 'extracting'); await tick(900)
    if (sim.disk) return failAt('unpack', 'engine.disk', 'freeSpace')
    st('unpack', 'done', 'unpacked', { files: 2814, bytes: Math.round(j.bytesTotal * 2.9) })
    st('selftest', 'active', 'testing'); await tick(1100)
    if (sim.policy) return failAt('selftest', 'engine.policy', 'installCft')
    if (sim.hang) return failAt('selftest', 'engine.selftest')
    st('selftest', 'done', 'selftestOk')
    st('switch', 'active', 'switching'); await tick(500)
    const e = cft(); const old = e.active
    e.installed.unshift({ version, sizeBytes: Math.round(j.bytesTotal * 2.9), path: e.path + '\\' + version, sha256: '9f2c4d7a1be0', active: true })
    e.installed.forEach((x) => { x.active = x.version === version })
    e.previous = old; e.active = version; e.status = 'ready'
    while (e.installed.length > 1 + mockSettings.keepOld) e.installed.pop()
    st('switch', 'done', old ? 'activeKept' : 'active', { version, previous: old })
    j.state = 'done'; j.stage = ''; emitJob(j)
  } catch (x) {
    if (!(x instanceof Stop)) throw x
    j.state = 'cancelled'; j.partialKept = j.bytesDone > 0; j.stages.forEach((s) => { if (s.state === 'active') s.state = 'pending' }); emitJob(j)
  } finally { cft().jobId = '' }
}

function startMock(kind: JobDTO['kind'], version: string): JobDTO {
  if ([...mockJobs.values()].some((x) => x.state === 'running' || x.state === 'paused')) throw err('conflict', { reason: 'jobRunning' })
  const j: JobDTO = { id: 'mock-' + ++seq, engineId: 'cft', kind, version, state: 'running', stage: '', stages: STAGES.map((key) => ({ key, state: 'pending' as StageState })), bytesDone: 0, bytesTotal: 0, bytesPerSec: 0, attempt: 0, partialKept: false, startedAt: new Date().toISOString() }
  mockJobs.set(j.id, j); ctl.set(j.id, { paused: false, cancelled: false }); cft().jobId = j.id
  void runMockJob(j, version)
  return structuredClone(j)
}

const mockDiag = (): Diag[] => [
  { id: 'browser', level: 'ok', code: 'found', params: { n: 2 } },
  sim.policy ? { id: 'policy', level: 'fail', code: 'blocked', params: { brands: ['chrome'] }, fixId: 'installCft' } : { id: 'policy', level: 'ok', code: 'ok' },
  sim.policy ? { id: 'pipe', level: 'fail', code: 'blocked', params: { engine: 'chrome' }, fixId: 'installCft' } : sim.hang ? { id: 'pipe', level: 'fail', code: 'timeout', params: { ms: 2000 } } : { id: 'pipe', level: 'ok', code: 'ok', params: { engine: 'chrome', version: '157.0.7955.60', ms: 960 } },
  { id: 'folder', level: 'ok', code: 'ok', params: { path: 'C:\\Users\\you\\AppData\\Local\\Ladilala' } },
  { id: 'job', level: 'ok', code: 'ok' },
  { id: 'relay', level: 'ok', code: 'ok', params: { addr: '127.0.0.1:18080' } },
]

const mock: EngineApi = {
  listEngines: async () => structuredClone(mockEngines.map((e) => ({ ...e, default: e.id === mockDefaultId }))),
  checkUpdates: async () => { await sleep(600); if (sim.network) throw err('engine.network', { fixId: 'retry' }); mockSettings.lastChecked = new Date().toISOString(); return [{ version: '157.0.8103.78', channel: 'Stable', revision: '1717349', url: '', installed: !!cft().installed.length }, { version: '158.0.8120.3', channel: 'Beta', revision: '1719000', url: '', installed: false }] },
  install: async (_id, version) => startMock('install', version || cft().latest),
  update: async () => startMock('update', cft().latest),
  redownload: async (_id, v) => startMock('redownload', v),
  pauseJob: async (id) => { ctl.get(id)!.paused = true; const j = mockJobs.get(id)!; j.state = 'paused'; emitJob(j); return structuredClone(j) },
  resumeJob: async (id) => { ctl.get(id)!.paused = false; const j = mockJobs.get(id)!; j.state = 'running'; emitJob(j); return structuredClone(j) },
  cancelJob: async (id) => { ctl.get(id)!.cancelled = true; await sleep(150); return structuredClone(mockJobs.get(id)!) },
  applyJobFix: async (id, fix) => {
    const old = mockJobs.get(id)!
    if (fix === 'redownload') sim.checksum = false
    if (fix === 'freeSpace') sim.disk = false
    if (fix === 'retry') sim.network = false
    if (fix === 'installCft') { sim.policy = false; sim.hang = false }
    return startMock(old.kind === 'redownload' ? 'install' : old.kind, old.version)
  },
  rollback: async () => { const e = cft(); if (!e.previous) throw err('engine.noPrevious'); [e.active, e.previous] = [e.previous, e.active]; e.installed.forEach((x) => { x.active = x.version === e.active }) },
  remove: async (id, v) => {
    if (id !== 'cft') throw err('engine.system')
    const e = cft(); e.installed = v ? e.installed.filter((x) => x.version !== v) : []
    if (!e.installed.some((x) => x.active)) { e.active = ''; e.previous = ''; e.status = 'missing'; if (mockDefaultId === 'cft') mockDefaultId = 'chrome' }
  },
  setDefault: async (id) => { if (id === 'patched') throw err('engine.unavailable'); if (!mockEngines.find((e) => e.id === id)?.active) throw err('engine.missing'); mockDefaultId = id as EngineId },
  getPins: async () => structuredClone(mockPins),
  setGroupPin: async (g, id) => { if (id) mockPins.groups[g] = id; else delete mockPins.groups[g] },
  getUpdateSettings: async () => ({ ...mockSettings }),
  setUpdateSettings: async (s) => { Object.assign(mockSettings, s); return { ...mockSettings } },
  runDiagnostics: async () => { await sleep(900); return mockDiag() },
  testLaunch: async () => {
    await sleep(900)
    const skip = { id: 'proxy', state: 'skipped' as const, code: 'noProfile' }
    if (sim.policy) return [{ id: 'launch', state: 'ok', params: { ms: 1800 } }, { id: 'cdp', state: 'fail', code: 'blocked' }, { id: 'fingerprint', state: 'skipped' }, skip]
    if (sim.hang) return [{ id: 'launch', state: 'fail', code: 'timeout' }, { id: 'cdp', state: 'skipped' }, { id: 'fingerprint', state: 'skipped' }, skip]
    return [{ id: 'launch', state: 'ok', params: { ms: 1800 } }, { id: 'cdp', state: 'ok', params: { version: '157.0.7955.60' } }, { id: 'fingerprint', state: 'ok' }, skip]
  },
  report: async () => mockDiag().map((d) => `[${d.level}] ${d.id}: ${d.code}`).join('\n'),
  openFolder: async () => {},
  onJob: (cb) => { jobListeners.add(cb); return () => jobListeners.delete(cb) },
}

export const engineApi: EngineApi = inWails ? real : mock
