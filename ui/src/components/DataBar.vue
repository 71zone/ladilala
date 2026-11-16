<script setup lang="ts">
import { computed } from 'vue'
import { ChevronLeft, ChevronRight, Columns3, Database, FileSpreadsheet, FileText, TriangleAlert } from '@lucide/vue'
import { checkColumns, usedVars } from '@/lib/columns'
import type { Macro } from '@/bridge/macros-types'
import type { TemplateFormat } from '@/bridge/data'
import { Button } from '@/components/ui/button'

// The strip above the step list: how many data columns the macro reads, the template buttons,
// and, when a data file is linked, its name, the preview row and the missing and unused columns.
const props = defineProps<{ macro: Macro; columns?: string[]; rows?: string[][]; sourceName?: string; total?: number; overrides?: Record<string, string>; canChoose?: boolean }>()
defineEmits<{ choose: []; template: [format: TemplateFormat] }>()
const rowNo = defineModel<number>('rowNo', { default: 1 })

const cols = computed(() => props.columns ?? [])
const rows = computed(() => props.rows ?? [])
const names = computed(() => [...usedVars(props.macro)])
const check = computed(() => checkColumns(props.macro, cols.value, props.overrides))
const last = computed(() => Math.max(1, rows.value.length))
const step = (d: number) => { rowNo.value = Math.min(last.value, Math.max(1, rowNo.value + d)) }
</script>
<template>
  <div class="flex flex-wrap items-center gap-x-4 gap-y-1.5 border-b bg-muted/40 px-5 py-2 text-xs">
    <span class="flex items-center gap-1.5 font-medium" data-testid="column-count"><Columns3 class="size-3.5 text-muted-foreground" />{{ $t('ux.bar.columns', { n: names.length }, names.length) }}</span>
    <template v-if="names.length">
      <Button size="xs" data-coach="template" @click="$emit('template', 'xlsx')"><FileSpreadsheet class="size-3" />{{ $t('ux.tpl.excel') }}</Button>
      <Button size="xs" variant="outline" @click="$emit('template', 'csv')"><FileText class="size-3" />{{ $t('ux.tpl.csv') }}</Button>
    </template>
    <span v-else class="text-muted-foreground">{{ $t('ux.bar.none') }}</span>
    <template v-if="cols.length">
      <span class="flex items-center gap-1.5 font-medium"><Database class="size-3.5 text-muted-foreground" />{{ sourceName }}</span>
      <span class="flex items-center gap-1 text-muted-foreground" :aria-label="$t('ux.bar.row')">
        <Button size="icon-xs" variant="ghost" :disabled="rowNo <= 1" :aria-label="$t('ux.bar.prev')" @click="step(-1)"><ChevronLeft /></Button>
        <span class="min-w-24 text-center tabular-nums">{{ $t('ux.bar.rowOf', { n: rowNo, total: (total ?? 0) > rows.length ? total : rows.length }) }}</span>
        <Button size="icon-xs" variant="ghost" :disabled="rowNo >= last" :aria-label="$t('ux.bar.next')" @click="step(1)"><ChevronRight /></Button>
      </span>
      <span v-if="check.missing.length" class="text-destructive"><TriangleAlert class="mr-1 inline size-3" />{{ $t('ux.bar.missing', { v: check.missing.join(', ') }) }}</span>
      <span v-if="check.extra.length" class="text-muted-foreground">{{ $t('ux.bar.extra', { v: check.extra.join(', ') }) }}</span>
      <span v-if="!check.missing.length && check.bound.length" class="text-success">{{ $t('ux.bar.ok', { n: check.bound.length }, check.bound.length) }}</span>
    </template>
    <Button v-else-if="canChoose" size="xs" variant="outline" class="ml-auto" @click="$emit('choose')"><Database class="size-3" />{{ $t('ux.bar.choose') }}</Button>
  </div>
</template>
