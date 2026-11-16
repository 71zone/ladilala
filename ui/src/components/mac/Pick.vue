<script setup lang="ts">
import { computed } from 'vue'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
// Pop-up button: shadcn Select over a flat options list. Values may be strings or numbers.
// The trigger text is rendered here (not read from the option DOM) so it follows the language switch.
const props = defineProps<{ modelValue: string | number; options: { value: string | number; label: string }[]; placeholder?: string }>(); defineEmits<{ 'update:modelValue': [any] }>()
const current = computed(() => props.options.find(o => o.value === props.modelValue)?.label ?? '')
</script>
<template>
  <Select :model-value="modelValue" @update:model-value="$emit('update:modelValue', $event)">
    <SelectTrigger class="min-w-0"><SelectValue :placeholder="placeholder">{{ current }}</SelectValue></SelectTrigger>
    <SelectContent><SelectItem v-for="o in options" :key="o.value" :value="o.value">{{ o.label }}</SelectItem></SelectContent>
  </Select>
</template>
