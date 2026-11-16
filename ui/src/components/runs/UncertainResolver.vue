<script setup lang="ts">
import { Check, RefreshCw, Info, X } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import Group from '@/components/mac/Group.vue'
import { ref, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { notify } from '@/store'
import { listRows, markUncertainRow, retryRows, maskData, type LedgerRow } from '@/bridge/runs'
import { errText } from './util'

// Uncertain rows passed their commit point and did not end clean. The engine
// never retries them: the user checks the site and decides here.
const props = defineProps<{ runId: string; stamp: number; profiles: Record<string, string> }>()
const emit = defineEmits<{ changed: [] }>()
const { t, locale } = useI18n()
const rows = ref<LedgerRow[]>([])
const total = ref(0)
const busy = ref<number | null>(null)
const error = ref('')

async function load() {
  try {
    const p = await listRows(props.runId, { states: ['uncertain'], limit: 50 })
    rows.value = p.rows
    total.value = p.total
  } catch (e) {
    error.value = errText(e)
  }
}
watch(() => [props.runId, props.stamp], load)
onMounted(load)

async function resolve(r: LedgerRow, how: 'submitted' | 'retry' | 'failed') {
  busy.value = r.index
  error.value = ''
  try {
    await markUncertainRow(props.runId, r.index, how === 'submitted' ? 'done' : 'failed')
    if (how === 'retry') await retryRows(props.runId, { indexes: [r.index], states: ['failed'] })
    notify(t('runs.resolvedToast', { row: r.index + 1, action: t('runs.resolve.' + how) }))
    await load()
    emit('changed')
  } catch (e) {
    error.value = errText(e)
  } finally {
    busy.value = null
  }
}
const time = (r: LedgerRow) => (r.finishedAt ? new Date(r.finishedAt).toLocaleTimeString(locale.value === 'vi' ? 'vi-VN' : 'en-US', { hour12: false }) : '')
const preview = (r: LedgerRow) => maskData(r.data).slice(0, 3).map(([k, v]) => k + ': ' + v).join(' \u00b7 ')
</script>
<template>
  <section>
    <div class="mb-1.5 flex items-center gap-1.5 px-1"><h3 class="text-xs font-medium text-muted-foreground">{{ $t('runs.uncertainQueue') }}</h3><Badge variant="outline" class="h-4 px-1.5 text-warning tabular-nums">{{ total }}</Badge>
      <Popover><PopoverTrigger as-child><Button variant="ghost" size="icon-xs" :aria-label="$t('runs.whatIsThis')"><Info /></Button></PopoverTrigger><PopoverContent class="w-80 space-y-1.5 text-xs"><p>{{ $t('runs.uncertainHelp') }}</p><p>{{ $t('runs.uncertainHelp2') }}</p></PopoverContent></Popover></div>
    <p v-if="error" class="mb-1.5 px-1 text-xs text-destructive" role="alert">{{ error }}</p>
    <Group :help="rows.length ? '' : $t('runs.nothingUncertain')">
      <div v-for="u in rows" :key="u.index" class="px-3 py-2">
        <div class="flex items-center gap-2"><b class="font-medium tabular-nums">{{ $t('runs.rowN', { n: u.index + 1 }) }}</b><span class="truncate text-muted-foreground">{{ profiles[u.profileId] ?? u.profileId }}</span><span class="flex-1" /><span class="text-xs text-muted-foreground tabular-nums">{{ time(u) }}</span></div>
        <p class="mt-0.5 text-xs text-muted-foreground">{{ $t('runs.whyUncertain', { step: u.stepId || '?' }) }} {{ u.errorMsg }}</p>
        <p class="mt-0.5 mb-1.5 truncate text-xs text-muted-foreground" :title="preview(u)">{{ preview(u) }}</p>
        <div class="flex flex-wrap gap-1.5">
          <Button size="sm" variant="outline" class="text-success" :disabled="busy === u.index" @click="resolve(u, 'submitted')"><Check class="size-3" />{{ $t('runs.markSubmitted') }}</Button>
          <Button size="sm" variant="outline" :disabled="busy === u.index" @click="resolve(u, 'retry')"><RefreshCw class="size-3" />{{ $t('runs.retryBtn') }}</Button>
          <Button size="sm" variant="outline" :disabled="busy === u.index" @click="resolve(u, 'failed')"><X class="size-3" />{{ $t('runs.markFailed') }}</Button>
        </div>
      </div>
      <p v-if="total > rows.length" class="px-3 py-2 text-xs text-muted-foreground">{{ $t('runs.uncertainMore', { n: total - rows.length }) }}</p>
    </Group>
  </section>
</template>
