// Types of the data bridge (mirror the Go DTOs in app/data_service.go).

export type SourceKind = 'csv' | 'excel' | 'json' | 'rest'
export type AuthKind = 'none' | 'bearer' | 'basic'
export type PagingKind = '' | 'page' | 'offset' | 'link'

/** A saved source. It never carries a secret. columns and rowCount are filled by getSource; rowCount -1 means unknown. */
export interface SourceDTO {
  id: string; name: string; kind: SourceKind; path: string; encoding: string; delimiter: string; sheet: string; nested: string
  url: string; rowsPath: string; authKind: AuthKind; basicUser: string; hasAuth: boolean; headerNames: string[]; paging: PagingKind; pageSize: number
  columns: string[]; rowCount: number; createdAt: string; updatedAt: string
}
export interface FileSourceInput { name: string; path: string; encoding: string; delimiter: string; sheet: string; nested: string }
export interface RESTSourceInput {
  name: string; url: string; headers: Record<string, string>; authKind: AuthKind; bearerToken: string; basicUser: string; basicPassword: string
  rowsPath: string; paging: PagingKind; pageParam: string; sizeParam: string; offsetParam: string; pageSize: number; startPage: number; nextPath: string; allowHttp: boolean
}
export interface RowIssue { rowIndex: number; level: 'error' | 'warn'; code: string; params?: Record<string, unknown> }
export interface SourcePreview {
  kind: SourceKind; encoding: string; delimiter: string; sheet: string; sheets: string[]; columns: string[]; rows: string[][]
  from: number; total: number; duplicates: string[]; issues: RowIssue[]
}
export interface MappingVariable { name: string; type: string; required: boolean; column: string; suggestion: string }
/** Variables of a macro matched to the columns of a source. Run setup reads missing, extra and duplicates. */
export interface MappingDTO {
  macroId: string; sourceId: string; variables: MappingVariable[]; columns: string[]; missing: string[]; extra: string[]; duplicates: string[]
  overrides: Record<string, string>; ok: boolean
}
export interface ValidationResult { checked: number; issues: RowIssue[]; truncated: boolean }

export const emptyFileInput = (path = ''): FileSourceInput => ({ name: '', path, encoding: '', delimiter: '', sheet: '', nested: '' })
export const emptyRestInput = (): RESTSourceInput => ({
  name: '', url: '', headers: {}, authKind: 'none', bearerToken: '', basicUser: '', basicPassword: '', rowsPath: '', paging: '', pageParam: '', sizeParam: '', offsetParam: '', pageSize: 0, startPage: 0, nextPath: '', allowHttp: false,
})
