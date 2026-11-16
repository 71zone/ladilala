<script setup lang="ts">
import { ChevronDown, X } from '@lucide/vue'
// One field of the fake web page (not app chrome). data-field is the drop target used by the pointer-event drag in MappingView.
defineProps<{ id: string; label: string; ph: string; select?: boolean; wide?: boolean; bound?: string; hover: boolean; armed: boolean; value: string }>()
defineEmits<{ pick: []; unbind: []; enter: []; leave: [] }>()
</script>
<template>
  <div :data-field="id" :class="['relative -m-1 rounded-md p-1', wide && 'col-span-2', hover && 'bg-blue-100 outline-2 outline-blue-600 outline-dashed', armed && 'cursor-crosshair']" @pointerenter="$emit('enter')" @pointerleave="$emit('leave')" @click="$emit('pick')">
    <label class="mb-1 block text-xs text-zinc-600">{{ label }}</label>
    <div :class="['flex h-8 items-center rounded-[5px] border bg-white px-2.5 text-[13px]', bound ? 'border-emerald-600 bg-emerald-50' : 'border-zinc-300']">
      <span v-if="value" class="text-zinc-900">{{ value }}</span><span v-else class="text-zinc-500">{{ ph }}</span><ChevronDown v-if="select" class="ml-auto size-3.5 text-zinc-500" />
    </div>
    <span v-if="bound" class="absolute right-2 bottom-2.5 flex items-center gap-1 rounded-full bg-emerald-700 pr-1 pl-2 font-mono text-[11px] text-white">{{ '{' + bound + '}' }}<button class="cursor-pointer px-0.5" :aria-label="$t('mapping.removeBinding')" @click.stop="$emit('unbind')"><X class="size-3" /></button></span>
  </div>
</template>
