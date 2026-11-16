// Typed bridge to the Go RunService (app/run_service.go, docs/api-contract.md).
// In the Wails window each function calls main.RunService.<Method>. In a plain
// browser (bun run proto) it answers from runs-mock.ts, so the prototype keeps working.
import { callService, inWails, onEvent } from './api'
import * as mock from './runs-mock'

export type RunState = 'created' | 'running' | 'paused' | 'takeover' | 'stopping' | 'finished' | 'failed' | 'crashed_recovered'
export type RowState = 'pending' | 'running' | 'done' | 'failed' | 'skipped' | 'uncertain'
export type CommitState = 'none' | 'submitting' | 'submitted'
export const ROW_STATES: RowState[] = ['pending', 'running', 'done', 'failed', 'skipped', 'uncertain']

export interface RunOptions { concurrency: number; delayMs: number; stopAfterErrors: number; maxRestarts: number; dryRun: boolean; testOne: boolean }
export interface CreateRunRequest { macroId: string; sourceId: string; from: number; to: number; profileIds: string[]; options: RunOptions }
export interface Run {
  id: string; macroId: string; macroName: string; macroVersion: number; macroHash: string; sourceId: string
  state: RunState; reason: string; options: RunOptions; profileIds: string[]; rowFrom: number; rowTo: number; total: number
  counts: Partial<Record<RowState, number>>; createdAt: string; startedAt?: string; finishedAt?: string
}
export interface LedgerRow {
  runId: string; index: number; idempotencyKey: string; state: RowState; commitState: CommitState; profileId: string; attempts: number
  data: Record<string, string>; errorKind: string; errorMsg: string; stepId: string; startedAt?: string; finishedAt?: string
}
export interface RowFilter { states?: RowState[]; indexes?: number[]; offset?: number; limit?: number }
export interface RowPage { rows: LedgerRow[]; total: number }
export interface RunFilter { states?: RunState[]; offset?: number; limit?: number }
export interface Recovery { runId: string; backToQueue: number; uncertain: number }

export interface ProgressEvent { runId: string; state: RunState; total: number; counts: Partial<Record<RowState, number>>; resync?: boolean }
export interface StateEvent { runId: string; state: RunState; reason: string; at: number }
export interface SessionEvent { runId: string; profileId: string; message: string; at: number }
export interface UncertainEvent { runId: string; rowIndex: number; profileId: string; message: string; at: number }
export interface LogLine { at: number; kind: string; rowIndex: number; profileId: string; message: string }
export interface LogEvent { runId: string; lines: LogLine[]; skipped?: number }

const svc = 'RunService'
const call = <T>(method: string, ...args: unknown[]) => callService<T>(svc, method, ...args)

export const createRun = (req: CreateRunRequest): Promise<Run> => (inWails ? call('CreateRun', req) : mock.createRun(req))
export const startRun = (id: string): Promise<void> => (inWails ? call('StartRun', id) : mock.startRun(id))
export const pauseRun = (id: string): Promise<void> => (inWails ? call('PauseRun', id) : mock.pauseRun(id))
export const resumeRun = (id: string): Promise<void> => (inWails ? call('ResumeRun', id) : mock.resumeRun(id))
export const stopRun = (id: string): Promise<void> => (inWails ? call('StopRun', id) : mock.stopRun(id))
export const takeover = (id: string): Promise<void> => (inWails ? call('Takeover', id) : mock.takeover(id))
export const releaseTakeover = (id: string): Promise<void> => (inWails ? call('ReleaseTakeover', id) : mock.releaseTakeover(id))
export const retryRows = (id: string, f: RowFilter = {}): Promise<number> => (inWails ? call('RetryRows', id, f) : mock.retryRows(id, f))
export const markUncertainRow = (id: string, index: number, res: 'done' | 'failed'): Promise<void> => (inWails ? call('MarkUncertainRow', id, index, res) : mock.markUncertainRow(id, index, res))
export const listRuns = (f: RunFilter = {}): Promise<Run[]> => (inWails ? call('ListRuns', f) : mock.listRuns(f))
export const getRun = (id: string): Promise<Run> => (inWails ? call('GetRun', id) : mock.getRun(id))
export const listRows = (id: string, f: RowFilter = {}): Promise<RowPage> => (inWails ? call('ListRows', id, f) : mock.listRows(id, f))
export const watchRun = (id: string): Promise<void> => (inWails ? call('Watch', id) : Promise.resolve())
export const recovered = (): Promise<Recovery[]> => (inWails ? call('Recovered') : mock.recovered())
export const dismissRecovered = (): Promise<void> => (inWails ? call('DismissRecovered') : mock.dismissRecovered())
// exportLedger returns the saved path, or "" when the user cancels the save dialog.
export const exportLedger = (id: string): Promise<string> => (inWails ? call('ExportLedger', id, '') : mock.exportLedger(id))

function sub<T>(name: string, cb: (e: T) => void): () => void {
  return inWails ? onEvent<T>(name, cb) : mock.bus.on(name, cb as (e: unknown) => void)
}
export const onRunProgress = (cb: (e: ProgressEvent) => void) => sub('run.progress', cb)
export const onRunState = (cb: (e: StateEvent) => void) => sub('run.state', cb)
export const onRunSession = (cb: (e: SessionEvent) => void) => sub('run.session', cb)
export const onRunLog = (cb: (e: LogEvent) => void) => sub('run.log', cb)
export const onRunUncertain = (cb: (e: UncertainEvent) => void) => sub('run.uncertain', cb)
export const onRunRecovered = (cb: (e: Recovery[]) => void) => sub('run.recovered', cb)

// Source rows can hold passwords. The ledger shows them masked.
const SECRET = /pass|pwd|secret|token|card|cvv|cvc|otp|pin/i
export const maskData = (d: Record<string, string>): [string, string][] => Object.entries(d).map(([k, v]) => [k, SECRET.test(k) && v ? '\u2022\u2022\u2022\u2022\u2022\u2022' : v])
