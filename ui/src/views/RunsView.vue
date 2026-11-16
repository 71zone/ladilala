<script setup lang="ts">
import { Play, Pause, Hand, Square, RefreshCw, Download, Plus, TriangleAlert } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Progress } from '@/components/ui/progress'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import Toolbar from '@/components/mac/Toolbar.vue'
import Group from '@/components/mac/Group.vue'
import StateDot from '@/components/mac/StateDot.vue'
import Confirm from '@/components/mac/Confirm.vue'
import RunSetupDialog from '@/components/runs/RunSetupDialog.vue'
import RunLedger from '@/components/runs/RunLedger.vue'
import UncertainResolver from '@/components/runs/UncertainResolver.vue'
import { errText, runTone, isActive } from '@/components/runs/util'
import { ref, reactive, computed, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { fmtNum, fmtDuration, fmtSec, fmtDate } from '@/format'
import { notify } from '@/store'
import { listProfiles } from '@/bridge/runs-deps'
import * as api from '@/bridge/runs'
import type { Run, LedgerRow, LogLine, Recovery, RowState } from '@/bridge/runs'

const { t, locale } = useI18n()
const runs = ref<Run[]>([])
const selectedId = ref('')
const run = computed(() => runs.value.find(r => r.id === selectedId.value))
const profiles = reactive<Record<string, string>>({})
const sessions = ref<LedgerRow[]>([])
const log = ref<(LogLine & { key: number })[]>([])
const recoveries = ref<Recovery[]>([])
const setupOpen = ref(false)
const stamp = ref(0)
const error = ref('')
const loadFailed = ref(false)
const now = ref(Date.now())
let logKey = 0
const offs: (() => void)[] = []
let clock: number | undefined

const counts = computed(() => run.value?.counts ?? {})
const n = (s: RowState) => counts.value[s] ?? 0
const settled = computed(() => n('done') + n('failed') + n('skipped') + n('uncertain'))
const total = computed(() => run.value?.total ?? 0)
const pct = computed(() => (total.value ? (settled.value / total.value) * 100 : 0))
const active = computed(() => !!run.value && isActive(run.value.state))
const uncertainTotal = computed(() => runs.value.reduce((a, r) => a + (r.counts.uncertain ?? 0), 0))
const recoveredRuns = computed(() => recoveries.value.filter(r => runs.value.some(x => x.id === r.runId)))
const canResume = computed(() => ['paused', 'failed', 'crashed_recovered'].includes(run.value?.state ?? ''))
const canStop = computed(() => !!run.value && !['finished', 'stopping'].includes(run.value.state))
const retryable = computed(() => n('failed') + n('skipped'))

function patch(id: string, p: Partial<Run>) {
  const r = runs.value.find(x => x.id === id)
  if (r) Object.assign(r, p)
}
async function reloadRun(id: string) {
  try {
    const fresh = await api.getRun(id)
    const i = runs.value.findIndex(x => x.id === id)
    if (i >= 0) runs.value[i] = fresh
    else runs.value.unshift(fresh)
  } catch (e) {
    error.value = errText(e)
  }
  stamp.value++
  await loadSessions()
}
async function loadSessions() {
  const r = run.value
  if (!r || !isActive(r.state)) { sessions.value = []; return }
  try {
    sessions.value = (await api.listRows(r.id, { states: ['running'], limit: 50 })).rows
  } catch {
    sessions.value = []
  }
}
async function load() {
  try {
    runs.value = await api.listRuns({ limit: 100 })
    loadFailed.value = false
  } catch (e) {
    loadFailed.value = true
    error.value = errText(e)
    return
  }
  recoveries.value = await api.recovered().catch(() => [])
  for (const p of await listProfiles().catch(() => [])) profiles[p.id] = p.name
  const pick = runs.value.find(r => isActive(r.state)) ?? runs.value[0]
  if (pick && !selectedId.value) selectedId.value = pick.id
  for (const r of runs.value.filter(x => isActive(x.state))) void api.watchRun(r.id)
}

function addLog(lines: LogLine[]) {
  for (const l of lines) log.value.push({ ...l, key: logKey++ })
  if (log.value.length > 200) log.value.splice(0, log.value.length - 200)
}
const clock2 = (ms: number) => new Date(ms).toLocaleTimeString(locale.value === 'vi' ? 'vi-VN' : 'en-US', { hour12: false })
const logText = (l: LogLine) => t('runs.logKind.' + l.kind.replace('.', '_'), { row: l.rowIndex + 1, profile: profiles[l.profileId] ?? l.profileId, message: l.message })
const logTone = (l: LogLine) => (l.kind === 'row.failed' || l.kind === 'row.skipped' || l.kind === 'row.uncertain' ? 'text-amber-300' : '')

onMounted(async () => {
  offs.push(
    api.onRunProgress(e => {
      patch(e.runId, { state: e.state, total: e.total, counts: e.counts })
      if (e.runId === selectedId.value) { stamp.value++; void loadSessions() }
    }),
    api.onRunState(e => {
      patch(e.runId, { state: e.state, reason: e.reason })
      if (e.state === 'finished') { const r = runs.value.find(x => x.id === e.runId); notify(t('runs.doneToast', { name: r?.macroName ?? '' })) }
      if (e.runId === selectedId.value) void reloadRun(e.runId)
    }),
    api.onRunLog(e => { if (e.runId === selectedId.value) addLog(e.lines) }),
    api.onRunSession(e => { if (e.runId === selectedId.value) addLog([{ at: e.at, kind: 'browser', rowIndex: -1, profileId: e.profileId, message: e.message }]) }),
    api.onRunUncertain(e => notify(t('runs.uncertainToast', { row: e.rowIndex + 1 }))),
    api.onRunRecovered(e => { recoveries.value = e; void load() }),
  )
  clock = window.setInterval(() => { now.value = Date.now(); if (active.value) void reloadRun(selectedId.value) }, 4000)
  await load()
})
onUnmounted(() => { offs.forEach(f => f()); window.clearInterval(clock) })
watch(selectedId, id => { log.value = []; error.value = ''; if (id) { void api.watchRun(id).catch(() => {}); void loadSessions(); stamp.value++ } })

// One handler for every header button: run the call, then show the real state.
async function act(fn: () => Promise<unknown>) {
  error.value = ''
  try {
    await fn()
  } catch (e) {
    error.value = errText(e)
  }
  if (selectedId.value) await reloadRun(selectedId.value)
}
const toggleTakeover = () => act(() => (run.value?.state === 'takeover' ? api.releaseTakeover(selectedId.value) : api.takeover(selectedId.value)))
const pauseOrResume = () => act(() => (run.value?.state === 'created' ? api.startRun(selectedId.value) : canResume.value ? api.resumeRun(selectedId.value) : api.pauseRun(selectedId.value)))
async function retryFailed() {
  const wasIdle = !active.value
  await act(async () => {
    const k = await api.retryRows(selectedId.value, { states: ['failed', 'skipped'] })
    notify(t('runs.retryToast', { n: fmtNum(k) }, k))
    if (wasIdle && k > 0) await api.resumeRun(selectedId.value)
  })
}
async function exportCsv() {
  error.value = ''
  try {
    const path = await api.exportLedger(selectedId.value)
    if (path) notify(t('runs.exportToast', { path }))
  } catch (e) {
    error.value = errText(e)
  }
}
async function leaveRecovered() {
  await api.dismissRecovered()
  recoveries.value = []
}
async function resumeRecovered(r: Recovery) {
  selectedId.value = r.runId
  recoveries.value = recoveries.value.filter(x => x.runId !== r.runId)
  await act(() => api.resumeRun(r.runId))
}
async function started(r: Run) {
  runs.value.unshift(r)
  selectedId.value = r.id
  await reloadRun(r.id)
}

const elapsed = (r: LedgerRow) => {
  const s = r.startedAt ? Math.max(0, Math.round((now.value - new Date(r.startedAt).getTime()) / 1000)) : 0
  return s < 60 ? fmtSec(s) : fmtDuration(s / 60)
}
const when = (r: Run) => fmtDate(r.createdAt)
const sessionState = computed(() => (run.value?.state === 'takeover' ? 'takeover' : run.value?.state === 'paused' ? 'paused' : 'running'))
</script>
<template>
  <Toolbar :title="$t('nav.runs')" :sub="run ? $t('runs.progress', { done: fmtNum(settled), total: fmtNum(total) }) : ''">
    <Badge v-if="uncertainTotal" variant="outline" class="text-warning tabular-nums">{{ $t('runs.uncertainCount', { n: uncertainTotal }) }}</Badge>
    <Button size="sm" @click="setupOpen = true"><Plus class="size-3.5" />{{ $t('runs.newRun') }}</Button>
  </Toolbar>
  <RunSetupDialog v-model:open="setupOpen" @started="started" />
  <div class="grid min-h-0 flex-1 grid-cols-1 overflow-auto min-[1100px]:grid-cols-[280px_minmax(0,1fr)]">
    <aside class="border-b bg-muted/60 p-5 min-[1100px]:border-r min-[1100px]:border-b-0">
      <Button size="lg" class="mb-5 w-full" @click="setupOpen = true"><Play class="size-3.5" />{{ $t('runs.newRun') }}</Button>
      <Group :title="$t('runs.history')" :help="runs.length ? '' : loadFailed ? $t('runs.loadFailed') : $t('runs.noRuns')">
        <button v-for="r in runs" :key="r.id" type="button" :class="['flex w-full min-w-0 flex-col gap-0.5 px-3 py-2 text-left hover:bg-muted/50 focus-visible:bg-muted/50 focus-visible:outline-none', r.id === selectedId && 'bg-muted']" :aria-current="r.id === selectedId" @click="selectedId = r.id">
          <span class="flex items-center gap-1 font-medium"><StateDot :s="runTone(r.state)" /><span class="truncate">{{ r.macroName }}</span></span>
          <span class="truncate text-xs text-muted-foreground tabular-nums">{{ $t('runs.state.' + r.state) }} · {{ $t('runs.progress', { done: fmtNum((r.counts.done ?? 0) + (r.counts.failed ?? 0) + (r.counts.skipped ?? 0) + (r.counts.uncertain ?? 0)), total: fmtNum(r.total) }) }} · {{ when(r) }}</span>
        </button>
      </Group>
    </aside>
    <div class="min-w-0 overflow-auto">
      <div v-for="r in recoveredRuns" :key="r.runId" class="m-5 mb-0 rounded-xl bg-card px-4 py-3 ring-1 ring-warning/60" role="alert">
        <div class="flex items-start gap-2"><TriangleAlert class="mt-0.5 size-4 shrink-0 text-warning" />
          <div class="min-w-0 flex-1"><p class="font-medium">{{ $t('runs.recovered.title', { name: runs.find(x => x.id === r.runId)?.macroName ?? '' }) }}</p>
            <p class="mt-0.5 text-xs text-muted-foreground">{{ $t('runs.recovered.desc', { queue: fmtNum(r.backToQueue), uncertain: fmtNum(r.uncertain) }) }}</p>
            <div class="mt-2 flex gap-1.5"><Button size="sm" @click="resumeRecovered(r)"><Play class="size-3" />{{ $t('runs.recovered.resume') }}</Button><Button size="sm" variant="outline" @click="leaveRecovered">{{ $t('runs.recovered.leave') }}</Button></div></div></div>
      </div>
      <div v-if="!run" class="flex h-full min-h-60 flex-col items-center justify-center gap-3 p-8 text-center text-muted-foreground">
        <p>{{ loadFailed ? $t('runs.loadFailed') : $t('runs.empty') }}</p>
        <Button v-if="!loadFailed" @click="setupOpen = true"><Plus class="size-3.5" />{{ $t('runs.newRun') }}</Button><Button v-else variant="outline" @click="load"><RefreshCw class="size-3.5" />{{ $t('runs.reload') }}</Button>
      </div>
      <template v-else>
        <div class="flex flex-wrap items-center gap-x-4 gap-y-2 border-b px-5 py-3">
          <div class="min-w-0 flex-1"><div class="flex items-center gap-1 font-medium"><StateDot :s="runTone(run.state)" /><span class="truncate">{{ run.macroName }}</span><span class="text-xs font-normal text-muted-foreground">{{ $t('runs.state.' + run.state) }}</span>
            <Badge v-if="run.options.dryRun" variant="outline" class="ml-1 h-4 px-1.5">{{ $t('runs.setup.dryRun') }}</Badge><Badge v-if="run.options.testOne" variant="outline" class="ml-1 h-4 px-1.5">{{ $t('runs.setup.testOne') }}</Badge></div>
            <p v-if="run.reason" class="truncate text-xs text-muted-foreground" :title="run.reason">{{ run.reason }}</p></div>
          <div class="flex flex-wrap items-center gap-1.5">
            <Button v-if="run.state === 'running' || canResume || run.state === 'created'" size="sm" variant="outline" class="whitespace-nowrap" @click="pauseOrResume"><component :is="run.state === 'running' ? Pause : Play" class="size-3.5" />{{ run.state === 'running' ? $t('common.pause') : run.state === 'created' ? $t('common.start') : $t('common.resume') }}</Button>
            <Button v-if="run.state === 'running' || run.state === 'paused' || run.state === 'takeover'" size="sm" variant="outline" class="whitespace-nowrap" :title="$t('runs.takeoverTip')" @click="toggleTakeover"><Hand class="size-3.5" />{{ run.state === 'takeover' ? $t('runs.handBack') : $t('runs.takeover') }}</Button>
            <Button v-if="retryable" size="sm" variant="outline" class="whitespace-nowrap" @click="retryFailed"><RefreshCw class="size-3.5" />{{ $t('runs.retryFailed', { n: fmtNum(retryable) }) }}</Button>
            <Button size="sm" variant="outline" class="whitespace-nowrap" @click="exportCsv"><Download class="size-3.5" />{{ $t('runs.exportCsv') }}</Button>
            <Confirm v-if="canStop" :title="$t('runs.stopTitle')" :description="$t('runs.stopDesc')" :action="$t('runs.stopAction')" @confirm="act(() => api.stopRun(selectedId))"><Button size="sm" variant="outline" class="whitespace-nowrap text-destructive"><Square class="size-3.5" />{{ $t('common.stop') }}</Button></Confirm>
          </div>
        </div>
        <p v-if="error" class="border-b px-5 py-2 text-xs text-destructive" role="alert">{{ error }}</p>
        <p v-if="run.state === 'takeover'" class="border-b bg-warning/10 px-5 py-2 text-xs">{{ $t('runs.takeoverNote') }}</p>
        <div class="flex items-center gap-3 border-b px-5 py-2.5"><span class="text-xs text-muted-foreground tabular-nums">{{ $t('runs.overall', { done: fmtNum(settled), total: fmtNum(total) }) }}</span>
          <Progress :model-value="pct" :class="['flex-1', (run.state === 'paused' || run.state === 'takeover') && '[&_[data-slot=progress-indicator]]:bg-warning']" />
          <span class="hidden gap-1.5 text-xs tabular-nums min-[1300px]:flex"><span class="text-success">{{ fmtNum(n('done')) }}</span><span class="text-destructive">{{ fmtNum(n('failed')) }}</span><span class="text-warning">{{ fmtNum(n('uncertain')) }}</span><span class="text-muted-foreground">{{ fmtNum(n('skipped')) }} / {{ fmtNum(n('pending')) }}</span></span></div>
        <Table v-if="sessions.length" class="table-fixed">
          <TableHeader class="bg-background"><TableRow class="hover:bg-transparent"><TableHead class="pl-5">{{ $t('runs.colProfile') }}</TableHead><TableHead class="w-[70px]">{{ $t('runs.colRow') }}</TableHead><TableHead class="w-[150px]">{{ $t('runs.colStep') }}</TableHead><TableHead class="w-[100px]">{{ $t('runs.colTime') }}</TableHead><TableHead class="w-[110px] pr-5">{{ $t('runs.colState') }}</TableHead></TableRow></TableHeader>
          <TableBody>
            <TableRow v-for="s in sessions" :key="s.index" class="h-9">
              <TableCell class="truncate pl-5 font-medium" :title="profiles[s.profileId] ?? s.profileId">{{ profiles[s.profileId] ?? s.profileId }}</TableCell><TableCell class="font-mono text-xs tabular-nums">{{ s.index + 1 }}</TableCell>
              <TableCell class="truncate font-mono text-xs" :title="s.stepId">{{ s.stepId }}</TableCell><TableCell class="text-xs tabular-nums">{{ elapsed(s) }}</TableCell>
              <TableCell class="pr-5"><StateDot :s="sessionState === 'running' ? 'running' : 'warn'" />{{ $t('runs.state.' + sessionState) }}</TableCell>
            </TableRow>
          </TableBody>
        </Table>
        <div class="grid grid-cols-1 gap-5 bg-muted/60 p-5">
          <RunLedger :run-id="run.id" :stamp="stamp" :counts="counts" :profiles="profiles" />
          <div class="grid grid-cols-1 gap-5 min-[1300px]:grid-cols-2">
            <section><h3 class="mb-1.5 px-1 text-xs font-medium text-muted-foreground">{{ $t('runs.logTitle') }}</h3><div class="h-44 overflow-auto rounded-xl bg-zinc-950 px-3 py-2.5 font-mono text-xs leading-relaxed text-zinc-300 ring-1 ring-border" role="log"><p v-if="!log.length" class="opacity-60">{{ $t('runs.logEmpty') }}</p><div v-for="l in log" :key="l.key" :class="logTone(l)"><span class="opacity-60">{{ clock2(l.at) }}</span> {{ logText(l) }}</div></div></section>
            <UncertainResolver :run-id="run.id" :stamp="stamp" :profiles="profiles" @changed="reloadRun(run.id)" />
          </div>
        </div>
      </template>
    </div>
  </div>
</template>
