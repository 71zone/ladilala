<script setup lang="ts">
import { useRouter } from 'vue-router'
import { Users, Globe, ListChecks, Play, Box, Settings, Moon, Plus, Stethoscope, RefreshCw, Languages } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { checkNow } from '@/appupdate'
import { store } from '@/store'
import { toggleDark } from '@/theme'
import { useLocale } from '@/composables/useLocale'
import { CommandDialog, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList, CommandSeparator, CommandShortcut } from '@/components/ui/command'
// Ctrl+K quick switcher: screens, profiles, actions. Item values hold the label in both languages so search works in either.
const open = defineModel<boolean>('open', { default: false })
const router = useRouter()
const { t } = useI18n()
const { toggle } = useLocale()
const go = (to: string) => { router.push(to); open.value = false }
const screens = [['/profiles', Users, 'profiles'], ['/proxies', Globe, 'proxies'], ['/macros', ListChecks, 'macros'], ['/runs', Play, 'runs'], ['/engines', Box, 'engines'], ['/settings', Settings, 'settings']] as const
const pick = (id: string) => { store.selId = id; go('/profiles') }
const find = (k: string) => t(k) + ' ' + t(k, {}, { locale: 'en' })
</script>
<template>
  <CommandDialog v-model:open="open" :title="$t('palette.title')" :description="$t('palette.description')">
    <CommandInput :placeholder="$t('palette.placeholder')" />
    <CommandList>
      <CommandEmpty>{{ $t('palette.empty') }}</CommandEmpty>
      <CommandGroup :heading="$t('palette.screens')"><CommandItem v-for="s in screens" :key="s[0]" :value="find('nav.' + s[2])" @select="go(s[0])"><component :is="s[1]" />{{ $t('nav.' + s[2]) }}</CommandItem></CommandGroup>
      <CommandSeparator />
      <CommandGroup :heading="$t('palette.actions')">
        <CommandItem :value="find('palette.newProfile')" @select="open = false; store.openEditor('new')"><Plus />{{ $t('palette.newProfile') }}</CommandItem>
        <CommandItem :value="find('palette.doctor')" @select="go('/engines')"><Stethoscope />{{ $t('palette.doctor') }}</CommandItem>
        <CommandItem :value="find('palette.checkUpdates')" @select="open = false; checkNow(); go('/settings')"><RefreshCw />{{ $t('palette.checkUpdates') }}</CommandItem>
        <CommandItem :value="find('palette.updateSettings')" @select="go('/settings')"><Settings />{{ $t('palette.updateSettings') }}</CommandItem>
        <CommandItem :value="find('palette.toggleDark')" @select="toggleDark(); open = false"><Moon />{{ $t('palette.toggleDark') }}</CommandItem>
        <CommandItem :value="find('palette.changeLanguage')" @select="toggle(); open = false"><Languages />{{ $t('palette.changeLanguage') }}</CommandItem>
      </CommandGroup>
      <CommandSeparator />
      <CommandGroup :heading="$t('palette.profiles')"><CommandItem v-for="p in store.profiles" :key="p.id" :value="p.name + ' ' + p.group" @select="pick(p.id)"><Users />{{ p.name }}<CommandShortcut>{{ p.group }}</CommandShortcut></CommandItem></CommandGroup>
    </CommandList>
  </CommandDialog>
</template>
