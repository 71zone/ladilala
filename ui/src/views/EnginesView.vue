<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { fmtSize, fmtSpeed, fmtSec, fmtPct } from '@/format'
import { Download, MoreHorizontal, Play, Pause, Check, X, RefreshCw, Stethoscope, Copy, TriangleAlert, ChevronRight, Link, Undo2, Loader, FolderOpen } from '@lucide/vue'
import { groupsList } from '@/store'
import { engines, eng, defaultEngine, pins, upd, checking, stateOf, statusText, canUpdate, canInstall, checkUpdates, saveUpdateSettings, setDefault, setGroupPin, rollback, removeEngine, openFolder, job, jobRunning, openInstall, openUpdate, openRedownload, pauseJob, resumeJob, cancelJob, applyJobFix, jobFix, jobFailText, stageDetail, selfTests, fixLabel, diag, diagSummary, runDiag, fixDiag, diagLabel, copyReport, launch, testLaunch, problems, elogs, healthIssues, type EngineDTO } from '@/engines'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Progress } from '@/components/ui/progress'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from '@/components/ui/alert-dialog'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import Toolbar from '@/components/mac/Toolbar.vue'
import Group from '@/components/mac/Group.vue'
import Row from '@/components/mac/Row.vue'
import Pick from '@/components/mac/Pick.vue'
import Segmented from '@/components/mac/Segmented.vue'
import StateDot from '@/components/mac/StateDot.vue'

const { t } = useI18n()
const MB = 1024 * 1024
const tab = ref('engines')
const tabOpts = computed<[string, string][]>(() => [['engines', t('nav.engines') + (healthIssues.value ? ' (' + healthIssues.value + ')' : '')], ['doctor', t('engines.tabDoctor')]])
const pinsOpen = ref(false); const removeTarget = ref<EngineDTO | null>(null)
const tier = (n: number) => engines.value.filter((e) => e.tier === n)
const blocked = computed(() => engines.value.filter((e) => e.status === 'blocked'))
const noBrowser = computed(() => engines.value.length > 0 && !engines.value.some((e) => e.status === 'ready'))
const pinOpts = computed(() => [{ value: 'default', label: t('engines.pins.default') }, ...engines.value.filter((e) => e.status === 'ready').map((e) => ({ value: e.id, label: e.name }))])
const testOpts = computed(() => engines.value.filter((e) => e.status === 'ready').map((e) => ({ value: e.id, label: e.name })))
const testEngine = ref('')
const testId = computed(() => testEngine.value || defaultEngine.value)
const keepOpts = computed(() => [0, 1, 2, 3].map((n) => ({ value: n, label: n === 0 ? t('engines.keepNone') : t('engines.keepN', { n }, n) })))
const bwOpts = computed(() => [{ value: 0, label: t('engines.unlimited') }, ...[2, 5, 10].map((n) => ({ value: n * MB, label: fmtSpeed(n) }))])
const chanOpts = computed(() => [{ value: 'Stable', label: t('engines.channel.stable') }, { value: 'Beta', label: t('engines.channel.beta') }])
const checkOpts = computed<[string, string][]>(() => [['daily', t('settings.check.daily')], ['weekly', t('settings.check.weekly')], ['off', t('settings.check.off')]])
const lvl = (s: string) => (s === 'ok' ? 'ok' : s === 'warn' ? 'warn' : s === 'fail' ? 'fail' : s === 'running' ? 'checking' : 'idle')
const firstFail = computed(() => launch.steps.findIndex((s) => s.state === 'fail'))
const stageClass = (s: string) => (s === 'done' ? 'bg-success/15 text-success' : s === 'fail' ? 'bg-destructive/15 text-destructive' : s === 'active' ? 'bg-primary/15 text-link' : 'bg-muted text-muted-foreground')
const doRemove = () => { if (removeTarget.value) void removeEngine(removeTarget.value); removeTarget.value = null }
const dlg = computed({ get: () => job.open, set: (v: boolean) => { job.open = v } })
const d = computed(() => job.dto)
const pct = computed(() => (d.value && d.value.bytesTotal > 0 ? (d.value.bytesDone / d.value.bytesTotal) * 100 : 0))
const eta = computed(() => (d.value && d.value.bytesPerSec > 0 ? Math.max(1, Math.round((d.value.bytesTotal - d.value.bytesDone) / d.value.bytesPerSec)) : 0))
const jobEngine = computed(() => engines.value.find((e) => e.id === d.value?.engineId))
const pinOf = (g: string) => pins.groups[g] || 'default'
const lastChecked = computed(() => (upd.lastChecked ? new Date(upd.lastChecked).toLocaleString() : t('appUpdate.never')))
const verLine = (e: EngineDTO) => e.active || t('engines.none')
const sizeOf = (e: EngineDTO) => e.installed.reduce((n, v) => n + v.sizeBytes, 0)
</script>
<template>
  <Toolbar :title="$t('nav.engines')" :sub="$t('engines.sub')">
    <Segmented v-model="tab" :options="tabOpts" /><Button variant="outline" @click="pinsOpen = true"><Link class="size-3.5" />{{ $t('engines.pin') }}</Button>
  </Toolbar>
  <div class="min-h-0 flex-1 overflow-auto">
    <!-- ENGINES: three tiers -->
    <div v-if="tab === 'engines'">
      <p class="mx-5 mt-4 text-xs text-muted-foreground"><i18n-t keypath="engines.intro" tag="span"><template #link><router-link to="/settings" class="text-link hover:underline">{{ $t('nav.settings') }}</router-link></template></i18n-t></p>
      <Alert v-if="blocked.length" variant="destructive" class="m-5 mb-0 w-auto"><TriangleAlert /><AlertTitle>{{ $t('engines.blocked.title') }}</AlertTitle>
        <AlertDescription>{{ $t('engines.blocked.desc', { names: blocked.map((b) => b.name).join(', ') }) }}<Button v-if="eng('cft') && canInstall(eng('cft')!)" size="sm" class="mt-2" :disabled="jobRunning" @click="openInstall('cft')"><Download class="size-3" />{{ $t('engines.blocked.action') }}</Button></AlertDescription></Alert>
      <Alert v-else-if="noBrowser" class="m-5 mb-0 w-auto"><TriangleAlert /><AlertTitle>{{ $t('engines.noBrowser.title') }}</AlertTitle>
        <AlertDescription>{{ $t('engines.noBrowser.desc') }}<Button size="sm" class="mt-2" :disabled="jobRunning" @click="openInstall('cft')"><Download class="size-3" />{{ $t('engines.install') }}</Button></AlertDescription></Alert>

      <section v-for="n in [1, 2, 3]" :key="n" class="mt-4">
        <div class="px-5"><h3 class="text-sm font-medium">{{ $t('engines.tier.' + n + '.title') }}</h3><p class="max-w-[760px] text-xs text-muted-foreground">{{ $t('engines.tier.' + n + '.help') }}</p></div>
        <Table class="mt-1 table-fixed">
          <TableHeader class="bg-background"><TableRow class="hover:bg-transparent"><TableHead class="pl-5">{{ $t('engines.colEngine') }}</TableHead><TableHead class="w-[170px]">{{ $t('engines.colVersion') }}</TableHead><TableHead class="w-[170px]">{{ $t('proxies.status') }}</TableHead><TableHead class="w-[80px] text-right">{{ $t('engines.colSize') }}</TableHead><TableHead class="w-[150px] pr-5" /></TableRow></TableHeader>
          <TableBody>
            <TableRow v-for="e in tier(n)" :key="e.id" class="[&>td]:py-2">
              <TableCell class="pl-5" :title="e.path"><div class="flex flex-wrap items-center gap-x-1.5"><b class="font-medium">{{ e.name }}</b><Badge v-if="e.default" variant="secondary" class="text-link">{{ $t('engines.default') }}</Badge></div>
                <div class="truncate text-xs text-muted-foreground">{{ $t('engines.role.' + e.id) }}</div><div v-if="e.path" class="truncate font-mono text-[11px] text-muted-foreground">{{ e.path }}</div></TableCell>
              <TableCell :title="e.installed.map((i) => i.version).join(', ')"><div class="truncate font-mono text-xs tabular-nums">{{ e.status === 'unavailable' ? '-' : verLine(e) }}<span v-if="e.installed.length > 1" class="text-muted-foreground"> +{{ e.installed.length - 1 }}</span></div>
                <div v-if="canUpdate(e)" class="truncate font-mono text-[11px] text-warning tabular-nums">{{ $t('engines.availableVer', { v: e.latest }) }}</div><div v-else-if="e.id === 'cft' && !e.active && e.latest" class="truncate font-mono text-[11px] text-muted-foreground tabular-nums">{{ $t('engines.latestVer', { v: e.latest }) }}</div><div v-else-if="e.previous" class="truncate font-mono text-[11px] text-muted-foreground tabular-nums">{{ $t('engines.prevVer', { v: e.previous }) }}</div></TableCell>
              <TableCell :title="statusText(e)"><StateDot :s="stateOf(e)" />{{ statusText(e) }}</TableCell>
              <TableCell class="text-right tabular-nums text-muted-foreground">{{ e.system || e.status === 'unavailable' ? '-' : e.active ? fmtSize(sizeOf(e) / MB) : e.downloadBytes ? fmtSize(e.downloadBytes / MB) : '-' }}</TableCell>
              <TableCell class="pr-5 text-right">
                <Button v-if="canInstall(e)" size="sm" :disabled="!!e.jobId || jobRunning" @click="openInstall(e.id)"><Download class="size-3" />{{ $t('engines.install') }}</Button>
                <Button v-else-if="canUpdate(e)" size="sm" :disabled="!!e.jobId || jobRunning" @click="openUpdate(e.id)"><Download class="size-3" />{{ $t('engines.update') }}</Button>
                <DropdownMenu v-if="e.status !== 'unavailable'"><DropdownMenuTrigger as-child><Button size="icon-sm" variant="ghost" class="ml-1" :aria-label="$t('engines.actionsFor', { name: e.name })"><MoreHorizontal /></Button></DropdownMenuTrigger>
                  <DropdownMenuContent align="end" class="w-56">
                    <DropdownMenuItem v-if="e.id === 'cft'" :disabled="!e.previous || !!e.jobId" @select="rollback(e)"><Undo2 />{{ e.previous ? $t('engines.rollbackTo', { v: e.previous.split('.')[0] }) : $t('engines.rollback') }}</DropdownMenuItem>
                    <DropdownMenuItem :disabled="e.status !== 'ready' || e.default" @select="setDefault(e)">{{ $t('engines.setDefault') }}</DropdownMenuItem>
                    <DropdownMenuItem @select="pinsOpen = true">{{ $t('engines.pinGroup') }}</DropdownMenuItem>
                    <DropdownMenuItem v-if="e.id === 'cft' && e.active" :disabled="jobRunning" @select="openRedownload(e.id, e.active)"><RefreshCw />{{ $t('engines.redownload') }}</DropdownMenuItem>
                    <DropdownMenuItem v-if="e.id === 'cft'" @select="openFolder"><FolderOpen />{{ $t('engines.openFolder') }}</DropdownMenuItem>
                    <DropdownMenuItem v-if="e.notesUrl" as-child><a :href="e.notesUrl" target="_blank" rel="noreferrer">{{ $t('settings.releaseNotes') }}</a></DropdownMenuItem>
                    <template v-if="e.id === 'cft'"><DropdownMenuSeparator /><DropdownMenuItem variant="destructive" :disabled="!e.active || !!e.jobId" @select="removeTarget = e">{{ $t('common.remove') }}</DropdownMenuItem></template>
                  </DropdownMenuContent></DropdownMenu>
                <Badge v-else variant="outline" class="text-muted-foreground">{{ $t('engines.status.unavailable') }}</Badge>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </section>

      <div class="mt-4 border-t bg-muted/60 px-6 py-5"><div class="max-w-[640px]">
        <Group :title="$t('engines.updSettings')" :help="$t('engines.updHelp')">
          <Row :label="$t('engines.checkNow')" :sub="$t('settings.lastChecked', { when: lastChecked })"><Button variant="outline" :disabled="checking" @click="checkUpdates()"><Loader v-if="checking" class="animate-spin" /><RefreshCw v-else class="size-3.5" />{{ $t('engines.checkNow') }}</Button></Row>
          <Row :label="$t('settings.channel')"><Pick :model-value="upd.channel" :options="chanOpts" @update:model-value="saveUpdateSettings({ channel: $event })" /></Row>
          <Row :label="$t('engines.checkFor')"><Segmented :model-value="upd.autoCheck" :options="checkOpts" @update:model-value="saveUpdateSettings({ autoCheck: $event as 'daily' | 'weekly' | 'off' })" /></Row>
          <Row :label="$t('engines.keepOld')"><Pick :model-value="upd.keepOld" :options="keepOpts" @update:model-value="saveUpdateSettings({ keepOld: $event })" /></Row>
          <Row :label="$t('engines.speedLimit')"><Pick :model-value="upd.bandwidthLimit" :options="bwOpts" @update:model-value="saveUpdateSettings({ bandwidthLimit: $event })" /></Row>
        </Group></div></div>
    </div>

    <!-- DOCTOR -->
    <div v-if="tab === 'doctor'" class="bg-muted/60 px-6 py-5"><div class="mx-auto grid max-w-[1100px] grid-cols-1 items-start gap-x-6 min-[1100px]:grid-cols-2">
      <div>
        <div class="mb-1.5 flex items-center px-1"><h3 class="text-xs font-medium text-muted-foreground">{{ $t('engines.doctor.diagnostics') }}</h3><span class="flex-1" /><Button size="sm" :disabled="diag.running" @click="runDiag"><Stethoscope class="size-3" />{{ diag.running ? $t('engines.doctor.running') : $t('engines.doctor.run') }}</Button></div>
        <Group :help="diag.ran && !diag.running ? $t('engines.doctor.summary', { ok: diagSummary.ok, warn: diagSummary.warn, fail: diagSummary.fail }, diagSummary.warn) : $t('engines.doctor.help')">
          <template v-if="diag.ran">
            <div v-for="i in diag.items" :key="i.id" class="flex items-start gap-2.5 px-3 py-2"><StateDot :s="lvl(i.state)" class="mt-1.5" /><div class="min-w-0 flex-1"><div class="font-medium">{{ diagLabel(i.id) }}</div><p v-if="i.text()" class="break-words text-xs text-muted-foreground">{{ i.text() }}</p></div>
              <Button v-if="i.fixId && (i.state === 'fail' || i.state === 'warn')" size="sm" :disabled="jobRunning" @click="fixDiag(i.fixId)">{{ fixLabel(i.fixId) }}</Button></div>
          </template>
          <Row v-else :label="$t('engines.doctor.notRun')" />
        </Group>
        <div class="mb-5 flex gap-2"><Button size="sm" variant="outline" :disabled="!diag.ran" @click="copyReport"><Copy class="size-3" />{{ $t('engines.doctor.copy') }}</Button></div>
      </div>
      <div>
        <div class="mb-1.5 flex items-center gap-2 px-1"><h3 class="text-xs font-medium text-muted-foreground">{{ $t('engines.doctor.testLaunch') }}</h3><span class="flex-1" /><Pick :model-value="testId" :options="testOpts" class="w-40" @update:model-value="testEngine = $event" /><Button size="sm" variant="outline" :disabled="launch.running || !testId" @click="testLaunch(testId)"><Play class="size-3" />{{ launch.running ? $t('engines.doctor.testing') : $t('engines.doctor.startTest') }}</Button></div>
        <Group :help="firstFail >= 0 ? $t('engines.doctor.stoppedAt', { n: firstFail + 1, step: $t('engines.launch.' + launch.steps[firstFail].k + '.label') }) : ''">
          <div v-for="(s, i) in launch.steps" :key="s.k" :class="['flex items-start gap-2.5 px-3 py-2', s.state === 'fail' && 'bg-destructive/10']">
            <span :class="['mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full text-[11px] tabular-nums', s.state === 'ok' ? 'bg-success/15 text-success' : s.state === 'fail' ? 'bg-destructive/15 text-destructive' : s.state === 'running' ? 'bg-primary/15 text-link' : 'bg-muted text-muted-foreground']"><Check v-if="s.state === 'ok'" class="size-3" /><X v-else-if="s.state === 'fail'" class="size-3" /><Loader v-else-if="s.state === 'running'" class="size-3 animate-spin motion-reduce:animate-none" /><template v-else>{{ i + 1 }}</template></span>
            <div class="min-w-0 flex-1"><div :class="['font-medium', s.state === 'fail' && 'text-destructive']">{{ $t('engines.launch.' + s.k + '.label') }}</div><p v-if="s.text()" class="break-words text-xs text-muted-foreground">{{ s.text() }}</p></div>
            <Button v-if="s.state === 'fail' && s.fix" size="sm" :disabled="jobRunning" @click="fixDiag(s.fix)">{{ fixLabel(s.fix) }}</Button>
          </div>
        </Group>
        <Group :title="$t('engines.doctor.problems')">
          <div v-for="p in problems" :key="p.k" class="flex items-start gap-3 px-3 py-2"><div class="min-w-0 flex-1"><div class="font-medium">{{ $t('engines.problems.' + p.k + '.t') }}</div><p class="text-xs text-muted-foreground">{{ $t('engines.problems.' + p.k + '.d') }}</p></div></div>
        </Group>
      </div>
    </div></div>

    <Collapsible class="border-t px-5 py-2"><CollapsibleTrigger class="flex w-full items-center gap-1.5 text-xs text-muted-foreground [&[data-state=open]>svg]:rotate-90"><ChevronRight class="size-3.5 transition-transform" />{{ $t('engines.logTitle') }}<span class="tabular-nums">({{ $t('engines.logLines', { n: elogs.length }, elogs.length) }})</span></CollapsibleTrigger>
      <CollapsibleContent><div class="mt-2 h-40 overflow-auto rounded-xl bg-zinc-950 px-3 py-2.5 font-mono text-xs leading-relaxed text-zinc-300 ring-1 ring-border"><div v-for="(x, i) in elogs" :key="i" :class="x.l === 'w' ? 'text-amber-300' : x.l === 'e' ? 'text-red-300' : ''"><span class="opacity-60">{{ x.t }}</span> {{ x.m() }}</div></div></CollapsibleContent></Collapsible>
  </div>

  <!-- INSTALL / UPDATE JOB -->
  <Dialog v-model:open="dlg">
    <DialogContent v-if="d" class="max-h-[90vh] gap-3 overflow-auto sm:max-w-[560px]">
      <DialogHeader><DialogTitle class="text-lg">{{ $t(d.kind === 'install' ? 'engines.job.installTitle' : d.kind === 'update' ? 'engines.job.updateTitle' : 'engines.job.redownloadTitle', { name: jobEngine?.name }) }}</DialogTitle><DialogDescription class="tabular-nums">{{ d.version ? $t('engines.job.version', { v: d.version }) : '' }}</DialogDescription></DialogHeader>
      <ol class="flex flex-col">
        <li v-for="s in d.stages" :key="s.key" class="grid grid-cols-[22px_minmax(0,1fr)] gap-x-3 py-1.5">
          <span :class="['mt-0.5 flex size-[22px] items-center justify-center rounded-full', stageClass(s.state)]"><Check v-if="s.state === 'done'" class="size-3" /><X v-else-if="s.state === 'fail'" class="size-3" /><RefreshCw v-else-if="s.state === 'active'" class="size-3 animate-spin motion-reduce:animate-none" /></span>
          <div class="min-w-0"><div :class="s.state === 'pending' ? 'text-muted-foreground' : 'font-medium'">{{ $t('engines.stage.' + s.key) }}</div><p v-if="s.state === 'fail' && d.error" class="text-xs text-destructive">{{ jobFailText(d).title }}</p><p v-else-if="stageDetail(s)" class="text-xs text-muted-foreground">{{ stageDetail(s) }}</p>
            <div v-if="s.key === 'download' && (s.state === 'active' || (pct > 0 && s.state !== 'done'))" class="mt-1.5"><Progress :model-value="pct" :class="[d.state === 'paused' && '[&_[data-slot=progress-indicator]]:bg-warning', s.state === 'fail' && '[&_[data-slot=progress-indicator]]:bg-destructive']" />
              <div class="mt-1 flex gap-3 text-xs text-muted-foreground tabular-nums"><span>{{ fmtPct(pct) }}</span><span>{{ fmtSize(d.bytesDone / MB) }} / {{ d.bytesTotal ? fmtSize(d.bytesTotal / MB) : '?' }}</span><span v-if="d.bytesPerSec">{{ fmtSpeed(d.bytesPerSec / MB) }}</span><span v-if="eta">{{ $t('engines.job.eta', { s: fmtSec(eta) }) }}</span><span v-if="d.attempt > 1">{{ $t('engines.job.tryN', { n: d.attempt }) }}</span></div></div>
            <ul v-if="s.key === 'selftest' && s.state !== 'pending'" class="mt-1 grid grid-cols-1 gap-0.5 min-[520px]:grid-cols-2"><li v-for="x in selfTests" :key="x" class="flex items-center text-xs"><StateDot :s="s.state === 'done' ? 'ok' : s.state === 'fail' ? 'fail' : 'checking'" />{{ $t('engines.job.test.' + x) }}</li></ul></div>
        </li>
      </ol>
      <Alert v-if="d.state === 'failed' && d.error" variant="destructive"><TriangleAlert /><AlertTitle>{{ jobFailText(d).title }}</AlertTitle>
        <AlertDescription><p>{{ jobFailText(d).why }}</p><p v-if="jobEngine?.active" class="mt-1 text-success">{{ $t('engines.job.untouched', { v: jobEngine.active }) }}</p>
          <span class="mt-2 flex gap-2"><Button size="sm" @click="applyJobFix(jobFix)">{{ fixLabel(jobFix) }}</Button><Button v-if="jobFix !== 'retry'" size="sm" variant="outline" @click="applyJobFix('retry')">{{ $t('engines.job.justRetry') }}</Button></span></AlertDescription></Alert>
      <Alert v-if="d.state === 'cancelled'"><TriangleAlert /><AlertTitle>{{ $t('engines.fail.cancel.title') }}</AlertTitle>
        <AlertDescription><p>{{ $t('engines.fail.cancel.why', { kept: jobEngine?.active ? $t('engines.fail.cancel.untouched', { version: jobEngine.active }) : $t('engines.fail.cancel.nothing'), partial: d.partialKept ? $t('engines.fail.cancel.partial') : '' }) }}</p>
          <span class="mt-2 flex gap-2"><Button size="sm" @click="applyJobFix('retry')">{{ fixLabel('resume') }}</Button></span></AlertDescription></Alert>
      <Alert v-if="d.state === 'done'"><Check /><AlertTitle>{{ $t('engines.job.done') }}</AlertTitle><AlertDescription>{{ $t('engines.job.doneDesc', { name: jobEngine?.name, v: jobEngine?.active }) }} {{ jobEngine?.previous ? $t('engines.job.prevKept', { v: jobEngine.previous }) : '' }}</AlertDescription></Alert>
      <DialogFooter class="gap-2">
        <template v-if="jobRunning"><Button variant="outline" :disabled="d.stage !== 'download'" @click="d.state === 'paused' ? resumeJob() : pauseJob()"><component :is="d.state === 'paused' ? Play : Pause" class="size-3.5" />{{ d.state === 'paused' ? $t('common.resume') : $t('common.pause') }}</Button><Button variant="outline" class="text-destructive" @click="cancelJob()">{{ $t('common.cancel') }}</Button></template>
        <Button :variant="jobRunning ? 'outline' : 'default'" @click="job.open = false">{{ jobRunning ? $t('common.hide') : $t('common.close') }}</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>

  <Dialog v-model:open="pinsOpen">
    <DialogContent class="sm:max-w-[440px]"><DialogHeader><DialogTitle class="text-lg">{{ $t('engines.pins.title') }}</DialogTitle><DialogDescription>{{ $t('engines.pins.desc') }}</DialogDescription></DialogHeader>
      <Group v-if="groupsList.length"><Row v-for="g in groupsList" :key="g" :label="g"><Pick :model-value="pinOf(g)" :options="pinOpts" @update:model-value="setGroupPin(g, $event === 'default' ? '' : $event)" /></Row></Group>
      <p v-else class="text-sm text-muted-foreground">{{ $t('engines.pins.noGroups') }}</p>
      <DialogFooter><Button @click="pinsOpen = false">{{ $t('common.done') }}</Button></DialogFooter></DialogContent>
  </Dialog>

  <AlertDialog :open="!!removeTarget" @update:open="(v: boolean) => { if (!v) removeTarget = null }">
    <AlertDialogContent><AlertDialogHeader><AlertDialogTitle>{{ $t('engines.removeTitle', { name: removeTarget?.name }) }}</AlertDialogTitle><AlertDialogDescription>{{ $t('engines.removeDesc') }}</AlertDialogDescription></AlertDialogHeader>
      <AlertDialogFooter><AlertDialogCancel>{{ $t('common.cancel') }}</AlertDialogCancel><AlertDialogAction class="bg-destructive text-white hover:bg-destructive/90" @click="doRemove">{{ $t('common.remove') }}</AlertDialogAction></AlertDialogFooter></AlertDialogContent>
  </AlertDialog>
</template>
