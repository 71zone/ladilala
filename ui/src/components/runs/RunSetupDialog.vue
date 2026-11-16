<script setup lang="ts">
import { Play, TriangleAlert, Check } from '@lucide/vue'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Slider } from '@/components/ui/slider'
import { Switch } from '@/components/ui/switch'
import Group from '@/components/mac/Group.vue'
import Row from '@/components/mac/Row.vue'
import Pick from '@/components/mac/Pick.vue'
import { reactive, ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { fmtNum } from '@/format'
import { createRun, startRun, type Run } from '@/bridge/runs'
import { listMacros, listSources, listProfiles, getMapping, type MacroOption, type SourceOption, type ProfileOption, type MappingCheck } from '@/bridge/runs-deps'
import { errText } from './util'

const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ started: [run: Run] }>()
const { t } = useI18n()

const macros = ref<MacroOption[]>([])
const sources = ref<SourceOption[]>([])
const profiles = ref<ProfileOption[]>([])
const mapping = ref<MappingCheck | null>(null)
const loadError = ref('')
const busy = ref(false)
const startError = ref('')
const search = ref('')
const form = reactive({ macroId: '', sourceId: '', from: 1, to: '' as string | number, testOne: false, dryRun: false, concurrency: 1, delayMs: 0, stopAfterErrors: 5, picked: [] as string[] })

watch(open, async o => {
  if (!o) return
  startError.value = ''
  loadError.value = ''
  try {
    ;[macros.value, sources.value, profiles.value] = await Promise.all([listMacros(), listSources(), listProfiles()])
  } catch (e) {
    loadError.value = errText(e)
    return
  }
  if (!macros.value.some(m => m.id === form.macroId)) form.macroId = macros.value[0]?.id ?? ''
  if (!sources.value.some(s => s.id === form.sourceId)) form.sourceId = sources.value[0]?.id ?? ''
  form.picked = form.picked.filter(id => profiles.value.some(p => p.id === id && !p.running))
})

watch(() => [form.macroId, form.sourceId, open.value] as const, async ([m, s, o]) => {
  mapping.value = null
  if (!o || !m || !s) return
  try {
    mapping.value = await getMapping(m, s)
  } catch {
    mapping.value = null
  }
}, { immediate: true })

const source = computed(() => sources.value.find(s => s.id === form.sourceId))
const macroOpts = computed(() => macros.value.map(m => ({ value: m.id, label: m.name })))
const sourceOpts = computed(() => sources.value.map(s => ({ value: s.id, label: s.name })))
const shown = computed(() => profiles.value.filter(p => p.name.toLowerCase().includes(search.value.trim().toLowerCase())))
const usable = computed(() => shown.value.filter(p => !p.running))
const maxConc = computed(() => Math.max(1, form.picked.length))
watch(maxConc, m => { if (form.concurrency > m) form.concurrency = m })
const conc = computed({ get: () => [Math.min(form.concurrency, maxConc.value)], set: (v: number[]) => { form.concurrency = v[0] } })
const toggle = (id: string) => { form.picked = form.picked.includes(id) ? form.picked.filter(x => x !== id) : [...form.picked, id] }
const selectAll = () => { form.picked = [...new Set([...form.picked, ...usable.value.map(p => p.id)])] }
const selectNone = () => { form.picked = form.picked.filter(id => !usable.value.some(p => p.id === id)) }

const toNum = computed(() => (form.to === '' || Number(form.to) < 1 ? -1 : Number(form.to)))
const fromIdx = computed(() => Math.max(0, Math.floor(Number(form.from) || 1) - 1))
const rangeBad = computed(() => toNum.value >= 0 && toNum.value <= fromIdx.value)
const rowCount = computed(() => {
  const total = source.value?.rows ?? -1
  if (form.testOne) return 1
  const end = toNum.value >= 0 ? (total >= 0 ? Math.min(toNum.value, total) : toNum.value) : total
  return end < 0 ? -1 : Math.max(0, end - fromIdx.value)
})
const mappingBad = computed(() => (mapping.value?.missing.length ?? 0) > 0 || (mapping.value?.duplicates.length ?? 0) > 0)
const canStart = computed(() => !busy.value && !!form.macroId && !!form.sourceId && form.picked.length > 0 && !rangeBad.value && rowCount.value !== 0)

const warnings = computed(() => {
  const w: string[] = []
  if (mapping.value?.missing.length) w.push(t('runs.setup.warnMissing', { cols: mapping.value.missing.join(', ') }, mapping.value.missing.length))
  if (mapping.value?.duplicates.length) w.push(t('runs.setup.warnDup', { cols: mapping.value.duplicates.join(', ') }, mapping.value.duplicates.length))
  if (!form.picked.length) w.push(t('runs.setup.warnNoProfile'))
  if (rangeBad.value) w.push(t('runs.setup.warnRange'))
  if (rowCount.value === 0) w.push(t('runs.setup.warnNoRows'))
  if (form.dryRun) w.push(t('runs.setup.noteDry'))
  else if (!form.testOne) w.push(t('runs.setup.warnCommit'))
  if (form.picked.length > form.concurrency) w.push(t('runs.setup.noteSpare', { spare: form.picked.length - form.concurrency }, form.picked.length - form.concurrency))
  return w
})

async function start() {
  busy.value = true
  startError.value = ''
  try {
    const run = await createRun({
      macroId: form.macroId, sourceId: form.sourceId, from: fromIdx.value, to: toNum.value, profileIds: form.picked,
      options: { concurrency: Math.min(form.concurrency, maxConc.value), delayMs: Number(form.delayMs) || 0, stopAfterErrors: Number(form.stopAfterErrors) || 5, maxRestarts: 3, dryRun: form.dryRun, testOne: form.testOne },
    })
    try {
      await startRun(run.id)
    } finally {
      emit('started', run)
    }
    open.value = false
  } catch (e) {
    startError.value = errText(e)
  } finally {
    busy.value = false
  }
}
</script>
<template>
  <Dialog v-model:open="open">
    <DialogContent class="flex max-h-[calc(100vh-3rem)] flex-col gap-0 p-0 sm:max-w-[640px]">
      <DialogHeader class="px-6 pt-6 pb-3"><DialogTitle>{{ $t('runs.setup.title') }}</DialogTitle><DialogDescription>{{ $t('runs.setup.desc') }}</DialogDescription></DialogHeader>
      <div class="min-h-0 flex-1 overflow-auto bg-muted/60 px-6 py-4">
        <p v-if="loadError" class="mb-3 text-xs text-destructive" role="alert">{{ loadError }}</p>
        <Group>
          <Row :label="$t('runs.macro')"><Pick v-model="form.macroId" :options="macroOpts" :placeholder="$t('runs.setup.pickMacro')" /></Row>
          <Row :label="$t('macros.source')" :sub="source ? (source.rows >= 0 ? $t('runs.rows', { n: fmtNum(source.rows) }, source.rows) : $t('runs.setup.rowsUnknown')) : ''"><Pick v-model="form.sourceId" :options="sourceOpts" :placeholder="$t('runs.setup.pickSource')" /></Row>
          <Row :label="$t('runs.setup.mapping')" :sub="mapping ? (mappingBad ? $t('runs.setup.mappingBad') : $t('runs.setup.mappingOk')) : $t('runs.setup.mappingChecking')"><span :class="mapping && (mappingBad ? 'text-warning' : 'text-success')"><component :is="mappingBad ? TriangleAlert : Check" v-if="mapping" class="size-4" /></span></Row>
        </Group>
        <Group :title="$t('runs.setup.range')" :help="$t('runs.setup.rangeHelp')">
          <Row :label="$t('runs.setup.from')"><Input v-model="form.from" type="number" min="1" class="w-24 text-right tabular-nums" :disabled="form.testOne" /></Row>
          <Row :label="$t('runs.setup.to')"><Input v-model="form.to" type="number" min="1" :placeholder="$t('runs.setup.toEnd')" class="w-24 text-right tabular-nums" :disabled="form.testOne" :aria-invalid="rangeBad" /></Row>
          <Row :label="$t('runs.setup.testOne')" :sub="$t('runs.setup.testOneHelp')"><Switch v-model="form.testOne" :aria-label="$t('runs.setup.testOne')" /></Row>
          <Row :label="$t('runs.setup.dryRun')" :sub="$t('runs.setup.dryRunHelp')"><Switch v-model="form.dryRun" :aria-label="$t('runs.setup.dryRun')" /></Row>
        </Group>
        <Group :title="$t('nav.profiles')" :help="$t('runs.setup.profilesHelp')">
          <div class="flex items-center gap-2 px-3 py-1.5"><Input v-model="search" :placeholder="$t('runs.setup.search')" :aria-label="$t('runs.setup.search')" class="flex-1" />
            <Button size="xs" variant="outline" @click="selectAll">{{ $t('runs.setup.selectAll') }}</Button><Button size="xs" variant="outline" @click="selectNone">{{ $t('runs.setup.selectNone') }}</Button></div>
          <div class="max-h-48 overflow-auto [&>*+*]:border-t">
            <Row v-for="p in shown" :key="p.id" :label="p.name" :sub="p.running ? $t('runs.setup.profileRunning') : p.group"><Checkbox :model-value="form.picked.includes(p.id)" :disabled="p.running" :aria-label="p.name" @update:model-value="toggle(p.id)" /></Row>
            <p v-if="!shown.length" class="px-3 py-3 text-xs text-muted-foreground">{{ $t('runs.setup.noProfiles') }}</p>
          </div>
        </Group>
        <Group :title="$t('runs.setup.pace')">
          <Row :label="$t('runs.concurrency', { n: Math.min(form.concurrency, maxConc) })" :sub="$t('runs.setup.concHelp', { n: Math.min(form.concurrency, maxConc), picked: form.picked.length }, form.picked.length)" stack><Slider v-model="conc" :min="1" :max="maxConc" :step="1" :disabled="maxConc < 2" /></Row>
          <Row :label="$t('runs.setup.delay')" :sub="$t('runs.setup.delayHelp')"><Input v-model="form.delayMs" type="number" min="0" step="100" class="w-24 text-right tabular-nums" /></Row>
          <Row :label="$t('runs.setup.stopAfter')" :sub="$t('runs.setup.stopAfterHelp')"><Input v-model="form.stopAfterErrors" type="number" min="1" class="w-24 text-right tabular-nums" /></Row>
        </Group>
        <section>
          <h3 class="mb-1.5 px-1 text-xs font-medium text-muted-foreground">{{ $t('runs.setup.summary') }}</h3>
          <div class="rounded-xl bg-card px-3 py-2.5 ring-1 ring-border">
            <p class="tabular-nums">{{ rowCount >= 0 ? $t('runs.setup.summaryLine', { rows: fmtNum(rowCount), n: Math.min(form.concurrency, maxConc) }, rowCount) : $t('runs.setup.summaryUnknown', { n: Math.min(form.concurrency, maxConc) }) }}</p>
            <ul class="mt-1.5 space-y-1 text-xs text-muted-foreground"><li v-for="(w, i) in warnings" :key="i" class="flex gap-1.5"><TriangleAlert class="mt-px size-3.5 shrink-0 text-warning" />{{ w }}</li></ul>
          </div>
        </section>
        <p v-if="startError" class="mt-3 text-xs text-destructive" role="alert">{{ startError }}</p>
      </div>
      <DialogFooter class="px-6 py-3"><Button variant="outline" @click="open = false">{{ $t('common.cancel') }}</Button><Button :disabled="!canStart" @click="start"><Play class="size-3.5" />{{ form.testOne ? $t('runs.setup.startTest') : $t('runs.startRun') }}</Button></DialogFooter>
    </DialogContent>
  </Dialog>
</template>
