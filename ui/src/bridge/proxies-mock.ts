// In-memory proxy pool for the browser prototype. See profiles-mock.ts.
import { ServiceError } from './api'
import { countries } from '../presets'
import type { Proxy } from '../store'
import { db, emit } from './profiles-mock'
import type { ImportResult, ProxyForm, TestResult } from './proxies'

const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v))
const find = (id: string) => {
  const p = db.proxies.find(x => x.id === id)
  if (!p) throw new ServiceError('notFound', { id })
  return p
}
const nextId = () => String(Math.max(0, ...db.proxies.map(x => +x.id)) + 1)

export const list = async (): Promise<Proxy[]> => clone(db.proxies)
export const create = async (f: ProxyForm): Promise<Proxy> => {
  const cc = Object.keys(countries)[(f.host.length + f.port) % Object.keys(countries).length]
  const p: Proxy = { id: nextId(), type: f.type, host: f.host, port: f.port, user: f.user, hasPass: !!f.password, cc, tz: countries[cc].tz, lang: countries[cc].lang, status: 'untested', latency: null, ip: '' }
  db.proxies.push(p); return clone(p)
}
export const update = async (id: string, f: ProxyForm): Promise<Proxy> => {
  const p = find(id); Object.assign(p, { type: f.type, host: f.host, port: f.port, user: f.user }); if (f.password !== undefined) p.hasPass = !!f.password
  return clone(p)
}
export const remove = async (id: string): Promise<void> => {
  const p = find(id)
  if (db.profiles.some(x => x.proxyId === id && x.state === 'running')) throw new ServiceError('conflict', { reason: 'running' })
  db.profiles.forEach(x => { if (x.proxyId === p.id) x.proxyId = null })
  db.proxies.splice(db.proxies.indexOf(p), 1)
}
export const setPassword = async (id: string, password: string): Promise<void> => { find(id).hasPass = !!password }
export const test = async (f: ProxyForm & { id?: string }): Promise<TestResult> => {
  await new Promise(r => setTimeout(r, 1100))
  if (!f.host) return { ok: false, status: 'down', code: 'proxy.down' }
  const p = db.proxies.find(x => x.id === f.id || x.host === f.host)
  if (p && p.status === 'down') return { ok: false, status: 'down', code: 'proxy.down' }
  const ccs = Object.keys(countries); const cc = p ? p.cc : ccs[f.host.length % ccs.length]
  return { ok: true, status: 'ok', ip: p ? p.ip : '203.0.113.' + (f.port % 250), latency: p?.latency || 140 + (f.port % 200), cc, tz: countries[cc].tz, lang: countries[cc].lang }
}
export const importLines = async (text: string): Promise<ImportResult> => {
  const res: ImportResult = { added: 0, skipped: [] }
  text.split('\n').forEach((raw, i) => {
    const line = raw.trim(); if (!line) return
    const [host, port, user, pass] = line.split(':')
    if (!host || !+port) { res.skipped.push({ line: i + 1, reason: 'format' }); return }
    void create({ type: 'socks5', host, port: +port, user: user || '', password: pass || '' }); res.added++
  })
  return res
}
export const healthCheck = async (ids: string[]): Promise<string> => {
  const job = 'mock-job-' + Date.now()
  const targets = ids.length ? db.proxies.filter(p => ids.includes(p.id)) : db.proxies
  targets.forEach(p => { p.status = 'checking'; p.latency = null; emit('proxy.status', { proxyId: p.id, status: 'checking' }) })
  targets.forEach((p, i) => setTimeout(() => {
    const down = (+p.id * 7) % 9 === 0; p.status = down ? 'down' : 'ok'; p.latency = down ? null : 50 + ((+p.id * 53) % 420)
    emit('proxy.status', { proxyId: p.id, status: p.status, latencyMs: p.latency ?? undefined, ip: p.ip, country: p.cc, timezone: p.tz, language: p.lang })
    if (i === targets.length - 1) emit('job.done', { jobId: job, ok: true })
  }, 250 + i * 160))
  return job
}
