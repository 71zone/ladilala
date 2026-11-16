<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ArrowDown, ArrowUp, ChevronDown, ChevronRight, Columns3, Lock, Plus, Trash2, TriangleAlert } from '@lucide/vue'
import { refsOf, targetLabel, type Locator, type LocatorKind, type Macro, type OnFail, type Problem, type RecordWarning, type Step } from '@/bridge/macros'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { PASSWORD_VAR, previewValue } from '@/lib/columns'
import { isPasswordStep } from '@/lib/matchColumns'
import Pick from '@/components/mac/Pick.vue'
import StateDot from '@/components/mac/StateDot.vue'

// The step list of a macro. Editable: reorder, switch off, delete, edit the value and the locator order.
// readonly shows the same table for a live recording and for the review.
// columns, rows and rowIndex feed the live preview (what each step types for the chosen data row);
const props = defineProps<{ macro: Macro; warnings?: RecordWarning[]; problems?: Problem[]; columns?: string[]; rows?: string[][]; rowIndex?: number; overrides?: Record<string, string>; readonly?: boolean }>()
const emit = defineEmits<{ applyColumn: [stepId: string, column: string] }>()
const open = defineModel<string | null>('selected', { default: null })
const { t } = useI18n()

const KINDS: LocatorKind[] = ['label', 'role', 'text', 'testid', 'css', 'xpath']
const kindOpts = computed(() => KINDS.map(k => ({ value: k, label: t('macros.locatorKinds.' + k) })))
const failOpts = computed(() => (['stop', 'skip', 'pause'] as OnFail[]).map(v => ({ value: v, label: t('macros.onFailKinds.' + v) })))
const VALUE_ACTIONS = ['navigate', 'fill', 'select', 'press', 'uploadFile', 'scroll', 'manual']
const COMMIT_ACTIONS = ['click', 'press', 'fill']
const CHOOSE = '__choose'
const colOpts = computed(() => [{ value: CHOOSE, label: t('ux.step.chooseColumn') }, ...(props.columns ?? []).map(c => ({ value: c, label: c }))])
const pickColumn = (s: Step, v: string | number) => { if (v !== CHOOSE) emit('applyColumn', s.id, String(v)) }
const previewOf = (s: Step) => (props.columns?.length ? previewValue(s, props.columns, props.rows?.[props.rowIndex ?? 0], props.overrides) : null)

const problemsOf = (s: Step, i: number) => (props.problems ?? []).filter(p => p.path.startsWith(`steps[${s.id}]`) || p.path.startsWith(`steps[${i}]`))
const warningsAt = (i: number) => (props.warnings ?? []).filter(w => w.stepIndex === i)
const trailing = computed(() => (props.warnings ?? []).filter(w => w.stepIndex >= props.macro.steps.length))
// A value made only of data columns needs no raw text next to its pills.
const pureRefs = (s: Step) => !!s.value && refsOf(s.value.expr).length > 0 && s.value.expr.replace(/\{\{[^}]*\}\}/g, '').trim() === ''
const hasValue = (s: Step) => VALUE_ACTIONS.includes(s.action)

function move(i: number, d: number) {
  const a = props.macro.steps
  const j = i + d
  if (j < 0 || j >= a.length) return
  ;[a[i], a[j]] = [a[j], a[i]]
}
function remove(i: number) {
  const [gone] = props.macro.steps.splice(i, 1)
  if (open.value === gone.id) open.value = null
}
function setValue(s: Step, v: string) {
  if (s.value) s.value.expr = v
  else s.value = { expr: v }
}
function setNumber(s: Step, key: 'retries', v: string | number) {
  const n = Math.max(0, Math.min(10, Math.trunc(Number(v)) || 0))
  if (n) s[key] = n
  else delete s[key]
}
function setText(s: Step, key: 'timeout', v: string | number) {
  const x = String(v).trim()
  if (x) s[key] = x
  else delete s[key]
}
function setFail(s: Step, v: OnFail) {
  if (v === 'stop') delete s.onFail
  else s.onFail = v
}
function setCommit(s: Step, v: boolean) {
  if (v) s.commitPoint = true
  else delete s.commitPoint
}
function setEnabled(s: Step, on: boolean) {
  if (on) delete s.disabled
  else s.disabled = true
}
function moveLocator(s: Step, i: number, d: number) {
  const a = s.target?.locators
  if (!a || i + d < 0 || i + d >= a.length) return
  ;[a[i], a[i + d]] = [a[i + d], a[i]]
}
function addLocator(s: Step) {
  if (!s.target) s.target = { locators: [] }
  s.target.locators.push({ kind: 'text', value: '' })
}
function setKind(l: Locator, k: LocatorKind) {
  l.kind = k
  if (k !== 'role') delete l.name
}
function toggle(s: Step) {
  open.value = open.value === s.id ? null : s.id
}
const canMap = (s: Step) => !props.readonly && (s.action === 'fill' || s.action === 'select') && !!s.value
</script>
<template>
  <Table class="w-full table-fixed">
    <TableHeader class="sticky top-0 z-10 bg-background">
      <TableRow class="hover:bg-transparent">
        <TableHead v-if="!readonly" class="w-[60px] pl-5" /><TableHead :class="readonly ? 'w-14 pl-5' : 'w-9'">#</TableHead>
        <TableHead v-if="!readonly" class="w-[52px]">{{ $t('macros.enabled') }}</TableHead>
        <TableHead class="w-[104px]">{{ $t('macros.action') }}</TableHead><TableHead class="w-[190px]">{{ $t('macros.target') }}</TableHead><TableHead>{{ $t('macros.value') }}</TableHead>
        <TableHead class="w-[84px]">{{ $t('macros.flags') }}</TableHead><TableHead v-if="!readonly" class="w-[84px] pr-5" />
      </TableRow>
    </TableHeader>
    <TableBody>
      <TableRow v-if="!macro.steps.length && !trailing.length" class="hover:bg-transparent"><TableCell :colspan="readonly ? 5 : 8" class="h-16 pl-5 text-muted-foreground">{{ $t('macros.noSteps') }}</TableCell></TableRow>
      <template v-for="(s, i) in macro.steps" :key="s.id">
        <TableRow v-for="(w, k) in warningsAt(i)" :key="'w' + i + k" class="bg-warning/10 hover:bg-warning/10">
          <TableCell :colspan="readonly ? 5 : 8" class="pl-5 text-xs"><TriangleAlert class="mr-1.5 inline size-3.5 text-warning" />{{ $t('macros.warnings.' + w.kind, { n: w.stepIndex + 1 }) }}<span v-if="w.locator" class="ml-1.5 text-muted-foreground">({{ w.locator.value }})</span></TableCell>
        </TableRow>
        <TableRow :class="['cursor-default', s.disabled && 'text-muted-foreground']" :data-state="open === s.id ? 'selected' : undefined" @click="!readonly && toggle(s)">
          <TableCell v-if="!readonly" class="pl-5" @click.stop>
            <div class="flex"><Button size="icon-xs" variant="ghost" :disabled="i === 0" :aria-label="$t('macros.moveUp')" :title="$t('macros.moveUp')" @click="move(i, -1)"><ArrowUp /></Button><Button size="icon-xs" variant="ghost" :disabled="i === macro.steps.length - 1" :aria-label="$t('macros.moveDown')" :title="$t('macros.moveDown')" @click="move(i, 1)"><ArrowDown /></Button></div>
          </TableCell>
          <TableCell :class="['tabular-nums text-muted-foreground', readonly && 'pl-5']">{{ i + 1 }}</TableCell>
          <TableCell v-if="!readonly" @click.stop><Switch :model-value="!s.disabled" :aria-label="$t('macros.enabled')" @update:model-value="setEnabled(s, !!$event)" /></TableCell>
          <TableCell><Badge variant="secondary" :class="s.disabled && 'opacity-60'">{{ $t('macros.actions.' + s.action) }}</Badge></TableCell>
          <TableCell :title="targetLabel(s)" class="truncate">{{ s.action === 'navigate' ? '' : targetLabel(s) }}<span v-if="s.target?.framePath?.length" class="ml-1 text-xs text-muted-foreground">{{ $t('macros.inFrame') }}</span></TableCell>
          <TableCell @click.stop>
            <div v-if="hasValue(s)" class="flex flex-wrap items-center gap-1.5">
              <Input v-if="!readonly" :model-value="s.value?.expr ?? ''" :aria-label="$t('macros.value')" class="h-6 min-w-0 flex-1 font-mono text-xs" :placeholder="$t('macros.valuePlaceholder')" @update:model-value="setValue(s, String($event))" />
              <span v-else-if="!pureRefs(s)" class="min-w-0 max-w-full truncate font-mono text-xs" :title="s.value?.expr">{{ s.value?.expr }}</span>
              <Badge v-for="v in refsOf(s.value?.expr ?? '')" :key="v" variant="outline" :title="$t('ux.step.dataColumn')" class="font-mono text-success"><Lock v-if="v === PASSWORD_VAR" class="size-3" /><Columns3 v-else class="size-3" />{{ v }}</Badge>
            </div>
            <div v-if="previewOf(s)" class="mt-0.5 truncate text-xs" :title="previewOf(s)!.text">
              <span v-if="previewOf(s)!.protected" class="text-muted-foreground"><Lock class="mr-1 inline size-3" />{{ $t('ux.step.protected') }}</span>
              <span v-else-if="previewOf(s)!.missing" class="text-destructive">{{ $t('ux.step.noColumn', { v: previewOf(s)!.missing }) }}</span>
              <span v-else class="font-mono text-muted-foreground">{{ '→ ' + previewOf(s)!.text }}</span>
            </div>
            <span v-else-if="s.timeout" class="text-xs text-muted-foreground">{{ s.timeout }}</span>
          </TableCell>
          <TableCell>
            <TriangleAlert v-if="s.commitPoint" class="mr-1 inline size-3.5 text-warning" :aria-label="$t('macros.commitPoint')" :title="$t('macros.commitPoint')" />
            <StateDot v-if="problemsOf(s, i).length" s="fail" :title="problemsOf(s, i).map(p => p.path + ': ' + p.msg).join('\n')" />
            <Badge v-if="s.expect?.length" variant="ghost" class="px-1" :title="$t('macros.expectCount', { n: s.expect.length }, s.expect.length)">{{ s.expect.length }}</Badge>
          </TableCell>
          <TableCell v-if="!readonly" class="pr-5 text-right" @click.stop>
            <Button size="icon-xs" variant="ghost" :aria-label="$t('macros.advanced')" :title="$t('macros.advanced')" @click="toggle(s)"><ChevronDown v-if="open === s.id" /><ChevronRight v-else /></Button>
            <Button size="icon-xs" variant="ghost" class="text-destructive" :aria-label="$t('macros.deleteStep')" :title="$t('macros.deleteStep')" @click="remove(i)"><Trash2 /></Button>
          </TableCell>
        </TableRow>
        <TableRow v-if="open === s.id && !readonly" class="bg-muted/50 hover:bg-muted/50">
          <TableCell colspan="8" class="px-5 py-3 whitespace-normal">
            <div v-if="s.target" class="mb-3">
              <div class="mb-1 text-xs font-medium text-muted-foreground">{{ $t('macros.locatorOrder') }}</div>
              <div v-for="(l, j) in s.target.locators" :key="j" class="mb-1 flex items-center gap-1.5">
                <span class="w-4 text-xs text-muted-foreground tabular-nums">{{ j + 1 }}</span>
                <Button size="icon-xs" variant="ghost" :disabled="j === 0" :aria-label="$t('macros.moveUp')" @click="moveLocator(s, j, -1)"><ArrowUp /></Button>
                <Button size="icon-xs" variant="ghost" :disabled="j === s.target.locators.length - 1" :aria-label="$t('macros.moveDown')" @click="moveLocator(s, j, 1)"><ArrowDown /></Button>
                <div class="w-28"><Pick :model-value="l.kind" :options="kindOpts" @update:model-value="setKind(l, $event)" /></div>
                <Input v-model="l.value" class="h-7 flex-1 font-mono text-xs" :aria-label="$t('macros.locatorValue')" />
                <Input v-if="l.kind === 'role'" :model-value="l.name ?? ''" class="h-7 w-40 text-xs" :placeholder="$t('macros.accessibleName')" :aria-label="$t('macros.accessibleName')" @update:model-value="l.name = String($event)" />
                <Button size="icon-xs" variant="ghost" class="text-destructive" :disabled="s.target.locators.length < 2" :aria-label="$t('macros.removeLocator')" @click="s.target.locators.splice(j, 1)"><Trash2 /></Button>
              </div>
              <Button size="xs" variant="outline" @click="addLocator(s)"><Plus />{{ $t('macros.addLocator') }}</Button>
              <p class="mt-1 text-xs text-muted-foreground">{{ $t('macros.locatorHelp') }}</p>
            </div>
            <div class="flex flex-wrap items-center gap-x-5 gap-y-2">
              <div v-if="COMMIT_ACTIONS.includes(s.action)" class="flex items-center gap-2"><Switch :id="'c' + s.id" :model-value="!!s.commitPoint" @update:model-value="setCommit(s, !!$event)" /><label :for="'c' + s.id" class="text-sm">{{ $t('macros.commitPoint') }}</label></div>
              <div class="flex items-center gap-2"><span class="text-sm">{{ $t('macros.onFail') }}</span><div class="w-32"><Pick :model-value="s.onFail ?? 'stop'" :options="failOpts" @update:model-value="setFail(s, $event)" /></div></div>
              <div class="flex items-center gap-2"><span class="text-sm">{{ $t('macros.retries') }}</span><Input type="number" min="0" max="10" :model-value="s.retries ?? 0" class="h-7 w-16" @update:model-value="setNumber(s, 'retries', $event)" /></div>
              <div class="flex items-center gap-2"><span class="text-sm">{{ $t('macros.timeout') }}</span><Input :model-value="s.timeout ?? ''" class="h-7 w-20 font-mono text-xs" placeholder="30s" @update:model-value="setText(s, 'timeout', $event)" /></div>
              <div v-if="canMap(s) && colOpts.length > 1" class="flex items-center gap-2">
                <span class="text-sm">{{ $t('ux.step.dataColumn') }}</span>
                <div class="w-44"><Pick :model-value="CHOOSE" :options="colOpts" @update:model-value="pickColumn(s, $event)" /></div>
              </div>
              <p v-if="canMap(s) && isPasswordStep(s)" class="basis-full text-xs text-muted-foreground"><Lock class="mr-1 inline size-3" />{{ $t('ux.step.protectedNote') }}</p>
            </div>
            <p v-if="s.expect?.length" class="mt-2 text-xs text-muted-foreground">{{ $t('macros.expectCount', { n: s.expect.length }, s.expect.length) }}: {{ s.expect.map(e => $t('macros.expectKinds.' + e.kind)).join(', ') }}</p>
          </TableCell>
        </TableRow>
      </template>
      <TableRow v-for="(w, k) in trailing" :key="'tw' + k" class="bg-warning/10 hover:bg-warning/10">
        <TableCell :colspan="readonly ? 5 : 8" class="pl-5 text-xs"><TriangleAlert class="mr-1.5 inline size-3.5 text-warning" />{{ $t('macros.warnings.' + w.kind, { n: w.stepIndex + 1 }) }}</TableCell>
      </TableRow>
    </TableBody>
  </Table>
</template>
