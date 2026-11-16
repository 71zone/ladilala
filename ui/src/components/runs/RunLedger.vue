<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useElementSize, useDebounceFn } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import Segmented from '@/components/mac/Segmented.vue'
import { fmtNum } from '@/format'
import { listRows, type LedgerRow, type RowState } from '@/bridge/runs'
import { rowTone } from './util'

// The per-row ledger. Only the rows in view are fetched, 100 at a time, so a
// run of 10 000 rows stays light. `stamp` changes when the run made progress.
const props = defineProps<{ runId: string; stamp: number; counts: Partial<Record<RowState, number>>; profiles: Record<string, string> }>()
const { t, locale } = useI18n()

const PAGE = 100
const ROW_H = 32
const OVERSCAN = 6
type Filter = 'all' | RowState
const filter = ref<Filter>('all')
const total = ref(0)
const cache = ref<Record<number, LedgerRow[]>>({})
const failed = ref(false)
const scroller = ref<HTMLElement>()
const scrollTop = ref(0)
const { height: viewH } = useElementSize(scroller)
let gen = 0
const loading = new Set<number>()

const options = computed<[string, string][]>(() => [['all', t('runs.ledger.all', { n: fmtNum(Object.values(props.counts).reduce((a, b) => a + (b ?? 0), 0)) })], ...(['failed', 'uncertain', 'skipped', 'pending', 'done'] as RowState[]).map(s => [s, t('runs.rowState.' + s) + ' ' + fmtNum(props.counts[s] ?? 0)] as [string, string])])

const first = computed(() => Math.max(0, Math.floor(scrollTop.value / ROW_H) - OVERSCAN))
const last = computed(() => Math.min(total.value - 1, Math.ceil((scrollTop.value + viewH.value) / ROW_H) + OVERSCAN))
const visible = computed(() => {
  const out: { i: number; row?: LedgerRow }[] = []
  for (let i = first.value; i <= last.value; i++) out.push({ i, row: cache.value[Math.floor(i / PAGE)]?.[i % PAGE] })
  return out
})

async function load(page: number, g: number) {
  if (loading.has(page)) return
  loading.add(page)
  try {
    const r = await listRows(props.runId, { states: filter.value === 'all' ? undefined : [filter.value], offset: page * PAGE, limit: PAGE })
    if (g !== gen) return
    total.value = r.total
    cache.value = { ...cache.value, [page]: r.rows }
    failed.value = false
  } catch {
    if (g === gen) failed.value = true
  } finally {
    loading.delete(page)
  }
}
function ensure() {
  for (let p = Math.floor(first.value / PAGE); p <= Math.floor(Math.max(last.value, first.value) / PAGE); p++) if (!cache.value[p]) void load(p, gen)
}
function reset() {
  gen++
  loading.clear()
  cache.value = {}
  total.value = 0
  scrollTop.value = 0
  if (scroller.value) scroller.value.scrollTop = 0
  void load(0, gen)
}
// A refresh keeps the old rows on screen and swaps each page when its new data arrives.
const refresh = useDebounceFn(() => {
  const g = ++gen
  loading.clear()
  const pages = new Set<number>([0])
  for (let p = Math.floor(first.value / PAGE); p <= Math.floor(Math.max(last.value, first.value) / PAGE); p++) pages.add(p)
  const keep = cache.value
  cache.value = Object.fromEntries([...pages].filter(p => keep[p]).map(p => [p, keep[p]]))
  pages.forEach(p => void load(p, g))
}, 800, { maxWait: 1500 })

watch([filter, () => props.runId], reset)
watch(() => props.stamp, () => refresh())
watch([first, last, viewH], ensure)
onMounted(reset)
onUnmounted(() => { gen++ })

const time = (r: LedgerRow) => {
  const v = r.finishedAt ?? r.startedAt
  return v ? new Date(v).toLocaleTimeString(locale.value === 'vi' ? 'vi-VN' : 'en-US', { hour12: false }) : ''
}
const errLabel = (r: LedgerRow) => (r.errorKind ? t('runs.errKind.' + r.errorKind) : '')
const COLS = 'grid-cols-[52px_92px_minmax(110px,1fr)_100px_minmax(160px,2fr)_76px]'
</script>
<template>
  <section>
    <div class="mb-1.5 flex flex-wrap items-center gap-2 px-1"><h3 class="text-xs font-medium text-muted-foreground">{{ $t('runs.ledger.title') }}</h3><span class="flex-1" /><Segmented v-model="filter" :options="options" /></div>
    <div class="overflow-hidden rounded-xl bg-card ring-1 ring-border">
      <div :class="['grid gap-2 border-b px-3 py-1.5 text-xs font-medium text-muted-foreground', COLS]" role="row">
        <span>{{ $t('runs.colRow') }}</span><span>{{ $t('runs.colState') }}</span><span>{{ $t('runs.colProfile') }}</span><span>{{ $t('runs.colStep') }}</span><span>{{ $t('runs.ledger.colError') }}</span><span class="text-right">{{ $t('runs.ledger.colTime') }}</span>
      </div>
      <div ref="scroller" class="relative h-72 overflow-auto" role="rowgroup" @scroll.passive="scrollTop = ($event.target as HTMLElement).scrollTop">
        <p v-if="failed" class="p-4 text-xs text-destructive" role="alert">{{ $t('runs.ledger.loadFailed') }}</p>
        <p v-else-if="total === 0 && !loading.size" class="p-4 text-xs text-muted-foreground">{{ $t('runs.ledger.empty') }}</p>
        <div :style="{ height: total * ROW_H + 'px' }" class="relative">
          <div v-for="v in visible" :key="v.i" :class="['absolute inset-x-0 grid items-center gap-2 border-b px-3 text-xs hover:bg-muted/50', COLS]" :style="{ top: v.i * ROW_H + 'px', height: ROW_H + 'px' }" role="row">
            <template v-if="v.row">
              <span class="font-mono tabular-nums">{{ v.row.index + 1 }}</span>
              <span :class="['truncate font-medium', rowTone(v.row.state)]">{{ $t('runs.rowState.' + v.row.state) }}</span>
              <span class="truncate" :title="profiles[v.row.profileId] ?? v.row.profileId">{{ profiles[v.row.profileId] ?? v.row.profileId }}</span>
              <span class="truncate font-mono" :title="v.row.stepId">{{ v.row.stepId }}</span>
              <span class="truncate text-muted-foreground" :title="errLabel(v.row) + ' ' + v.row.errorMsg"><template v-if="v.row.errorKind"><b class="font-medium text-foreground">{{ errLabel(v.row) }}</b> {{ v.row.errorMsg }}</template></span>
              <span class="text-right text-muted-foreground tabular-nums">{{ time(v.row) }}</span>
            </template>
            <span v-else class="col-span-6 h-3 w-1/2 animate-pulse rounded bg-muted motion-reduce:animate-none" />
          </div>
        </div>
      </div>
    </div>
  </section>
</template>
