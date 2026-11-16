<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useLocale } from '@/composables/useLocale'
import { fmtSize } from '@/format'
import { Lock, Loader, ChevronRight, TriangleAlert } from '@lucide/vue'
import { notify } from '@/store'
import { theme } from '@/theme'
import { hintOff, setHintOff } from '@/lib/coach'
import { errorText } from '@/errtext'
import { settingsApi, type GeneralSettings, type DataFolderInfo, type LogLevel } from '@/bridge/settings'
import { engines, setDefault } from '@/engines'
import { au, checkNow, skipVersion, fmtChecked, saveUpdateSettings } from '@/appupdate'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'
import { Alert, AlertDescription } from '@/components/ui/alert'
import Toolbar from '@/components/mac/Toolbar.vue'
import Group from '@/components/mac/Group.vue'
import Row from '@/components/mac/Row.vue'
import Segmented from '@/components/mac/Segmented.vue'
import Pick from '@/components/mac/Pick.vue'

const { t } = useI18n()
const { locale } = useLocale()
const s = ref<GeneralSettings | null>(null)
const folder = ref<DataFolderInfo | null>(null)
const usage = ref(0)
const feed = ref('')
const showHint = ref(!hintOff())
watch(showHint, v => setHintOff(!v))
const checkOpts = computed(() => ['daily', 'weekly', 'off'].map((v) => ({ value: v, label: t('settings.check.' + v) })))
const logOpts = computed(() => (['error', 'warn', 'info', 'debug'] as LogLevel[]).map((v) => ({ value: v, label: t('settings.log.' + v) })))
const themeOpts = computed<[string, string][]>(() => [['system', t('settings.theme.system')], ['light', t('settings.theme.light')], ['dark', t('settings.theme.dark')]])
const engineOpts = computed(() => engines.value.filter((e) => e.status === 'ready').map((e) => ({ value: e.id, label: e.name })))
const defaultId = computed(() => engines.value.find((e) => e.default)?.id ?? '')
const delay = ref(0); const conc = ref(1); const testUrl = ref('')
const status = computed(() => au.phase === 'checking' ? t('settings.status.checking') : au.phase === 'downloading' ? t('settings.status.downloading', { version: au.rel?.version }) : au.phase === 'ready' ? t('settings.status.ready', { version: au.rel?.version }) : au.phase === 'failed' ? t('settings.status.failed') : au.rel ? t('settings.status.available', { version: au.rel.version }) : !au.configured ? t('settings.status.notConfigured') : t('settings.status.upToDate'))

async function load() {
  try {
    const [g, f, u] = await Promise.all([settingsApi.get(), settingsApi.dataFolder(), settingsApi.dataFolderUsage()])
    s.value = g; folder.value = f; usage.value = u
    delay.value = g.defaultDelayMs; conc.value = g.concurrency; testUrl.value = g.proxyTestUrl
  } catch (e) { notify(errorText(e)) }
  feed.value = au.feedFromEnv ? '' : au.feedUrl
}
onMounted(load)

async function save(patch: Partial<GeneralSettings>) {
  if (!s.value) return
  try {
    s.value = await settingsApi.save({ ...s.value, ...patch })
    delay.value = s.value.defaultDelayMs; conc.value = s.value.concurrency; testUrl.value = s.value.proxyTestUrl
  } catch (e) { notify(errorText(e)); if (s.value) { delay.value = s.value.defaultDelayMs; conc.value = s.value.concurrency; testUrl.value = s.value.proxyTestUrl } }
}
const saveNumbers = () => save({ defaultDelayMs: Math.round(Number(delay.value) || 0), concurrency: Math.round(Number(conc.value) || 1) })
async function changeFolder() {
  try {
    const p = await settingsApi.pickDataFolder()
    if (!p) return
    folder.value = await settingsApi.changeDataFolder(p)
  } catch (e) { notify(errorText(e)) }
}
async function resetFolder() { try { folder.value = await settingsApi.changeDataFolder('') } catch (e) { notify(errorText(e)) } }
const openFolder = () => settingsApi.openDataFolder().catch((e) => notify(errorText(e)))
const openLogs = () => settingsApi.openLogsFolder().catch((e) => notify(errorText(e)))
const saveFeed = async () => { if (!au.feedFromEnv && feed.value.trim() !== au.feedUrl) await saveUpdateSettings({ feedUrl: feed.value.trim() }) }
</script>
<template>
  <Toolbar :title="$t('nav.settings')" />
  <div class="min-h-0 flex-1 overflow-auto bg-muted/60 px-6 py-5"><div class="mx-auto max-w-[640px]">
    <Group :title="$t('language.groupTitle')" :help="$t('language.help')"><Row :label="$t('language.label')" :sub="$t('language.sub')"><Segmented v-model="locale" :options="[['vi', 'Tiếng Việt'], ['en', 'English']]" :aria-label="$t('language.label')" /></Row></Group>
    <Group :title="$t('settings.appearance')"><Row :label="$t('settings.theme.label')" :sub="$t('settings.theme.sub')"><Segmented v-model="theme" :options="themeOpts" :aria-label="$t('settings.theme.label')" /></Row>
      <Row :label="$t('ux.settings.hintTitle')" :sub="$t('ux.settings.hintSub')"><Switch v-model="showHint" :aria-label="$t('ux.settings.hintTitle')" /></Row></Group>

    <Group v-if="s" :title="$t('settings.runs.title')" :help="$t('settings.runs.help')">
      <Row :label="$t('settings.runs.engine')" :sub="$t('settings.runs.engineSub')"><Pick :model-value="defaultId" :options="engineOpts" class="w-48" @update:model-value="setDefault(engines.find((e) => e.id === $event)!)" /></Row>
      <Row :label="$t('settings.runs.delay')" :sub="$t('settings.runs.delaySub')"><Input v-model="delay" type="number" min="0" max="600000" step="100" class="w-28 text-right tabular-nums" @change="saveNumbers" /></Row>
      <Row :label="$t('settings.runs.concurrency')" :sub="$t('settings.runs.concurrencySub')"><Input v-model="conc" type="number" min="1" max="64" class="w-28 text-right tabular-nums" @change="saveNumbers" /></Row>
      <Row :label="$t('settings.runs.proxyTest')" :sub="$t('settings.runs.proxyTestSub')"><Input v-model="testUrl" class="w-72 font-mono text-xs" @change="save({ proxyTestUrl: testUrl })" /></Row>
    </Group>

    <Group v-if="folder" :title="$t('settings.dataFolder')" :help="$t('settings.dataFolderHelp', { size: fmtSize(usage / 1048576) })">
      <Row :label="$t('settings.location')"><Input :model-value="folder.path" readonly class="w-72 font-mono text-xs" :title="folder.path" /><Button variant="outline" :disabled="folder.overridden" @click="changeFolder">{{ $t('settings.change') }}</Button><Button variant="outline" @click="openFolder">{{ $t('settings.open') }}</Button></Row>
      <Row v-if="folder.overridden" :label="$t('settings.folderEnv')" />
      <div v-if="folder.pending" class="px-3 py-2"><Alert><TriangleAlert /><AlertDescription><p>{{ $t('settings.folderPending', { path: folder.pending }) }}</p><p class="mt-1">{{ $t('settings.folderNoMove') }}</p><Button size="sm" variant="outline" class="mt-2" @click="resetFolder">{{ $t('settings.folderReset') }}</Button></AlertDescription></Alert></div>
    </Group>

    <Group v-if="s" :title="$t('settings.logs.title')">
      <Row :label="$t('settings.logs.level')" :sub="$t('settings.logs.levelSub')"><Pick :model-value="s.logLevel" :options="logOpts" class="w-32" @update:model-value="save({ logLevel: $event })" /></Row>
      <Row :label="$t('settings.logs.folder')"><Button variant="outline" @click="openLogs">{{ $t('settings.open') }}</Button></Row>
    </Group>

    <Group :title="$t('settings.appUpdates')" :help="$t('settings.appUpdatesHelp') + ' ' + $t('settings.chanHelp.' + au.channel)">
      <Row :label="$t('settings.version')" :sub="au.configured ? $t('settings.lastChecked', { when: fmtChecked() }) : $t('settings.notConfiguredSub')"><span class="tabular-nums text-muted-foreground">{{ au.current }}</span><Badge variant="outline" :class="au.rel ? 'text-warning' : au.configured ? 'text-success' : 'text-muted-foreground'">{{ status }}</Badge>
        <Button variant="outline" :disabled="au.phase === 'checking' || !au.configured" @click="checkNow"><Loader v-if="au.phase === 'checking'" class="animate-spin" />{{ au.rel && au.phase !== 'checking' ? $t('settings.viewUpdate') : $t('settings.checkNow') }}</Button></Row>
      <Row :label="$t('settings.feed')" :sub="au.feedFromEnv ? $t('settings.feedEnv') : $t('settings.feedSub')"><Input :model-value="au.feedFromEnv ? au.feedUrl : feed" :placeholder="$t('settings.feedPlaceholder')" :readonly="au.feedFromEnv" class="w-72 font-mono text-xs" @update:model-value="feed = String($event)" @change="saveFeed" /></Row>
      <Row :label="$t('settings.channel')" :sub="au.channel !== 'stable' ? $t('settings.caution.' + au.channel) : undefined"><Segmented :model-value="au.channel" :options="[['stable', t('appUpdate.channel.stable')], ['beta', t('appUpdate.channel.beta')], ['nightly', t('appUpdate.channel.nightly')]]" @update:model-value="saveUpdateSettings({ channel: $event as 'stable' | 'beta' | 'nightly' })" /></Row>
      <Row :label="$t('settings.autoCheck')"><Pick :model-value="au.autoCheck" :options="checkOpts" class="w-28" @update:model-value="saveUpdateSettings({ autoCheck: $event })" /></Row>
      <Row :label="$t('settings.installOnQuit')" :sub="$t('settings.installOnQuitSub')"><Switch :model-value="au.installOnQuit" :aria-label="$t('settings.installOnQuit')" @update:model-value="saveUpdateSettings({ installOnQuit: $event })" /></Row>
      <Row :label="$t('settings.skipVersion')" :sub="au.rel ? $t('settings.skipHide', { version: au.rel.version }) : au.skipped ? $t('settings.skipped', { version: au.skipped }) : $t('settings.noSkip')"><Button variant="outline" :disabled="!au.rel || au.phase === 'downloading'" @click="skipVersion">{{ $t('settings.skip') }}</Button></Row>
      <Row :label="$t('settings.releaseNotes')"><button class="flex items-center gap-0.5 text-link hover:underline disabled:opacity-50" :disabled="!au.rel" @click="au.sheet = true">{{ $t('settings.whatsNew') }}<ChevronRight class="size-3.5" /></button></Row>
    </Group>

    <Group :title="$t('settings.secrets')"><Row :label="$t('settings.credManager')" :sub="$t('settings.credManagerSub')"><Lock class="size-4 text-muted-foreground" /></Row></Group>
  </div></div>
</template>
