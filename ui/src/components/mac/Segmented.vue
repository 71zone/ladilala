<script setup lang="ts">
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
// macOS segmented control built from shadcn ToggleGroup (single selection, cannot be empty).
defineProps<{ modelValue: string; options: (string | [string, string])[] }>(); const emit = defineEmits<{ 'update:modelValue': [string] }>()
const k = (o: string | [string, string]) => (typeof o === 'string' ? o : o[0]); const l = (o: string | [string, string]) => (typeof o === 'string' ? o : o[1])
</script>
<template>
  <ToggleGroup type="single" size="sm" :model-value="modelValue" class="rounded-md bg-muted p-0.5 ring-1 ring-border" @update:model-value="(v: any) => v && emit('update:modelValue', v)">
    <ToggleGroupItem v-for="o in options" :key="k(o)" :value="k(o)" class="h-6 rounded-[5px] px-2.5 text-xs text-foreground/70 data-[state=on]:bg-card dark:data-[state=on]:bg-secondary data-[state=on]:text-foreground data-[state=on]:shadow-xs data-[state=on]:ring-1 data-[state=on]:ring-border">{{ l(o) }}</ToggleGroupItem>
  </ToggleGroup>
</template>
