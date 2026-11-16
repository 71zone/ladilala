<script setup lang="ts">
import { ref, reactive, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RefreshCw, Upload, Trash2, Activity, UserPlus, TriangleAlert, Globe } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Checkbox } from '@/components/ui/checkbox'
import { Skeleton } from '@/components/ui/skeleton'
import { Textarea } from '@/components/ui/textarea'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import Toolbar from '@/components/mac/Toolbar.vue'
import Group from '@/components/mac/Group.vue'
import Row from '@/components/mac/Row.vue'
import Segmented from '@/components/mac/Segmented.vue'
import StateDot from '@/components/mac/StateDot.vue'
import Confirm from '@/components/mac/Confirm.vue'

import { fmtMs, countryName } from '../format'
import { store, notify, attempt, loadData, refreshProfiles, refreshProxies } from '../store'
import { inWails } from '@/bridge/api'
import { assignProxy, errorCodeText, errorText } from '@/bridge/profiles'
import { deleteProxy, getLookup, healthCheck, importProxies, setLookup, testProxy, type ProxyLookup } from '@/bridge/proxies'
import type { Proxy } from '@/store'

const { t } = useI18n()
const bulk = ref(inWails ? '' : 'gw.example.net:1080:alice:secret\n185.44.12.9:8080:bob:hunter2\nbad line here')
const scheme = ref<Proxy['type']>('socks5')
const skipped = ref<{ line: number; reason: string }[]>([])
const checking = ref(false)
const testing = ref<string[]>([])
const assigned = (id: string) => store.profiles.filter(p => p.proxyId === id)

async function importBulk() {
  const r = await attempt(() => importProxies(bulk.value, scheme.value))
  if (!r) return
  skipped.value = r.skipped
  notify(r.skipped.length ? t('toasts.importedSkipped', { n: r.added, bad: r.skipped.length }) : t('toasts.imported', { n: r.added }))
  if (r.added) { if (inWails) bulk.value = r.skipped.length ? bulk.value : ''; await refreshProxies() }
}
async function health() {
  checking.value = true
  try {
    const done = await healthCheck()
    await done
    notify(t('toasts.healthDone'))
  } catch (e) { notify(errorText(e)) } finally { checking.value = false; await refreshProxies() }
}
async function test(p: Proxy) {
  testing.value = [...testing.value, p.id]
  try {
    const r = await attempt(() => testProxy({ id: p.id, type: p.type, host: p.host, port: p.port, user: p.user }))
    if (r) notify(r.ok ? t('proxies.toast.testOk', { host: p.host, ms: r.latency ?? 0 }) : t('proxies.toast.testFail', { host: p.host, reason: errorCodeText(r.code ?? 'proxy.down') }))
    await refreshProxies()
  } finally { testing.value = testing.value.filter(x => x !== p.id) }
}
async function remove(id: string) {
  if (await attempt(async () => { await deleteProxy(id); return true })) { await refreshProxies(); await refreshProfiles() }
}

// Assign one proxy to profiles.
const dlg = reactive({ open: false, proxy: null as Proxy | null, picked: [] as string[] })
function openAssign(p: Proxy) { dlg.proxy = p; dlg.picked = assigned(p.id).map(x => x.id); dlg.open = true }
const pickToggle = (id: string) => { dlg.picked = dlg.picked.includes(id) ? dlg.picked.filter(x => x !== id) : [...dlg.picked, id] }
async function applyAssign() {
  const p = dlg.proxy; if (!p) return
  dlg.open = false
  const had = assigned(p.id).map(x => x.id)
  const add = dlg.picked.filter(id => !had.includes(id)); const drop = had.filter(id => !dlg.picked.includes(id))
  const ok = await attempt(async () => { if (add.length) await assignProxy(add, p.id); if (drop.length) await assignProxy(drop, null); return true })
  if (ok) notify(t('proxies.toast.assigned', { n: add.length + drop.length }, add.length + drop.length))
  await refreshProfiles()
}

// The two public lookup services the proxy test uses (docs/proxy-lookup.md).
const lookup = reactive<ProxyLookup>({ ipUrl: '', geoUrl: '' })
if (inWails) void getLookup().then(l => Object.assign(lookup, l))
async function saveLookup() {
  const l = await attempt(() => setLookup({ ipUrl: lookup.ipUrl, geoUrl: lookup.geoUrl }))
  if (l) { Object.assign(lookup, l); notify(t('proxies.lookup.saved')) }
}
const reason = (r: string) => t('proxies.reason.' + r)
const hasRows = computed(() => store.proxies.length > 0)
</script>
<template>
  <Toolbar :title="$t('nav.proxies')" :sub="$t('proxies.count', { n: store.proxies.length })"><Button :disabled="checking || !hasRows" @click="health"><RefreshCw :class="['size-3.5', checking && 'animate-spin']" />{{ checking ? $t('proxies.checking') : $t('proxies.healthAll') }}</Button></Toolbar>
  <div class="grid min-h-0 flex-1 grid-cols-1 gap-0 min-[1100px]:grid-cols-[minmax(0,1fr)_300px]">
    <div class="min-h-0 overflow-auto">
      <div v-if="store.loadError" class="flex flex-col items-center gap-3 p-8 text-center">
        <TriangleAlert class="size-6 text-destructive" /><p class="text-sm">{{ $t('proxies.loadError') }}</p><p class="max-w-md text-xs text-muted-foreground">{{ store.loadError }}</p>
        <Button variant="outline" @click="loadData">{{ $t('profiles.retry') }}</Button>
      </div>
      <div v-else-if="store.loading" class="flex flex-col gap-2 p-5" aria-busy="true"><Skeleton v-for="i in 6" :key="i" class="h-6 w-full" /></div>
      <div v-else-if="!hasRows" class="flex flex-col items-center gap-3 p-8 text-center">
        <Globe class="size-6 text-muted-foreground" /><p class="text-sm font-medium">{{ $t('proxies.emptyTitle') }}</p><p class="max-w-sm text-xs text-muted-foreground">{{ $t('proxies.emptyDesc') }}</p>
      </div>
      <Table v-else class="table-fixed">
        <TableHeader class="sticky top-0 z-10 bg-background"><TableRow class="hover:bg-transparent"><TableHead class="w-[72px] pl-5">{{ $t('proxies.type') }}</TableHead><TableHead>{{ $t('proxies.host') }}</TableHead><TableHead class="w-[170px]">{{ $t('proxies.country') }}</TableHead><TableHead class="w-[92px]">{{ $t('proxies.status') }}</TableHead><TableHead class="w-[76px] text-right">{{ $t('proxies.latency') }}</TableHead><TableHead class="w-[72px] text-right">{{ $t('nav.profiles') }}</TableHead><TableHead class="w-28" /></TableRow></TableHeader>
        <TableBody>
          <TableRow v-for="p in store.proxies" :key="p.id">
            <TableCell class="pl-5"><Badge variant="secondary">{{ p.type }}</Badge></TableCell>
            <TableCell class="font-mono text-xs" :title="p.host + ':' + p.port">{{ p.host }}:{{ p.port }}</TableCell>
            <TableCell :title="p.cc ? countryName(p.cc) : ''"><template v-if="p.cc">{{ countryName(p.cc) }} ({{ p.cc }})</template><span v-else class="text-muted-foreground">-</span></TableCell>
            <TableCell><StateDot :s="p.status" />{{ $t('proxyStatus.' + p.status) }}</TableCell>
            <TableCell class="text-right tabular-nums text-muted-foreground">{{ p.latency ? fmtMs(p.latency) : '-' }}</TableCell>
            <TableCell class="text-right tabular-nums" :title="assigned(p.id).map(a => a.name).join(', ')">{{ assigned(p.id).length }}</TableCell>
            <TableCell class="text-right">
              <Button variant="ghost" size="icon-xs" :disabled="testing.includes(p.id)" :aria-label="$t('proxies.testAria')" @click="test(p)"><Activity :class="testing.includes(p.id) && 'animate-pulse'" /></Button>
              <Button variant="ghost" size="icon-xs" :aria-label="$t('proxies.assignAria')" @click="openAssign(p)"><UserPlus /></Button>
              <Confirm :title="$t('proxies.removeTitle')" :description="assigned(p.id).length ? $t('proxies.removeUsed', { n: assigned(p.id).length }, assigned(p.id).length) : $t('proxies.removeUnused')" :action="$t('common.remove')" @confirm="remove(p.id)"><Button variant="ghost" size="icon-xs" :aria-label="$t('proxies.removeAria')"><Trash2 /></Button></Confirm>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>
    <aside class="border-t bg-muted/60 p-5 min-[1100px]:border-t-0 min-[1100px]:border-l">
      <Group :title="$t('proxies.bulk')" :help="$t('proxies.bulkHelp')"><div class="p-2"><Textarea v-model="bulk" rows="8" class="resize-none border-0 font-mono text-xs shadow-none focus-visible:ring-0" /></div></Group>
      <div class="mb-3 flex items-center gap-2"><span class="text-xs text-muted-foreground">{{ $t('proxies.defaultScheme') }}</span><Segmented v-model="scheme" :options="['http', 'https', 'socks5']" /></div>
      <Button :disabled="!bulk.trim()" @click="importBulk"><Upload class="size-3.5" />{{ $t('common.import') }}</Button>
      <ul v-if="skipped.length" class="mt-3 max-h-40 overflow-auto text-xs text-destructive" :aria-label="$t('proxies.skippedTitle')">
        <li v-for="s in skipped" :key="s.line" class="tabular-nums">{{ $t('proxies.skippedLine', { line: s.line, reason: reason(s.reason) }) }}</li>
      </ul>
      <Group v-if="inWails" class="mt-5" :title="$t('proxies.lookup.title')" :help="$t('proxies.lookup.help')">
        <Row :label="$t('proxies.lookup.ipUrl')" stack><Input v-model="lookup.ipUrl" class="font-mono text-xs" /></Row>
        <Row :label="$t('proxies.lookup.geoUrl')" stack><Input v-model="lookup.geoUrl" class="font-mono text-xs" /></Row>
      </Group>
      <Button v-if="inWails" variant="outline" @click="saveLookup">{{ $t('proxies.lookup.save') }}</Button>
    </aside>
  </div>
  <Dialog v-model:open="dlg.open">
    <DialogContent class="sm:max-w-md">
      <DialogHeader><DialogTitle>{{ $t('proxies.assignTitle') }}</DialogTitle><DialogDescription class="font-mono text-xs">{{ dlg.proxy?.host }}:{{ dlg.proxy?.port }}</DialogDescription></DialogHeader>
      <div class="max-h-72 overflow-auto rounded-lg border">
        <label v-for="p in store.profiles" :key="p.id" class="flex cursor-default items-center gap-2 border-b px-3 py-1.5 text-sm last:border-b-0">
          <Checkbox :model-value="dlg.picked.includes(p.id)" :aria-label="p.name" @update:model-value="pickToggle(p.id)" /><span class="min-w-0 flex-1 truncate">{{ p.name }}</span><StateDot :s="p.state" />
        </label>
      </div>
      <DialogFooter class="flex-row justify-end gap-2"><Button variant="outline" @click="dlg.open = false">{{ $t('common.cancel') }}</Button><Button @click="applyAssign">{{ $t('profiles.groupDialog.apply') }}</Button></DialogFooter>
    </DialogContent>
  </Dialog>
</template>
