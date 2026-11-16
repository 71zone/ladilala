// Types and pure helpers of the macro bridge (mirror the Go DTOs in app/macro_service.go).

// ---- types (mirror the Go DTOs) -------------------------------------------------

export type StepAction = 'navigate' | 'click' | 'dblclick' | 'fill' | 'select' | 'press' | 'check' | 'uploadFile' | 'wait' | 'assert' | 'scroll' | 'manual'
export type LocatorKind = 'role' | 'label' | 'text' | 'css' | 'xpath' | 'testid'
export interface Locator { kind: LocatorKind; value: string; name?: string }
export interface Target { locators: Locator[]; framePath?: string[]; shadowPath?: string[] }
export interface ValueExpr { expr: string; transforms?: string[] }
export type ExpectKind = 'urlMatches' | 'elementPresent' | 'elementAbsent' | 'textContains' | 'textEquals' | 'valueEquals'
export interface Expectation { kind: ExpectKind; target?: Target; value?: ValueExpr }
export type OnFail = 'stop' | 'skip' | 'pause'
export interface Step {
  id: string; action: StepAction; target?: Target; value?: ValueExpr; commitPoint?: boolean
  expect?: Expectation[]; timeout?: string; retries?: number; onFail?: OnFail; disabled?: boolean
}
export type VarType = 'string' | 'number' | 'bool' | 'date'
export type VarKind = 'text' | 'email' | 'phone' | 'number' | 'url' | 'date' | 'password'
export interface VariableDecl { name: string; type: VarType; required?: boolean; kind?: VarKind; default?: string | number | boolean }
export interface Guard { kind: string; pattern?: string; locator?: Locator; action: string }
export interface Macro { schemaVersion: 1; id: string; name: string; allowedOrigins: string[]; variables?: VariableDecl[]; steps: Step[]; guards?: Guard[] }

export interface Problem { path: string; msg: string }
export interface RecordWarning { kind: 'canvas_unsupported' | 'file_input_unsupported'; stepIndex: number; locator?: Locator; message?: string }

/** One row of the Macros list. Run setup reads id, name and variables. */
export interface MacroSummary {
  id: string; name: string; description: string; stepCount: number; version: number; hash: string
  variables: string[]; sourceId: string; warningCount: number; updatedAt: string
}
export interface MacroDTO {
  macro: Macro; version: number; hash: string; description: string; sourceId: string
  warnings: RecordWarning[]; createdAt: string; updatedAt: string
}
export interface SaveMacroInput { macro: Macro; description: string; warnings?: RecordWarning[] }
export interface ImportReview {
  reviewId: string; macro: Macro | null; allowedOrigins: string[]; commitPointSteps: string[]
  variables: string[]; problems: Problem[]; hash: string; stepCount: number
}
export interface StepPreview { stepId: string; action: StepAction; value: string; error?: 'missingColumn' | 'invalid'; column?: string }
/** A data column the user made by typing {{name}} in a page field. steps is how many steps read it. */
export interface RecordColumn { name: string; kind: VarKind; protected: boolean; steps: number }
export interface RecordingDraft { macro: Macro; warnings: RecordWarning[]; problems: Problem[]; columns: RecordColumn[] }
export type RecordState = 'starting' | 'recording' | 'paused' | 'stopped' | 'closed'
export interface RecordStatus { recordId: string; state: RecordState; stepCount: number; warningCount: number }
/** lang is vi or en (the texts of the page overlay). hint shows the first-use hint card in the page. */
export interface StartRecordingInput { profileId: string; startUrl: string; name: string; lang?: string; hint?: boolean }
export interface RecordProfile { id: string; name: string; group: string; running: boolean }

export interface RecordStepEvent { recordId: string; stepIndex: number; step: Step }
/** The whole step list, sent when a column was renamed or deleted in the page overlay. */
export interface RecordStepsEvent { recordId: string; steps: Step[] }
/** The user closed the hint card in the page (never: with "never show again"). */
export interface RecordHintEvent { recordId: string; closed: boolean; never: boolean }
export interface RecordWarningEvent extends RecordWarning { recordId: string }
export interface RecordStateEvent { recordId: string; state: RecordState }

// ---- helpers shared by the screens ---------------------------------------------

const REF = /(?<!\\)\{\{\s*([A-Za-z_][A-Za-z0-9_]*)/g
/** The placeholder form of a variable name, for display. */
export const varText = (name: string) => '{{' + name + '}}'
/** Variable names a value expression reads. */
export function refsOf(expr: string): string[] {
  return [...new Set([...expr.matchAll(REF)].map(m => m[1]))]
}
/** The user-facing text of a locator: the name or text the person sees, not a selector. */
export function locatorLabel(l: Locator): string {
  if (l.kind === 'role') return l.name || l.value
  if (l.kind === 'css') return prettyCss(l.value)
  return l.value
}
function prettyCss(css: string): string {
  const id = /^#([\w-]+)$/.exec(css) ?? /\[name=["']?([\w-]+)["']?\]/.exec(css)
  return id ? id[1].replace(/[_-]+/g, ' ') : css
}
/** Friendly label of a step target: first locator that is not a raw selector wins. */
export function targetLabel(s: Step): string {
  if (s.action === 'navigate') return s.value?.expr ?? ''
  const ls = s.target?.locators ?? []
  const nice = ls.find(l => l.kind === 'label' || l.kind === 'role' || l.kind === 'text')
  return ls.length ? locatorLabel(nice ?? ls[0]) : ''
}
export function newStepId(m: Macro, action: string): string {
  const used = new Set(m.steps.map(s => s.id))
  for (let n = m.steps.length + 1; ; n++) {
    const id = `s${String(n).padStart(3, '0')}-${action}`
    if (!used.has(id)) return id
  }
}
export function originOf(url: string): string | null {
  try { const u = new URL(url); return u.protocol === 'http:' || u.protocol === 'https:' ? u.origin : null } catch { return null }
}
export function emptyMacro(): Macro { return { schemaVersion: 1, id: '', name: '', allowedOrigins: [], variables: [], steps: [] } }
export const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v))
