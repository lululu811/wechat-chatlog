<template>
  <div class="fixed top-5 right-5 z-[9999] flex flex-col gap-2.5 pointer-events-none max-w-sm w-full">
    <transition-group 
      enter-active-class="transform ease-out duration-300 transition"
      enter-from-class="translate-y-2 opacity-0 sm:translate-y-0 sm:translate-x-4"
      enter-to-class="translate-y-0 opacity-100 sm:translate-x-0"
      leave-active-class="transition ease-in duration-200"
      leave-from-class="opacity-100"
      leave-to-class="opacity-0 scale-95"
    >
      <div
        v-for="t in toasts"
        :key="t.id"
        class="pointer-events-auto flex items-center gap-3 px-4 py-3 rounded-xl border shadow-floating text-sm font-medium backdrop-blur-md transition-all"
        :class="toastClasses(t.type)"
      >
        <span class="w-2 h-2 rounded-full flex-shrink-0" :class="dotClasses(t.type)"></span>
        <span class="flex-1 leading-snug break-words">{{ t.message }}</span>
        <button 
          @click="remove(t.id)" 
          class="opacity-60 hover:opacity-100 text-xs transition-opacity p-0.5"
        >
          ✕
        </button>
      </div>
    </transition-group>
  </div>
</template>

<script setup>
import { useToast } from '@/composables/useToast';

const { toasts, remove } = useToast();

function toastClasses(type) {
  switch (type) {
    case 'success':
      return 'bg-emerald-50/90 dark:bg-emerald-950/80 border-emerald-200 dark:border-emerald-800/80 text-emerald-900 dark:text-emerald-200';
    case 'error':
      return 'bg-rose-50/90 dark:bg-rose-950/80 border-rose-200 dark:border-rose-800/80 text-rose-900 dark:text-rose-200';
    case 'warn':
      return 'bg-amber-50/90 dark:bg-amber-950/80 border-amber-200 dark:border-amber-800/80 text-amber-900 dark:text-amber-200';
    default:
      return 'bg-white/90 dark:bg-slate-900/90 border-slate-200 dark:border-slate-800 text-slate-800 dark:text-slate-200';
  }
}

function dotClasses(type) {
  switch (type) {
    case 'success': return 'bg-emerald-500 shadow-[0_0_8px_rgba(16,185,129,0.5)]';
    case 'error': return 'bg-rose-500 shadow-[0_0_8px_rgba(244,63,94,0.5)]';
    case 'warn': return 'bg-amber-500 shadow-[0_0_8px_rgba(245,158,11,0.5)]';
    default: return 'bg-brand-500';
  }
}
</script>
