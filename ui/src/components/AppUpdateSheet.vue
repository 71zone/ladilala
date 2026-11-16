<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { fmtDate, fmtSize, fmtSpeed, fmtSec, fmtNum } from '@/format'
import { Check, X, Loader, Circle, TriangleAlert, ShieldCheck } from '@lucide/vue'
import { au, dlMb, totalMb, needsEngine, engineNote, startDownload, cancelDl, applyFix, restartNow, restartWhenIdle, cancelWhenIdle } from '@/appupdate'
import { notify } from '@/store'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Progress } from '@/components/ui/progress'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
// What's new + update flow for Ladilala itself. Engines (browser binaries) update on the Engines screen.
const router = useRouter()
const { t } = useI18n()
const toEngines = () => { au.sheet = false; router.push('/engines') }
const later = () => { au.sheet = false; notify(t('toasts.remindTomorrow')) }
const flow = () => ['downloading', 'ready', 'failed'].includes(au.phase)
const failKey = computed(() => ({ 'update.network': 'net', 'update.signature': 'sig', 'update.disk': 'disk' })[au.failure?.code ?? ''] ?? 'other')
const failFix = computed(() => (typeof au.failure?.details?.fixId === 'string' ? au.failure.details.fixId : 'retry'))
const detail = (s: { code?: string; params?: Record<string, unknown> }) => {
  if (s.code === 'downloaded' && typeof s.params?.bytes === 'number') return fmtSize(s.params.bytes / 1048576)
  if (s.code === 'signed') return t('appUpdate.detail.signed')
  if (s.code === 'staged') return t('appUpdate.detail.staged')
  return ''
}
const notes = computed(() => (au.rel && !au.rel.news.length && !au.rel.fixed.length ? au.rel.notes : ''))
</script>
<template>
  <Sheet v-model:open="au.sheet">
    <SheetContent class="w-[420px] gap-0 overflow-auto sm:max-w-[420px]">
      <template v-if="au.rel">
        <SheetHeader class="border-b pb-3">
          <SheetTitle class="flex items-center gap-2">Ladilala {{ au.rel.version }}<Badge v-if="au.rel.channel" variant="outline">{{ $t('appUpdate.channel.' + au.rel.channel) }}</Badge></SheetTitle>
          <SheetDescription>{{ $t('appUpdate.sheetDesc', { date: au.rel.publishedAt ? fmtDate(au.rel.publishedAt) : '-', size: au.rel.sizeBytes ? fmtSize(au.rel.sizeBytes / 1048576) : '-' }) }}</SheetDescription>
        </SheetHeader>
        <div class="min-h-0 flex-1 space-y-4 overflow-auto px-4 py-4">
          <section v-if="au.rel.news.length"><h4 class="mb-1 font-medium">{{ $t('appUpdate.new') }}</h4><ul class="list-disc space-y-0.5 pl-5 text-muted-foreground"><li v-for="n in au.rel.news" :key="n">{{ n }}</li></ul></section>
          <section v-if="au.rel.fixed.length"><h4 class="mb-1 font-medium">{{ $t('appUpdate.fixed') }}</h4><ul class="list-disc space-y-0.5 pl-5 text-muted-foreground"><li v-for="n in au.rel.fixed" :key="n">{{ n }}</li></ul></section>
          <section v-if="notes"><h4 class="mb-1 font-medium">{{ $t('appUpdate.new') }}</h4><p class="whitespace-pre-line text-muted-foreground">{{ notes }}</p></section>
          <section><h4 class="mb-1 font-medium">{{ $t('appUpdate.engineCompat') }}</h4><p class="text-muted-foreground">{{ engineNote }}</p></section>

          <Alert v-if="needsEngine" variant="destructive"><TriangleAlert /><AlertTitle>{{ $t('appUpdate.needsEngineTitle', { n: au.rel.minEngine, installed: au.engineMajor || '?' }) }}</AlertTitle>
            <AlertDescription><p>{{ $t('appUpdate.needsEngineDesc') }}</p><Button size="sm" variant="outline" class="mt-2" @click="toEngines">{{ $t('appUpdate.goEngines') }}</Button></AlertDescription></Alert>

          <section v-if="flow()" class="rounded-xl bg-card p-3 ring-1 ring-border">
            <ul class="space-y-1.5">
              <li v-for="s in au.stages" :key="s.key" class="flex items-center gap-2">
                <Check v-if="s.state === 'done'" class="size-4 text-success" /><X v-else-if="s.state === 'fail'" class="size-4 text-destructive" /><Loader v-else-if="s.state === 'active'" class="size-4 animate-spin" /><Circle v-else class="size-4 text-muted-foreground/50" />
                <span :class="s.state === 'pending' ? 'text-muted-foreground' : ''">{{ $t('appUpdate.stage.' + s.key) }}</span><span class="ml-auto truncate text-xs text-muted-foreground">{{ detail(s) }}</span>
              </li>
            </ul>
            <div v-if="au.phase === 'downloading' && au.stages[1]?.state === 'active'" class="mt-3">
              <Progress :model-value="au.pct" />
              <div class="mt-1.5 flex items-center text-xs text-muted-foreground"><span class="tabular-nums">{{ $t('appUpdate.dlLine', { got: fmtNum(dlMb, { maximumFractionDigits: 0 }), total: fmtSize(totalMb), speed: fmtSpeed(au.speed), eta: au.eta ? $t('appUpdate.eta', { s: fmtSec(au.eta) }) : '' }) }}</span><span class="flex-1" />
                <Button size="sm" variant="ghost" @click="cancelDl">{{ $t('common.cancel') }}</Button></div>
            </div>
          </section>

          <Alert v-if="au.failure" variant="destructive"><TriangleAlert /><AlertTitle>{{ $t('appUpdate.fail.' + failKey + '.title') }}</AlertTitle>
            <AlertDescription><p>{{ $t('appUpdate.fail.' + failKey + '.why') }}</p><p class="mt-1 text-success">{{ $t('appUpdate.untouched', { version: au.current }) }}</p><Button size="sm" class="mt-2" @click="applyFix(failFix)">{{ $t('appUpdate.fail.' + failKey + '.fix') }}</Button></AlertDescription></Alert>

          <Alert v-if="au.phase === 'ready'"><ShieldCheck /><AlertTitle>{{ $t('appUpdate.readyTitle') }}</AlertTitle>
            <AlertDescription><p>{{ au.activeRuns > 0 ? $t('appUpdate.readyBusy', { runs: au.activeRuns }, au.activeRuns) : $t('appUpdate.readyDesc') }}</p>
              <p v-if="au.whenIdle" class="mt-1 text-foreground">{{ $t('appUpdate.waitingIdle') }}</p>
              <p v-else-if="au.installOnQuit" class="mt-1">{{ $t('appUpdate.installOnQuit') }}</p></AlertDescription></Alert>
        </div>
        <div class="flex gap-2 border-t p-4">
          <template v-if="au.phase === 'available'"><Button @click="startDownload">{{ $t('appUpdate.updateNow') }}</Button><Button variant="outline" @click="later">{{ $t('appUpdate.later') }}</Button></template>
          <template v-else-if="au.phase === 'ready'"><Button :disabled="au.restarting || au.activeRuns > 0" @click="restartNow"><Loader v-if="au.restarting" class="animate-spin" />{{ $t('appUpdate.restartNow') }}</Button>
            <Button v-if="!au.whenIdle" variant="outline" @click="restartWhenIdle">{{ $t('appUpdate.restartIdle') }}</Button><Button v-else variant="outline" @click="cancelWhenIdle">{{ $t('appUpdate.cancelIdle') }}</Button></template>
          <template v-else-if="au.phase === 'failed'"><Button variant="outline" @click="au.sheet = false">{{ $t('common.close') }}</Button></template>
          <template v-else><Button variant="outline" @click="au.sheet = false">{{ $t('common.hide') }}</Button></template>
        </div>
      </template>
      <template v-else>
        <SheetHeader><SheetTitle>Ladilala {{ au.current }}</SheetTitle><SheetDescription>{{ au.configured ? $t('appUpdate.newest', { channel: $t('appUpdate.channel.' + au.channel) }) : $t('appUpdate.notConfigured') }}</SheetDescription></SheetHeader>
      </template>
    </SheetContent>
  </Sheet>
</template>
