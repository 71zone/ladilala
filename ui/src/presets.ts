// Static device data and helpers shared by the store and the profile bridge.
// No imports from store.ts or bridge/*, so both can import this file.
import type { Profile } from './store'

export const OS_PRESETS: Record<string, { label: string; platform: string; uaOs: string; gpus: string[]; screens: string[]; fonts: number }> = {
  'Windows 11': { label: 'Windows 11', platform: 'win', uaOs: 'Windows NT 10.0; Win64; x64', gpus: ['NVIDIA GeForce RTX 3060', 'NVIDIA GeForce GTX 1660 SUPER', 'Intel(R) UHD Graphics 770', 'AMD Radeon RX 6600', 'NVIDIA GeForce RTX 4070'], screens: ['1920x1080', '2560x1440', '1536x864', '1366x768'], fonts: 212 },
  'Windows 10': { label: 'Windows 10', platform: 'win', uaOs: 'Windows NT 10.0; Win64; x64', gpus: ['NVIDIA GeForce GTX 1050 Ti', 'Intel(R) HD Graphics 630', 'AMD Radeon RX 580', 'NVIDIA GeForce RTX 2060'], screens: ['1920x1080', '1366x768', '1600x900', '1440x900'], fonts: 187 },
  'macOS 14': { label: 'macOS 14', platform: 'mac', uaOs: 'Macintosh; Intel Mac OS X 10_15_7', gpus: ['Apple M1', 'Apple M2 Pro', 'AMD Radeon Pro 5500M'], screens: ['1440x900', '1728x1117', '2560x1600'], fonts: 164 },
}
// Chrome major version of new fingerprints. The real app sets it from the Go
// service (setEngineMajor); `export let` keeps importers in sync.
export let ENGINE_MAJOR = 150
export const setEngineMajor = (n: number) => { if (n > 0) ENGINE_MAJOR = n }
export const makeUA = (os: string, major = ENGINE_MAJOR) => 'Mozilla/5.0 (' + OS_PRESETS[os].uaOs + ') AppleWebKit/537.36 (KHTML, like Gecko) Chrome/' + major + '.0.0.0 Safari/537.36'

export const rnd = (seed: number) => () => { seed = (seed * 1664525 + 1013904223) % 4294967296; return seed / 4294967296 }

export const countries: Record<string, { tz: string; lang: string }> = {
  US: { tz: 'America/New_York', lang: 'en-US' }, DE: { tz: 'Europe/Berlin', lang: 'de-DE' }, GB: { tz: 'Europe/London', lang: 'en-GB' },
  FR: { tz: 'Europe/Paris', lang: 'fr-FR' }, JP: { tz: 'Asia/Tokyo', lang: 'ja-JP' }, BR: { tz: 'America/Sao_Paulo', lang: 'pt-BR' },
  NL: { tz: 'Europe/Amsterdam', lang: 'nl-NL' }, SG: { tz: 'Asia/Singapore', lang: 'en-SG' }, CA: { tz: 'America/Toronto', lang: 'en-CA' }, VN: { tz: 'Asia/Ho_Chi_Minh', lang: 'vi-VN' },
}

type SeedFields = Pick<Profile, 'os' | 'seed' | 'gpu' | 'screen' | 'threads' | 'mem'>
// Mock only: derives the hardware of a profile from its seed. The real app
// asks the Go service (PreviewFingerprint).
export function applySeed(p: SeedFields) {
  const r = rnd(p.seed); const o = OS_PRESETS[p.os]
  p.gpu = o.gpus[Math.floor(r() * o.gpus.length)]; p.screen = o.screens[Math.floor(r() * o.screens.length)]
  p.threads = [4, 6, 8, 12, 16][Math.floor(r() * 5)]; p.mem = [4, 8, 8, 16, 16, 32][Math.floor(r() * 6)]
  if (p.os === 'macOS 14') { p.threads = [8, 10, 12][Math.floor(r() * 3)]; p.mem = [8, 16, 16, 32][Math.floor(r() * 4)] }
}
