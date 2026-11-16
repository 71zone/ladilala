<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { COACH_STEPS, coach, finishCoach } from '@/lib/coach'

// A spotlight on a real element, with an arrow card next to it. The element is found by its
// data-coach attribute; when the screen does not show it, nothing is drawn.
const STEP_ANCHOR: Record<number, string> = { 1: 'record', 2: 'live', 3: 'template', 4: 'run' }
const rect = ref<DOMRect | null>(null)
let timer = 0
function measure() {
  const el = coach.step ? document.querySelector<HTMLElement>(`[data-coach="${STEP_ANCHOR[coach.step]}"]`) : null
  const r = el && el.getClientRects().length ? el.getBoundingClientRect() : null
  const p = rect.value
  if (!r) { if (p) rect.value = null; return }
  if (!p || r.x !== p.x || r.y !== p.y || r.width !== p.width || r.height !== p.height) rect.value = r
}
onMounted(() => { measure(); timer = window.setInterval(measure, 150) })
onBeforeUnmount(() => window.clearInterval(timer))

const PAD = 6
const CARD_W = 300
const spot = computed(() => rect.value && { left: rect.value.left - PAD, top: rect.value.top - PAD, width: rect.value.width + PAD * 2, height: rect.value.height + PAD * 2 })
// The card sits below the element, or above it near the bottom of the window. Step 2 points at a box
// inside the Record dialog: the card goes to its right when there is room, so it hides no step. Step 3
// points at the template button above the step list: the card goes above it.
const side = computed<'right' | 'below' | 'above'>(() => {
  const r = rect.value
  if (!r) return 'below'
  if (coach.step === 2 && window.innerWidth - r.right > CARD_W + 24) return 'right'
  if (coach.step === 3 && r.top > 200) return 'above'
  return r.bottom + 190 < window.innerHeight ? 'below' : 'above'
})
const card = computed(() => {
  const r = rect.value
  if (!r) return {}
  if (side.value === 'right') return { left: r.right + PAD + 14 + 'px', top: Math.max(12, r.top) + 'px' }
  const left = Math.max(12, Math.min(window.innerWidth - CARD_W - 12, r.left + r.width / 2 - CARD_W / 2))
  return side.value === 'below' ? { left: left + 'px', top: r.bottom + PAD + 12 + 'px' } : { left: left + 'px', bottom: window.innerHeight - r.top + PAD + 12 + 'px' }
})
// The arrow is a rotated square on the card edge that faces the element.
const arrow = computed(() => {
  const r = rect.value
  if (!r) return {}
  const edge = '1px solid var(--border)'
  const along = Math.max(16, Math.min(CARD_W - 16, r.left + r.width / 2 - parseFloat(String(card.value.left ?? 0)))) - 6 + 'px'
  if (side.value === 'right') return { left: '-7px', top: '16px', borderLeft: edge, borderBottom: edge }
  if (side.value === 'below') return { left: along, top: '-7px', borderLeft: edge, borderTop: edge }
  return { left: along, bottom: '-7px', borderRight: edge, borderBottom: edge }
})
const ph = '{' + '{email}' + '}'
const last = computed(() => coach.step === COACH_STEPS)
// Step 1 and 2 move on by themselves when the user does the thing; "Next" on step 1 presses the real button.
function next() {
  if (coach.step === 1) document.querySelector<HTMLElement>('[data-coach="record"]')?.click()
  else if (last.value) finishCoach()
}
</script>
<template>
  <Teleport to="body">
    <div v-if="coach.step && spot" class="pointer-events-none fixed inset-0 z-[200]" data-coach-root>
      <div class="absolute rounded-lg ring-2 ring-primary transition-all duration-150" :style="{ left: spot.left + 'px', top: spot.top + 'px', width: spot.width + 'px', height: spot.height + 'px', boxShadow: '0 0 0 9999px rgba(0,0,0,.45)' }" />
      <div data-coach-card class="pointer-events-auto absolute rounded-xl bg-popover p-4 text-popover-foreground shadow-lg ring-1 ring-border" :style="{ ...card, width: CARD_W + 'px' }" role="dialog" :aria-label="$t('ux.coach.e.title.' + coach.step, { ph })">
        <span class="absolute size-3 rotate-45 bg-popover" :style="arrow" />
        <p class="text-xs text-muted-foreground tabular-nums">{{ $t('ux.coach.e.stepOf', { n: coach.step, total: COACH_STEPS }) }}</p>
        <p class="mt-1 text-sm font-medium">{{ $t('ux.coach.e.title.' + coach.step, { ph }) }}</p>
        <p class="mt-1 text-xs text-muted-foreground">{{ $t('ux.coach.e.body.' + coach.step, { ph }) }}</p>
        <div class="mt-3 flex justify-end gap-2">
          <Button size="xs" variant="ghost" @click="finishCoach">{{ $t('ux.coach.e.skip') }}</Button>
          <Button v-if="coach.step === 1 || last" size="xs" @click="next">{{ last ? $t('ux.coach.e.done') : $t('ux.coach.e.press') }}</Button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
