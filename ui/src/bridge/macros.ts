// Typed bridge to the Go MacroService (app/macro_service.go, app/recording_service.go,
// docs/api-contract.md). In the Wails window each function calls main.MacroService.<Method>.
// In a plain browser (bun run proto) it answers from an in-memory mock, so the prototype keeps working.
// Other screens import listMacros from here.
import { callService, inWails, onEvent } from './api'
import * as mock from './macros-mock'
import { store } from '../store'
import type { ImportReview, Macro, MacroDTO, MacroSummary, Problem, RecordProfile, RecordStateEvent, RecordHintEvent, RecordStatus, RecordStepEvent, RecordStepsEvent, RecordWarningEvent, RecordingDraft, SaveMacroInput, StartRecordingInput, StepPreview } from './macros-types'

export * from './macros-types'

// ---- service calls ---------------------------------------------------------------

const svc = 'MacroService'
const call = <T>(method: string, ...args: unknown[]) => callService<T>(svc, method, ...args)

export const listMacros = (): Promise<MacroSummary[]> => (inWails ? call('ListMacros') : mock.listMacros())
export const getMacro = (id: string): Promise<MacroDTO> => (inWails ? call('GetMacro', id) : mock.getMacro(id))
export const saveMacro = (input: SaveMacroInput): Promise<MacroDTO> => (inWails ? call('SaveMacro', input) : mock.saveMacro(input))
export const validateMacro = (m: Macro): Promise<Problem[]> => (inWails ? call('ValidateMacro', m) : mock.validateMacro(m))
export const duplicateMacro = (id: string, name: string): Promise<MacroDTO> => (inWails ? call('DuplicateMacro', id, name) : mock.duplicateMacro(id, name))
export const deleteMacro = (id: string): Promise<void> => (inWails ? call('DeleteMacro', id) : mock.deleteMacro(id))
export const importMacro = (content: string): Promise<ImportReview> => (inWails ? call('ImportMacro', content) : mock.importMacro(content))
export const confirmImport = (reviewId: string): Promise<MacroDTO> => (inWails ? call('ConfirmImport', reviewId) : mock.confirmImport(reviewId))
export const mapVariable = (macroId: string, stepId: string, column: string): Promise<MacroDTO> => (inWails ? call('MapVariable', macroId, stepId, column) : mock.mapVariable(macroId, stepId, column))
export const setMacroSource = (macroId: string, sourceId: string): Promise<void> => (inWails ? call('SetMacroSource', macroId, sourceId) : mock.setMacroSource(macroId, sourceId))
export const previewRow = (macroId: string, sourceId: string, rowIndex: number): Promise<StepPreview[]> => (inWails ? call('PreviewRow', macroId, sourceId, rowIndex) : mock.previewRow(macroId, sourceId, rowIndex))

/** Export: Wails asks for a path and writes the file; the browser downloads it. Resolves false when the user cancels. */
export async function exportMacro(id: string, fileName: string): Promise<boolean> {
  if (!inWails) return mock.exportMacro(id, fileName)
  const path = await call<string>('PickMacroSavePath', fileName)
  if (!path) return false
  await call('ExportMacro', id, path)
  return true
}

export const startRecording = (input: StartRecordingInput): Promise<string> => (inWails ? call('StartRecording', input) : mock.startRecording(input))
export const pauseRecording = (id: string): Promise<void> => (inWails ? call('PauseRecording', id) : mock.pauseRecording(id))
export const resumeRecording = (id: string): Promise<void> => (inWails ? call('ResumeRecording', id) : mock.resumeRecording(id))
export const stopRecording = (id: string): Promise<RecordingDraft> => (inWails ? call('StopRecording', id) : mock.stopRecording(id))
export const discardRecording = (id: string): Promise<void> => (inWails ? call('DiscardRecording', id) : mock.discardRecording(id))
export const getRecordingStatus = (id: string): Promise<RecordStatus> => (inWails ? call('GetRecordingStatus', id) : mock.getRecordingStatus(id))

export const onRecordStep = (cb: (e: RecordStepEvent) => void) => (inWails ? onEvent('record.step', cb) : mock.bus.on('record.step', cb))
export const onRecordSteps = (cb: (e: RecordStepsEvent) => void) => (inWails ? onEvent('record.steps', cb) : mock.bus.on('record.steps', cb))
export const onRecordHint = (cb: (e: RecordHintEvent) => void) => (inWails ? onEvent('record.hint', cb) : mock.bus.on('record.hint', cb))
export const onRecordWarning = (cb: (e: RecordWarningEvent) => void) => (inWails ? onEvent('record.warning', cb) : mock.bus.on('record.warning', cb))
export const onRecordState = (cb: (e: RecordStateEvent) => void) => (inWails ? onEvent('record.state', cb) : mock.bus.on('record.state', cb))

/** Profiles the Record dialog can pick. The Go side is ProfileService; the screen needs only these fields. */
export async function listRecordProfiles(): Promise<RecordProfile[]> {
  if (inWails) {
    const list = await callService<{ id: string; name: string; group: string; state: string }[]>('ProfileService', 'ListProfiles', {})
    return list.map(p => ({ id: p.id, name: p.name, group: p.group, running: p.state === 'running' }))
  }
  return store.profiles.map(p => ({ id: 'p' + p.id, name: p.name, group: p.group, running: p.state === 'running' }))
}
