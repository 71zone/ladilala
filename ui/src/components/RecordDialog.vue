<script setup lang="ts">
import { computed, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Circle, Columns3, Lock, Pause, Play, Square, TriangleAlert } from '@lucide/vue'
import {
  discardRecording, emptyMacro, listRecordProfiles, onRecordHint, onRecordState, onRecordStep, onRecordSteps, onRecordWarning, originOf, pauseRecording, resumeRecording, saveMacro, startRecording, stopRecording,
  type Macro, type Problem, type RecordProfile, type RecordWarning, type RecordingDraft,
} from '@/bridge/macros'
import { usedVars } from '@/lib/columns'
import { hintOff, reach, setHintOff } from '@/lib/coach'
import { useLocale } from '@/composables/useLocale'
import { useTemplate } from '@/composables/useTemplate'
import { errorText, isCode } from '@/macros'
import { notify } from '@/store'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import Group from '@/components/mac/Group.vue'
import Pick from '@/components/mac/Pick.vue'
import Row from '@/components/mac/Row.vue'
import StateDot from '@/components/mac/StateDot.vue'
import DataBar from '@/components/DataBar.vue'
import StepTable from '@/components/StepTable.vue'

// The Record flow: choose a profile and a start URL, record (type {{name}} in a page field to make a data
// column), stop, review the steps and the data columns, download the template, save.
const open = defineModel<boolean>('open', { default: false })
const emit = defineEmits<{ saved: [id: string] }>()
const { t } = useI18n()
const { locale } = useLocale()
const template = useTemplate()

type Phase = 'setup' | 'starting' | 'recording' | 'review'
const phase = ref<Phase>('setup')
const profiles = ref<RecordProfile[]>([])
const loadingProfiles = ref(false)
const f = reactive({ profileId: '', startUrl: 'https://', name: '' })
const recordId = ref('')
const live = reactive<Macro>(emptyMacro())
const warnings = ref<RecordWarning[]>([])
const paused = ref(false)
const error = ref('')
const draft = ref<RecordingDraft | null>(null)
const problems = ref<Problem[]>([])
const busy = ref(false)
const ph = (n: string) => '{' + '{' + n + '}' + '}'

const profileOpts = computed(() => profiles.value.map(p => ({ value: p.id, label: p.name + (p.group ? ' · ' + p.group : '') + (p.running ? ' · ' + t('record.inUse') : '') })))
const picked = computed(() => profiles.value.find(p => p.id === f.profileId))
const urlOk = computed(() => !!originOf(f.startUrl.trim()))
const canStart = computed(() => !!picked.value && urlOk.value && !picked.value.running && !busy.value)
const active = computed(() => phase.value === 'starting' || phase.value === 'recording')
const mine = (id: string) => !recordId.value || id === recordId.value

const liveColumns = computed(() => usedVars(live).size)
const offs = [
  onRecordStep(e => { if (mine(e.recordId) && active.value) live.steps[e.stepIndex] = e.step }),
  onRecordSteps(e => { if (mine(e.recordId) && active.value) live.steps = e.steps }),
  onRecordHint(e => { if (mine(e.recordId) && e.never) setHintOff(true) }),
  onRecordWarning(e => { if (mine(e.recordId) && active.value) warnings.value.push({ kind: e.kind, stepIndex: e.stepIndex, locator: e.locator, message: e.message }) }),
  onRecordState(e => {
    if (!mine(e.recordId) || !active.value) return
    paused.value = e.state === 'paused'
    if (e.state === 'stopped' || e.state === 'closed') void finish()
  }),
]
onUnmounted(() => offs.forEach(off => off()))

function reset() {
  Object.assign(live, emptyMacro())
  Object.assign(f, { startUrl: 'https://', name: '' })
  phase.value = 'setup'
  recordId.value = ''
  warnings.value = []
  paused.value = false
  error.value = ''
  draft.value = null
  problems.value = []
  busy.value = false
}
watch(open, async o => {
  if (!o) return
  reset()
  loadingProfiles.value = true
  try {
    profiles.value = await listRecordProfiles()
    f.profileId = (profiles.value.find(p => !p.running) ?? profiles.value[0])?.id ?? ''
  } catch (e) { error.value = errorText(e) } finally { loadingProfiles.value = false }
})

async function start() {
  if (!canStart.value) return
  error.value = ''
  busy.value = true
  phase.value = 'starting'
  live.steps = []
  warnings.value = []
  try {
    recordId.value = await startRecording({ profileId: f.profileId, startUrl: f.startUrl.trim(), name: f.name.trim(), lang: locale.value, hint: !hintOff() })
    phase.value = 'recording'
  } catch (e) {
    phase.value = 'setup'
    error.value = errorText(e)
  } finally { busy.value = false }
}
async function togglePause() {
  try {
    if (paused.value) await resumeRecording(recordId.value)
    else await pauseRecording(recordId.value)
    paused.value = !paused.value
  } catch (e) { error.value = errorText(e) }
}
async function finish() {
  if (phase.value === 'review' || busy.value) return
  busy.value = true
  try {
    const d = await stopRecording(recordId.value)
    draft.value = d
    problems.value = d.problems
    warnings.value = d.warnings
    if (!d.macro.name || d.macro.name === d.macro.id) d.macro.name = f.name.trim() || t('record.defaultName', { date: new Date().toLocaleDateString() })
    phase.value = 'review'
    reach(3)
  } catch (e) {
    error.value = errorText(e)
    phase.value = 'setup'
  } finally { busy.value = false }
}
async function discard() {
  if (active.value && recordId.value) {
    try { await discardRecording(recordId.value) } catch { /* already gone */ }
  }
  open.value = false
}
async function save() {
  if (!draft.value || busy.value) return
  error.value = ''
  busy.value = true
  try {
    const dto = await saveMacro({ macro: draft.value.macro, description: '', warnings: warnings.value })
    notify(t('record.saved', { name: dto.macro.name }))
    open.value = false
    emit('saved', dto.macro.id)
  } catch (e) {
    error.value = isCode(e, 'conflict') ? t('record.nameTaken') : errorText(e)
  } finally { busy.value = false }
}
// The coach card sits outside the dialog: a click on it must not close the dialog.
const inCoach = (e: Event) => !!((e as CustomEvent).detail?.originalEvent?.target as HTMLElement | undefined)?.closest?.('[data-coach-card]')
const guard = (e: Event) => { if (active.value || inCoach(e)) e.preventDefault() }
function onOpen(v: boolean) { if (v) open.value = true; else if (!active.value) open.value = false }
</script>
<template>
  <Dialog :open="open" @update:open="onOpen">
    <DialogContent class="sm:max-w-3xl" @interact-outside="guard" @escape-key-down="guard">
      <DialogHeader>
        <DialogTitle>{{ phase === 'review' ? $t('record.reviewTitle') : $t('record.title') }}</DialogTitle>
        <DialogDescription>{{ phase === 'setup' ? $t('record.setupHelp') : phase === 'review' ? $t('record.reviewHelp') : $t('record.liveHelp') }}</DialogDescription>
      </DialogHeader>
      <Alert v-if="error" variant="destructive"><AlertDescription>{{ error }}</AlertDescription></Alert>
      <div v-if="phase === 'setup' || phase === 'starting'">
        <Group>
          <Row :label="$t('record.profile')" :sub="picked?.running ? $t('record.profileRunning') : $t('record.profileHelp')"><Pick v-model="f.profileId" :options="profileOpts" :placeholder="loadingProfiles ? $t('record.loading') : $t('record.noProfiles')" /></Row>
          <Row :label="$t('record.startUrl')" :sub="f.startUrl.length > 8 && !urlOk ? $t('record.urlBad') : $t('record.urlHelp')"><Input v-model="f.startUrl" class="w-72 font-mono text-xs" :aria-invalid="f.startUrl.length > 8 && !urlOk" @keydown.enter="start" /></Row>
          <Row :label="$t('record.name')" :sub="$t('record.nameHelp')"><Input v-model="f.name" class="w-72" @keydown.enter="start" /></Row>
        </Group>
        <p class="mt-2 px-1 text-xs text-muted-foreground">{{ $t('ux.record.setupTip', { ph: ph('email') }) }}</p>
        <p v-if="!loadingProfiles && !profiles.length" class="px-1 text-xs text-muted-foreground">{{ $t('record.noProfilesHelp') }}</p>
      </div>
      <template v-else>
        <div v-if="phase === 'recording'" class="flex items-center border-b pb-2 text-xs" :class="paused ? 'text-muted-foreground' : 'text-destructive'"><StateDot :s="paused ? 'warn' : 'checking'" />{{ paused ? $t('record.paused') : $t('record.recording', { profile: picked?.name ?? '' }) }}<span class="ml-auto tabular-nums text-muted-foreground">{{ $t('macros.stepsCount', { n: live.steps.length }, live.steps.length) }}</span></div>
        <div v-if="phase === 'recording'" data-coach="live" class="rounded-xl bg-muted/50 px-3 py-2 text-xs ring-1 ring-border">
          <p>{{ $t('ux.record.liveTip', { ph: ph('email') }) }}</p>
          <p class="mt-1 flex items-center gap-1.5 text-muted-foreground"><Columns3 class="size-3.5" />{{ $t('ux.bar.columns', { n: liveColumns }, liveColumns) }}</p>
        </div>
        <DataBar v-if="phase === 'review' && draft" :macro="draft.macro" @template="fmt => template({ macro: draft!.macro }, draft!.macro.name, fmt)" />
        <div v-if="phase === 'review' && draft && draft.columns.length" class="flex flex-wrap gap-1.5 px-1" data-testid="column-pills">
          <Badge v-for="c in draft.columns" :key="c.name" variant="outline" class="font-mono"><Lock v-if="c.protected" class="size-3" /><Columns3 v-else class="size-3" />{{ c.name }}<span class="font-sans text-muted-foreground">{{ $t('ux.kind.' + c.kind) }} · {{ $t('macros.stepsCount', { n: c.steps }, c.steps) }}</span></Badge>
        </div>
        <div class="max-h-[50vh] min-h-40 overflow-auto rounded-xl ring-1 ring-border">
          <StepTable :macro="draft ? draft.macro : live" :warnings="warnings" :problems="problems" readonly />
        </div>
        <Alert v-if="warnings.length"><TriangleAlert /><AlertDescription>{{ $t('record.warningHelp', { n: warnings.length }, warnings.length) }}</AlertDescription></Alert>
        <div v-if="phase === 'review'" class="grid gap-1">
          <label class="text-sm" for="rec-name">{{ $t('record.name') }}</label>
          <Input id="rec-name" v-if="draft" v-model="draft.macro.name" :aria-invalid="!draft.macro.name.trim()" />
          <ul v-if="problems.length" class="mt-1 text-xs text-destructive"><li v-for="p in problems" :key="p.path">{{ p.path }}: {{ p.msg }}</li></ul>
        </div>
      </template>
      <DialogFooter>
        <template v-if="phase === 'setup' || phase === 'starting'">
          <Button variant="outline" @click="open = false">{{ $t('common.cancel') }}</Button>
          <Button :disabled="!canStart" @click="start"><Circle class="size-3.5 text-destructive-foreground" />{{ phase === 'starting' ? $t('record.starting') : $t('record.start') }}</Button>
        </template>
        <template v-else-if="phase === 'recording'">
          <Button variant="outline" @click="discard">{{ $t('record.discard') }}</Button>
          <Button variant="outline" @click="togglePause"><Play v-if="paused" class="size-3.5" /><Pause v-else class="size-3.5" />{{ paused ? $t('common.resume') : $t('common.pause') }}</Button>
          <Button :disabled="busy" @click="finish"><Square class="size-3.5" />{{ $t('macros.stopRecording') }}</Button>
        </template>
        <template v-else>
          <Button variant="outline" @click="open = false">{{ $t('record.discard') }}</Button>
          <Button :disabled="busy || !draft || !draft.macro.name.trim() || problems.length > 0" @click="save">{{ $t('record.save') }}</Button>
        </template>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
