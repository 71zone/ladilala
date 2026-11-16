import { t } from './i18n'
// A message resolved at render time, so text stored in reactive state follows the language switch.
export type Msg = () => string
export const lz = (key: string, params: Record<string, unknown> = {}): Msg => () => t(key, params)
export const lzn = (key: string, n: number, params: Record<string, unknown> = {}): Msg => () => t(key, { ...params, n }, n)
