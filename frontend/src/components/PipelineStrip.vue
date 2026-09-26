<template>
  <div class="bg-white dark:bg-[#121215] rounded-xl border border-zinc-200 dark:border-zinc-800 p-5 shadow-sm mb-6 transition-all">
    <!-- Header Row -->
    <div class="flex items-center justify-between gap-4 mb-4 pb-3 border-b border-zinc-100 dark:border-zinc-800/80">
      <div class="flex items-center gap-2.5">
        <span class="relative flex h-2.5 w-2.5">
          <span class="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75"></span>
          <span class="relative inline-flex rounded-full h-2.5 w-2.5 bg-emerald-500"></span>
        </span>
        <div class="flex items-center gap-2">
          <h2 class="text-sm font-bold text-zinc-900 dark:text-zinc-100">
            内容流水线
          </h2>
          <span class="text-[11px] text-zinc-400">
            抓取 ➔ 归档 ➔ 推送 ➔ 摘要 自动化链路
          </span>
        </div>
      </div>

      <!-- Action Buttons -->
      <div class="flex items-center gap-2">
        <button
          v-if="failedCount > 0"
          @click="$emit('open-failures')"
          class="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-xs font-medium bg-rose-50 dark:bg-rose-950/40 border border-rose-200 dark:border-rose-900 text-rose-700 dark:text-rose-300 hover:bg-rose-100 transition-colors"
        >
          <AlertCircle class="w-3.5 h-3.5 text-rose-500" />
          <span>{{ failedCount }} 篇异常</span>
        </button>

        <button
          @click="handleTrigger"
          :disabled="isTriggering"
          class="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-semibold text-white bg-zinc-900 hover:bg-zinc-800 dark:bg-emerald-600 dark:hover:bg-emerald-500 disabled:opacity-50 transition-all shadow-sm"
        >
          <Play class="w-3 h-3 fill-current" :class="{ 'animate-spin': isTriggering }" />
          <span>{{ isTriggering ? '推进中…' : '推进流水线' }}</span>
        </button>
      </div>
    </div>

    <!-- 4 Connected Funnel Steps -->
    <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
      <!-- Stage 1: Pending -->
      <div class="p-3.5 rounded-xl bg-zinc-50/80 dark:bg-zinc-900/40 border border-zinc-200/70 dark:border-zinc-800/80 flex items-center justify-between gap-3">
        <div class="flex items-center gap-3">
          <div class="w-8 h-8 rounded-lg bg-amber-50 dark:bg-amber-950/60 text-amber-600 dark:text-amber-400 flex items-center justify-center border border-amber-200/60 dark:border-amber-900/60 flex-shrink-0">
            <Clock class="w-4 h-4" />
          </div>
          <div>
            <div class="text-[11px] font-medium text-zinc-500 dark:text-zinc-400">1. 待处理</div>
            <div class="text-base font-bold text-zinc-900 dark:text-zinc-100 font-mono tabular-nums">
              {{ formatNumber(status?.counts?.pending) }}
            </div>
          </div>
        </div>
        <span class="text-[10px] text-zinc-400 font-mono">文章入库</span>
      </div>

      <!-- Stage 2: MD Export -->
      <div class="p-3.5 rounded-xl bg-zinc-50/80 dark:bg-zinc-900/40 border border-zinc-200/70 dark:border-zinc-800/80 flex items-center justify-between gap-3">
        <div class="flex items-center gap-3">
          <div class="w-8 h-8 rounded-lg bg-blue-50 dark:bg-blue-950/60 text-blue-600 dark:text-blue-400 flex items-center justify-center border border-blue-200/60 dark:border-blue-900/60 flex-shrink-0">
            <FileDown class="w-4 h-4" />
          </div>
          <div>
            <div class="text-[11px] font-medium text-zinc-500 dark:text-zinc-400">2. MD 归档</div>
            <div class="text-base font-bold text-zinc-900 dark:text-zinc-100 font-mono tabular-nums">
              {{ formatNumber(exportedCount) }}
            </div>
          </div>
        </div>
        <span class="text-[10px] text-zinc-400 font-mono">本地正文</span>
      </div>

      <!-- Stage 3: IMA Push -->
      <div class="p-3.5 rounded-xl bg-zinc-50/80 dark:bg-zinc-900/40 border border-zinc-200/70 dark:border-zinc-800/80 flex items-center justify-between gap-3">
        <div class="flex items-center gap-3">
          <div class="w-8 h-8 rounded-lg bg-indigo-50 dark:bg-indigo-950/60 text-indigo-600 dark:text-indigo-400 flex items-center justify-center border border-indigo-200/60 dark:border-indigo-900/60 flex-shrink-0">
            <Send class="w-4 h-4" />
          </div>
          <div>
            <div class="text-[11px] font-medium text-zinc-500 dark:text-zinc-400">3. IMA 推送</div>
            <div class="text-base font-bold text-zinc-900 dark:text-zinc-100 font-mono tabular-nums">
              {{ formatNumber(status?.counts?.pushed) }}
            </div>
          </div>
        </div>
        <span class="text-[10px] text-zinc-400 font-mono">知识库就绪</span>
      </div>

      <!-- Stage 4: AI Summary -->
      <div class="p-3.5 rounded-xl bg-zinc-50/80 dark:bg-zinc-900/40 border border-zinc-200/70 dark:border-zinc-800/80 flex items-center justify-between gap-3">
        <div class="flex items-center gap-3">
          <div class="w-8 h-8 rounded-lg bg-purple-50 dark:bg-purple-950/60 text-purple-600 dark:text-purple-400 flex items-center justify-center border border-purple-200/60 dark:border-purple-900/60 flex-shrink-0">
            <Sparkles class="w-4 h-4" />
          </div>
          <div>
            <div class="text-[11px] font-medium text-zinc-500 dark:text-zinc-400">4. AI 摘要</div>
            <div class="text-base font-bold text-zinc-900 dark:text-zinc-100 font-mono tabular-nums">
              {{ formatNumber(summarizedCount) }}
            </div>
          </div>
        </div>
        <span class="text-[10px] text-zinc-400 font-mono">核心要点</span>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue';
import { 
  Clock, FileDown, Send, Sparkles, 
  AlertCircle, Play 
} from 'lucide-vue-next';
import { api } from '@/api/client';
import { useToast } from '@/composables/useToast';

const emit = defineEmits(['open-failures']);
const toast = useToast();

const status = ref(null);
const isTriggering = ref(false);

const exportedCount = computed(() => {
  const counts = status.value?.counts || {};
  return (counts.md_exported || 0) + (counts.summarized || 0) + (counts.pushed || 0);
});

const summarizedCount = computed(() => {
  const counts = status.value?.counts || {};
  return (counts.summarized || 0) + (counts.pushed || 0);
});

const failedCount = computed(() => {
  return status.value?.totals?.failed || 0;
});

function formatNumber(num) {
  if (num === undefined || num === null) return '0';
  return num.toLocaleString();
}

async function loadStatus() {
  try {
    const data = await api.getPipelineStatus();
    status.value = data;
  } catch {
    // Non-blocking
  }
}

async function handleTrigger() {
  if (isTriggering.value) return;
  isTriggering.value = true;
  try {
    await api.triggerPipeline();
    toast.success('已触发内容流水线，后台开始推进');
    setTimeout(loadStatus, 800);
    setTimeout(loadStatus, 2500);
  } catch (err) {
    toast.error('触发流水线失败: ' + err.message);
  } finally {
    setTimeout(() => {
      isTriggering.value = false;
    }, 1500);
  }
}

onMounted(() => {
  loadStatus();
  window.addEventListener('bizhub-sync-finished', loadStatus);
});

defineExpose({
  loadStatus,
  status,
});
</script>
