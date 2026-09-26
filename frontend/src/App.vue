<template>
  <div class="min-h-screen flex flex-col bg-[#f8fafc] dark:bg-[#09090b] text-zinc-900 dark:text-zinc-100 selection:bg-emerald-500 selection:text-white transition-colors duration-150">
    <AppHeader />
    <div class="flex-1 max-w-7xl w-full mx-auto px-4 sm:px-6 lg:px-8 py-6">
      <router-view />
    </div>
    <ToastContainer />
  </div>
</template>

<script setup>
import AppHeader from '@/components/AppHeader.vue';
import ToastContainer from '@/components/ToastContainer.vue';
import { useTheme } from '@/composables/useTheme';

// Initialize theme on app load
useTheme();
</script>
