import { computed } from 'vue'
import { i18n, setLocale, LOCALES, type Locale } from '@/i18n'

// Reactive locale switch for components. Persists to localStorage and updates <html lang>.
export function useLocale() {
  const locale = computed<Locale>({ get: () => i18n.global.locale.value as Locale, set: setLocale })
  const toggle = () => setLocale(locale.value === 'vi' ? 'en' : 'vi')
  return { locale, locales: LOCALES, setLocale, toggle }
}
