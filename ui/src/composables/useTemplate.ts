import { useI18n } from 'vue-i18n'
import { downloadTemplate, type TemplateFormat } from '@/bridge/data'
import type { Macro } from '@/bridge/macros'
import { errorText } from '@/macros'
import { notify } from '@/store'
import { useLocale } from '@/composables/useLocale'

// Download an empty Excel or CSV file with the macro's data columns. A saved macro is read by id;
// a draft (the review after a recording) is sent whole.
export function useTemplate() {
  const { t } = useI18n()
  const { locale } = useLocale()
  return async (target: { macroId: string } | { macro: Macro }, name: string, format: TemplateFormat) => {
    try {
      const r = await downloadTemplate(target, name || 'macro', format, locale.value)
      if (r) notify(t('ux.tpl.saved', { path: r.path, n: r.columns }, r.columns))
    } catch (e) { notify(errorText(e)) }
  }
}
