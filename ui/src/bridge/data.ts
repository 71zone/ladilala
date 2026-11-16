// Typed bridge to the Go DataService (app/data_service.go, docs/api-contract.md).
// In the Wails window each function calls main.DataService.<Method>. In a plain browser
// (bun run proto) it answers from data-mock.ts, so the prototype keeps working.
// Run setup imports listSources and getMapping from here.
import { callService, inWails } from './api'
import * as mock from './data-mock'
import type { Macro } from './macros-types'
import type { FileSourceInput, MappingDTO, RESTSourceInput, SourceDTO, SourcePreview, ValidationResult } from './data-types'

export * from './data-types'

const call = <T>(method: string, ...args: unknown[]) => callService<T>('DataService', method, ...args)

/** Native file dialog. Resolves '' when the user cancels. */
export const pickDataFile = (): Promise<string> => (inWails ? call('PickDataFile') : mock.pickDataFile())
/** Header and first rows of a file with the given options. Saves nothing. */
export const inspectFile = (input: FileSourceInput): Promise<SourcePreview> => (inWails ? call('InspectFile', input) : mock.inspectFile(input))
export const createSourceFromFile = (input: FileSourceInput): Promise<SourceDTO> => (inWails ? call('CreateSourceFromFile', input) : mock.createSourceFromFile(input))
/** Sends the first request and returns the columns and first rows. Saves nothing. */
export const testRestSource = (input: RESTSourceInput): Promise<SourcePreview> => (inWails ? call('TestRESTSource', input) : mock.testRestSource(input))
export const createRestSource = (input: RESTSourceInput): Promise<SourceDTO> => (inWails ? call('CreateRESTSource', input) : mock.createRestSource(input))
export const listSources = (): Promise<SourceDTO[]> => (inWails ? call('ListSources') : mock.listSources())
export const getSource = (id: string): Promise<SourceDTO> => (inWails ? call('GetSource', id) : mock.getSource(id))
export const deleteSource = (id: string): Promise<void> => (inWails ? call('DeleteSource', id) : mock.deleteSource(id))
export const previewSource = (id: string, from: number, limit: number): Promise<SourcePreview> => (inWails ? call('PreviewSource', id, from, limit) : mock.previewSource(id, from, limit))
/** Macro variables matched to source columns, with the overrides the user saved. */
export const getMapping = (macroId: string, sourceId: string): Promise<MappingDTO> => (inWails ? call('ValidateMapping', macroId, sourceId) : mock.getMapping(macroId, sourceId))
export const setMapping = (macroId: string, sourceId: string, overrides: Record<string, string>): Promise<MappingDTO> => (inWails ? call('SetMapping', macroId, sourceId, overrides) : mock.setMapping(macroId, sourceId, overrides))
/** Checks rows [from, to) against the macro. to < 0 means to the end. */
export const validateRows = (macroId: string, sourceId: string, from: number, to: number): Promise<ValidationResult> => (inWails ? call('ValidateRows', macroId, sourceId, from, to) : mock.validateRows(macroId, sourceId, from, to))

export interface TemplateResult { path: string; columns: number }
export type TemplateFormat = 'xlsx' | 'csv'
/**
 * Saves an empty data file for a macro: row 1 holds its data columns in macro order. xlsx adds a "Guide" sheet.
 * The target is a saved macro (id) or a draft that is not saved yet. Wails asks for a path; resolves null when the user cancels.
 */
export async function downloadTemplate(target: { macroId: string } | { macro: Macro }, name: string, format: TemplateFormat, lang: string): Promise<TemplateResult | null> {
  const fileName = name.replace(/[\\/:*?"<>|]+/g, '_') + (format === 'csv' ? '.csv' : '.xlsx')
  if (!inWails) return mock.exportTemplate(target, fileName, format)
  const path = await call<string>('PickTemplateSavePath', fileName, format)
  if (!path) return null
  return 'macroId' in target ? call<TemplateResult>('ExportTemplate', target.macroId, path, lang) : call<TemplateResult>('ExportDraftTemplate', target.macro, path, lang)
}
