import { createI18n } from 'vue-i18n'
import vi from './locales/vi.json'
import en from './locales/en.json'

export type Locale = 'vi' | 'en'
export const LOCALES: Locale[] = ['vi', 'en']
export const STORAGE_KEY = 'ladilala.locale'

const initial = (): Locale => (localStorage.getItem(STORAGE_KEY) === 'en' ? 'en' : 'vi')

// vi is the default and the source of truth; en is the fallback for any missing key.
export const i18n = createI18n({ legacy: false, locale: initial(), fallbackLocale: 'en', messages: { vi, en } })
export const t = i18n.global.t
document.documentElement.lang = i18n.global.locale.value

export function setLocale(l: Locale) {
  i18n.global.locale.value = l
  localStorage.setItem(STORAGE_KEY, l)
  document.documentElement.lang = l
}
