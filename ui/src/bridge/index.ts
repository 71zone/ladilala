// Bridge to the Go shell. Inside the Wails window it calls the Shell service;
// in a plain browser (bun run proto) it answers from a mock so the prototype
// keeps working. Views do not import this file; only the gate panel does.
import { Call, Events } from '@wailsio/runtime'

export interface VersionInfo {
  app: string
  go: string
  os: string
  arch: string
  webview2: string
  pid: number
}
export interface LaunchResult {
  brand: string
  path: string
  version: string
  pid: number
  title: string
  hash: string
}
export interface UpdateInfo {
  current: string
  available: boolean
  version: string
  notes: string
}

export interface Bridge {
  kind: 'wails' | 'mock'
  ping(): Promise<string>
  version(): Promise<VersionInfo>
  ready(firstPaintEpochMs: number): Promise<{ firstPaintMs: number }>
  autorun(): Promise<boolean>
  autoInstall(): Promise<boolean>
  writeReport(json: string): Promise<void>
  launchChrome(): Promise<LaunchResult>
  closeBrowsers(): Promise<void>
  checkUpdate(): Promise<UpdateInfo>
  installUpdate(): Promise<void>
  onTick(cb: (n: number) => void): () => void
}

const call = <T>(method: string, ...args: unknown[]) => Call.ByName(`main.Shell.${method}`, ...args) as Promise<T>

const wailsBridge: Bridge = {
  kind: 'wails',
  ping: () => call('Ping'),
  version: () => call('Version'),
  ready: (t) => call('Ready', t),
  autorun: () => call('Autorun'),
  autoInstall: () => call('AutoInstall'),
  writeReport: (json) => call('WriteReport', json),
  launchChrome: () => call('LaunchChrome'),
  closeBrowsers: () => call('CloseBrowsers'),
  checkUpdate: () => call('CheckUpdate'),
  installUpdate: () => call('InstallUpdate'),
  onTick: (cb) => Events.On('gate:tick', (e: { data: number }) => cb(e.data)),
}

const mockBridge: Bridge = {
  kind: 'mock',
  ping: async () => 'pong (mock)',
  version: async () => ({ app: '0.0.0-mock', go: 'none', os: 'browser', arch: '', webview2: '', pid: 0 }),
  ready: async () => ({ firstPaintMs: 0 }),
  autorun: async () => false,
  autoInstall: async () => false,
  writeReport: async () => {},
  launchChrome: async () => {
    throw new Error('mock bridge: no Go shell')
  },
  closeBrowsers: async () => {},
  checkUpdate: async () => ({ current: '0.0.0-mock', available: false, version: '', notes: '' }),
  installUpdate: async () => {
    throw new Error('mock bridge: no Go shell')
  },
  onTick: (cb) => {
    let n = 0
    const id = setInterval(() => cb(++n), 1000)
    return () => clearInterval(id)
  },
}

// WebView2 exposes window.chrome.webview; desktop Chrome does not.
const inWails = typeof (window as unknown as { chrome?: { webview?: unknown } }).chrome?.webview !== 'undefined'

export const bridge: Bridge = inWails ? wailsBridge : mockBridge
