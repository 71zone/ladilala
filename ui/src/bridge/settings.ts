// Bridge to the Go SettingsService. In a plain browser (bun run proto) it
// keeps the values in memory so the Settings screen keeps working.
import { callService, inWails } from './api'

export type Theme = 'system' | 'light' | 'dark'
export type LogLevel = 'error' | 'warn' | 'info' | 'debug'
export interface GeneralSettings {
  language: 'vi' | 'en'; theme: Theme; defaultDelayMs: number; concurrency: number
  proxyTestUrl: string; logLevel: LogLevel; defaultEngine: string
}
export interface DataFolderInfo { path: string; pending: string; default: string; overridden: boolean }

export interface SettingsApi {
  get(): Promise<GeneralSettings>
  save(s: GeneralSettings): Promise<GeneralSettings>
  setLocale(lang: 'vi' | 'en'): Promise<void>
  setTheme(theme: Theme): Promise<void>
  dataFolder(): Promise<DataFolderInfo>
  dataFolderUsage(): Promise<number>
  pickDataFolder(): Promise<string>
  changeDataFolder(path: string): Promise<DataFolderInfo>
  openDataFolder(): Promise<void>
  openLogsFolder(): Promise<void>
}

const S = 'SettingsService'
const real: SettingsApi = {
  get: () => callService(S, 'GetSettings'),
  save: (s) => callService(S, 'SaveSettings', s),
  setLocale: (l) => callService(S, 'SetLocale', l),
  setTheme: (t) => callService(S, 'SetTheme', t),
  dataFolder: () => callService(S, 'GetDataFolder'),
  dataFolderUsage: () => callService(S, 'DataFolderUsage'),
  pickDataFolder: () => callService(S, 'PickDataFolder'),
  changeDataFolder: (p) => callService(S, 'ChangeDataFolder', p),
  openDataFolder: () => callService(S, 'OpenDataFolder'),
  openLogsFolder: () => callService(S, 'OpenLogsFolder'),
}

const mem: GeneralSettings = { language: 'vi', theme: 'system', defaultDelayMs: 0, concurrency: 1, proxyTestUrl: 'https://api.ipify.org', logLevel: 'info', defaultEngine: 'chrome' }
const memFolder: DataFolderInfo = { path: 'C:\\Users\\you\\AppData\\Local\\Ladilala', pending: '', default: 'C:\\Users\\you\\AppData\\Local\\Ladilala', overridden: false }
const mock: SettingsApi = {
  get: async () => ({ ...mem }),
  save: async (s) => { Object.assign(mem, s, { defaultEngine: mem.defaultEngine }); return { ...mem } },
  setLocale: async (l) => { mem.language = l },
  setTheme: async (t) => { mem.theme = t },
  dataFolder: async () => ({ ...memFolder }),
  dataFolderUsage: async () => 2.4 * 1024 * 1024 * 1024,
  pickDataFolder: async () => 'D:\\Ladilala data',
  changeDataFolder: async (p) => { memFolder.pending = p === memFolder.default ? '' : p; return { ...memFolder } },
  openDataFolder: async () => {},
  openLogsFolder: async () => {},
}

export const settingsApi: SettingsApi = inWails ? real : mock
