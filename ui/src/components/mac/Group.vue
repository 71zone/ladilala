<script setup lang="ts">
// System Settings style grouped inset list: 10px container, 40px rows, hairlines, optional header and helper text.
defineProps<{ title?: string; help?: string }>()
</script>
<template>
  <section class="mb-5">
    <h3 v-if="title" class="mb-1.5 px-1 text-xs font-medium text-muted-foreground">{{ title }}</h3>
    <div class="overflow-hidden rounded-xl bg-card ring-1 ring-border [&>*+*]:border-t"><slot /></div>
    <p v-if="help" class="mt-1.5 px-1 text-xs text-muted-foreground">{{ help }}</p>
  </section>
</template>
