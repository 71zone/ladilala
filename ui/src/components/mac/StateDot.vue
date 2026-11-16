<script setup lang="ts">
// Real state only (running, error, warn, ok). Never decoration.
defineProps<{ s: string }>()
const C: Record<string, string> = { running: 'bg-success', ok: 'bg-success', error: 'bg-destructive', down: 'bg-destructive', fail: 'bg-destructive', warn: 'bg-warning', checking: 'bg-warning animate-pulse motion-reduce:animate-none' }
</script>
<template><span :class="['mr-1.5 inline-block size-2 shrink-0 rounded-full', C[s] || 'bg-idle']" /></template>
