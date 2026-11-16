import { i18n } from './i18n'

// Intl wrappers. Every call reads the current locale, so anything rendered from a template or a lazy message re-renders on language switch.
const tag = () => (i18n.global.locale.value === 'vi' ? 'vi-VN' : 'en-US')
export const fmtNum = (n: number, o?: Intl.NumberFormatOptions) => new Intl.NumberFormat(tag(), o).format(n)
const unit = (n: number, u: string, o: Intl.NumberFormatOptions = {}) => fmtNum(n, { style: 'unit', unit: u, unitDisplay: 'short', ...o })
export const fmtPct = (n: number) => fmtNum(n / 100, { style: 'percent', maximumFractionDigits: 0 })
export const fmtMs = (n: number) => fmtNum(n) + ' ms'
export const fmtSec = (n: number) => unit(n, 'second')
export const fmtSpeed = (mbps: number) => unit(mbps, 'megabyte-per-second', { maximumFractionDigits: 1 })
export const fmtSize = (mb: number) => (mb >= 1024 ? unit(mb / 1024, 'gigabyte', { maximumFractionDigits: 1 }) : unit(mb, 'megabyte', { maximumFractionDigits: mb < 10 ? 1 : 0 }))
export const fmtDuration = (min: number) => { const h = Math.floor(min / 60), m = Math.round(min % 60); return [h ? unit(h, 'hour') : '', m || !h ? unit(m, 'minute') : ''].filter(Boolean).join(' ') }
export const fmtRel = (n: number, u: Intl.RelativeTimeFormatUnit) => new Intl.RelativeTimeFormat(tag(), { numeric: 'auto' }).format(n, u)
export const fmtDate = (iso: string) => new Intl.DateTimeFormat(tag(), { dateStyle: 'medium' }).format(new Date(iso))
export const fmtClock = (ms: number) => new Intl.DateTimeFormat(tag(), { hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(ms))
export const countryName = (cc: string) => new Intl.DisplayNames([tag()], { type: 'region' }).of(cc) ?? cc
export const cap = (s: string) => s.charAt(0).toLocaleUpperCase(tag()) + s.slice(1)
