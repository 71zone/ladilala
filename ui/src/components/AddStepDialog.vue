<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ExpectKind, LocatorKind, Step } from '@/bridge/macros'
import { originOf } from '@/bridge/macros'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import Pick from '@/components/mac/Pick.vue'
import Segmented from '@/components/mac/Segmented.vue'

// Adds a wait, an assert or a navigate step. The editor gives it an ID and places it.
const open = defineModel<boolean>('open', { default: false })
const emit = defineEmits<{ add: [step: Omit<Step, 'id'>] }>()
const { t } = useI18n()

type Kind = 'wait' | 'assert' | 'navigate'
const kind = ref<Kind>('wait')
const f = reactive({ waitMode: 'time', timeout: '2s', locKind: 'text' as LocatorKind, locValue: '', expectKind: 'urlMatches' as ExpectKind, text: '', url: 'https://' })
watch(open, o => { if (o) Object.assign(f, { waitMode: 'time', timeout: '2s', locKind: 'text', locValue: '', expectKind: 'urlMatches', text: '', url: 'https://' }) })

const locOpts = computed(() => (['label', 'role', 'text', 'testid', 'css'] as LocatorKind[]).map(k => ({ value: k, label: t('macros.locatorKinds.' + k) })))
const expectOpts = computed(() => (['urlMatches', 'elementPresent', 'elementAbsent', 'textContains'] as ExpectKind[]).map(k => ({ value: k, label: t('macros.expectKinds.' + k) })))
const needsTarget = computed(() => kind.value === 'wait' ? f.waitMode === 'element' : f.expectKind !== 'urlMatches')
const needsText = computed(() => kind.value === 'assert' && (f.expectKind === 'urlMatches' || f.expectKind === 'textContains'))
const target = () => ({ locators: [{ kind: f.locKind, value: f.locValue.trim() }] })
const valid = computed(() => {
  if (kind.value === 'navigate') return !!originOf(f.url.trim())
  if (needsTarget.value && !f.locValue.trim()) return false
  if (needsText.value && !f.text.trim()) return false
  return kind.value === 'assert' || f.waitMode === 'element' || /^\d+(\.\d+)?(ms|s|m)$/.test(f.timeout.trim())
})

function add() {
  if (!valid.value) return
  if (kind.value === 'navigate') emit('add', { action: 'navigate', value: { expr: f.url.trim() } })
  else if (kind.value === 'wait') emit('add', f.waitMode === 'time' ? { action: 'wait', timeout: f.timeout.trim() } : { action: 'wait', target: target(), timeout: '10s' })
  else emit('add', { action: 'assert', expect: [{ kind: f.expectKind, ...(needsTarget.value ? { target: target() } : {}), ...(needsText.value ? { value: { expr: f.text.trim() } } : {}) }] })
  open.value = false
}
</script>
<template>
  <Dialog v-model:open="open">
    <DialogContent class="sm:max-w-lg">
      <DialogHeader><DialogTitle>{{ $t('macros.addStep') }}</DialogTitle><DialogDescription>{{ $t('macros.addStepHelp') }}</DialogDescription></DialogHeader>
      <div class="grid gap-3">
        <Segmented v-model="kind" :options="[['wait', t('macros.actions.wait')], ['assert', t('macros.actions.assert')], ['navigate', t('macros.actions.navigate')]]" />
        <template v-if="kind === 'navigate'">
          <label class="grid gap-1 text-sm">{{ $t('macros.addUrl') }}<Input v-model="f.url" class="font-mono text-xs" /></label>
          <p class="text-xs text-muted-foreground">{{ $t('macros.addOriginHelp') }}</p>
        </template>
        <template v-else>
          <Segmented v-if="kind === 'wait'" v-model="f.waitMode" :options="[['time', t('macros.waitTime')], ['element', t('macros.waitElement')]]" />
          <label v-if="kind === 'wait' && f.waitMode === 'time'" class="grid gap-1 text-sm">{{ $t('macros.timeout') }}<Input v-model="f.timeout" class="w-28 font-mono text-xs" placeholder="2s" /></label>
          <div v-if="kind === 'assert'" class="grid gap-1 text-sm">{{ $t('macros.assertWhat') }}<Pick v-model="f.expectKind" :options="expectOpts" /></div>
          <div v-if="needsTarget" class="grid grid-cols-[130px_1fr] items-end gap-2">
            <div class="grid gap-1 text-sm">{{ $t('macros.locatorKind') }}<Pick v-model="f.locKind" :options="locOpts" /></div>
            <label class="grid gap-1 text-sm">{{ $t('macros.locatorValue') }}<Input v-model="f.locValue" /></label>
          </div>
          <label v-if="needsText" class="grid gap-1 text-sm">{{ f.expectKind === 'urlMatches' ? $t('macros.urlPattern') : $t('macros.expectedText') }}<Input v-model="f.text" :class="f.expectKind === 'urlMatches' && 'font-mono text-xs'" /></label>
        </template>
      </div>
      <DialogFooter><Button variant="outline" @click="open = false">{{ $t('common.cancel') }}</Button><Button :disabled="!valid" @click="add">{{ $t('macros.addStep') }}</Button></DialogFooter>
    </DialogContent>
  </Dialog>
</template>
