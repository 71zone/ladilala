<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Circle, CircleHelp, Copy, Download, FileUp, Link, Play, Plus, Trash2, TriangleAlert } from '@lucide/vue'
import { deleteMacro, duplicateMacro, exportMacro, listMacros, type MacroSummary } from '@/bridge/macros'
import { listSources } from '@/bridge/data'
import { errorText, relTime, txt } from '@/macros'
import { notify } from '@/store'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import Confirm from '@/components/mac/Confirm.vue'
import Toolbar from '@/components/mac/Toolbar.vue'
import ImportDialog from '@/components/ImportDialog.vue'
import { COACH_STEPS, coach, finishCoach, reach, replayCoach, startCoachOnce } from '@/lib/coach'
import RecordDialog from '@/components/RecordDialog.vue'

const router = useRouter()
const { t } = useI18n()
const list = ref<MacroSummary[]>([])
const sourceNames = ref<Record<string, string>>({})
const loading = ref(true)
const error = ref('')
const recording = ref(false)
const importing = ref(false)
const sourceOf = (m: MacroSummary) => sourceNames.value[m.sourceId] ?? ''
const empty = computed(() => !loading.value && !error.value && !list.value.length)

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [macros, sources] = await Promise.all([listMacros(), listSources()])
    list.value = macros
    sourceNames.value = Object.fromEntries(sources.map(s => [s.id, s.name]))
  } catch (e) { error.value = errorText(e) } finally { loading.value = false }
}
onMounted(() => {
  void load()
  startCoachOnce()
})
// Pressing Record moves the coach to step 2, the hint card in the recorded page.
function openRecord() { reach(2); recording.value = true }
// A saved recording opens in the editor, where step 4 points at Run.
function saved(id: string) { reach(COACH_STEPS); void load(); open(id) }

async function act(f: () => Promise<void>) {
  try { await f() } catch (e) { notify(errorText(e)) }
}
const duplicate = (m: MacroSummary) => act(async () => {
  const d = await duplicateMacro(m.id, t('macros.copyName', { name: m.name }))
  notify(t('macros.duplicated', { name: d.macro.name }))
  await load()
})
const exportOne = (m: MacroSummary) => act(async () => {
  if (await exportMacro(m.id, m.name.replace(/[\\/:*?"<>|]+/g, '_') + '.json')) notify(t('macros.exported', { name: m.name }))
})
const remove = (m: MacroSummary) => act(async () => {
  await deleteMacro(m.id)
  notify(t('macros.deleted', { name: m.name }))
  await load()
})
function runFromList(id: string) {
  if (coach.step === COACH_STEPS) finishCoach()
  router.push({ path: '/runs', query: { macro: id } })
}
const open = (id: string, tab?: string) => router.push('/macros/' + id + (tab ? '?tab=' + tab : ''))
</script>
<template>
  <Toolbar :title="$t('nav.macros')" :sub="$t('macros.count', { n: list.length }, list.length)">
    <Button variant="ghost" size="icon" :aria-label="$t('ux.help')" :title="$t('ux.help')" @click="replayCoach"><CircleHelp /></Button>
    <Button variant="outline" @click="importing = true"><FileUp class="size-3.5" />{{ $t('common.import') }}</Button>
    <Button variant="outline" data-coach="record" @click="openRecord"><Circle class="size-3.5 text-destructive" />{{ $t('macros.record') }}</Button>
    <Button @click="router.push('/macros/new')"><Plus class="size-3.5" />{{ $t('macros.new') }}</Button>
  </Toolbar>
  <div class="min-h-0 flex-1 overflow-auto">
    <Alert v-if="error" variant="destructive" class="m-5 w-auto"><AlertDescription class="flex items-center gap-3">{{ error }}<Button size="sm" variant="outline" @click="load">{{ $t('macros.retry') }}</Button></AlertDescription></Alert>
    <div v-else-if="loading" class="grid gap-2 p-5"><Skeleton v-for="n in 4" :key="n" class="h-10 w-full" /></div>
    <div v-else-if="empty" class="mx-auto mt-14 max-w-lg rounded-xl bg-card p-8 text-center ring-1 ring-border">
      <p class="text-base font-medium">{{ $t('ux.empty.title') }}</p><p class="mx-auto mt-1 max-w-sm text-xs text-muted-foreground">{{ $t('ux.empty.help') }}</p>
      <div class="mt-5 flex justify-center gap-2"><Button @click="openRecord"><Circle class="size-3.5 text-destructive" />{{ $t('macros.record') }}</Button><Button variant="outline" @click="importing = true"><FileUp class="size-3.5" />{{ $t('common.import') }}</Button></div>
      <p class="mt-3 text-xs text-muted-foreground">{{ $t('ux.empty.tip') }}</p>
    </div>
    <Table v-else class="table-fixed">
      <TableHeader class="sticky top-0 z-10 bg-background"><TableRow class="hover:bg-transparent"><TableHead class="pl-5">{{ $t('macros.name') }}</TableHead><TableHead class="w-16 text-right">{{ $t('macros.steps') }}</TableHead><TableHead class="w-[170px]">{{ $t('macros.source') }}</TableHead><TableHead class="w-[100px]">{{ $t('macros.updated') }}</TableHead><TableHead class="w-[330px] pr-5" /></TableRow></TableHeader>
      <TableBody>
        <TableRow v-for="m in list" :key="m.id" class="h-10 cursor-default" @click="open(m.id)">
          <TableCell class="pl-5" :title="txt(m.description)"><span class="font-medium">{{ m.name }}</span><span class="ml-2 text-xs text-muted-foreground">{{ txt(m.description) }}</span><Badge v-if="m.warningCount" variant="outline" class="ml-2 text-warning" :title="$t('macros.warningBadge', { n: m.warningCount }, m.warningCount)"><TriangleAlert />{{ m.warningCount }}</Badge></TableCell>
          <TableCell class="text-right tabular-nums">{{ m.stepCount }}</TableCell><TableCell class="font-mono text-xs" :title="sourceOf(m)">{{ sourceOf(m) || '–' }}</TableCell><TableCell class="text-muted-foreground">{{ relTime(m.updatedAt) }}</TableCell>
          <TableCell class="pr-5 text-right" @click.stop>
            <Button size="sm" variant="outline" @click="open(m.id, 'map')"><Link class="size-3" />{{ $t('macros.mapping') }}</Button>
            <Button size="sm" variant="outline" class="ml-1 text-success" @click="runFromList(m.id)"><Play class="size-3" />{{ $t('common.run') }}</Button>
            <Button size="icon-sm" variant="ghost" class="ml-1" :aria-label="$t('macros.duplicate')" :title="$t('macros.duplicate')" @click="duplicate(m)"><Copy /></Button>
            <Button size="icon-sm" variant="ghost" :aria-label="$t('common.export')" :title="$t('common.export')" @click="exportOne(m)"><Download /></Button>
            <Confirm :title="$t('macros.deleteTitle', { name: m.name })" :description="$t('macros.deleteBody')" :action="$t('common.delete')" @confirm="remove(m)"><Button size="icon-sm" variant="ghost" class="text-destructive" :aria-label="$t('common.delete')" :title="$t('common.delete')"><Trash2 /></Button></Confirm>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
  </div>
  <RecordDialog v-model:open="recording" @saved="saved" />
  <ImportDialog v-model:open="importing" @imported="id => { load(); open(id) }" />
</template>
