<script setup lang="ts">
// One row of a Group: label (and optional sub-label) left, control right.
defineProps<{ label: string; sub?: string; stack?: boolean }>()
</script>
<template>
  <div :class="['flex min-h-10 gap-3 px-3 py-1.5', stack ? 'flex-col items-stretch' : 'items-center']">
    <div class="min-w-0 flex-1"><div class="truncate">{{ label }}</div><div v-if="sub" class="text-xs text-muted-foreground">{{ sub }}</div></div>
    <div :class="stack ? '' : 'flex min-w-0 max-w-[68%] shrink-0 items-center justify-end gap-2 [&>span]:min-w-0 [&>span]:truncate'"><slot /></div>
  </div>
</template>
