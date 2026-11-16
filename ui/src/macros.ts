import { i18n, t } from './i18n'
import { fmtRel, cap } from './format'
import { ServiceError } from './bridge/api'

// Mock text starting with "@" is a key under macroData.* and follows the language; any other text is user input and shown as typed.
export const txt = (v?: string) => (v && v.startsWith('@') ? t('macroData.' + v.slice(1)) : v || '')

/** The prototype list the Runs screen still reads. Run setup moves to listMacros in bridge/macros.ts, then this goes. */
export const macros = [
  { id: 1, name: 'Signup form' },
  { id: 2, name: 'Checkout flow' },
  { id: 3, name: 'Price check' },
  { id: 4, name: 'Newsletter unsub' },
]

const UNITS: [number, Intl.RelativeTimeFormatUnit][] = [[31557600, 'year'], [2629800, 'month'], [604800, 'week'], [86400, 'day'], [3600, 'hour'], [60, 'minute'], [1, 'second']]
/** "3 days ago" style text for an RFC 3339 time. */
export function relTime(iso: string): string {
  const s = (new Date(iso).getTime() - Date.now()) / 1000
  const [size, unit] = UNITS.find(([sz]) => Math.abs(s) >= sz) ?? UNITS[UNITS.length - 1]
  return cap(fmtRel(Math.trunc(s / size), unit))
}

/** The message for a service error. The core sends a code and details; the text lives in the locale files. */
export function errorText(e: unknown): string {
  if (!(e instanceof ServiceError)) return t('macros.errors.internal')
  const g = i18n.global
  const key = ['errors.' + e.code, 'macros.errors.' + e.code].find(k => g.te(k))
  if (e.code === 'invalid') {
    const first = (e.details.problems as { path: string; msg: string }[] | undefined)?.[0]
    return first ? t('macros.errors.invalidAt', { path: first.path, msg: first.msg }) : t('macros.errors.invalid')
  }
  return key ? t(key, e.details as Record<string, unknown>) : t('macros.errors.internal')
}
export const isCode = (e: unknown, code: string) => e instanceof ServiceError && e.code === code
