// Helpers that tie macro variables to data columns, for the "Data column" screens.
// A variable binds to the column with the same name (case and edge spaces ignored, the Go rule in
// core/data/mapping.go) unless an override says otherwise.
import { refsOf, type Macro, type Step } from '@/bridge/macros-types'
import { varNameOf } from '@/lib/matchColumns'

export const PASSWORD_VAR = 'password'
const norm = (s: string) => s.trim().toLowerCase()

/** The column a variable reads: the override, else the same-named column, else ''. */
export function columnOfVar(v: string, columns: readonly string[], overrides: Record<string, string> = {}): string {
  const want = norm(overrides[v] ?? v)
  return columns.find(c => norm(c) === want) ?? ''
}

/** Points a fill or select step at a column: its value becomes {{variable}} and the variable is declared. Returns the variable name. */
export function applyColumn(m: Macro, stepId: string, column: string): string {
  const s = m.steps.find(x => x.id === stepId)
  if (!s) return ''
  const v = varNameOf(column)
  s.value = { expr: `{{${v}}}`, transforms: s.value?.transforms }
  m.variables ??= []
  if (!m.variables.some(d => d.name === v)) m.variables.push({ name: v, type: 'string', required: true })
  return v
}

/** Variable names the steps read, in values and in locators. */
export function usedVars(m: Macro): Set<string> {
  const out = new Set<string>()
  for (const s of m.steps) {
    for (const r of refsOf(s.value?.expr ?? '')) out.add(r)
    for (const l of s.target?.locators ?? []) for (const r of refsOf(l.value)) out.add(r)
  }
  return out
}

export interface DataCheck {
  /** Variables a step reads that no column feeds (the password variable is never listed: it is protected). */
  missing: string[]
  /** Columns no variable reads. */
  extra: string[]
  /** Variables that have a column. */
  bound: string[]
}
export function checkColumns(m: Macro, columns: readonly string[], overrides: Record<string, string> = {}): DataCheck {
  const used = [...usedVars(m)].filter(v => v !== PASSWORD_VAR)
  const bound = used.filter(v => columnOfVar(v, columns, overrides))
  const cols = new Set(bound.map(v => columnOfVar(v, columns, overrides)))
  return { missing: used.filter(v => !bound.includes(v)), extra: columns.filter(c => !cols.has(c)), bound }
}

/** What a step types for one row: its value with every {{variable}} replaced. null when the step types nothing. */
export function previewValue(s: Step, columns: readonly string[], row: readonly string[] | undefined, overrides: Record<string, string> = {}): { text: string; missing: string; protected: boolean } | null {
  const expr = s.value?.expr
  if (!expr || !refsOf(expr).length) return null
  let missing = ''
  let prot = false
  const text = expr.replace(/\{\{\s*([A-Za-z_][A-Za-z0-9_]*)[^}]*\}\}/g, (_, v: string) => {
    if (v === PASSWORD_VAR) { prot = true; return '' }
    const c = columnOfVar(v, columns, overrides)
    if (!c) { missing ||= v; return '' }
    return row?.[columns.indexOf(c)] ?? ''
  })
  return { text, missing, protected: prot }
}
