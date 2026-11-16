import { i18n, t } from './i18n'
import { ServiceError } from './bridge/api'

// Turns an error from the engine, settings or update services into text for a
// toast. Codes are translated through errors.<code> ("engine.network" is
// errors.engine.network). A conflict carries a reason ("runActive") that has
// its own text. Anything else falls back to errors.internal.
export function errorText(e: unknown): string {
  if (!(e instanceof ServiceError)) return t('errors.internal')
  const te = i18n.global.te
  if (e.code === 'invalid') {
    const probs = (e.details.problems as { path: string }[] | undefined)?.map((p) => p.path).join(', ')
    return probs ? t('errors.invalid') + ' (' + probs + ')' : t('errors.invalid')
  }
  const reason = typeof e.details.reason === 'string' ? e.details.reason : ''
  const keys = [reason && 'errors.engine.' + reason, reason && 'errors.update.' + reason, 'errors.' + e.code].filter(Boolean) as string[]
  const key = keys.find((k) => te(k))
  return key ? t(key, e.details as Record<string, unknown>) : t('errors.internal')
}
