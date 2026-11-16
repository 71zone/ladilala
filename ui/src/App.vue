<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { Users, Globe, ListChecks, Play, Box, Settings, Moon, Sun, Search, TriangleAlert, Download } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { useLocale } from '@/composables/useLocale'
import { fmtPct, fmtSec } from '@/format'
import Segmented from '@/components/mac/Segmented.vue'
import { store, runningCount } from './store'
import { sim, SIMS, resetSim, healthIssues } from './engines'
import { au, AUS, setScenario, indicator } from './appupdate'
import { inWails } from './bridge/api'
import { isDark, toggleDark } from './theme'
import { SidebarProvider, Sidebar, SidebarContent, SidebarFooter, SidebarGroup, SidebarGroupLabel, SidebarGroupContent, SidebarHeader, SidebarMenu, SidebarMenuItem, SidebarMenuButton, SidebarMenuBadge } from '@/components/ui/sidebar'
import { TooltipProvider } from '@/components/ui/tooltip'
import { Toaster } from '@/components/ui/sonner'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Kbd } from '@/components/ui/kbd'
import { useMediaQuery } from '@vueuse/core'
import ProfileEditor from '@/components/ProfileEditor.vue'
import CommandPalette from '@/components/CommandPalette.vue'
import AppUpdateSheet from '@/components/AppUpdateSheet.vue'
import CoachSpot from '@/components/CoachSpot.vue'

const route = useRoute()
const { t } = useI18n()
const { locale } = useLocale()
const palette = ref(false)
const narrow = useMediaQuery('(max-width: 1100px)')
function key(e: KeyboardEvent) {
  const t = e.target as HTMLElement
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); palette.value = !palette.value }
  else if (e.key === '/' && !/INPUT|TEXTAREA|SELECT/.test(t.tagName)) { const el = document.querySelector<HTMLInputElement>('input[data-search]'); if (el) { e.preventDefault(); el.focus() } }
}
onMounted(() => window.addEventListener('keydown', key)); onUnmounted(() => window.removeEventListener('keydown', key))
const lib = [['/profiles', Users, 'nav.profiles'], ['/proxies', Globe, 'nav.proxies'], ['/macros', ListChecks, 'nav.macros'], ['/runs', Play, 'nav.runs']] as const
const sys = [['/engines', Box, 'nav.engines'], ['/settings', Settings, 'nav.settings']] as const
const indicatorText = () => (au.phase === 'downloading' ? t('appUpdate.indicator.downloading', { pct: fmtPct(au.pct), eta: fmtSec(au.eta) }) : t('appUpdate.indicator.' + au.phase))
const active = (to: string) => route.path === to || route.path.startsWith(to + '/')
const simCount = () => Object.values(sim).filter(Boolean).length
</script>
<template>
  <TooltipProvider>
    <SidebarProvider class="h-full min-h-0" :style="{ '--sidebar-width': narrow ? '184px' : '220px' }">
      <Sidebar collapsible="none" class="vibrancy-sidebar h-full shrink-0 border-r border-sidebar-border !bg-transparent">
        <SidebarHeader class="px-3 pt-4 pb-1"><div class="flex items-center gap-2 px-1.5"><ListChecks class="size-4" /><span class="text-[15px] font-semibold">Ladilala</span><span class="ml-auto shrink-0 whitespace-nowrap rounded border px-1 text-[10px] text-muted-foreground">{{ $t('common.prototype') }}</span></div></SidebarHeader>
        <SidebarContent class="gap-0 px-1.5">
          <SidebarGroup class="p-1.5"><SidebarGroupLabel class="h-6 px-1.5 text-xs text-muted-foreground">{{ $t('nav.library') }}</SidebarGroupLabel>
            <SidebarGroupContent><SidebarMenu class="gap-px">
              <SidebarMenuItem v-for="i in lib" :key="i[0]"><SidebarMenuButton as-child :is-active="active(i[0])" class="h-7 rounded-md px-2 data-[active=true]:bg-sidebar-accent data-[active=true]:font-normal"><router-link :to="i[0]"><component :is="i[1]" class="size-4" /><span>{{ $t(i[2]) }}</span></router-link></SidebarMenuButton>
                <SidebarMenuBadge v-if="i[0] === '/profiles'" class="tabular-nums text-muted-foreground">{{ runningCount }}</SidebarMenuBadge></SidebarMenuItem>
            </SidebarMenu></SidebarGroupContent></SidebarGroup>
          <SidebarGroup class="p-1.5"><SidebarGroupLabel class="h-6 px-1.5 text-xs text-muted-foreground">{{ $t('nav.system') }}</SidebarGroupLabel>
            <SidebarGroupContent><SidebarMenu class="gap-px">
              <SidebarMenuItem v-for="i in sys" :key="i[0]"><SidebarMenuButton as-child :is-active="active(i[0])" class="h-7 rounded-md px-2 data-[active=true]:bg-sidebar-accent data-[active=true]:font-normal"><router-link :to="i[0]"><component :is="i[1]" class="size-4" /><span>{{ $t(i[2]) }}</span></router-link></SidebarMenuButton>
                <SidebarMenuBadge v-if="i[0] === '/engines' && healthIssues > 0" class="tabular-nums text-destructive">{{ healthIssues }}</SidebarMenuBadge></SidebarMenuItem>
            </SidebarMenu></SidebarGroupContent></SidebarGroup>
        </SidebarContent>
        <SidebarFooter class="gap-1.5 p-3">
          <button v-if="indicator" class="flex w-full items-center gap-2 rounded-md border bg-card px-2.5 py-1.5 text-left text-xs hover:bg-accent" @click="au.sheet = true">
            <svg v-if="au.phase === 'downloading'" viewBox="0 0 20 20" class="size-4 shrink-0 -rotate-90" :aria-label="$t('appUpdate.progressAria')"><circle cx="10" cy="10" r="8" fill="none" stroke="currentColor" stroke-opacity=".2" stroke-width="2.5" /><circle cx="10" cy="10" r="8" fill="none" stroke="var(--primary)" stroke-width="2.5" stroke-linecap="round" :stroke-dasharray="50.27" :stroke-dashoffset="50.27 * (1 - au.pct / 100)" /></svg>
            <TriangleAlert v-else-if="au.phase === 'failed'" class="size-4 shrink-0 text-destructive" /><Download v-else class="size-4 shrink-0 text-primary" />
            <span class="min-w-0 flex-1"><b class="block truncate font-medium">Ladilala {{ au.rel?.version }}</b><span class="block truncate text-muted-foreground">{{ indicatorText() }}</span></span>
          </button>
          <Button variant="outline" size="sm" class="w-full justify-start gap-2 font-normal text-muted-foreground" @click="palette = true"><Search class="size-3.5" />{{ $t('palette.search') }}<span class="ml-auto flex gap-0.5"><Kbd>Ctrl</Kbd><Kbd>K</Kbd></span></Button>
          <Popover v-if="!inWails">
            <PopoverTrigger as-child><Button variant="outline" size="sm" class="w-full justify-start gap-2 font-normal text-muted-foreground"><TriangleAlert class="size-3.5" />{{ $t('simulate.button') }}<span v-if="simCount()" class="ml-auto rounded bg-destructive/15 px-1 tabular-nums text-destructive">{{ simCount() }}</span></Button></PopoverTrigger>
            <PopoverContent side="right" align="end" :side-offset="8" class="max-h-[80vh] w-80 overflow-auto p-3">
              <div class="mb-2 flex items-center"><b>{{ $t('simulate.title') }}</b><span class="flex-1" /><Button size="sm" variant="outline" @click="resetSim()">{{ $t('simulate.reset') }}</Button></div>
              <label v-for="s in SIMS" :key="s" class="flex items-start gap-2.5 py-1.5"><Switch v-model="sim[s]" :aria-label="$t(`simulate.sims.${s}.label`)" class="mt-0.5" /><span class="min-w-0"><b class="font-medium">{{ $t(`simulate.sims.${s}.label`) }}</b><span class="block text-xs text-muted-foreground">{{ $t(`simulate.sims.${s}.hint`) }}</span></span></label>
              <p class="mt-1 text-xs text-muted-foreground">{{ $t('simulate.then') }}</p>
              <div class="mt-3 border-t pt-2"><b>{{ $t('simulate.appTitle') }}</b>
                <button v-for="a in AUS" :key="a" class="flex w-full flex-col rounded-md px-2 py-1 text-left hover:bg-accent" :class="au.scenario === a ? 'bg-accent' : ''" @click="setScenario(a)"><span class="font-medium">{{ $t(`simulate.app.${a}.label`) }}</span><span class="text-xs text-muted-foreground">{{ $t(`simulate.app.${a}.hint`) }}</span></button></div>
            </PopoverContent>
          </Popover>
          <div class="flex items-center text-xs text-muted-foreground"><span class="min-w-0 truncate tabular-nums">{{ $t('nav.running', { n: runningCount }) }}</span><span class="flex-1" />
            <Button variant="ghost" size="icon-sm" :aria-label="$t('common.toggleDark')" @click="toggleDark()"><Sun v-if="isDark" class="size-4" /><Moon v-else class="size-4" /></Button></div>
          <Segmented v-model="locale" :options="[['vi', 'VI'], ['en', 'EN']]" :aria-label="$t('language.label')" class="w-full [&>*]:flex-1" />
        </SidebarFooter>
      </Sidebar>
      <main class="flex min-h-0 min-w-0 flex-1 flex-col bg-background"><router-view /></main>
    </SidebarProvider>
  </TooltipProvider>

  <ProfileEditor v-if="store.editing !== null" />
  <CommandPalette v-model:open="palette" />
  <AppUpdateSheet />
  <CoachSpot />
  <Toaster position="bottom-center" :container-aria-label="$t('common.notifications')" />
</template>
