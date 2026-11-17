<script setup lang="ts">
import { Check, TriangleAlert, CircleX, RefreshCw, Shuffle, Upload, Download, Trash2 } from '@lucide/vue'
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription, SheetFooter } from '@/components/ui/sheet'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Badge } from '@/components/ui/badge'
import Group from '@/components/mac/Group.vue'
import Row from '@/components/mac/Row.vue'
import Pick from '@/components/mac/Pick.vue'
import Segmented from '@/components/mac/Segmented.vue'
import StateDot from '@/components/mac/StateDot.vue'
import Confirm from '@/components/mac/Confirm.vue'
import { reactive, ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { fmtMs, countryName } from '../format'
import { store, OS_PRESETS, consistency, checkText, notify, groupsList, ENGINE_MAJOR, refreshProfiles, refreshProxies, type Profile, type NoiseMode } from '../store'
import { inWails } from '../bridge/api'
import { createProfile, errorCodeText, errorText, previewFingerprint, randomSeed, updateProfile, type ProfileForm } from '../bridge/profiles'
import { createProxy, setProxyPassword, testProxy } from '../bridge/proxies'
import { engines, readyEngines, pins, setProfileEngine } from '../engines'
const { t } = useI18n()
const isNew = store.editing === 'new'
const src = store.profiles.find(p => p.id === store.editing)
const draft = reactive<Profile>(src ? JSON.parse(JSON.stringify(src)) : { id: '', name: t('editor.defaultName'), group: inWails ? '' : 'Research', tags: [], notes: '', os: 'Windows 11', ua: '', screen: '', threads: 8, mem: 8, gpu: '', lang: 'en-US', tz: 'America/New_York', seed: randomSeed(), noise: { canvas: 'noise', audio: 'noise', webgl: 'noise', fonts: 'real', rects: 'noise', webrtc: 'off' }, proxyId: null, state: 'stopped', cookies: 0, sessions: [] })
// The device (UA, screen, GPU, threads, memory) follows from the OS preset and the seed. Ask the service for it.
async function refreshDevice() {
  try {
    const d = await previewFingerprint(draft.os, draft.seed, draft.lang, draft.tz)
    Object.assign(draft, { ua: d.ua, screen: d.screen, threads: d.threads, mem: d.mem, gpu: d.gpu })
  } catch (e) { notify(errorText(e)) }
}
if (!src) void refreshDevice()
const cur = store.proxies.find(p => p.id === draft.proxyId)
const px = reactive({ type: cur?.type || 'socks5', host: cur?.host || '', port: cur?.port || 1080, user: cur?.user || '', pass: '', hasPass: cur?.hasPass ?? false })
const tab = ref('general')
const saving = ref(false)
const warnCount = computed(() => checks.value.filter(c => c.level !== 'ok').length)
const proxyOpts = computed(() => [{ value: '', label: t('editor.noProxy') }, ...store.proxies.map(p => ({ value: p.id, label: (p.cc ? p.cc + ' ' : '') + p.host + ':' + p.port + (p.status === 'down' ? '  (' + t('proxyStatus.down') + ')' : '') }))])
// The engine this profile is pinned to; empty follows its group pin or the default. fpc shows only when installed.
const engineId = ref(src ? pins.profiles[src.id] ?? '' : '')
const engineOpts = computed(() => [{ value: '', label: t('editor.engineDefault') }, ...readyEngines.value.map(e => ({ value: e.id, label: e.name }))])
const fpcMajor = computed(() => engines.value.find(e => e.id === 'fpc')?.chromiumMajor ?? 148)
const groupOpts = computed(() => groupsList.map(g => ({ value: g, label: g })))
const open = computed({ get: () => true, set: (v: boolean) => { if (!v) store.editing = null } })
const osKeys = Object.keys(OS_PRESETS)
const test = reactive<{ state: 'idle' | 'running' | 'done' | 'fail'; ip: string; cc: string; latency: number; tz: string; lang: string; code: string }>({ state: 'idle', ip: '', cc: '', latency: 0, tz: '', lang: '', code: '' })
const tagText = computed({ get: () => draft.tags.join(', '), set: v => { draft.tags = v.split(',').map(x => x.trim()).filter(Boolean) } })
// The pool entry that matches the fields in the form, if any.
const matched = () => store.proxies.find(x => x.host === px.host && x.port === px.port && x.type === px.type && x.user === px.user)
function pick(id: string) { const p = store.proxies.find(x => x.id === id); draft.proxyId = id || null; if (p) Object.assign(px, { type: p.type, host: p.host, port: p.port, user: p.user, pass: '', hasPass: p.hasPass }); test.state = 'idle' }
async function runTest() {
  test.state = 'running'
  try {
    const m = matched()
    const r = await testProxy({ id: m?.id, type: px.type, host: px.host, port: px.port, user: px.user, password: px.pass || undefined })
    if (!r.ok) { Object.assign(test, { state: 'fail', code: r.code ?? '' }); return }
    Object.assign(test, { state: 'done', cc: r.cc ?? '', ip: r.ip ?? '', latency: r.latency ?? 0, tz: r.tz ?? '', lang: r.lang ?? '', code: '' })
    if (m) void refreshProxies()
  } catch (e) { Object.assign(test, { state: 'fail', code: '' }); notify(errorText(e)) }
}
function autofill() { if (test.tz) draft.tz = test.tz; if (test.lang) draft.lang = test.lang; notify(t('toasts.tzLangSet')); void refreshDevice() }
async function setOs(o: string) { draft.os = o; await refreshDevice() }
async function shuffle() { draft.seed = randomSeed(); notify(t('toasts.newSeed', { seed: draft.seed })); await refreshDevice() }
const checks = computed(() => { const tmp = { ...draft }; const m = matched(); (tmp as Profile).proxyId = m ? m.id : draft.proxyId; return consistency(tmp as Profile) })
async function save() {
  if (saving.value) return
  saving.value = true
  try {
    let proxyId: string | null = null
    if (px.host) {
      let m = matched()
      if (!m) m = await createProxy({ type: px.type, host: px.host, port: px.port, user: px.user, password: px.pass || undefined })
      else if (px.pass) await setProxyPassword(m.id, px.pass)
      proxyId = m.id
    }
    const form: ProfileForm = { name: draft.name, group: draft.group, tags: draft.tags, notes: draft.notes, proxyId, os: draft.os, seed: draft.seed, lang: draft.lang, tz: draft.tz, noise: draft.noise }
    const saved = isNew ? await createProfile(form) : await updateProfile(src!.id, form)
    if (engineId.value !== (pins.profiles[saved.id] ?? '')) await setProfileEngine(saved.id, engineId.value)
    await refreshProxies(); await refreshProfiles()
    store.selId = saved.id
    store.editing = null; notify(t('toasts.profileSaved'))
  } catch (e) { notify(errorText(e)) } finally { saving.value = false }
}
const NK: (keyof Profile['noise'])[] = ['canvas', 'audio', 'webgl', 'fonts', 'rects', 'webrtc']
const modeOpts = computed<[string, string][]>(() => modes.map(m => [m, t('noise.modes.' + m)]))
const modes: NoiseMode[] = ['noise', 'real', 'off']
const stat = (l: string) => (l === 'ok' ? 'ok' : l === 'warn' ? 'warn' : 'fail')
</script>
<template>
  <Sheet v-model:open="open">
    <SheetContent class="gap-0 p-0 data-[side=right]:w-full data-[side=right]:sm:max-w-[540px]">
      <SheetHeader class="border-b px-5 py-3"><SheetTitle class="text-lg">{{ isNew ? $t('editor.newTitle') : $t('editor.editTitle') }}</SheetTitle><SheetDescription>{{ draft.name }}</SheetDescription></SheetHeader>
      <Tabs v-model="tab" class="flex min-h-0 flex-1 flex-col gap-0">
        <div class="border-b px-5 py-2"><TabsList class="w-full"><TabsTrigger value="general">{{ $t('editor.tabGeneral') }}</TabsTrigger><TabsTrigger value="proxy">{{ $t('profiles.proxy') }}</TabsTrigger><TabsTrigger value="fp">{{ $t('profiles.fingerprint') }}</TabsTrigger><TabsTrigger value="check">{{ $t('editor.tabChecks') }}<Badge v-if="warnCount" variant="secondary" class="ml-1.5 h-4 px-1.5 text-warning tabular-nums">{{ warnCount }}</Badge></TabsTrigger><TabsTrigger v-if="!inWails" value="cookies">{{ $t('editor.cookies') }}</TabsTrigger></TabsList></div>
        <div class="min-h-0 flex-1 overflow-auto bg-muted/60 px-5 py-4">
          <TabsContent value="general">
            <Group><Row :label="$t('editor.name')"><Input v-model="draft.name" class="w-60 text-right" /></Row><Row :label="$t('editor.group')"><Input v-if="inWails" v-model="draft.group" list="editor-groups" class="w-60 text-right" /><Pick v-else v-model="draft.group" :options="groupOpts" /><datalist id="editor-groups"><option v-for="g in groupsList" :key="g" :value="g" /></datalist></Row><Row :label="$t('editor.tags')"><Input v-model="tagText" class="w-60 text-right" /></Row></Group>
            <Group :help="engineId === 'fpc' ? $t('editor.engineOld', { major: fpcMajor }) : ''"><Row :label="$t('editor.engine')"><Pick v-model="engineId" :options="engineOpts" /></Row></Group>
            <Group :title="$t('editor.notes')"><div class="p-2"><Textarea v-model="draft.notes" rows="3" class="resize-none border-0 shadow-none focus-visible:ring-0" :placeholder="$t('editor.notesPlaceholder')" /></div></Group>
          </TabsContent>
          <TabsContent value="proxy">
            <Group :title="$t('editor.connection')">
              <Row :label="$t('editor.fromPool')"><Pick :model-value="draft.proxyId || ''" :options="proxyOpts" @update:model-value="pick" /></Row>
              <Row :label="$t('proxies.type')"><Segmented v-model="px.type" :options="['http', 'https', 'socks5']" /></Row>
              <Row :label="$t('proxies.host')"><Input v-model="px.host" class="w-60 text-right" /></Row><Row :label="$t('editor.port')"><Input v-model.number="px.port" type="number" class="w-24 text-right tabular-nums" /></Row>
              <Row :label="$t('editor.login')"><Input v-model="px.user" class="w-60 text-right" /></Row><Row :label="$t('editor.password')"><Input v-model="px.pass" type="password" autocomplete="off" :placeholder="px.hasPass ? $t('profiles.pwKeep') : ''" class="w-60 text-right" /></Row>
            </Group>
            <div class="mb-5 flex items-center gap-2"><Button :disabled="test.state === 'running'" @click="runTest"><RefreshCw :class="['size-3.5', test.state === 'running' && 'animate-spin']" />{{ test.state === 'running' ? $t('editor.testing') : $t('editor.testProxy') }}</Button>
              <span v-if="test.state === 'idle'" class="text-xs text-muted-foreground">{{ $t('editor.notTested') }}</span>
              <span v-if="test.state === 'fail'" class="flex items-center text-xs text-destructive"><StateDot s="error" />{{ test.code ? errorCodeText(test.code) : $t('editor.testFailed') }}</span></div>
            <Group v-if="test.state === 'done'" :title="$t('editor.testResult')" :help="draft.tz !== test.tz || draft.lang !== test.lang ? $t('editor.tzLangDiffers') : $t('editor.tzLangMatch')">
              <Row :label="$t('profiles.exitIp')"><span class="font-mono text-xs tabular-nums">{{ test.ip }}</span></Row>
              <Row v-if="test.cc" :label="$t('proxies.country')"><span>{{ countryName(test.cc) }} ({{ test.cc }})</span></Row>
              <Row :label="$t('proxies.latency')"><span class="tabular-nums">{{ fmtMs(test.latency) }}</span></Row>
              <Row v-if="test.tz" :label="$t('editor.timezone')"><span>{{ test.tz }}</span><Badge variant="outline" :class="draft.tz === test.tz ? 'text-success' : 'text-warning'">{{ draft.tz === test.tz ? $t('editor.matchesProxy') : $t('editor.profileValue', { v: draft.tz }) }}</Badge></Row>
              <Row v-if="test.lang" :label="$t('editor.language')"><span>{{ test.lang }}</span><Badge variant="outline" :class="draft.lang === test.lang ? 'text-success' : 'text-warning'">{{ draft.lang === test.lang ? $t('editor.matchesProxy') : $t('editor.profileValue', { v: draft.lang }) }}</Badge></Row>
            </Group>
            <Button v-if="test.state === 'done' && (draft.tz !== test.tz || draft.lang !== test.lang)" variant="outline" @click="autofill">{{ $t('editor.autofill') }}</Button>
          </TabsContent>
          <TabsContent value="fp">
            <Group :title="$t('editor.identity')">
              <Row :label="$t('editor.osPreset')"><Pick :model-value="draft.os" :options="osKeys.map(k => ({ value: k, label: k }))" @update:model-value="setOs" /></Row>
              <Row :label="$t('editor.seed')" :sub="'Chrome ' + ENGINE_MAJOR"><span class="font-mono text-xs tabular-nums">{{ draft.seed }}</span><Button size="sm" variant="outline" @click="shuffle"><Shuffle class="size-3" />{{ $t('editor.shuffle') }}</Button></Row>
              <Row :label="$t('editor.userAgent')" stack><Textarea v-model="draft.ua" rows="2" :readonly="inWails" class="font-mono text-xs" /></Row>
            </Group>
            <Group :title="$t('editor.hardware')">
              <Row :label="$t('editor.screen')"><Input v-model="draft.screen" :disabled="inWails" class="w-36 text-right tabular-nums" /></Row><Row label="GPU"><Input v-model="draft.gpu" :disabled="inWails" class="w-60 text-right" /></Row>
              <Row :label="$t('editor.threads')"><Input v-model.number="draft.threads" type="number" :disabled="inWails" class="w-24 text-right tabular-nums" /></Row><Row :label="$t('editor.memory')"><Input v-model.number="draft.mem" type="number" :disabled="inWails" class="w-24 text-right tabular-nums" /></Row>
              <Row :label="$t('editor.language')"><Input v-model="draft.lang" class="w-36 text-right" /></Row><Row :label="$t('editor.timezone')"><Input v-model="draft.tz" class="w-60 text-right" /></Row>
            </Group>
            <Group :title="$t('editor.noise')" :help="$t('editor.noiseHelp')"><Row v-for="n in NK" :key="n" :label="$t('noise.kinds.' + n)"><Segmented v-model="draft.noise[n]" :options="modeOpts" /></Row></Group>
          </TabsContent>
          <TabsContent value="check">
            <Group :title="$t('editor.consistencyTitle')" :help="$t('editor.consistencyHelp')"><Row v-for="(c, i) in checks" :key="i" :label="checkText(c)"><Check v-if="c.level === 'ok'" class="size-4 text-success" /><TriangleAlert v-else-if="c.level === 'warn'" class="size-4 text-warning" /><CircleX v-else class="size-4 text-destructive" /></Row></Group>
          </TabsContent>
          <TabsContent v-if="!inWails" value="cookies">
            <Group :title="$t('editor.cookies')" :help="$t('editor.cookiesHelp')"><Row :label="$t('editor.storedCookies')"><span class="tabular-nums">{{ draft.cookies }}</span></Row></Group>
            <div class="flex gap-2"><Button variant="outline" @click="notify(t('toasts.pickCookieFile'))"><Upload class="size-3.5" />{{ $t('common.import') }}</Button><Button variant="outline" @click="notify(t('toasts.exportedCookies', { n: draft.cookies }, draft.cookies))"><Download class="size-3.5" />{{ $t('common.export') }}</Button>
              <Confirm :title="$t('editor.clearTitle')" :description="$t('editor.clearDesc', { n: draft.cookies }, draft.cookies)" :action="$t('editor.clearAction')" @confirm="draft.cookies = 0; notify(t('toasts.cookiesCleared'))"><Button variant="outline" class="text-destructive"><Trash2 class="size-3.5" />{{ $t('editor.clear') }}</Button></Confirm></div>
          </TabsContent>
        </div>
      </Tabs>
      <SheetFooter class="flex-row justify-end gap-2 border-t px-5 py-3"><Button variant="outline" @click="store.editing = null">{{ $t('common.cancel') }}</Button><Button :disabled="saving" @click="save">{{ $t('editor.save') }}</Button></SheetFooter>
    </SheetContent>
  </Sheet>
</template>
