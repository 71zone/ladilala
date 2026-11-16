<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { cn } from 'cn'

const props = defineProps<{
  class?: HTMLAttributes['class']
}>()
</script>

<template>
  <tr
    data-slot="table-row"
    :class="cn('hover:bg-accent data-[state=selected]:bg-primary/12 data-[state=selected]:text-link border-b border-border/70 transition-colors has-aria-expanded:bg-muted/50', props.class)"
  >
    <slot />
  </tr>
</template>
