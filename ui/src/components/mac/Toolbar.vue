<script setup lang="ts">
// Window toolbar: 52px, title left, controls right, hairline bottom. Vibrancy applies here only.
defineProps<{ title: string; sub?: string }>()
</script>
<template>
  <header class="vibrancy-toolbar flex h-[52px] shrink-0 items-center gap-3 border-b px-5">
    <h1 class="shrink-0 text-lg font-semibold">{{ title }}</h1><span v-if="sub" class="truncate text-xs text-muted-foreground">{{ sub }}</span>
    <div class="flex min-w-0 flex-1 items-center justify-end gap-2"><slot /></div>
  </header>
</template>
