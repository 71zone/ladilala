// Small helpers about the fields a macro fills. Pure functions, no UI.
import type { Step } from '@/bridge/macros-types'

/** Lowercase, no diacritics, no spaces, dashes or underscores: "Số điện thoại" becomes "sodienthoai". */
export function normalize(s: string): string {
  return fold(s).replace(/[^a-z0-9]+/g, '')
}
function fold(s: string): string {
  return s.normalize('NFD').replace(/\p{M}+/gu, '').replace(/đ/g, 'd').replace(/Đ/g, 'D').toLowerCase()
}
/** True when the hints describe a password field. */
export function isProtectedField(hints: string[]): boolean {
  return hints.some(h => /pass(word|wd)?\b|matkhau/.test(normalize(h)) || /^pass(word|wd)?$/.test(normalize(h)))
}

const quoted = /\[(?:name|placeholder|aria-label|id)\s*=\s*["']?([^"'\]]+)["']?\]/g
/** The words a person or the page uses for the field of a step: label, role name, text, #id, [name=...]. */
export function hintsOfStep(s: Step): string[] {
  const out: string[] = []
  for (const l of s.target?.locators ?? []) {
    if (l.kind === 'label' || l.kind === 'text' || l.kind === 'testid') out.push(l.value)
    else if (l.kind === 'role') out.push(l.name ?? '')
    else if (l.kind === 'css') {
      for (const m of l.value.matchAll(quoted)) out.push(m[1])
      const id = /#([\w-]+)/.exec(l.value)
      if (id) out.push(id[1])
    }
  }
  return [...new Set(out.map(h => h.trim()).filter(Boolean))]
}

const PASSWORD_VAR = /\{\{\s*password\b/
/** A fill that holds a password: the placeholder the recorder writes, or hints that say so. */
export function isPasswordStep(s: Step): boolean {
  return PASSWORD_VAR.test(s.value?.expr ?? '') || isProtectedField(hintsOfStep(s))
}

/** The macro variable name for a column: the column itself when it is a valid name, else its ASCII form. Same rule as the recorder in Go. */
export function varNameOf(column: string): string {
  if (/^[A-Za-z_][A-Za-z0-9_]*$/.test(column)) return column
  const s = column.normalize('NFD').replace(/\p{M}+/gu, '').replace(/đ/g, 'd').replace(/Đ/g, 'D').replace(/[^A-Za-z0-9]+/g, '_').replace(/^_+|_+$/g, '')
  if (!s) return 'column'
  return /^[0-9]/.test(s) ? 'c_' + s : s
}
