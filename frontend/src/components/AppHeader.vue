<template>
  <header class="sticky top-0 z-40 w-full backdrop-blur-md bg-white/95 dark:bg-[#09090b]/95 border-b border-zinc-200 dark:border-zinc-800 transition-colors shadow-xs">
    <div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 h-16 flex items-center justify-between gap-4">
      <!-- Left: Logo & Nav -->
      <div class="flex items-center gap-6 md:gap-8">
        <router-link to="/biz" class="flex items-center gap-2.5 group">
          <div class="w-8 h-8 rounded-xl bg-emerald-600 text-white flex items-center justify-center shadow-xs group-hover:bg-emerald-500 transition-colors">
            <Newspaper class="w-4.5 h-4.5" />
          </div>
          <div class="flex items-baseline gap-2">
            <span class="font-serif font-bold text-base sm:text-lg tracking-tight text-zinc-900 dark:text-zinc-100">
              公众号助手
            </span>
            <span class="text-xs text-zinc-400 font-mono tracking-wider uppercase hidden sm:inline">
              BizHub
            </span>
          </div>
        </router-link>

        <!-- Navigation Tabs -->
        <nav class="flex items-center gap-1 p-1 bg-zinc-100 dark:bg-zinc-900 rounded-xl border border-zinc-200/80 dark:border-zinc-800">
          <router-link 
            to="/biz" 
            class="flex items-center gap-1.5 px-3.5 py-1.5 rounded-lg text-sm font-medium transition-all"
            :class="isStreamActive ? 'bg-white dark:bg-zinc-800 text-zinc-900 dark:text-zinc-100 shadow-xs font-semibold' : 'text-zinc-600 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-200'"
          >
            <Compass class="w-4 h-4" />
            <span>动态</span>
          </router-link>

          <router-link 
            to="/biz/digest" 
            class="flex items-center gap-1.5 px-3.5 py-1.5 rounded-lg text-sm font-medium transition-all"
            :class="isDigestActive ? 'bg-white dark:bg-zinc-800 text-zinc-900 dark:text-zinc-100 shadow-xs font-semibold' : 'text-zinc-600 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-200'"
          >
            <Sparkles class="w-4 h-4 text-emerald-600 dark:text-emerald-400" />
            <span>看点</span>
          </router-link>

          <router-link 
            to="/biz/reports" 
            class="flex items-center gap-1.5 px-3.5 py-1.5 rounded-lg text-sm font-medium transition-all"
            :class="isReportsActive ? 'bg-white dark:bg-zinc-800 text-zinc-900 dark:text-zinc-100 shadow-xs font-semibold' : 'text-zinc-600 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-200'"
          >
            <FileText class="w-4 h-4" />
            <span>报告</span>
          </router-link>

          <router-link 
            to="/biz/admin" 
            class="flex items-center gap-1.5 px-3.5 py-1.5 rounded-lg text-sm font-medium transition-all"
            :class="isAdminActive ? 'bg-white dark:bg-zinc-800 text-zinc-900 dark:text-zinc-100 shadow-xs font-semibold' : 'text-zinc-600 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-200'"
          >
            <SlidersHorizontal class="w-4 h-4" />
            <span>管理</span>
          </router-link>
        </nav>
      </div>

      <!-- Right: Sync & Actions -->
      <div class="flex items-center gap-3">
        <!-- Sync Button -->
        <button
          @click="handleSync"
          :disabled="isSyncing"
          class="inline-flex items-center gap-2 px-3.5 py-1.5 rounded-xl text-sm font-medium text-zinc-700 dark:text-zinc-300 bg-white dark:bg-zinc-900 border border-zinc-200/80 dark:border-zinc-800 hover:bg-zinc-50 dark:hover:bg-zinc-800/80 hover:text-zinc-900 dark:hover:text-zinc-100 hover:border-zinc-300 dark:hover:border-zinc-700 active:scale-95 disabled:opacity-50 disabled:pointer-events-none shadow-xs transition-all"
          title="增量同步微信公众号最新文章"
        >
          <RefreshCw class="w-4 h-4 text-emerald-600 dark:text-emerald-400" :class="{ 'animate-spin': isSyncing }" />
          <span>{{ isSyncing ? '同步中…' : '同步数据' }}</span>
        </button>

        <div class="h-4 w-px bg-zinc-200 dark:bg-zinc-800"></div>

        <!-- Theme Toggle -->
        <button
          @click="toggleTheme"
          class="p-2 rounded-xl text-zinc-500 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100 hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors"
          title="切换白天 / 夜间模式"
        >
          <Sun v-if="isDark" class="w-4.5 h-4.5 text-amber-400" />
          <Moon v-else class="w-4.5 h-4.5" />
        </button>

        <!-- Return to chatlog -->
        <a
          href="/"
          class="inline-flex items-center gap-1.5 text-sm font-medium text-zinc-500 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100 px-2.5 py-1.5 rounded-xl hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors"
          title="返回 chatlog 原生聊天记录页"
        >
          <CornerUpLeft class="w-4 h-4" />
          <span class="hidden sm:inline">chatlog</span>
        </a>
      </div>
    </div>
  </header>
</template>

<script setup>
import { ref, computed } from 'vue';
import { useRoute } from 'vue-router';
import { 
  Newspaper, Compass, Sparkles, FileText, SlidersHorizontal, 
  RefreshCw, Sun, Moon, CornerUpLeft 
} from 'lucide-vue-next';
import { api } from '@/api/client';
import { useToast } from '@/composables/useToast';
import { useTheme } from '@/composables/useTheme';

const route = useRoute();
const toast = useToast();
const { isDark, toggle: toggleTheme } = useTheme();

const isSyncing = ref(false);

const isStreamActive = computed(() => {
  return route.path === '/biz' && route.query.mode !== 'digest';
});

const isDigestActive = computed(() => {
  return route.path === '/biz/digest' || (route.path === '/biz' && route.query.mode === 'digest');
});

const isReportsActive = computed(() => {
  return route.path.startsWith('/biz/reports');
});

const isAdminActive = computed(() => {
  return route.path.startsWith('/biz/admin');
});

async function handleSync() {
  if (isSyncing.value) return;
  isSyncing.value = true;
  try {
    await api.triggerSync();
    toast.info('已触发同步，正在拉取微信最新文章…');
    pollSync();
  } catch (err) {
    isSyncing.value = false;
    toast.error('触发同步失败: ' + err.message);
  }
}

async function pollSync() {
  try {
    const status = await api.getSyncStatus();
    if (status.syncing) {
      setTimeout(pollSync, 2000);
    } else {
      isSyncing.value = false;
      toast.success('微信公众号数据同步完成！');
      window.dispatchEvent(new CustomEvent('bizhub-sync-finished'));
    }
  } catch {
    isSyncing.value = false;
  }
}
</script>
