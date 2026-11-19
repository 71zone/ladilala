import { reactive, computed } from 'vue'
import { toast } from 'vue-sonner'
import { t } from './i18n'
import { countryName } from './format'
import { inWails } from './bridge/api'
import * as prof from './bridge/profiles'
import * as prox from './bridge/proxies'
import { MOCK_GROUPS, snapshot } from './bridge/profiles-mock'
import { ENGINE_MAJOR, OS_PRESETS } from './presets'

export { OS_PRESETS, ENGINE_MAJOR, makeUA, applySeed, countries } from './presets'

export type NoiseMode = 'noise' | 'real' | 'off'
export type PState = 'running' | 'stopped' | 'error'
// `pass` never exists here: the password lives in the Go secret store. hasPass says one is saved.
export interface Proxy { id: string; type: 'http' | 'https' | 'socks5'; host: string; port: number; user: string; hasPass: boolean; cc: string; tz: string; lang: string; status: 'ok' | 'down' | 'checking' | 'untested'; latency: number | null; ip: string }
export interface Profile {
  id: string; name: string; group: string; tags: string[]; notes: string
  os: string; ua: string; screen: string; threads: number; mem: number; gpu: string
  lang: string; tz: string; seed: number
  noise: { canvas: NoiseMode; audio: NoiseMode; webgl: NoiseMode; fonts: NoiseMode; rects: NoiseMode; webrtc: NoiseMode }
  proxyId: string | null; state: PState; err?: 'proxyDown' | 'engineExit'; errCode?: string; dataDir?: string
  cookies: number; sessions: { day: number; time: string; min: number; pages: number }[]
}

// Groups and tags in use. Reactive arrays that refresh keeps in step with the profiles.
export const groupsList = reactive<string[]>([])
export const allTags = reactive<string[]>([])

export const store = reactive({
  profiles: [] as Profile[], proxies: [] as Proxy[],
  selId: '', editing: null as null | string | 'new', openEditor: (id: string | 'new') => { store.editing = id },
  loading: true, loadError: '' as string,
  busy: [] as string[], // profile ids with a start or stop in progress
})

const uniq = (xs: string[]) => { const u = [...new Set(xs.filter(Boolean))]; return inWails ? u.sort((a, b) => a.localeCompare(b)) : u }
function syncLists() {
  const g = uniq(store.profiles.map(p => p.group)); const tg = uniq(store.profiles.flatMap(p => p.tags))
  groupsList.splice(0, groupsList.length, ...(g.length || inWails ? g : MOCK_GROUPS)); allTags.splice(0, allTags.length, ...tg)
}
function applyProfiles(list: Profile[]) {
  store.profiles = list
  if (!list.some(p => p.id === store.selId)) store.selId = list[0]?.id ?? ''
  syncLists()
}
export async function refreshProfiles() { applyProfiles(await prof.listProfiles()) }
export async function refreshProxies() { store.proxies = await prox.listProxies() }
export async function loadData() {
  store.loading = true; store.loadError = ''
  try {
    await prof.initPresets()
    const [pr, px] = await Promise.all([prox.listProxies(), prof.listProfiles()])
    store.proxies = pr; applyProfiles(px)
  } catch (e) { store.loadError = prof.errorText(e) } finally { store.loading = false }
}

if (inWails) void loadData()
else { const s = snapshot(); store.proxies = s.proxies; applyProfiles(s.profiles); store.loading = false }

prof.onProfileState(e => {
  const p = store.profiles.find(x => x.id === e.profileId)
  if (p) { p.state = e.state; p.err = e.err; p.errCode = e.code }
})
prox.onProxyStatus(e => {
  const p = store.proxies.find(x => x.id === e.proxyId)
  if (!p) return
  p.status = e.status; p.latency = e.latency ?? null; p.ip = e.ip ?? p.ip
  if (e.cc) { p.cc = e.cc; p.tz = e.tz ?? p.tz; p.lang = e.lang ?? p.lang }
})

export const proxyOf = (p: Profile) => store.proxies.find(x => x.id === p.proxyId) || null
export const fpSummary = (p: Profile) => p.os + ' · Chrome ' + ENGINE_MAJOR + ' · ' + p.gpu.replace('NVIDIA GeForce ', '').replace('(R) ', '').replace(' Graphics', '') + ' · ' + p.screen + ' · ' + p.threads + 'c/' + p.mem + 'G'
export function notify(m: string) { toast(m) }

// ---- actions: call the bridge, show the result, refresh the lists ----------------------------------
export const isBusy = (id: string) => store.busy.includes(id)
const mark = (ids: string[], on: boolean) => { store.busy = on ? [...new Set([...store.busy, ...ids])] : store.busy.filter(x => !ids.includes(x)) }
const nameOf = (id: string) => store.profiles.find(p => p.id === id)?.name ?? id

// Runs fn, shows a service error as a toast, gives back undefined on failure.
export async function attempt<T>(fn: () => Promise<T>): Promise<T | undefined> {
  try { return await fn() } catch (e) { notify(prof.errorText(e)); return undefined }
}

export async function startProfiles(ids: string[]) {
  const todo = ids.filter(id => store.profiles.find(p => p.id === id)?.state !== 'running')
  if (!todo.length) return
  mark(todo, true)
  try {
    const res = await attempt(() => prof.startProfiles(todo))
    for (const r of res ?? []) {
      if (r.ok) notify(t('toasts.started', { name: nameOf(r.profileId) }))
      else notify(r.code === 'proxy.down' ? t('toasts.startRefused', { name: nameOf(r.profileId) }) : nameOf(r.profileId) + ': ' + prof.errorCodeText(r.code ?? 'internal'))
    }
    await refreshProfiles()
  } finally { mark(todo, false) }
}
export async function stopProfiles(ids: string[]) {
  const run = ids.filter(id => store.profiles.find(p => p.id === id)?.state === 'running')
  if (!run.length) return
  mark(run, true)
  try {
    if (await attempt(async () => { await prof.stopProfiles(run); return true })) {
      if (run.length === 1) notify(t('toasts.stopped', { name: nameOf(run[0]) }))
      else notify(t('toasts.stoppedN', { n: run.length }, run.length))
    }
    await refreshProfiles()
  } finally { mark(run, false) }
}
export const toggleProfile = (p: Profile) => (p.state === 'running' ? stopProfiles([p.id]) : startProfiles([p.id]))

export async function deleteProfiles(ids: string[]) {
  if (await attempt(async () => { await prof.deleteProfiles(ids); return true })) notify(t('toasts.deletedN', { n: ids.length }, ids.length))
  await refreshProfiles(); await refreshProxies()
}
export async function shuffleProfiles(ids: string[]) {
  if (await attempt(async () => { await prof.shuffleFingerprints(ids); return true })) notify(t('toasts.shuffledN', { n: ids.length }, ids.length))
  await refreshProfiles()
}
export async function assignProxiesRoundRobin(ids: string[]) {
  const ok = store.proxies.filter(x => x.status === 'ok')
  const pool = ok.length ? ok : store.proxies
  if (!pool.length) { notify(t('profiles.toast.noProxies')); return }
  if (await attempt(async () => { await prof.assignProxiesRoundRobin(ids, pool.map(x => x.id)); return true })) notify(t('toasts.assigned'))
  await refreshProfiles(); await refreshProxies()
}
export async function cloneProfile(id: string) {
  const c = await attempt(() => prof.cloneProfile(id))
  if (c) { notify(t('profiles.toast.cloned', { name: c.name })); await refreshProfiles(); store.selId = c.id }
}
export async function moveToGroup(ids: string[], group: string, tags: string[]) {
  const ok = await attempt(async () => {
    if (group.trim()) await prof.setGroup(ids, group.trim())
    if (tags.length) await prof.addTags(ids, tags)
    return true
  })
  if (ok) notify(t('profiles.toast.grouped', { n: ids.length }, ids.length))
  await refreshProfiles()
}
export async function applyProxyGeo(id: string) {
  if (await attempt(async () => { await prof.applyProxyGeo(id); return true })) notify(t('toasts.tzLangSet'))
  await refreshProfiles()
}

export interface Check { level: 'ok' | 'warn' | 'fail'; k: string; p?: Record<string, unknown> }
export const checkText = (c: Check) => t('checks.' + c.k, c.p ?? {})
export function consistency(p: Profile): Check[] {
  const out: Check[] = []; const px = proxyOf(p); const o = OS_PRESETS[p.os]
  const m = /Chrome\/(\d+)/.exec(p.ua); const major = m ? +m[1] : 0
  out.push(major === ENGINE_MAJOR ? { level: 'ok', k: 'uaOk', p: { engine: ENGINE_MAJOR } } : { level: 'fail', k: 'uaMismatch', p: { major, engine: ENGINE_MAJOR } })
  const gpuMac = /Apple|Radeon Pro/.test(p.gpu)
  out.push((o.platform === 'mac' ? gpuMac : !/Apple/.test(p.gpu)) ? { level: 'ok', k: 'gpuOk' } : { level: 'fail', k: 'gpuBad', p: { gpu: p.gpu, os: p.os } })
  out.push(p.ua.includes(o.uaOs) ? { level: 'ok', k: 'uaPlatformOk' } : { level: 'fail', k: 'uaPlatformBad', p: { os: p.os } })
  if (!px) out.push({ level: 'warn', k: 'noProxy' })
  else {
    out.push(px.status === 'down' ? { level: 'warn', k: 'proxyDown' } : { level: 'ok', k: 'proxyOk' })
    if (px.tz && px.cc && px.lang) {
      out.push(p.tz === px.tz ? { level: 'ok', k: 'tzOk', p: { tz: px.tz } } : { level: 'warn', k: 'tzDiff', p: { tz: p.tz, proxy: px.tz } })
      const cn = countryName(px.cc)
      out.push(p.lang.split('-')[1] === px.cc || p.lang.startsWith(px.lang.split('-')[0]) ? { level: 'ok', k: 'langOk', p: { lang: p.lang, country: cn } } : { level: 'warn', k: 'langOdd', p: { lang: p.lang, country: cn } })
    }
  }
  out.push(p.noise.webrtc === 'real' && px ? { level: 'warn', k: 'webrtcLeak' } : { level: 'ok', k: 'webrtcOk' })
  const [w] = p.screen.split('x').map(Number)
  out.push(o.platform === 'win' && w < 1280 ? { level: 'warn', k: 'screenRare', p: { screen: p.screen } } : { level: 'ok', k: 'screenOk' })
  out.push(p.threads / 2 > p.mem * 1 ? { level: 'warn', k: 'threadsHigh', p: { mem: p.mem } } : { level: 'ok', k: 'threadsOk' })
  return out
}
export const runningCount = computed(() => store.profiles.filter(p => p.state === 'running').length)
