// What the run setup dialog needs from the macro, data and profile services.
// Macros come from macros.ts. Sources and mapping call DataService directly
// until data.ts publishes listSources and getMapping: then import them from
// there and delete this file.
import { callService, inWails } from './api'
import { listMacros as listSavedMacros } from './macros'
import { store } from '../store'

export interface MacroOption { id: string; name: string; variables: string[] }
export interface SourceOption { id: string; name: string; kind: string; rows: number } // rows is -1 when unknown
export interface ProfileOption { id: string; name: string; group: string; running: boolean }
export interface MappingCheck { missing: string[]; extra: string[]; duplicates: string[] }

type Loose = Record<string, unknown>
const str = (v: unknown) => (typeof v === 'string' ? v : '')
const strs = (v: unknown) => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : [])

export async function listMacros(): Promise<MacroOption[]> {
  return (await listSavedMacros()).map(m => ({ id: m.id, name: m.name, variables: m.variables }))
}

export async function listSources(): Promise<SourceOption[]> {
  if (!inWails) return [{ id: 's1', name: 'sheet-april.csv', kind: 'csv', rows: 412 }, { id: 's2', name: 'orders.xlsx', kind: 'excel', rows: 1280 }, { id: 's3', name: 'REST /customers', kind: 'rest', rows: -1 }]
  const list = await callService<Loose[]>('DataService', 'ListSources')
  return list.map(s => ({ id: str(s.id), name: str(s.name), kind: str(s.kind), rows: typeof s.rowCount === 'number' ? s.rowCount : typeof s.rows === 'number' ? s.rows : -1 }))
}

export async function getMapping(macroId: string, sourceId: string): Promise<MappingCheck> {
  if (!inWails) return { missing: sourceId === 's2' ? ['password'] : [], extra: sourceId === 's1' ? ['phone'] : [], duplicates: [] }
  const r = await callService<Loose>('DataService', 'ValidateMapping', macroId, sourceId)
  return { missing: strs(r.missing), extra: strs(r.extra), duplicates: strs(r.duplicates) }
}

export async function listProfiles(): Promise<ProfileOption[]> {
  if (!inWails) return store.profiles.map(p => ({ id: 'p' + p.id, name: p.name, group: p.group, running: p.state === 'running' }))
  const list = await callService<Loose[]>('ProfileService', 'ListProfiles', {})
  return list.map(p => ({ id: str(p.id), name: str(p.name), group: str(p.group), running: p.state === 'running' }))
}
