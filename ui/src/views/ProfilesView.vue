<script setup lang="ts">
import { ref, reactive, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Plus, Search, Pencil, Play, Square, Upload, Download, TriangleAlert, Check, Play as PlayIcon, Square as StopIcon, MoreHorizontal, Trash2, Copy, Users } from '@lucide/vue'
import { store, proxyOf, fpSummary, toggleProfile, startProfiles, stopProfiles, shuffleProfiles, assignProxiesRoundRobin, deleteProfiles, cloneProfile, moveToGroup, applyProxyGeo, loadData, isBusy, consistency, checkText, notify, groupsList, allTags } from '@/store'
import { inWails } from '@/bridge/api'
import { errorCodeText } from '@/bridge/profiles'
import { fmtMs, fmtDuration, fmtRel, countryName, cap } from '@/format'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Checkbox } from '@/components/ui/checkbox'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableRow } from '@/components/ui/table'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import Toolbar from '@/components/mac/Toolbar.vue'
import Group from '@/components/mac/Group.vue'
import Row from '@/components/mac/Row.vue'
import Pick from '@/components/mac/Pick.vue'
import Segmented from '@/components/mac/Segmented.vue'
import StateDot from '@/components/mac/StateDot.vue'
import Confirm from '@/components/mac/Confirm.vue'

const { t } = useI18n()
const f = reactive({ q: '', group: 'all', tag: 'all', state: 'all' })
const filtered = computed(() => store.profiles.filter(p => (!f.q || (p.name + p.tags.join(' ') + p.group).toLowerCase().includes(f.q.toLowerCase())) && (f.group === 'all' || p.group === f.group) && (f.tag === 'all' || p.tags.includes(f.tag)) && (f.state === 'all' || p.state === f.state)))
const opt = (all: string, xs: string[]) => [{ value: 'all', label: all }, ...xs.map(x => ({ value: x, label: x }))]
const cur = computed(() => store.profiles.find(p => p.id === store.selId) || filtered.value[0])
const px = computed(() => (cur.value ? proxyOf(cur.value) : null))
const issues = computed(() => (cur.value ? consistency(cur.value).filter(c => c.level !== 'ok') : []))
const sel = ref<string[]>([])
const all = computed({ get: () => filtered.value.length > 0 && filtered.value.every(p => sel.value.includes(p.id)), set: (v: boolean) => { sel.value = v ? filtered.value.map(p => p.id) : [] } })
const toggleSel = (id: string) => { sel.value = sel.value.includes(id) ? sel.value.filter(x => x !== id) : [...sel.value, id] }
const keepSel = () => { sel.value = sel.value.filter(id => store.profiles.some(p => p.id === id)) }
const bulkStart = () => startProfiles([...sel.value])
const bulkStop = () => stopProfiles([...sel.value])
const bulkShuffle = () => shuffleProfiles([...sel.value])
const bulkAssign = () => assignProxiesRoundRobin([...sel.value])
const bulkDelete = async () => { await deleteProfiles([...sel.value]); keepSel() }
const removeCur = async () => { if (cur.value) { await deleteProfiles([cur.value.id]); keepSel() } }
const dlg = reactive({ open: false, group: '', tags: '' })
const openGroupDialog = () => { dlg.group = ''; dlg.tags = ''; dlg.open = true }
const applyGroupDialog = async () => { dlg.open = false; await moveToGroup([...sel.value], dlg.group, dlg.tags.split(',').map(x => x.trim()).filter(Boolean)) }
const runs = [['Signup form', 'sheet-april.csv', '12 / 12', 'done'], ['Checkout', 'orders.xlsx', '7 / 9', 'uncertain'], ['Price scrape', 'REST /skus', '40 / 40', 'done']]
const stateOpts = () => (['all', 'running', 'stopped', 'error'] as const).map(k => [k, t('profiles.state.' + k)] as [string, string])
const sessionAt = (s: { day: number; time: string }) => cap(fmtRel(s.day, 'day')) + ' ' + s.time
// The banner text: the mock shows a fixed engine exit code, the real app shows the error code the service gave.
const faultText = (p: { err?: string; errCode?: string }) => (inWails && p.errCode ? errorCodeText(p.errCode) : t('errors.' + p.err, { code: '0xC0000005' }))
</script>
<template>
  <Toolbar :title="$t('nav.profiles')" :sub="$t('profiles.count', { n: filtered.length, total: store.profiles.length })">
    <Segmented v-model="f.state" :options="stateOpts()" />
    <div class="relative"><Search class="absolute top-1.5 left-2 size-3.5 text-muted-foreground" /><Input v-model="f.q" data-search :placeholder="$t('common.search')" class="w-48 pl-7" /></div>
    <Button @click="store.openEditor('new')"><Plus class="size-3.5" />{{ $t('profiles.new') }}</Button>
  </Toolbar>
  <div v-if="store.loadError" class="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center">
    <TriangleAlert class="size-6 text-destructive" /><p class="text-sm">{{ $t('profiles.loadError') }}</p><p class="max-w-md text-xs text-muted-foreground">{{ store.loadError }}</p>
    <Button variant="outline" @click="loadData">{{ $t('profiles.retry') }}</Button>
  </div>
  <div v-else-if="store.loading" class="grid min-h-0 flex-1 grid-cols-[minmax(220px,300px)_minmax(0,1fr)]" aria-busy="true">
    <section class="flex flex-col gap-2 border-r p-3"><Skeleton v-for="i in 8" :key="i" class="h-6 w-full" /></section>
    <div class="flex flex-col gap-3 bg-muted/60 px-6 py-5"><Skeleton class="h-7 w-64" /><Skeleton class="h-28 w-full" /><Skeleton class="h-28 w-full" /></div>
  </div>
  <div v-else-if="!store.profiles.length" class="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center">
    <Users class="size-6 text-muted-foreground" /><p class="text-sm font-medium">{{ $t('profiles.emptyTitle') }}</p><p class="max-w-sm text-xs text-muted-foreground">{{ $t('profiles.emptyDesc') }}</p>
    <Button @click="store.openEditor('new')"><Plus class="size-3.5" />{{ $t('profiles.new') }}</Button>
  </div>
  <div v-else class="grid min-h-0 flex-1 grid-cols-[minmax(220px,300px)_minmax(0,1fr)]">
    <section class="flex min-h-0 flex-col border-r">
      <div class="flex items-center gap-2 border-b px-3 py-2"><div class="w-24"><Pick v-model="f.group" :options="opt(t('profiles.allGroups'), groupsList)" /></div><div class="w-[84px]"><Pick v-model="f.tag" :options="opt(t('profiles.allTags'), allTags)" /></div></div>
      <div class="flex h-8 items-center gap-2 border-b px-2 text-xs text-muted-foreground">
        <Checkbox v-model="all" :aria-label="$t('profiles.selectAll')" /><span v-if="!sel.length">{{ $t('profiles.selectAll') }}</span>
        <template v-else><span class="tabular-nums">{{ $t('profiles.selected', { n: sel.length }) }}</span><span class="flex-1" />
          <Button size="xs" variant="outline" @click="bulkStart"><PlayIcon />{{ $t('common.start') }}</Button><Button size="xs" variant="outline" @click="bulkStop"><StopIcon />{{ $t('common.stop') }}</Button>
          <DropdownMenu><DropdownMenuTrigger as-child><Button size="icon-xs" variant="outline" :aria-label="$t('common.moreActions')"><MoreHorizontal /></Button></DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuItem @select="bulkAssign">{{ $t('profiles.assignProxies') }}</DropdownMenuItem><DropdownMenuItem @select="bulkShuffle">{{ $t('profiles.shuffleFp') }}</DropdownMenuItem><DropdownMenuItem @select="openGroupDialog">{{ $t('profiles.moveToGroup') }}</DropdownMenuItem></DropdownMenuContent></DropdownMenu>
          <Confirm :title="$t('profiles.deleteTitle')" :description="$t('profiles.deleteDesc', { n: sel.length }, sel.length)" :action="$t('common.delete')" @confirm="bulkDelete"><Button size="icon-xs" variant="outline" class="text-destructive" :aria-label="$t('profiles.deleteSelected')"><Trash2 /></Button></Confirm></template>
      </div>
      <div class="min-h-0 flex-1 overflow-auto">
        <p v-if="!filtered.length" class="p-4 text-center text-xs text-muted-foreground">{{ $t('profiles.noMatch') }}</p>
        <Table class="table-fixed">
          <TableBody>
            <TableRow v-for="p in filtered" :key="p.id" :data-state="cur && cur.id === p.id ? 'selected' : undefined" class="cursor-default" @click="store.selId = p.id" @dblclick="store.openEditor(p.id)">
              <TableCell class="w-8 pr-0"><Checkbox :model-value="sel.includes(p.id)" :aria-label="$t('profiles.selectOne', { name: p.name })" @click.stop @update:model-value="toggleSel(p.id)" /></TableCell>
              <TableCell class="w-5 px-0"><StateDot :s="isBusy(p.id) ? 'checking' : p.state" /></TableCell>
              <TableCell :title="p.name + ' - ' + p.group + ' - ' + p.os" class="max-w-0"><span class="truncate">{{ p.name }}</span></TableCell>
              <TableCell class="w-12 text-right font-mono text-xs text-muted-foreground tabular-nums">{{ proxyOf(p)?.cc || '' }}</TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
    </section>
    <div v-if="cur" class="min-h-0 overflow-auto bg-muted/60 px-6 py-5">
      <div class="mb-4 flex flex-wrap items-center gap-2">
        <StateDot :s="cur.state" /><h2 class="text-xl font-semibold">{{ cur.name }}</h2><Badge v-for="t in cur.tags" :key="t" variant="secondary">{{ t }}</Badge><span class="flex-1" />
        <Button variant="outline" @click="store.openEditor(cur.id)"><Pencil class="size-3.5" />{{ $t('common.edit') }}</Button>
        <Button variant="outline" @click="cloneProfile(cur.id)"><Copy class="size-3.5" />{{ $t('profiles.clone') }}</Button>
        <Confirm :title="$t('profiles.deleteTitle')" :description="$t('profiles.deleteDesc', { n: 1 }, 1)" :action="$t('common.delete')" @confirm="removeCur"><Button size="icon" variant="outline" class="text-destructive" :disabled="cur.state === 'running'" :aria-label="$t('common.delete')"><Trash2 /></Button></Confirm>
        <Button :variant="cur.state === 'running' ? 'outline' : 'default'" :class="cur.state === 'running' && 'text-destructive'" :disabled="isBusy(cur.id)" @click="toggleProfile(cur)"><component :is="cur.state === 'running' ? Square : Play" class="size-3.5" />{{ cur.state === 'running' ? $t('common.stop') : $t('common.start') }}</Button>
      </div>
      <div v-if="cur.err" class="mb-4 flex items-center gap-2 rounded-xl bg-destructive/10 px-3 py-2 text-destructive ring-1 ring-destructive/30"><TriangleAlert class="size-4 shrink-0" />{{ faultText(cur) }}</div>
      <Group :title="$t('profiles.proxy')">
        <template v-if="px"><Row :label="$t('profiles.location')"><span v-if="px.cc">{{ countryName(px.cc) }} ({{ px.cc }})</span><span v-else class="text-muted-foreground">-</span></Row><Row :label="$t('profiles.endpoint')"><span class="truncate font-mono text-xs">{{ px.type }}://{{ px.host }}:{{ px.port }}</span></Row>
          <Row :label="$t('profiles.status')"><StateDot :s="px.status" /><span class="tabular-nums">{{ px.status === 'ok' && px.latency ? fmtMs(px.latency) : $t('proxyStatus.' + px.status) }}</span></Row><Row :label="$t('profiles.exitIp')"><span class="font-mono text-xs tabular-nums">{{ px.ip || '-' }}</span></Row>
          <Row v-if="inWails" :label="$t('profiles.setFromProxy')"><Button size="sm" variant="outline" :disabled="cur.state === 'running'" @click="applyProxyGeo(cur.id)">{{ $t('profiles.groupDialog.apply') }}</Button></Row></template>
        <Row v-else :label="$t('profiles.connection')"><span class="text-muted-foreground">{{ $t('profiles.direct') }}</span></Row>
      </Group>
      <Group :title="$t('profiles.fingerprint')">
        <Row :label="$t('profiles.summary')"><span class="truncate">{{ fpSummary(cur) }}</span></Row>
        <Row :label="$t('editor.userAgent')"><span class="max-w-72 truncate font-mono text-xs" :title="cur.ua">{{ cur.ua }}</span></Row>
        <Row :label="$t('profiles.langTz')"><span>{{ cur.lang }}, {{ cur.tz }}</span></Row>
        <Row :label="$t('editor.seed')"><span class="font-mono text-xs tabular-nums">{{ cur.seed }}</span></Row>
        <Row :label="$t('profiles.noise')"><span class="flex flex-wrap justify-end gap-1"><Badge v-for="(v, k) in cur.noise" :key="k" variant="outline" :class="v === 'noise' ? 'text-link' : 'text-muted-foreground'">{{ $t('noise.kinds.' + k) }} {{ $t('noise.modes.' + v) }}</Badge></span></Row>
        <Row v-if="cur.dataDir" :label="$t('profiles.dataDir')"><span class="max-w-72 truncate font-mono text-xs" :title="cur.dataDir">{{ cur.dataDir }}</span></Row>
      </Group>
      <Group :title="$t('profiles.consistency')" :help="issues.length ? '' : $t('profiles.allPass')">
        <Row v-for="(c, i) in issues.slice(0, 4)" :key="i" :label="checkText(c)"><TriangleAlert class="size-4" :class="c.level === 'warn' ? 'text-warning' : 'text-destructive'" /></Row>
        <Row v-if="!issues.length" :label="$t('profiles.noIssues')"><Check class="size-4 text-success" /></Row>
      </Group>
      <template v-if="!inWails">
        <Group :title="$t('editor.cookies')"><Row :label="$t('editor.storedCookies')"><span class="tabular-nums">{{ cur.cookies }}</span><Button size="sm" variant="outline" @click="notify(t('toasts.pickCookie'))"><Upload class="size-3" />{{ $t('common.import') }}</Button><Button size="sm" variant="outline" @click="notify(t('toasts.exportedCookies', { n: cur.cookies }, cur.cookies))"><Download class="size-3" />{{ $t('common.export') }}</Button></Row></Group>
        <Group :title="$t('profiles.sessions')"><Row v-for="s in cur.sessions" :key="s.day" :label="sessionAt(s)"><span class="tabular-nums text-muted-foreground">{{ fmtDuration(s.min) }} - {{ $t('profiles.pages', { n: s.pages }, s.pages) }}</span></Row></Group>
        <Group :title="$t('profiles.runs')"><Row v-for="r in runs" :key="r[0]" :label="r[0]" :sub="r[1]"><span class="tabular-nums text-muted-foreground">{{ r[2] }}</span><Badge variant="outline" :class="r[3] === 'done' ? 'text-success' : 'text-warning'">{{ $t('status.' + r[3]) }}</Badge></Row></Group>
      </template>
    </div>
  </div>
  <Dialog v-model:open="dlg.open">
    <DialogContent class="sm:max-w-md">
      <DialogHeader><DialogTitle>{{ $t('profiles.groupDialog.title') }}</DialogTitle><DialogDescription>{{ $t('profiles.selected', { n: sel.length }) }}</DialogDescription></DialogHeader>
      <Group>
        <Row :label="$t('editor.group')"><Input v-model="dlg.group" list="group-names" :placeholder="$t('profiles.groupDialog.keep')" class="w-56 text-right" /></Row>
        <Row :label="$t('profiles.groupDialog.addTags')"><Input v-model="dlg.tags" :placeholder="$t('profiles.groupDialog.tagsPlaceholder')" class="w-56 text-right" /></Row>
      </Group>
      <datalist id="group-names"><option v-for="g in groupsList" :key="g" :value="g" /></datalist>
      <DialogFooter class="flex-row justify-end gap-2"><Button variant="outline" @click="dlg.open = false">{{ $t('common.cancel') }}</Button><Button :disabled="!dlg.group.trim() && !dlg.tags.trim()" @click="applyGroupDialog">{{ $t('profiles.groupDialog.apply') }}</Button></DialogFooter>
    </DialogContent>
  </Dialog>
</template>
