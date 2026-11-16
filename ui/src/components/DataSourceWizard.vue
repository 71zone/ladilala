<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { FolderOpen, Plus, Trash2, TriangleAlert } from '@lucide/vue'
import {
  createRestSource, createSourceFromFile, deleteSource, emptyFileInput, emptyRestInput, getMapping, inspectFile, pickDataFile, setMapping, testRestSource, validateRows,
  type MappingDTO, type RowIssue, type SourceDTO, type SourcePreview, type ValidationResult,
} from '@/bridge/data'
import { setMacroSource } from '@/bridge/macros'
import { errorText } from '@/macros'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import Group from '@/components/mac/Group.vue'
import Pick from '@/components/mac/Pick.vue'
import Row from '@/components/mac/Row.vue'
import Segmented from '@/components/mac/Segmented.vue'
import StateDot from '@/components/mac/StateDot.vue'

// Data source wizard: pick a file or a REST endpoint, preview it, choose encoding and sheet,
// map the macro variables to columns (Missing and Extra shown), check the rows.
const open = defineModel<boolean>('open', { default: false })
const props = defineProps<{ macroId?: string }>()
const emit = defineEmits<{ done: [sourceId: string] }>()
const { t } = useI18n()

type Step = 'source' | 'mapping' | 'check'
const step = ref<Step>('source')
const kind = ref<'file' | 'rest' | 'sheets'>('file')
const file = reactive(emptyFileInput())
const rest = reactive(emptyRestInput())
const headers = ref<{ k: string; v: string }[]>([])
const preview = ref<SourcePreview | null>(null)
const source = ref<SourceDTO | null>(null)
const mapping = ref<MappingDTO | null>(null)
const result = ref<ValidationResult | null>(null)
const error = ref('')
const busy = ref(false)
const AUTO = '__auto'

const encOpts = computed(() => [{ value: AUTO, label: t('data.auto') }, ...['utf-8', 'utf-8-bom', 'utf-16le', 'utf-16be', 'windows-1258', 'windows-1252'].map(v => ({ value: v, label: v }))])
const delimOpts = computed(() => [{ value: AUTO, label: t('data.auto') }, { value: ',', label: t('data.delims.comma') }, { value: ';', label: t('data.delims.semicolon') }, { value: 'tab', label: t('data.delims.tab') }])
const sheetOpts = computed(() => (preview.value?.sheets ?? []).map(s => ({ value: s, label: s })))
const authOpts = computed(() => (['none', 'bearer', 'basic'] as const).map(v => ({ value: v, label: t('data.auth.' + v) })))
const pagingOpts = computed(() => (['', 'page', 'offset', 'link'] as const).map(v => ({ value: v || AUTO, label: t('data.paging.' + (v || 'none')) })))
const colOpts = computed(() => [{ value: AUTO, label: t('data.auto') }, ...(mapping.value?.columns ?? []).map(c => ({ value: c, label: c }))])
const steps = computed<Step[]>(() => (props.macroId ? ['source', 'mapping', 'check'] : ['source']))
const canNext = computed(() => !busy.value && !!preview.value && (kind.value === 'file' ? !!file.path : !!rest.url.trim()))

watch(open, o => {
  if (!o) return
  step.value = 'source'
  kind.value = 'file'
  Object.assign(file, emptyFileInput())
  Object.assign(rest, emptyRestInput())
  headers.value = []
  preview.value = source.value = mapping.value = result.value = null
  error.value = ''
  busy.value = false
})

async function run<T>(f: () => Promise<T>): Promise<T | undefined> {
  error.value = ''
  busy.value = true
  try { return await f() } catch (e) { error.value = errorText(e); return undefined } finally { busy.value = false }
}
async function choose() {
  const path = await run(pickDataFile)
  if (!path) return
  Object.assign(file, emptyFileInput(path))
  file.name = path.split(/[\\/]/).pop() ?? path
  await inspect()
}
async function inspect() {
  const p = await run(() => inspectFile({ ...file }))
  if (!p) { preview.value = null; return }
  preview.value = p
  if (p.kind === 'excel' && !file.sheet) file.sheet = p.sheet
}
function setOpt(key: 'encoding' | 'delimiter', v: string) {
  file[key] = v === AUTO ? '' : v
  void inspect()
}
function setSheet(v: string) { file.sheet = v; void inspect() }
const restInput = () => ({ ...rest, headers: Object.fromEntries(headers.value.filter(h => h.k.trim()).map(h => [h.k.trim(), h.v])), name: rest.name.trim() || rest.url })
async function testRest() {
  const p = await run(() => testRestSource(restInput()))
  preview.value = p ?? null
}

async function next() {
  if (step.value === 'source') {
    const s = await run(() => (kind.value === 'file' ? createSourceFromFile({ ...file }) : createRestSource(restInput())))
    if (!s) return
    source.value = s
    if (!props.macroId) { emit('done', s.id); open.value = false; return }
    await loadMapping()
    if (mapping.value) step.value = 'mapping'
  } else if (step.value === 'mapping') {
    step.value = 'check'
    result.value = (await run(() => validateRows(props.macroId!, source.value!.id, 0, -1))) ?? null
  }
}
async function loadMapping() { mapping.value = (await run(() => getMapping(props.macroId!, source.value!.id))) ?? null }
async function pickColumn(variable: string, col: string) {
  if (!mapping.value) return
  const ov = { ...mapping.value.overrides }
  if (col === AUTO) delete ov[variable]
  else ov[variable] = col
  mapping.value = (await run(() => setMapping(props.macroId!, source.value!.id, ov))) ?? mapping.value
}
async function finish() {
  if (!source.value) return
  if (props.macroId) await run(() => setMacroSource(props.macroId!, source.value!.id))
  emit('done', source.value.id)
  open.value = false
}
async function cancel() {
  // A source made by this wizard but not finished is removed, so Cancel leaves nothing behind.
  if (source.value && step.value !== 'check') await run(() => deleteSource(source.value!.id))
  open.value = false
}
const issueText = (i: RowIssue) => t('data.issues.' + i.code, { row: i.rowIndex + 1, ...i.params })
</script>
<template>
  <Dialog :open="open" @update:open="v => (v ? (open = true) : cancel())">
    <DialogContent class="sm:max-w-3xl">
      <DialogHeader>
        <DialogTitle>{{ $t('data.wizard.title') }}</DialogTitle>
        <DialogDescription>{{ $t('data.wizard.step', { n: steps.indexOf(step) + 1, total: steps.length }) }}: {{ $t('data.wizard.' + step) }}</DialogDescription>
      </DialogHeader>
      <Alert v-if="error" variant="destructive"><AlertDescription>{{ error }}</AlertDescription></Alert>
      <div class="max-h-[62vh] overflow-auto pr-1">
        <template v-if="step === 'source'">
          <div class="mb-3"><Segmented v-model="kind" :options="[['file', t('data.kinds.file')], ['rest', t('data.kinds.rest')], ['sheets', t('data.kinds.sheets')]]" @update:model-value="preview = null" /></div>
          <template v-if="kind === 'file'">
            <Group>
              <Row :label="$t('data.file')" :sub="file.path || $t('data.fileHelp')"><Button variant="outline" :disabled="busy" @click="choose"><FolderOpen class="size-3.5" />{{ $t('data.choose') }}</Button></Row>
              <template v-if="preview">
                <Row :label="$t('data.name')"><Input v-model="file.name" class="w-64" /></Row>
                <Row v-if="preview.kind === 'csv'" :label="$t('data.encoding')" :sub="$t('data.detected', { v: preview.encoding })"><div class="w-40"><Pick :model-value="file.encoding || AUTO" :options="encOpts" @update:model-value="setOpt('encoding', $event)" /></div></Row>
                <Row v-if="preview.kind === 'csv'" :label="$t('data.delimiter')"><div class="w-40"><Pick :model-value="file.delimiter || AUTO" :options="delimOpts" @update:model-value="setOpt('delimiter', $event)" /></div></Row>
                <Row v-if="preview.kind === 'excel'" :label="$t('data.sheet')" :sub="$t('data.sheetHelp')"><div class="w-48"><Pick :model-value="file.sheet" :options="sheetOpts" @update:model-value="setSheet($event)" /></div></Row>
              </template>
            </Group>
          </template>
          <template v-else-if="kind === 'rest'">
            <Group :help="$t('data.restHelp')">
              <Row :label="$t('data.name')"><Input v-model="rest.name" class="w-64" /></Row>
              <Row :label="$t('data.url')"><Input v-model="rest.url" class="w-80 font-mono text-xs" placeholder="https://" /></Row>
              <Row :label="$t('data.rowsPath')" :sub="$t('data.rowsPathHelp')"><Input v-model="rest.rowsPath" class="w-48 font-mono text-xs" :placeholder="'$.data.items'" /></Row>
              <Row :label="$t('data.authKind')"><div class="w-40"><Pick v-model="rest.authKind" :options="authOpts" /></div></Row>
              <Row v-if="rest.authKind === 'bearer'" :label="$t('data.token')" :sub="$t('data.secretHelp')"><Input v-model="rest.bearerToken" type="password" class="w-64" autocomplete="off" /></Row>
              <template v-if="rest.authKind === 'basic'">
                <Row :label="$t('data.user')"><Input v-model="rest.basicUser" class="w-48" autocomplete="off" /></Row>
                <Row :label="$t('data.password')" :sub="$t('data.secretHelp')"><Input v-model="rest.basicPassword" type="password" class="w-48" autocomplete="off" /></Row>
              </template>
              <Row :label="$t('data.paging.title')"><div class="w-40"><Pick :model-value="rest.paging || AUTO" :options="pagingOpts" @update:model-value="rest.paging = $event === AUTO ? '' : ($event as typeof rest.paging)" /></div></Row>
              <Row v-if="rest.paging" :label="$t('data.pageSize')"><Input v-model.number="rest.pageSize" type="number" min="0" class="w-24" /></Row>
              <Row :label="$t('data.headers')" :sub="$t('data.headersHelp')" stack>
                <div v-for="(h, i) in headers" :key="i" class="mb-1 flex gap-1.5"><Input v-model="h.k" class="h-7 w-40 text-xs" :placeholder="$t('data.headerName')" /><Input v-model="h.v" type="password" class="h-7 flex-1 text-xs" :placeholder="$t('data.headerValue')" autocomplete="off" /><Button size="icon-xs" variant="ghost" :aria-label="$t('common.remove')" @click="headers.splice(i, 1)"><Trash2 /></Button></div>
                <div><Button size="xs" variant="outline" @click="headers.push({ k: '', v: '' })"><Plus />{{ $t('data.addHeader') }}</Button></div>
              </Row>
              <Row :label="$t('data.test')" :sub="$t('data.testHelp')"><Button variant="outline" :disabled="busy || !rest.url.trim()" @click="testRest">{{ $t('data.test') }}</Button></Row>
            </Group>
          </template>
          <Alert v-else><TriangleAlert /><AlertDescription>{{ $t('data.sheetsNeedsSetup') }}</AlertDescription></Alert>
          <template v-if="preview && kind !== 'sheets'">
            <div class="mt-3 mb-1.5 flex items-center gap-2 px-1 text-xs text-muted-foreground"><span>{{ $t('data.previewTitle') }}</span><span class="tabular-nums">{{ preview.total < 0 ? $t('data.rowsUnknown') : $t('data.rows', { n: preview.total }, preview.total) }}</span></div>
            <div class="overflow-auto rounded-xl bg-card ring-1 ring-border">
              <Table><TableHeader><TableRow class="hover:bg-transparent"><TableHead v-for="c in preview.columns" :key="c" class="font-mono text-xs">{{ c }}</TableHead></TableRow></TableHeader>
                <TableBody><TableRow v-for="(r, i) in preview.rows" :key="i"><TableCell v-for="(c, j) in r" :key="j" class="max-w-40 truncate text-xs" :title="c">{{ c }}</TableCell></TableRow></TableBody></Table>
            </div>
            <p v-if="preview.duplicates.length" class="mt-2 px-1 text-xs text-warning"><TriangleAlert class="mr-1 inline size-3" />{{ $t('data.duplicates', { v: preview.duplicates.join(', ') }) }}</p>
            <div v-for="(x, i) in preview.issues" :key="i" class="flex items-start gap-1 px-1 py-0.5 text-xs"><StateDot s="fail" class="mt-1" />{{ issueText(x) }}</div>
          </template>
        </template>
        <template v-else-if="step === 'mapping' && mapping">
          <Group :title="$t('data.mapping.title')" :help="$t('data.mapping.help')">
            <Row v-if="!mapping.variables.length" :label="$t('data.mapping.noVariables')" />
            <Row v-for="v in mapping.variables" :key="v.name" :label="v.name" :sub="v.suggestion ? $t('data.mapping.suggest', { v: v.suggestion }) : undefined">
              <StateDot :s="v.column ? 'ok' : v.required ? 'fail' : 'warn'" /><Badge v-if="v.required" variant="outline" class="text-xs">{{ $t('data.mapping.required') }}</Badge>
              <div class="w-44"><Pick :model-value="mapping.overrides[v.name] ?? AUTO" :options="colOpts" @update:model-value="pickColumn(v.name, $event)" /></div>
              <span class="w-28 truncate text-xs text-muted-foreground" :title="v.column">{{ v.column ? '→ ' + v.column : $t('data.mapping.noColumn') }}</span>
            </Row>
          </Group>
          <div class="px-1 text-xs">
            <p v-if="mapping.missing.length" class="mb-1 text-destructive"><TriangleAlert class="mr-1 inline size-3" />{{ $t('data.mapping.missing', { v: mapping.missing.join(', ') }) }}</p>
            <p v-if="mapping.extra.length" class="mb-1 text-muted-foreground">{{ $t('data.mapping.extra', { v: mapping.extra.join(', ') }) }}</p>
            <p v-if="mapping.duplicates.length" class="mb-1 text-warning">{{ $t('data.duplicates', { v: mapping.duplicates.join(', ') }) }}</p>
            <p v-if="mapping.ok" class="text-success">{{ $t('data.mapping.ok') }}</p>
          </div>
        </template>
        <template v-else-if="step === 'check'">
          <p v-if="!result" class="text-muted-foreground">{{ $t('data.checking') }}</p>
          <template v-else>
            <p class="mb-2" :class="result.issues.length ? 'text-warning' : 'text-success'">{{ result.issues.length ? $t('data.check.issues', { n: result.issues.length, rows: result.checked }, result.issues.length) : $t('data.check.ok', { rows: result.checked }, result.checked) }}<template v-if="result.truncated"> {{ $t('data.check.truncated') }}</template></p>
            <div v-for="(x, i) in result.issues.slice(0, 100)" :key="i" class="flex items-start gap-1 px-1 py-0.5 text-xs"><StateDot :s="x.level === 'error' ? 'fail' : 'warn'" class="mt-1" />{{ issueText(x) }}</div>
          </template>
        </template>
      </div>
      <DialogFooter>
        <Button variant="outline" @click="cancel">{{ $t('common.cancel') }}</Button>
        <Button v-if="step === 'check'" variant="outline" :disabled="busy" @click="step = 'mapping'">{{ $t('data.wizard.back') }}</Button>
        <Button v-if="step === 'check'" :disabled="busy || !result" @click="finish">{{ $t('data.wizard.finish') }}</Button>
        <Button v-else :disabled="step === 'source' ? !canNext || kind === 'sheets' : busy" @click="next">{{ step === 'source' && !props.macroId ? $t('data.wizard.save') : $t('data.wizard.next') }}</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
