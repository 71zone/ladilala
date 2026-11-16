<script setup lang="ts">
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger } from '@/components/ui/alert-dialog'
// Destructive confirm: wrap the trigger button in the default slot.
defineProps<{ title: string; description: string; action: string }>(); defineEmits<{ confirm: [] }>()
</script>
<template>
  <AlertDialog>
    <AlertDialogTrigger as-child><slot /></AlertDialogTrigger>
    <AlertDialogContent>
      <AlertDialogHeader><AlertDialogTitle>{{ title }}</AlertDialogTitle><AlertDialogDescription>{{ description }}</AlertDialogDescription></AlertDialogHeader>
      <AlertDialogFooter><AlertDialogCancel>{{ $t('common.cancel') }}</AlertDialogCancel><AlertDialogAction class="bg-destructive text-white hover:bg-destructive/90" @click="$emit('confirm')">{{ action }}</AlertDialogAction></AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>
</template>
