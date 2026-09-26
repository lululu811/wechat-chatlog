<template>
  <div v-if="isOpen">
    <!-- Backdrop -->
    <div 
      class="fixed inset-0 z-50 bg-black/40 backdrop-blur-sm transition-opacity"
      @click="$emit('close')"
    ></div>

    <!-- Drawer -->
    <div 
      class="fixed inset-y-0 right-0 z-50 w-full max-w-lg bg-white dark:bg-slate-900 shadow-2xl border-l border-slate-200 dark:border-slate-800 flex flex-col transform transition-transform duration-300 ease-out"
    >
      <div class="px-6 py-5 border-b border-slate-200/80 dark:border-slate-800 flex items-center justify-between">
        <div>
          <h2 class="text-base font-bold text-slate-900 dark:text-slate-100 flex items-center gap-2">
            <AlertCircle class="w-5 h-5 text-rose-500" />
            <span>流水线异常排查</span>
          </h2>
          <div class="text-xs text-slate-400 mt-1">
            共 {{ totals?.failed || 0 }} 篇异常 · 抓取: {{ stageFails?.fetch || 0 }} · 归档: {{ stageFails?.mdexport || 0 }} · 摘要: {{ stageFails?.summarize || 0 }} · 推送: {{ stageFails?.imapush || 0 }}
          </div>
        </div>

        <button 
          @click="$emit('close')"
          class="p-2 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 rounded-lg hover:bg-slate-100 dark:hover:bg-slate-800 transition-colors"
        >
          <X class="w-5 h-5" />
        </button>
      </div>

      <div class="flex-1 overflow-y-auto px-6 py-5 space-y-4">
        <div v-if="items.length === 0" class="py-16 text-center text-xs text-slate-400">
          暂无失败条目
        </div>

        <div
          v-for="it in items"
          :key="it.id"
          class="p-4 rounded-xl border border-slate-200/80 dark:border-slate-800 bg-slate-50/60 dark:bg-slate-800/40 space-y-2.5"
        >
          <div class="flex items-start justify-between gap-3">
            <div class="text-sm font-semibold text-slate-900 dark:text-slate-100 line-clamp-2">
              {{ it.title || '无标题' }}
            </div>
            <button
              @click="retryItem(it.id)"
              :disabled="retrying[it.id]"
              class="flex-shrink-0 text-xs px-2.5 py-1 rounded-lg bg-emerald-600 hover:bg-emerald-500 text-white font-medium disabled:opacity-50 transition-colors"
            >
              {{ retrying[it.id] ? '重试中…' : '重试此篇' }}
            </button>
          </div>

          <div class="flex items-center gap-2 text-xs text-slate-400">
            <span>{{ it.ghName || it.gh_name || it.ghID || it.gh_id }}</span>
            <span class="px-2 py-0.5 rounded text-[10px] font-medium bg-amber-50 dark:bg-amber-950/60 text-amber-600 dark:text-amber-400 border border-amber-200/60 dark:border-amber-900/60">
              {{ stageLabel(it.pipelineStatus || it.pipeline_status) }}
            </span>
          </div>

          <div class="p-2.5 rounded-lg bg-rose-50/80 dark:bg-rose-950/40 border border-rose-100 dark:border-rose-900/40 text-xs font-mono text-rose-700 dark:text-rose-300 break-all leading-relaxed">
            {{ it.pipelineError || it.pipeline_error || '未知错误' }}
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue';
import { X, AlertCircle } from 'lucide-vue-next';
import { api } from '@/api/client';
import { useToast } from '@/composables/useToast';

const props = defineProps({
  isOpen: Boolean,
  statusData: Object,
});

const emit = defineEmits(['close', 'pipeline-updated']);
const toast = useToast();

const retrying = ref({});

const totals = computed(() => props.statusData?.totals || {});
const stageFails = computed(() => props.statusData?.failed_by_stage || {});
const items = computed(() => props.statusData?.recent || []);

function stageLabel(statusStr = '') {
  const stage = statusStr.replace('failed:', '');
  const labels = {
    fetch: '抓取阶段',
    mdexport: '归档阶段',
    summarize: '摘要阶段',
    imapush: '推送阶段',
  };
  return labels[stage] || stage || '未知阶段';
}

async function retryItem(articleId) {
  retrying.value[articleId] = true;
  try {
    await api.retryPipelineItem(articleId);
    toast.success('已加入重试队列');
    emit('pipeline-updated');
  } catch (err) {
    toast.error('重试失败: ' + err.message);
  } finally {
    retrying.value[articleId] = false;
  }
}
</script>
