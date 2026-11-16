import { i18n } from '@/i18n'
import { ServiceError } from '@/bridge/api'
import type { RowState, RunState } from '@/bridge/runs'

// Text for an error from a service call. The code picks the message; details fill it.
export function errText(e: unknown): string {
  const { t, te } = i18n.global
  if (e instanceof ServiceError && te('errors.' + e.code)) return t('errors.' + e.code, e.details)
  return t('runs.errUnknown')
}

// StateDot tone of a run state.
export const runTone = (s: RunState) => (s === 'running' ? 'running' : s === 'failed' ? 'error' : s === 'finished' || s === 'created' ? 'idle' : 'warn')
// Text colour class of a ledger row state.
export const rowTone = (s: RowState) => (s === 'done' ? 'text-success' : s === 'failed' ? 'text-destructive' : s === 'uncertain' ? 'text-warning' : s === 'running' ? 'text-foreground' : 'text-muted-foreground')
// A run that can still move: the controls act on it.
export const isActive = (s: RunState) => s === 'running' || s === 'paused' || s === 'takeover' || s === 'stopping'
