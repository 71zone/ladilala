// Typed bridge to the Go ProxyService (app/proxy_service.go, docs/api-contract.md).
// In the Wails window each function calls main.ProxyService.<Method>. In a plain
// browser (bun run proto) it answers from proxies-mock.ts, so the prototype keeps working.
import { callService, inWails, onEvent } from './api'
import type { Proxy } from '../store'
import * as mock from './proxies-mock'
import { bus } from './profiles-mock'

// What the editor sends. `password` is write-only: undefined keeps the stored
// one, '' removes it.
export interface ProxyForm { type: Proxy['type']; host: string; port: number; user: string; password?: string }
export interface TestResult { ok: boolean; status: Proxy['status']; ip?: string; latency?: number; cc?: string; tz?: string; lang?: string; code?: string }
export interface ImportResult { added: number; skipped: { line: number; reason: string }[] }
export interface ProxyStatusEvent { proxyId: string; status: Proxy['status']; latency?: number; ip?: string; cc?: string; tz?: string; lang?: string }
export interface ProxyLookup { ipUrl: string; geoUrl: string }

interface ProxyDTO {
  id: string; scheme: Proxy['type']; host: string; port: number; username: string; hasPassword: boolean
  country: string; timezone: string; language: string; status: Proxy['status']; latencyMs?: number; ip?: string; usedBy: string[]
}
interface CheckDTO { ok: boolean; status: Proxy['status']; ip?: string; latencyMs?: number; country?: string; timezone?: string; language?: string; code?: string }

const fromDTO = (d: ProxyDTO): Proxy => ({
  id: d.id, type: d.scheme, host: d.host, port: d.port, user: d.username, hasPass: d.hasPassword, cc: d.country, tz: d.timezone, lang: d.language,
  status: d.status, latency: d.latencyMs ?? null, ip: d.ip ?? '',
})
const toInput = (f: ProxyForm) => ({ scheme: f.type, host: f.host, port: f.port, username: f.user, password: f.password ?? null })

const svc = 'ProxyService'
const call = <T>(method: string, ...args: unknown[]) => callService<T>(svc, method, ...args)

export const listProxies = async (): Promise<Proxy[]> => (inWails ? (await call<ProxyDTO[]>('ListProxies')).map(fromDTO) : mock.list())
export const createProxy = async (f: ProxyForm): Promise<Proxy> => (inWails ? fromDTO(await call<ProxyDTO>('CreateProxy', toInput(f))) : mock.create(f))
export const updateProxy = async (id: string, f: ProxyForm): Promise<Proxy> => (inWails ? fromDTO(await call<ProxyDTO>('UpdateProxy', id, toInput(f))) : mock.update(id, f))
export const deleteProxy = async (id: string): Promise<void> => (inWails ? call('DeleteProxy', id) : mock.remove(id))
export const setProxyPassword = async (id: string, password: string): Promise<void> => (inWails ? call('SetProxyPassword', id, password) : mock.setPassword(id, password))

export async function testProxy(f: ProxyForm & { id?: string }): Promise<TestResult> {
  if (!inWails) return mock.test(f)
  const r = await call<CheckDTO>('TestProxy', { id: f.id ?? '', scheme: f.type, host: f.host, port: f.port, username: f.user, password: f.password ?? '' })
  return { ok: r.ok, status: r.status, ip: r.ip, latency: r.latencyMs, cc: r.country, tz: r.timezone, lang: r.language, code: r.code }
}

export async function importProxies(text: string, defaultScheme: Proxy['type'] = 'socks5'): Promise<ImportResult> {
  if (!inWails) return mock.importLines(text)
  const r = await call<{ added: number; skipped: { line: number; reason: string }[] | null }>('ImportProxies', text, defaultScheme)
  return { added: r.added, skipped: r.skipped ?? [] }
}

export const getLookup = (): Promise<ProxyLookup> => (inWails ? call('GetProxyLookup') : Promise.resolve({ ipUrl: 'https://api.ipify.org', geoUrl: 'https://ipwho.is/{ip}' }))
export const setLookup = (l: ProxyLookup): Promise<ProxyLookup> => (inWails ? call('SetProxyLookup', l) : Promise.resolve(l))

// healthCheck starts the check and gives back a promise that settles when the
// job ends. The listener exists before the call, so a fast job cannot be missed.
export async function healthCheck(ids: string[] = []): Promise<Promise<void>> {
  const seen = new Set<string>()
  let want = ''
  let finish = () => {}
  const done = new Promise<void>(res => { finish = res })
  const off = (inWails ? onEvent<{ jobId: string }> : (n: string, cb: (e: { jobId: string }) => void) => bus.on(n, e => cb(e as { jobId: string })))('job.done', e => {
    seen.add(e.jobId)
    if (e.jobId === want) { off(); finish() }
  })
  try {
    want = inWails ? await call<string>('HealthCheck', ids) : await mock.healthCheck(ids)
  } catch (e) {
    off()
    throw e
  }
  if (seen.has(want)) { off(); finish() }
  return done
}

export function onProxyStatus(cb: (e: ProxyStatusEvent) => void): () => void {
  const map = (e: { proxyId: string; status: Proxy['status']; latencyMs?: number; ip?: string; country?: string; timezone?: string; language?: string }) =>
    cb({ proxyId: e.proxyId, status: e.status, latency: e.latencyMs, ip: e.ip, cc: e.country, tz: e.timezone, lang: e.language })
  if (!inWails) return bus.on('proxy.status', e => map(e as never))
  return onEvent<Parameters<typeof map>[0]>('proxy.status', map)
}
