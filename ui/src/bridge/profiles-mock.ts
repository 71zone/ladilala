// In-memory profiles and proxies for the browser prototype (bun run proto).
// The real app never loads this data: it calls the Go services (profiles.ts, proxies.ts).
import { ServiceError } from './api'
import { applySeed, countries, makeUA, rnd } from '../presets'
import type { Profile, Proxy } from '../store'
import type { PreviewView, ProfileForm, StartResult } from './profiles'

type Handler = (e: unknown) => void
const handlers = new Map<string, Set<Handler>>()
export const bus = {
  on(name: string, cb: Handler) {
    if (!handlers.has(name)) handlers.set(name, new Set())
    handlers.get(name)!.add(cb)
    return () => { handlers.get(name)?.delete(cb) }
  },
}
export const emit = (name: string, data: unknown) => handlers.get(name)?.forEach(h => h(data))

const ccs = Object.keys(countries)
const proxies: Proxy[] = Array.from({ length: 18 }, (_, i) => {
  const cc = ccs[i % ccs.length]; const r = rnd(i + 7)
  const st = i === 5 ? 'down' : i === 11 ? 'down' : 'ok'
  return { id: String(i + 1), type: (['socks5', 'http', 'socks5', 'https'] as const)[i % 4], host: 'gw' + (i + 1) + '.' + cc.toLowerCase() + '.proxyhub.example', port: 8000 + i * 3, user: 'u' + (1000 + i), hasPass: true, cc, tz: countries[cc].tz, lang: countries[cc].lang, status: st, latency: st === 'ok' ? 60 + ((i * 97 + 13) % 380) : null, ip: [r() * 223 + 1, r() * 255, r() * 255, r() * 255].map(n => Math.floor(n)).join('.') } as Proxy
})

const names = ['Amazon US main', 'Amazon DE seller', 'eBay UK 01', 'eBay UK 02', 'Shopee VN 1', 'Shopee VN 2', 'Lazada SG', 'Rakuten JP', 'Mercado BR', 'FB Ads 01', 'FB Ads 02', 'FB Ads 03', 'TikTok shop FR', 'Etsy CA', 'Etsy NL', 'Signup farm A1', 'Signup farm A2', 'Signup farm A3', 'Signup farm B1', 'Signup farm B2', 'Research (clean)', 'Price watch DE', 'Price watch US', 'QA staging']
export const MOCK_GROUPS = ['Marketplace', 'Ads', 'Signups', 'Research']
const tagPool = ['seller', 'warm', 'new', 'vip', 'batch-3', 'no-cookies', 'checkout']
const oses = ['Windows 11', 'Windows 11', 'Windows 10', 'Windows 11', 'Windows 10', 'macOS 14', 'Windows 11', 'Windows 10']
const profiles: Profile[] = names.map((name, i) => {
  const r = rnd(i * 31 + 5); const os = oses[i % oses.length]
  const px = i === 20 ? null : proxies[i % proxies.length]
  const p: Profile = {
    id: String(i + 1), name, group: MOCK_GROUPS[i % 4], tags: [tagPool[i % 7], ...(i % 3 === 0 ? [tagPool[(i + 3) % 7]] : [])], notes: '',
    os, ua: makeUA(os), screen: '', threads: 8, mem: 8, gpu: '', lang: px ? px.lang : 'en-US', tz: px ? px.tz : 'America/New_York', seed: Math.floor(r() * 1e9),
    noise: { canvas: 'noise', audio: 'noise', webgl: 'noise', fonts: 'real', rects: 'noise', webrtc: 'off' },
    proxyId: px ? px.id : null, state: [0, 3, 9, 14].includes(i) ? 'running' : 'stopped', cookies: Math.floor(r() * 400),
    sessions: [{ day: 0, time: '09:12', min: 41, pages: 63 }, { day: -1, time: '17:40', min: 72, pages: 118 }, { day: -3, time: '10:03', min: 9, pages: 12 }],
  }
  applySeed(p); return p
})
profiles[7].state = 'error'; profiles[7].err = 'proxyDown'; profiles[7].proxyId = '6'
profiles[16].state = 'error'; profiles[16].err = 'engineExit'
profiles[20].tags = ['clean']; profiles[20].noise.webrtc = 'real'
profiles[2].tz = 'America/Chicago' // deliberate mismatch with proxy

export const db = { profiles, proxies }
const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v))
const find = (id: string) => {
  const p = profiles.find(x => x.id === id)
  if (!p) throw new ServiceError('notFound', { id })
  return p
}
const nextId = (list: { id: string }[]) => String(Math.max(0, ...list.map(x => +x.id)) + 1)
const nameTaken = (name: string, except?: string) => profiles.some(p => p.id !== except && p.name.toLowerCase() === name.trim().toLowerCase())
const refuseRunning = (ids: string[]) => {
  const run = ids.filter(id => find(id).state === 'running')
  if (run.length) throw new ServiceError('conflict', { reason: 'running', ids: run })
}

export const snapshot = () => ({ profiles: clone(profiles), proxies: clone(proxies) })
export const list = async (): Promise<Profile[]> => clone(profiles)
export const create = async (f: ProfileForm): Promise<Profile> => {
  if (!f.name.trim()) throw new ServiceError('invalid', { problems: [{ path: 'name', msg: 'empty' }] })
  if (nameTaken(f.name)) throw new ServiceError('conflict', { reason: 'name' })
  const p: Profile = { id: nextId(profiles), name: f.name.trim(), group: f.group, tags: [...f.tags], notes: f.notes, os: f.os, ua: makeUA(f.os), screen: '', threads: 8, mem: 8, gpu: '', lang: f.lang, tz: f.tz, seed: f.seed, noise: { ...f.noise }, proxyId: f.proxyId, state: 'stopped', cookies: 0, sessions: [] }
  applySeed(p); profiles.unshift(p); return clone(p)
}
export const update = async (id: string, f: ProfileForm): Promise<Profile> => {
  const p = find(id)
  if (!f.name.trim()) throw new ServiceError('invalid', { problems: [{ path: 'name', msg: 'empty' }] })
  if (nameTaken(f.name, id)) throw new ServiceError('conflict', { reason: 'name' })
  Object.assign(p, { name: f.name.trim(), group: f.group, tags: [...f.tags], notes: f.notes, os: f.os, seed: f.seed, lang: f.lang, tz: f.tz, noise: { ...f.noise }, proxyId: f.proxyId })
  return clone(p)
}
export const cloneProfile = async (id: string, name?: string): Promise<Profile> => {
  const s = find(id)
  const nm = name?.trim() || s.name + ' (copy)'
  if (nameTaken(nm)) throw new ServiceError('conflict', { reason: 'name' })
  const p: Profile = { ...clone(s), id: nextId(profiles), name: nm, seed: Math.floor(Math.random() * 1e9), state: 'stopped', err: undefined, cookies: 0, sessions: [] }
  applySeed(p); profiles.splice(profiles.indexOf(s) + 1, 0, p); return clone(p)
}
export const remove = async (ids: string[]): Promise<void> => {
  ids.forEach(find); refuseRunning(ids)
  ids.forEach(id => profiles.splice(profiles.findIndex(p => p.id === id), 1))
}
export const start = async (ids: string[]): Promise<StartResult[]> => ids.map(id => {
  const p = find(id)
  if (p.state === 'running') return { profileId: id, ok: true }
  const px = proxies.find(x => x.id === p.proxyId)
  if (px && px.status === 'down') {
    p.state = 'error'; p.err = 'proxyDown'; p.errCode = 'proxy.down'
    emit('profile.state', { profileId: id, state: 'error', err: 'proxyDown', code: 'proxy.down' })
    return { profileId: id, ok: false, code: 'proxy.down' }
  }
  p.state = 'running'; p.err = undefined; p.errCode = undefined
  emit('profile.state', { profileId: id, state: 'running' })
  return { profileId: id, ok: true }
})
export const stop = async (ids: string[]): Promise<void> => {
  ids.forEach(id => { const p = find(id); p.state = 'stopped'; p.err = undefined; emit('profile.state', { profileId: id, state: 'stopped' }) })
}
export const shuffle = async (ids: string[]): Promise<void> => {
  ids.forEach(find); refuseRunning(ids)
  ids.forEach(id => { const p = find(id); p.seed = Math.floor(Math.random() * 1e9); applySeed(p) })
}
export const assignProxy = async (ids: string[], proxyId: string | null): Promise<void> => { ids.forEach(find); refuseRunning(ids); ids.forEach(id => { find(id).proxyId = proxyId }) }
export const assignRoundRobin = async (ids: string[], proxyIds: string[]): Promise<void> => { ids.forEach(find); ids.forEach((id, i) => { find(id).proxyId = proxyIds[i % proxyIds.length] }) }
export const setGroup = async (ids: string[], group: string): Promise<void> => { ids.forEach(id => { find(id).group = group }) }
export const addTags = async (ids: string[], add: string[]): Promise<void> => { ids.forEach(id => { const p = find(id); p.tags = [...new Set([...p.tags, ...add])] }) }
export const applyProxyGeo = async (id: string): Promise<Profile> => {
  const p = find(id); const px = proxies.find(x => x.id === p.proxyId)
  if (!px) throw new ServiceError('invalid', { problems: [{ path: 'proxyId', msg: 'no proxy' }] })
  p.tz = px.tz; p.lang = px.lang; return clone(p)
}
export const preview = async (os: string, seed: number): Promise<PreviewView> => {
  const t = { os, seed, gpu: '', screen: '', threads: 8, mem: 8 }; applySeed(t)
  return { ua: makeUA(os), screen: t.screen, threads: t.threads, mem: t.mem, gpu: t.gpu, seed }
}
