// Typed bridge to the Go ProfileService (app/profile_service.go, docs/api-contract.md).
// In the Wails window each function calls main.ProfileService.<Method>. In a plain
// browser (bun run proto) it answers from profiles-mock.ts, so the prototype keeps working.
import { callService, inWails, onEvent, ServiceError } from './api'
import { i18n } from '../i18n'
import { setEngineMajor } from '../presets'
import type { NoiseMode, Profile } from '../store'
import * as mock from './profiles-mock'

// What the editor sends. `os` is a key of OS_PRESETS ('Windows 11').
export interface ProfileForm {
  name: string; group: string; tags: string[]; notes: string; proxyId: string | null
  os: string; seed: number; lang: string; tz: string; noise: Profile['noise']
}
export interface StartResult { profileId: string; ok: boolean; code?: string }
export interface PreviewView { ua: string; screen: string; threads: number; mem: number; gpu: string; seed: number }
export interface ProfileStateEvent { profileId: string; state: Profile['state']; err?: 'proxyDown' | 'engineExit'; code?: string }

interface FingerprintDTO {
  seed: number; preset: string; platform: string; userAgent: string; screenWidth: number; screenHeight: number
  hardwareThreads: number; deviceMemoryGb: number; gpuRenderer: string; language: string; timezone: string
  canvas: NoiseMode; audio: NoiseMode; webgl: NoiseMode; fonts: NoiseMode; clientRects: NoiseMode; webrtc: NoiseMode
}
interface ProfileDTO {
  id: string; name: string; group: string; tags: string[]; notes: string; proxyId?: string; fingerprint: FingerprintDTO
  state: Profile['state']; err?: Profile['err']; errCode?: string; cookieCount: number; dataDir: string
}

const OS_OF_PRESET: Record<string, string> = { 'windows-11': 'Windows 11', 'windows-10': 'Windows 10', 'macos-14': 'macOS 14' }
const PRESET_OF_OS: Record<string, string> = { 'Windows 11': 'windows-11', 'Windows 10': 'windows-10', 'macOS 14': 'macos-14' }

// "ANGLE (NVIDIA, NVIDIA GeForce RTX 3060 (0x00002504) Direct3D11 vs_5_0 ps_5_0, D3D11)" -> "NVIDIA GeForce RTX 3060"
export function gpuLabel(renderer: string): string {
  let m = /^ANGLE \([^,]+, (.+?)(?: \(0x[0-9A-Fa-f]+\))? Direct3D/.exec(renderer)
  if (m) return m[1]
  m = /^ANGLE \([^,]+, ANGLE Metal Renderer: (.+?), Unspecified Version\)$/.exec(renderer)
  if (m) return m[1]
  m = /^ANGLE \([^,]+, (?:Mesa )?(.+?)(?: \([^)]*\))?, OpenGL/.exec(renderer)
  return m ? m[1] : renderer
}

function fromDTO(d: ProfileDTO): Profile {
  const f = d.fingerprint
  return {
    id: d.id, name: d.name, group: d.group, tags: d.tags ?? [], notes: d.notes, os: OS_OF_PRESET[f.preset] ?? 'Windows 11', ua: f.userAgent,
    screen: f.screenWidth + 'x' + f.screenHeight, threads: f.hardwareThreads, mem: f.deviceMemoryGb, gpu: gpuLabel(f.gpuRenderer),
    lang: f.language, tz: f.timezone, seed: f.seed,
    noise: { canvas: f.canvas, audio: f.audio, webgl: f.webgl, fonts: f.fonts, rects: f.clientRects, webrtc: f.webrtc },
    proxyId: d.proxyId || null, state: d.state, err: d.err, errCode: d.errCode, cookies: d.cookieCount, sessions: [], dataDir: d.dataDir,
  }
}

const toInput = (f: ProfileForm) => ({
  name: f.name, group: f.group, tags: f.tags, notes: f.notes, proxyId: f.proxyId ?? '', preset: PRESET_OF_OS[f.os] ?? 'windows-11', seed: f.seed,
  language: f.lang, timezone: f.tz, canvas: f.noise.canvas, audio: f.noise.audio, webgl: f.noise.webgl, fonts: f.noise.fonts, clientRects: f.noise.rects, webrtc: f.noise.webrtc,
})

const svc = 'ProfileService'
const call = <T>(method: string, ...args: unknown[]) => callService<T>(svc, method, ...args)

export const initPresets = async (): Promise<void> => {
  if (!inWails) return
  const r = await call<{ engineMajor: number }>('Presets')
  setEngineMajor(r.engineMajor)
}
export const listProfiles = async (): Promise<Profile[]> => (inWails ? (await call<ProfileDTO[]>('ListProfiles', {})).map(fromDTO) : mock.list())
export const createProfile = async (f: ProfileForm): Promise<Profile> => (inWails ? fromDTO(await call<ProfileDTO>('CreateProfile', toInput(f))) : mock.create(f))
export const updateProfile = async (id: string, f: ProfileForm): Promise<Profile> => (inWails ? fromDTO(await call<ProfileDTO>('UpdateProfile', id, toInput(f))) : mock.update(id, f))
export const cloneProfile = async (id: string, name = ''): Promise<Profile> => (inWails ? fromDTO(await call<ProfileDTO>('CloneProfile', id, name)) : mock.cloneProfile(id, name))
export const deleteProfiles = async (ids: string[]): Promise<void> => (inWails ? call('DeleteProfiles', ids, true) : mock.remove(ids))
export const stopProfiles = async (ids: string[]): Promise<void> => (inWails ? call('StopProfiles', ids) : mock.stop(ids))
export const shuffleFingerprints = async (ids: string[]): Promise<void> => (inWails ? call('ShuffleFingerprint', ids) : mock.shuffle(ids))
export const assignProxy = async (ids: string[], proxyId: string | null): Promise<void> => (inWails ? call('AssignProxy', ids, proxyId ?? '') : mock.assignProxy(ids, proxyId))
export const assignProxiesRoundRobin = async (ids: string[], proxyIds: string[]): Promise<void> => (inWails ? call('AssignProxyRoundRobin', ids, proxyIds) : mock.assignRoundRobin(ids, proxyIds))
export const setGroup = async (ids: string[], group: string): Promise<void> => (inWails ? call('BulkSetGroup', ids, group) : mock.setGroup(ids, group))
export const addTags = async (ids: string[], add: string[]): Promise<void> => (inWails ? call('BulkSetTags', ids, add, []) : mock.addTags(ids, add))
export const applyProxyGeo = async (id: string): Promise<Profile> => (inWails ? fromDTO(await call<ProfileDTO>('ApplyProxyGeo', id)) : mock.applyProxyGeo(id))

export const startProfiles = async (ids: string[]): Promise<StartResult[]> => {
  if (!inWails) return mock.start(ids)
  const r = await call<{ profileId: string; ok: boolean; error?: { code: string } }[]>('StartProfiles', ids)
  return r.map(x => ({ profileId: x.profileId, ok: x.ok, code: x.error?.code }))
}

// The editor shows the device a preset and a seed give. Hardware fields come from here.
export async function previewFingerprint(os: string, seed: number, lang: string, tz: string): Promise<PreviewView> {
  if (!inWails) return mock.preview(os, seed)
  const f = await call<FingerprintDTO>('PreviewFingerprint', { preset: PRESET_OF_OS[os] ?? 'windows-11', seed, language: lang, timezone: tz })
  return { ua: f.userAgent, screen: f.screenWidth + 'x' + f.screenHeight, threads: f.hardwareThreads, mem: f.deviceMemoryGb, gpu: gpuLabel(f.gpuRenderer), seed: f.seed }
}

export function onProfileState(cb: (e: ProfileStateEvent) => void): () => void {
  return inWails ? onEvent<ProfileStateEvent>('profile.state', cb) : mock.bus.on('profile.state', e => cb(e as ProfileStateEvent))
}

// A random seed in the range a JavaScript number holds exactly and Go accepts.
export const randomSeed = () => Math.floor(Math.random() * 2 ** 52) + 1

// errorCodeText turns a service error code into a message in the UI language.
// Codes are translated through errors.* (docs/api-contract.md, "Error shape").
export function errorCodeText(c: string, details: Record<string, unknown> = {}): string {
  const { t, te } = i18n.global
  if (c === 'conflict') {
    const reason = String(details.reason ?? 'name')
    return te('errors.profileConflict.' + reason) ? t('errors.profileConflict.' + reason) : t('errors.profileConflict.name')
  }
  if (c === 'invalid') {
    const probs = (details.problems as { path: string }[] | undefined) ?? []
    const paths = [...new Set(probs.map(p => p.path))].join(', ')
    return paths ? t('errors.invalid') + ' (' + paths + ')' : t('errors.invalid')
  }
  const key = c.startsWith('engine.') ? 'errors.profileEngine.' + c.slice(7) : 'errors.' + c
  return te(key) ? t(key) : t('errors.internal')
}

export const errorText = (e: unknown): string => (e instanceof ServiceError ? errorCodeText(e.code, e.details) : i18n.global.t('errors.internal'))
