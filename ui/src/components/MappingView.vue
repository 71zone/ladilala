<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Plus, Trash2, TriangleAlert } from '@lucide/vue'
import { deleteSource, getMapping, getSource, listSources, previewSource, setMapping, validateRows, type MappingDTO, type RowIssue, type SourceDTO, type SourcePreview, type ValidationResult } from '@/bridge/data'
import { previewRow, setMacroSource, type StepPreview } from '@/bridge/macros'
import { errorText } from '@/macros'
import { notify } from '@/store'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import Confirm from '@/components/mac/Confirm.vue'
import Group from '@/components/mac/Group.vue'
import Pick from '@/components/mac/Pick.vue'
import Row from '@/components/mac/Row.vue'
import StateDot from '@/components/mac/StateDot.vue'
import DataSourceWizard from '@/components/DataSourceWizard.vue'
import { fmtNum } from '../format'

// Mapping and data: which source feeds the macro, which column each variable reads, and whether the rows are fit to run.
const props = defineProps<{ macroId: string; sourceId: string; dirty: boolean }>()
const emit = defineEmits<{ source: [id: string] }>()
const { t } = useI18n()
const AUTO = '__auto'

const sources = ref<SourceDTO[]>([])
const sel = ref(props.sourceId)
const detail = ref<SourceDTO | null>(null)
const preview = ref<SourcePreview | null>(null)
const mapping = ref<MappingDTO | null>(null)
const checked = ref<ValidationResult | null>(null)
const rowNo = ref(1)
const values = ref<StepPreview[] | null>(null)
const loading = ref(true)
const error = ref('')
const wizard = ref(false)

const srcOpts = computed(() => sources.value.map(s => ({ value: s.id, label: s.name })))
const colOpts = computed(() => [{ value: AUTO, label: t('data.auto') }, ...(mapping.value?.columns ?? []).map(c => ({ value: c, label: c }))])
const issueText = (i: RowIssue) => t('data.issues.' + i.code, { row: i.rowIndex + 1, ...i.params })
const rowsText = computed(() => (detail.value ? (detail.value.rowCount < 0 ? t('data.rowsUnknown') : t('data.rows', { n: fmtNum(detail.value.rowCount) }, detail.value.rowCount)) : ''))

async function guard<T>(f: () => Promise<T>): Promise<T | undefined> {
  error.value = ''
  try { return await f() } catch (e) { error.value = errorText(e); return undefined }
}
async function loadSources() { sources.value = (await guard(listSources)) ?? [] }
async function loadSelected() {
  detail.value = preview.value = mapping.value = checked.value = values.value = null
  if (!sel.value || !sources.value.some(s => s.id === sel.value)) return
  loading.value = true
  const [d, p, m] = await Promise.all([guard(() => getSource(sel.value)), guard(() => previewSource(sel.value, 0, 6)), guard(() => getMapping(props.macroId, sel.value))])
  detail.value = d ?? null
  preview.value = p ?? null
  mapping.value = m ?? null
  loading.value = false
}
onMounted(async () => { await loadSources(); await loadSelected(); loading.value = false })
watch(() => props.sourceId, v => { if (v !== sel.value) { sel.value = v; void loadSelected() } })

async function choose(id: string) {
  sel.value = id
  await guard(() => setMacroSource(props.macroId, id))
  emit('source', id)
  await loadSelected()
}
async function wizardDone(id: string) {
  await loadSources()
  await choose(id)
}
async function remove() {
  const id = sel.value
  const ok = await guard(async () => { await deleteSource(id); return true })
  if (!ok) return
  notify(t('data.deleted'))
  sel.value = ''
  emit('source', '')
  await loadSources()
  await loadSelected()
}
async function pickColumn(variable: string, col: string) {
  if (!mapping.value) return
  const ov = { ...mapping.value.overrides }
  if (col === AUTO) delete ov[variable]
  else ov[variable] = col
  mapping.value = (await guard(() => setMapping(props.macroId, sel.value, ov))) ?? mapping.value
  checked.value = values.value = null
}
async function validate() { checked.value = (await guard(() => validateRows(props.macroId, sel.value, 0, -1))) ?? null }
async function showRow() { values.value = (await guard(() => previewRow(props.macroId, sel.value, Math.max(0, rowNo.value - 1)))) ?? null }
const stepValues = computed(() => (values.value ?? []).filter(v => v.value || v.error))
</script>
<template>
  <div class="grid grid-cols-1 items-start min-[1100px]:grid-cols-[minmax(0,1fr)_380px]">
    <div class="min-w-0 p-5">
      <Alert v-if="error" variant="destructive" class="mb-4"><AlertDescription>{{ error }}</AlertDescription></Alert>
      <Alert v-if="dirty" class="mb-4"><TriangleAlert /><AlertDescription>{{ $t('data.saveFirst') }}</AlertDescription></Alert>
      <div v-if="loading" class="grid gap-2"><Skeleton class="h-10 w-full" /><Skeleton class="h-10 w-full" /><Skeleton class="h-10 w-full" /></div>
      <div v-else-if="!sel || !mapping" class="rounded-xl bg-card p-8 text-center ring-1 ring-border">
        <p class="font-medium">{{ $t('data.empty.title') }}</p><p class="mx-auto mt-1 max-w-md text-xs text-muted-foreground">{{ $t('data.empty.help') }}</p>
        <Button class="mt-4" @click="wizard = true"><Plus class="size-3.5" />{{ $t('data.add') }}</Button>
      </div>
      <template v-else>
        <Group :title="$t('data.mapping.title')" :help="$t('data.mapping.help')">
          <Row v-if="!mapping.variables.length" :label="$t('data.mapping.noVariables')" />
          <Row v-for="v in mapping.variables" :key="v.name" :label="'{{' + v.name + '}}'" :sub="v.suggestion ? $t('data.mapping.suggest', { v: v.suggestion }) : v.required ? $t('data.mapping.required') : undefined">
            <StateDot :s="v.column ? 'ok' : v.required ? 'fail' : 'warn'" />
            <div class="w-44"><Pick :model-value="mapping.overrides[v.name] ?? AUTO" :options="colOpts" @update:model-value="pickColumn(v.name, $event)" /></div>
            <span class="w-32 truncate text-xs text-muted-foreground" :title="v.column">{{ v.column ? '→ ' + v.column : $t('data.mapping.noColumn') }}</span>
          </Row>
        </Group>
        <div class="mb-5 px-1 text-xs">
          <p v-if="mapping.missing.length" class="mb-1 text-destructive"><TriangleAlert class="mr-1 inline size-3" />{{ $t('data.mapping.missing', { v: mapping.missing.join(', ') }) }}</p>
          <p v-if="mapping.extra.length" class="mb-1 text-muted-foreground">{{ $t('data.mapping.extra', { v: mapping.extra.join(', ') }) }}</p>
          <p v-if="mapping.duplicates.length" class="mb-1 text-warning">{{ $t('data.duplicates', { v: mapping.duplicates.join(', ') }) }}</p>
          <p v-if="mapping.ok" class="text-success">{{ $t('data.mapping.ok') }}</p>
        </div>
        <Group :title="$t('data.rowPreview.title')" :help="$t('data.rowPreview.help')">
          <Row :label="$t('data.rowPreview.row')"><Input v-model.number="rowNo" type="number" min="1" class="w-24" @keydown.enter="showRow" /><Button variant="outline" @click="showRow">{{ $t('data.rowPreview.show') }}</Button></Row>
          <Row v-for="v in stepValues" :key="v.stepId" :label="$t('macros.actions.' + v.action)" :sub="v.stepId">
            <span v-if="v.error" class="text-xs text-destructive">{{ $t('data.rowPreview.' + v.error, { v: v.column ?? '' }) }}</span><span v-else class="font-mono text-xs">{{ v.value }}</span>
          </Row>
        </Group>
      </template>
    </div>
    <aside class="min-w-0 border-l bg-muted/60 p-5">
      <h3 class="mb-1.5 px-1 text-xs font-medium text-muted-foreground">{{ $t('macros.source') }}</h3>
      <div class="mb-2 flex items-center gap-2">
        <div class="min-w-0 flex-1"><Pick :model-value="sel" :options="srcOpts" :placeholder="$t('data.none')" @update:model-value="choose($event)" /></div>
        <Button variant="outline" size="icon" :aria-label="$t('data.add')" :title="$t('data.add')" @click="wizard = true"><Plus /></Button>
        <Confirm v-if="sel" :title="$t('data.deleteTitle')" :description="$t('data.deleteBody')" :action="$t('common.delete')" @confirm="remove"><Button variant="outline" size="icon" :aria-label="$t('common.delete')" :title="$t('common.delete')"><Trash2 /></Button></Confirm>
      </div>
      <template v-if="detail">
        <p class="mb-2 px-1 font-mono text-xs break-all text-muted-foreground">{{ detail.kind === 'rest' ? detail.url : detail.path }}<template v-if="detail.sheet"> · {{ detail.sheet }}</template><template v-if="detail.encoding"> · {{ detail.encoding }}</template></p>
        <div v-if="detail.hasAuth" class="mb-2 px-1"><Badge variant="outline">{{ $t('data.hasAuth') }}</Badge></div>
      </template>
      <template v-if="preview">
        <div class="mb-1.5 overflow-auto rounded-xl bg-card ring-1 ring-border">
          <Table><TableHeader><TableRow class="hover:bg-transparent"><TableHead class="w-8">#</TableHead><TableHead v-for="c in preview.columns" :key="c" class="font-mono text-xs">{{ c }}</TableHead></TableRow></TableHeader>
            <TableBody><TableRow v-for="(r, i) in preview.rows" :key="i"><TableCell class="tabular-nums text-muted-foreground">{{ i + 1 }}</TableCell><TableCell v-for="(c, j) in r" :key="j" class="max-w-32 truncate text-xs" :title="c">{{ c }}</TableCell></TableRow></TableBody></Table>
        </div>
        <p class="mb-3 px-1 text-xs text-muted-foreground tabular-nums">{{ rowsText }} · {{ $t('data.showing', { n: preview.rows.length }, preview.rows.length) }}</p>
      </template>
      <Button v-if="mapping" variant="outline" size="sm" class="mb-2" @click="validate">{{ $t('data.validate') }}</Button>
      <template v-if="checked">
        <p class="mb-1 px-1 text-xs" :class="checked.issues.length ? 'text-warning' : 'text-success'">{{ checked.issues.length ? $t('data.check.issues', { n: checked.issues.length, rows: checked.checked }, checked.issues.length) : $t('data.check.ok', { rows: checked.checked }, checked.checked) }}<template v-if="checked.truncated"> {{ $t('data.check.truncated') }}</template></p>
        <div v-for="(x, i) in checked.issues.slice(0, 50)" :key="i" class="flex items-start gap-1 px-1 py-0.5 text-xs"><StateDot :s="x.level === 'error' ? 'fail' : 'warn'" class="mt-1" />{{ issueText(x) }}</div>
      </template>
    </aside>
  </div>
  <DataSourceWizard v-model:open="wizard" :macro-id="macroId" @done="wizardDone" />
</template>
