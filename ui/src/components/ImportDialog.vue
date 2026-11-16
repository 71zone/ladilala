<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { FileUp, ShieldAlert } from '@lucide/vue'
import { confirmImport, importMacro, targetLabel, varText, type ImportReview } from '@/bridge/macros'
import { errorText } from '@/macros'
import { notify } from '@/store'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import Group from '@/components/mac/Group.vue'
import Row from '@/components/mac/Row.vue'

// Import a macro file. Nothing is saved until the user has seen the origins and the commit points.
const open = defineModel<boolean>('open', { default: false })
const emit = defineEmits<{ imported: [id: string] }>()
const { t } = useI18n()
const review = ref<ImportReview | null>(null)
const fileName = ref('')
const error = ref('')
const busy = ref(false)
const input = ref<HTMLInputElement | null>(null)

watch(open, o => { if (o) { review.value = null; fileName.value = ''; error.value = ''; busy.value = false } })

async function chosen(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  ;(e.target as HTMLInputElement).value = ''
  if (!file) return
  fileName.value = file.name
  error.value = ''
  busy.value = true
  try { review.value = await importMacro(await file.text()) } catch (err) { error.value = errorText(err) } finally { busy.value = false }
}
async function confirm() {
  if (!review.value?.reviewId || busy.value) return
  busy.value = true
  try {
    const dto = await confirmImport(review.value.reviewId)
    notify(t('macros.import.done', { name: dto.macro.name }))
    open.value = false
    emit('imported', dto.macro.id)
  } catch (err) { error.value = errorText(err) } finally { busy.value = false }
}
const labelOf = (id: string) => { const s = review.value?.macro?.steps.find(x => x.id === id); return s ? t('macros.actions.' + s.action) + ' ' + targetLabel(s) : id }
</script>
<template>
  <Dialog v-model:open="open">
    <DialogContent class="sm:max-w-xl">
      <DialogHeader><DialogTitle>{{ $t('macros.import.title') }}</DialogTitle><DialogDescription>{{ $t('macros.import.help') }}</DialogDescription></DialogHeader>
      <Alert v-if="error" variant="destructive"><AlertDescription>{{ error }}</AlertDescription></Alert>
      <input ref="input" type="file" accept=".json,application/json" class="hidden" data-testid="import-file" @change="chosen">
      <div class="flex items-center gap-3"><Button variant="outline" :disabled="busy" @click="input?.click()"><FileUp class="size-3.5" />{{ $t('macros.import.choose') }}</Button><span class="truncate font-mono text-xs text-muted-foreground">{{ fileName || $t('macros.import.none') }}</span></div>
      <template v-if="review">
        <Alert v-if="review.problems.length" variant="destructive">
          <ShieldAlert /><AlertDescription>
            <div class="mb-1 font-medium">{{ $t('macros.import.problems', { n: review.problems.length }, review.problems.length) }}</div>
            <ul class="max-h-40 overflow-auto text-xs"><li v-for="p in review.problems" :key="p.path + p.msg"><span class="font-mono">{{ p.path }}</span>: {{ p.msg }}</li></ul>
          </AlertDescription>
        </Alert>
        <template v-else-if="review.macro">
          <Group :title="review.macro.name" :help="$t('macros.import.trust')">
            <Row :label="$t('macros.steps')"><span class="tabular-nums">{{ review.stepCount }}</span></Row>
            <Row :label="$t('macros.import.origins')" :sub="$t('macros.import.originsHelp')" stack><div class="flex flex-wrap gap-1.5"><Badge v-for="o in review.allowedOrigins" :key="o" variant="outline" class="font-mono">{{ o }}</Badge></div></Row>
            <Row :label="$t('macros.import.commits')" :sub="$t('macros.import.commitsHelp')" stack>
              <span v-if="!review.commitPointSteps.length" class="text-muted-foreground">{{ $t('macros.import.noCommits') }}</span>
              <ul v-else class="text-sm"><li v-for="id in review.commitPointSteps" :key="id">{{ labelOf(id) }}</li></ul>
            </Row>
            <Row :label="$t('macros.import.variables')" stack><div class="flex flex-wrap gap-1.5"><span v-if="!review.variables.length" class="text-muted-foreground">{{ $t('macros.import.noVariables') }}</span><Badge v-for="v in review.variables" :key="v" variant="outline" class="font-mono text-success">{{ varText(v) }}</Badge></div></Row>
          </Group>
        </template>
      </template>
      <DialogFooter><Button variant="outline" @click="open = false">{{ $t('common.cancel') }}</Button><Button :disabled="busy || !review?.reviewId" @click="confirm">{{ $t('macros.import.confirm') }}</Button></DialogFooter>
    </DialogContent>
  </Dialog>
</template>
