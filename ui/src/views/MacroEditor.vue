<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ChevronLeft, Play, Plus, Save, Trash2, TriangleAlert } from '@lucide/vue'
import {
  clone, emptyMacro, getMacro, newStepId, originOf, saveMacro, validateMacro, varText,
  type Macro, type MacroDTO, type Problem, type RecordWarning, type Step, type VarType,
} from '@/bridge/macros'
import { getMapping, getSource, previewSource, setMapping } from '@/bridge/data'
import { applyColumn } from '@/lib/columns'
import { COACH_STEPS, coach, finishCoach } from '@/lib/coach'
import { useTemplate } from '@/composables/useTemplate'
import { errorText, isCode, txt } from '@/macros'
import { notify } from '@/store'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import AddStepDialog from '@/components/AddStepDialog.vue'
import DataBar from '@/components/DataBar.vue'
import MappingView from '@/components/MappingView.vue'
import StepTable from '@/components/StepTable.vue'
import Group from '@/components/mac/Group.vue'
import Pick from '@/components/mac/Pick.vue'
import Row from '@/components/mac/Row.vue'
import Segmented from '@/components/mac/Segmented.vue'
import Toolbar from '@/components/mac/Toolbar.vue'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const id = computed(() => String(route.params.id))
const tab = computed(() => String(route.query.tab || 'steps'))

const draft = ref<Macro>(emptyMacro())
const description = ref('')
const warnings = ref<RecordWarning[]>([])
const sourceId = ref('')
const columns = ref<string[]>([])
const sourceName = ref('')
const rows = ref<string[][]>([])
const total = ref(0)
const rowNo = ref(1)
const saved = ref<Record<string, string>>({})
const pending = ref<Record<string, string>>({})
const problems = ref<Problem[]>([])
const snapshot = ref('')
const loading = ref(true)
const loadError = ref('')
const error = ref('')
const saving = ref(false)
const selected = ref<string | null>(null)
const adding = ref(false)
const newOrigin = ref('')

const isNew = computed(() => id.value === 'new')
const state = () => JSON.stringify([draft.value, description.value, warnings.value])
const dirty = computed(() => !loading.value && state() !== snapshot.value)
const typeOpts = computed(() => (['string', 'number', 'bool', 'date'] as VarType[]).map(v => ({ value: v, label: t('macros.varTypes.' + v) })))

function adopt(d: MacroDTO) {
  draft.value = d.macro
  description.value = d.description
  warnings.value = d.warnings
  sourceId.value = d.sourceId
  snapshot.value = state()
}
// The data the strip and the live preview use: columns, the first 20 rows, the saved column choices.
async function loadColumns() {
  columns.value = []
  rows.value = []
  saved.value = {}
  sourceName.value = ''
  if (!sourceId.value) return
  try {
    const d = await getSource(sourceId.value)
    columns.value = d.columns
    sourceName.value = d.name
    total.value = d.rowCount
    rows.value = (await previewSource(sourceId.value, 0, 20)).rows
    if (!isNew.value) saved.value = (await getMapping(draft.value.id, sourceId.value)).overrides
  } catch { /* the Mapping tab shows the reason */ }
}
const overrides = computed(() => ({ ...saved.value, ...pending.value }))
const template = useTemplate()
async function load() {
  loading.value = true
  loadError.value = ''
  try {
    if (isNew.value) adopt({ macro: emptyMacro(), version: 0, hash: '', description: '', sourceId: '', warnings: [], createdAt: '', updatedAt: '' })
    else adopt(await getMacro(id.value))
    await loadColumns()
  } catch (e) { loadError.value = errorText(e) } finally { loading.value = false }
}
onMounted(load)
watch(id, load)

let timer = 0
watch(draft, m => {
  window.clearTimeout(timer)
  timer = window.setTimeout(async () => {
    try { problems.value = await validateMacro(clone(m)) } catch { problems.value = [] }
  }, 250)
}, { deep: true })
const visibleProblems = computed(() => (isNew.value && !draft.value.steps.length ? [] : problems.value))

async function save(): Promise<boolean> {
  if (saving.value) return false
  error.value = ''
  saving.value = true
  try {
    const d = await saveMacro({ macro: draft.value, description: description.value, warnings: warnings.value })
    const wasNew = isNew.value
    adopt(d)
    if (sourceId.value && Object.keys(pending.value).length) {
      saved.value = (await setMapping(d.macro.id, sourceId.value, overrides.value)).overrides
      pending.value = {}
    }
    notify(t('macros.saved', { name: d.macro.name }))
    if (wasNew) await router.replace('/macros/' + d.macro.id)
    return true
  } catch (e) {
    error.value = isCode(e, 'conflict') ? t('macros.errors.nameTaken') : errorText(e)
    return false
  } finally { saving.value = false }
}
// A data column choice edits the draft; Save stores it. A column whose name is not a valid
// variable name gets a rewritten variable and a saved column choice, so the run finds it again.
function useColumn(stepId: string, column: string) {
  const v = applyColumn(draft.value, stepId, column)
  if (v && v !== column) pending.value = { ...pending.value, [v]: column }
}
function addStep(s: Omit<Step, 'id'>) {
  const step = { ...s, id: newStepId(draft.value, s.action) } as Step
  const at = selected.value ? draft.value.steps.findIndex(x => x.id === selected.value) + 1 : draft.value.steps.length
  draft.value.steps.splice(at, 0, step)
  selected.value = step.id
  const o = step.action === 'navigate' ? originOf(step.value?.expr ?? '') : null
  if (o && !draft.value.allowedOrigins.includes(o)) draft.value.allowedOrigins.push(o)
}
function addOrigin() {
  const o = originOf(newOrigin.value.trim())
  if (!o) { error.value = t('macros.originBad'); return }
  error.value = ''
  if (!draft.value.allowedOrigins.includes(o)) draft.value.allowedOrigins.push(o)
  newOrigin.value = ''
}
function addVariable() {
  draft.value.variables ??= []
  let n = draft.value.variables.length + 1
  while (draft.value.variables.some(v => v.name === 'var' + n)) n++
  draft.value.variables.push({ name: 'var' + n, type: 'string', required: true })
}
function setRequired(i: number, on: boolean) {
  const v = draft.value.variables![i]
  if (on) v.required = true
  else delete v.required
}
function setVarType(i: number, ty: VarType) {
  const v = draft.value.variables![i]
  v.type = ty
  delete v.default
}
// Step 4 of the coach points at Run. Running a macro ends the tour.
function runMacro() {
  if (coach.step === COACH_STEPS) finishCoach()
  router.push({ path: '/runs', query: { macro: draft.value.id } })
}
onBeforeRouteLeave(() => (dirty.value && !window.confirm(t('macros.unsaved'))) ? false : true)
const title = computed(() => draft.value.name || t('macros.untitled'))
</script>
<template>
  <Toolbar :title="title" :sub="$t('macros.stepsCount', { n: draft.steps.length }, draft.steps.length)">
    <Button variant="ghost" size="sm" class="mr-auto" @click="router.push('/macros')"><ChevronLeft class="size-3.5" />{{ $t('nav.macros') }}</Button>
    <Badge v-if="dirty" variant="outline" class="text-warning">{{ $t('macros.unsavedBadge') }}</Badge>
    <Badge v-if="visibleProblems.length" variant="destructive" :title="visibleProblems.map(p => p.path + ': ' + p.msg).join('\n')">{{ $t('macros.problemsCount', { n: visibleProblems.length }, visibleProblems.length) }}</Badge>
    <Segmented :model-value="tab" :options="[['steps', t('macros.tabSteps')], ['map', t('macros.tabMap')], ['settings', t('macros.tabSettings')]]" @update:model-value="router.replace({ query: { tab: $event } })" />
    <Button v-if="tab === 'steps'" variant="outline" @click="adding = true"><Plus class="size-3.5" />{{ $t('macros.addStep') }}</Button>
    <Button variant="outline" :disabled="!dirty || saving || visibleProblems.length > 0" @click="save"><Save class="size-3.5" />{{ $t('macros.save') }}</Button>
    <Button data-coach="run" :disabled="isNew || dirty" :title="dirty ? $t('macros.saveBeforeRun') : undefined" @click="runMacro"><Play class="size-3.5" />{{ $t('common.run') }}</Button>
  </Toolbar>
  <div class="min-h-0 flex-1 overflow-auto">
    <Alert v-if="loadError" variant="destructive" class="m-5 w-auto"><AlertDescription class="flex items-center gap-3">{{ loadError }}<Button size="sm" variant="outline" @click="load">{{ $t('macros.retry') }}</Button></AlertDescription></Alert>
    <div v-else-if="loading" class="grid gap-2 p-5"><Skeleton v-for="n in 5" :key="n" class="h-9 w-full" /></div>
    <template v-else>
      <Alert v-if="error" variant="destructive" class="m-5 mb-0 w-auto"><AlertDescription>{{ error }}</AlertDescription></Alert>
      <template v-if="tab === 'steps'">
        <div v-if="warnings.length" class="flex items-start gap-2 border-b bg-warning/10 px-5 py-2 text-xs">
          <TriangleAlert class="mt-0.5 size-3.5 shrink-0 text-warning" /><span class="flex-1">{{ $t('macros.warningsHelp', { n: warnings.length }, warnings.length) }}</span>
          <Button size="xs" variant="outline" @click="warnings = []">{{ $t('macros.dismissWarnings') }}</Button>
        </div>
        <DataBar v-model:row-no="rowNo" :macro="draft" :columns="columns" :rows="rows" :total="total" :source-name="sourceName" :overrides="overrides" can-choose @choose="router.replace({ query: { tab: 'map' } })" @template="f => template(isNew ? { macro: draft } : { macroId: draft.id }, draft.name, f)" />
        <StepTable v-model:selected="selected" :macro="draft" :warnings="warnings" :problems="problems" :columns="columns" :rows="rows" :row-index="rowNo - 1" :overrides="overrides" @apply-column="useColumn" />
      </template>
      <div v-else-if="tab === 'map' && isNew" class="p-5"><Alert><TriangleAlert /><AlertDescription>{{ $t('data.saveFirst') }}</AlertDescription></Alert></div>
      <MappingView v-else-if="tab === 'map'" :macro-id="draft.id" :source-id="sourceId" :dirty="dirty || isNew" @source="v => { sourceId = v; loadColumns() }" />
      <div v-else class="max-w-2xl p-5">
        <Group>
          <Row :label="$t('macros.name')"><Input v-model="draft.name" class="w-72" :aria-invalid="!draft.name.trim()" /></Row>
          <Row :label="$t('macros.description')"><Input :model-value="txt(description)" class="w-72" @update:model-value="description = String($event)" /></Row>
        </Group>
        <Group :title="$t('macros.origins')" :help="$t('macros.originsHelp')">
          <Row v-for="(o, i) in draft.allowedOrigins" :key="o" :label="o"><Button size="icon-sm" variant="ghost" class="text-destructive" :aria-label="$t('common.remove')" @click="draft.allowedOrigins.splice(i, 1)"><Trash2 /></Button></Row>
          <Row :label="$t('macros.addOrigin')"><Input v-model="newOrigin" class="w-64 font-mono text-xs" placeholder="https://example.com" @keydown.enter="addOrigin" /><Button variant="outline" @click="addOrigin"><Plus class="size-3.5" />{{ $t('macros.add') }}</Button></Row>
        </Group>
        <Group :title="$t('macros.variables')" :help="$t('macros.variablesHelp')">
          <Row v-for="(v, i) in draft.variables" :key="i" :label="varText(v.name)">
            <Input v-model="v.name" class="h-7 w-40 font-mono text-xs" :aria-label="$t('macros.varName')" />
            <div class="w-28"><Pick :model-value="v.type" :options="typeOpts" @update:model-value="setVarType(i, $event)" /></div>
            <Switch :model-value="!!v.required" :aria-label="$t('macros.varRequired')" :title="$t('macros.varRequired')" @update:model-value="setRequired(i, !!$event)" />
            <Button size="icon-sm" variant="ghost" class="text-destructive" :aria-label="$t('common.remove')" @click="draft.variables!.splice(i, 1)"><Trash2 /></Button>
          </Row>
          <Row :label="$t('macros.addVariable')"><Button variant="outline" @click="addVariable"><Plus class="size-3.5" />{{ $t('macros.add') }}</Button></Row>
        </Group>
      </div>
    </template>
  </div>
  <AddStepDialog v-model:open="adding" @add="addStep" />
</template>
