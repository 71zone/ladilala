import { reactive, computed } from 'vue'
import { notify } from './store'
import { t } from './i18n'
import { fmtRel, fmtClock } from './format'
import { errorText } from './errtext'
import { appUpdateApi, mockSetScenario, SCENARIOS, type AppUpdateSettings, type AppUpdateState, type Scenario } from './bridge/appupdate'
import { inWails } from './bridge/api'

// State of the Ladilala APP updates (not the browser engines: see engines.ts).
// The Go AppUpdateService owns the state; this store mirrors it and adds what
// the screens need (percent, ETA, the sheet switch).
export type { Scenario }
export const AUS: Scenario[] = [...SCENARIOS]
const MB = 1024 * 1024

export const au = reactive({
  current: '', channel: 'stable' as AppUpdateState['channel'], autoCheck: 'daily' as AppUpdateState['autoCheck'], installOnQuit: true,
  skipped: '', lastChecked: '', configured: true, feedUrl: '', feedFromEnv: false,
  phase: 'idle' as AppUpdateState['phase'], rel: null as AppUpdateState['release'], stages: [] as AppUpdateState['stages'],
  bytesDone: 0, bytesTotal: 0, pct: 0, speed: 0, eta: 0, failure: null as AppUpdateState['failure'] | null,
  whenIdle: false, activeRuns: 0, engineMajor: 0, engineCompatible: true,
  sheet: false, restarting: false, scenario: 'none' as Scenario,
})

function apply(s: AppUpdateState) {
  const was = au.phase, hadRel = au.rel?.version
  Object.assign(au, {
    current: s.current, channel: s.channel, autoCheck: s.autoCheck, installOnQuit: s.installOnQuit, skipped: s.skipped, lastChecked: s.lastChecked,
    configured: s.configured, feedUrl: s.feedUrl, feedFromEnv: s.feedFromEnv, phase: s.phase, rel: s.release, stages: s.stages,
    bytesDone: s.bytesDone, bytesTotal: s.bytesTotal, pct: s.bytesTotal > 0 ? (s.bytesDone / s.bytesTotal) * 100 : 0, speed: s.bytesPerSec / MB,
    eta: s.bytesPerSec > 0 ? Math.max(1, Math.round((s.bytesTotal - s.bytesDone) / s.bytesPerSec)) : 0,
    failure: s.failure ?? null, whenIdle: s.whenIdle, activeRuns: s.activeRuns, engineMajor: s.engineMajor, engineCompatible: s.engineCompatible,
  })
  if (s.phase === 'available' && (was !== 'available' || hadRel !== s.release?.version) && s.release) notify(t('toasts.appAvailable', { version: s.release.version }))
  if (s.phase === 'ready' && was !== 'ready' && s.release) notify(t('toasts.appReady', { version: s.release.version }))
}

appUpdateApi.onState(apply)
appUpdateApi.state().then(apply).catch(() => {})

const run = async <T>(fn: () => Promise<T>): Promise<T | undefined> => {
  try { return await fn() } catch (e) { notify(errorText(e)); return undefined }
}

export const engineNote = computed(() => {
  const r = au.rel
  if (!r || r.minEngine === 0) return t('appUpdate.engineNone')
  return au.engineCompatible ? t('appUpdate.engineOk', { n: r.minEngine }) : t('appUpdate.engineNeeds', { n: r.minEngine })
})
export const needsEngine = computed(() => !!au.rel && !au.engineCompatible)
export const dlMb = computed(() => au.bytesDone / MB)
export const totalMb = computed(() => (au.rel ? au.rel.sizeBytes / MB : au.bytesTotal / MB))

export function setScenario(sc: Scenario) {
  au.scenario = sc
  mockSetScenario(sc)
  if (sc === 'none') notify(t('toasts.appUpToDate'))
}
export async function checkNow() {
  if (au.phase === 'checking') return
  if (au.phase === 'downloading' || au.phase === 'ready' || au.phase === 'failed') { au.sheet = true; return }
  const s = await run(() => appUpdateApi.check(true))
  if (s && s.phase === 'idle') notify(t('toasts.appCurrent', { version: au.current }))
}
export const startDownload = async () => { au.sheet = true; await run(() => appUpdateApi.download()) }
export const cancelDl = () => run(() => appUpdateApi.cancel())
export const applyFix = (fixId: string) => run(() => appUpdateApi.applyFix(fixId))
export async function restartNow() {
  au.restarting = true; notify(t('toasts.restarting'))
  const done = await run(() => appUpdateApi.restartNow().then(() => true))
  au.restarting = false
  if (done && !inWails) { au.sheet = false; notify(t('toasts.updatedTo', { version: au.current })) }
}
export async function restartWhenIdle() { if (await run(() => appUpdateApi.restartWhenIdle())) notify(t('toasts.restartWhenIdle')) }
export const cancelWhenIdle = () => run(() => appUpdateApi.cancelRestartWhenIdle())
export async function skipVersion() {
  if (!au.rel) return
  const v = au.rel.version
  if (await run(() => appUpdateApi.skip(v))) { notify(t('toasts.skipping', { version: v })); au.sheet = false }
}
export const saveUpdateSettings = (patch: Partial<AppUpdateSettings>) =>
  run(() => appUpdateApi.setSettings({ channel: au.channel, autoCheck: au.autoCheck, installOnQuit: au.installOnQuit, feedUrl: au.feedFromEnv ? '' : au.feedUrl, ...patch }))
export const indicator = computed(() => !!au.rel && au.phase !== 'idle' && au.phase !== 'checking')
export const fmtChecked = () => {
  if (!au.lastChecked) return t('appUpdate.never')
  const ms = new Date(au.lastChecked).getTime(), m = Math.round((Date.now() - ms) / 60000)
  return m < 1 ? t('appUpdate.justNow') : m < 60 ? fmtRel(-m, 'minute') : t('appUpdate.todayAt', { time: fmtClock(ms) })
}
