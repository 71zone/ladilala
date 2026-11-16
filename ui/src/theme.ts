import { watch } from 'vue'
import { useDark, usePreferredDark, useStorage, useToggle } from '@vueuse/core'
import { settingsApi, type Theme } from './bridge/settings'
import { inWails } from './bridge/api'
import { i18n, setLocale, type Locale } from './i18n'

// shadcn-vue dark mode: a "dark" class on <html>. `theme` is the user's choice
// (system, light or dark). With "system" the window follows the OS. The sidebar
// toggle still works: it sets an explicit light or dark choice.
export const isDark = useDark({ selector: 'html', valueDark: 'dark', valueLight: 'light', initialValue: 'auto' })
export const toggleDark = useToggle(isDark)
export const theme = useStorage<Theme>('ladilala.theme', 'system')
const prefersDark = usePreferredDark()
const wantDark = () => (theme.value === 'system' ? prefersDark.value : theme.value === 'dark')
watch([theme, prefersDark], () => { isDark.value = wantDark() }, { immediate: true })
watch(isDark, (v) => { if (v !== wantDark()) theme.value = v ? 'dark' : 'light' })

// The Go settings file is the saved copy. Keep it in step with the window.
let synced = !inWails
watch(theme, (v) => { if (synced) void settingsApi.setTheme(v).catch(() => {}) })
watch(() => i18n.global.locale.value, (l) => { if (synced) void settingsApi.setLocale(l as Locale).catch(() => {}) })
if (inWails) {
  settingsApi.get().then((s) => { theme.value = s.theme; if (s.language !== i18n.global.locale.value) setLocale(s.language) }).catch(() => {}).finally(() => { synced = true })
}
