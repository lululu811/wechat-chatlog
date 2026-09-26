<template>
  <div class="space-y-6">
    <!-- Card 1: MD 归档 -->
    <div class="bg-white dark:bg-[#121215] rounded-2xl border border-zinc-200/80 dark:border-zinc-800/80 p-6 shadow-xs space-y-4">
      <div class="flex items-start justify-between gap-4">
        <div class="space-y-1">
          <h3 class="text-base font-bold text-zinc-900 dark:text-zinc-100 flex items-center gap-2">
            <FileDown class="w-4.5 h-4.5 text-blue-500" />
            <span>本地 Markdown 归档管道</span>
            <span class="text-[11px] font-normal px-2 py-0.5 rounded-full bg-blue-50 text-blue-600 dark:bg-blue-950/50 dark:text-blue-400 border border-blue-200/60 dark:border-blue-900/50">支持断点续传</span>
          </h3>
          <p class="text-xs text-zinc-400 leading-relaxed">
            把公众号文章与高清图片全量抓取并导出为本地 Markdown。断点续传已启用：已成功归档项自动严格跳过，不重复处理；任务常驻服务端后台运行，关闭页面不受影响。
          </p>
        </div>
      </div>

      <!-- Status metrics cards -->
      <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
        <div class="p-3 rounded-xl bg-zinc-50 dark:bg-zinc-900/60 border border-zinc-200/60 dark:border-zinc-800/60">
          <div class="text-[11px] text-zinc-400">待归档文章</div>
          <div class="text-lg font-bold text-zinc-900 dark:text-zinc-100 font-mono mt-0.5">
            {{ exportPendingCount }} <span class="text-xs font-normal text-zinc-400">篇</span>
          </div>
        </div>
        <div class="p-3 rounded-xl bg-emerald-50/50 dark:bg-emerald-950/20 border border-emerald-200/50 dark:border-emerald-900/30">
          <div class="text-[11px] text-emerald-600 dark:text-emerald-400">已归档（自动跳过）</div>
          <div class="text-lg font-bold text-emerald-700 dark:text-emerald-400 font-mono mt-0.5">
            {{ exportExportedCount }} <span class="text-xs font-normal text-emerald-600/70 dark:text-emerald-500">篇</span>
          </div>
        </div>
        <div class="p-3 rounded-xl bg-amber-50/50 dark:bg-amber-950/20 border border-amber-200/50 dark:border-amber-900/30">
          <div class="text-[11px] text-amber-600 dark:text-amber-400">需人工处理</div>
          <div class="text-lg font-bold text-amber-700 dark:text-amber-400 font-mono mt-0.5">
            {{ exportBlockedCount }} <span class="text-xs font-normal text-amber-600/70 dark:text-amber-500">篇</span>
          </div>
        </div>
      </div>

      <div class="flex flex-wrap items-center gap-3 pt-1">
        <div class="flex items-center gap-2">
          <label for="exportDays" class="text-xs font-medium text-zinc-600 dark:text-zinc-400">时间范围</label>
          <select 
            id="exportDays" 
            v-model="exportDays" 
            @change="loadExportStatus"
            class="text-xs px-3 py-1.5 rounded-lg border border-zinc-200 dark:border-zinc-800 bg-zinc-50 dark:bg-zinc-900 text-zinc-800 dark:text-zinc-200 outline-none focus:border-emerald-500"
          >
            <option value="7">近 7 天</option>
            <option value="30">近 30 天</option>
            <option value="90">近 90 天</option>
            <option value="180">近 180 天</option>
            <option value="365">近 365 天</option>
          </select>
        </div>

        <div class="flex items-center gap-2">
          <label class="text-xs font-medium text-zinc-600 dark:text-zinc-400">每批上限</label>
          <input 
            type="number" 
            v-model.number="exportLimit" 
            min="1" 
            max="1000"
            class="text-xs w-20 px-3 py-1.5 rounded-lg border border-zinc-200 dark:border-zinc-800 bg-zinc-50 dark:bg-zinc-900 text-zinc-800 dark:text-zinc-200 outline-none focus:border-emerald-500 font-mono"
          />
        </div>

        <!-- Auto Resume Toggle -->
        <label class="flex items-center gap-1.5 cursor-pointer text-xs text-zinc-600 dark:text-zinc-400 select-none py-1 px-2.5 rounded-lg bg-zinc-50 dark:bg-zinc-900/60 border border-zinc-200/60 dark:border-zinc-800/60">
          <input 
            type="checkbox" 
            v-model="autoResume" 
            class="rounded border-zinc-300 dark:border-zinc-700 text-emerald-600 focus:ring-emerald-500 w-3.5 h-3.5"
          />
          <span>自动连续续传 (每批完成后自动继续下一批)</span>
        </label>

        <!-- Primary Action Button -->
        <button 
          @click="startExportJob" 
          :disabled="isExportRunning"
          class="text-xs px-4 py-1.5 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white font-medium disabled:opacity-50 transition-colors shadow-xs flex items-center gap-1.5"
        >
          <span v-if="isExportRunning" class="w-2 h-2 rounded-full bg-white animate-ping"></span>
          <span>{{ isExportRunning ? '正在归档…' : (exportExportedCount > 0 && exportPendingCount > 0 ? `继续归档下一批 (续传剩余 ${exportPendingCount} 篇)` : '开始归档') }}</span>
        </button>

        <button 
          v-if="exportBlockedCount > 0"
          @click="retryExportJob" 
          :disabled="isExportRunning"
          class="text-xs px-3 py-1.5 rounded-xl border border-zinc-200 dark:border-zinc-700 hover:bg-zinc-50 dark:hover:bg-zinc-800 text-zinc-700 dark:text-zinc-300 disabled:opacity-50 transition-colors"
        >
          重试未成功项 ({{ exportBlockedCount }})
        </button>

        <button 
          v-if="isExportRunning"
          @click="cancelExportJob" 
          class="text-xs px-3 py-1.5 rounded-xl bg-rose-50 text-rose-600 dark:bg-rose-950/40 dark:text-rose-400 hover:bg-rose-100 border border-rose-200 dark:border-rose-900/60 transition-colors"
        >
          取消任务
        </button>

        <button
          v-if="autoResumeCountdown > 0"
          @click="cancelAutoResume"
          class="text-xs px-3 py-1.5 rounded-xl bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300 hover:bg-amber-100 border border-amber-200 dark:border-amber-900/60 transition-colors animate-pulse"
        >
          {{ autoResumeCountdown }}s 后续传下一批 (点击暂停)
        </button>
      </div>

      <!-- Segmented Progress Bar & Active item display -->
      <div v-if="isExportRunning || exportTotal > 0" class="space-y-2 pt-2 bg-zinc-50/70 dark:bg-zinc-900/40 p-3 rounded-xl border border-zinc-200/50 dark:border-zinc-800/50">
        <div class="h-2 w-full bg-zinc-200/70 dark:bg-zinc-800 rounded-full overflow-hidden flex">
          <div class="bg-emerald-500 transition-all duration-300" :style="{ width: `${exportSegOk}%` }"></div>
          <div class="bg-rose-500 transition-all duration-300" :style="{ width: `${exportSegBad}%` }"></div>
          <div class="bg-zinc-300 dark:bg-zinc-600 transition-all duration-300" :style="{ width: `${exportSegSkip}%` }"></div>
        </div>
        <div class="text-[11px] text-zinc-500 dark:text-zinc-400 font-mono flex flex-wrap justify-between items-center gap-2">
          <span>本批进度: {{ exportProcessed }} / {{ exportTotal }} 篇 ({{ exportPercent }}%)</span>
          <span class="flex items-center gap-3">
            <span class="text-emerald-600 dark:text-emerald-400 font-medium">成功: {{ exportDone }}</span>
            <span class="text-rose-600 dark:text-rose-400 font-medium">失败: {{ exportFailed }}</span>
            <span v-if="exportSkipped > 0" class="text-zinc-400">跳过: {{ exportSkipped }}</span>
          </span>
        </div>
        <div v-if="currentExportArticleTitle" class="text-[11px] text-zinc-500 dark:text-zinc-400 flex items-center gap-1.5 truncate">
          <span class="w-1.5 h-1.5 rounded-full bg-blue-500 shrink-0 animate-pulse"></span>
          <span class="font-medium text-zinc-700 dark:text-zinc-300 shrink-0">{{ currentExportArticleAccount ? `[${currentExportArticleAccount}]` : '' }}</span>
          <span class="truncate">{{ currentExportArticleTitle }}</span>
        </div>
      </div>
    </div>

    <!-- Card 2: IMA 推送 -->
    <div class="bg-white dark:bg-[#121215] rounded-2xl border border-zinc-200/80 dark:border-zinc-800/80 p-6 shadow-xs space-y-4">
      <div class="space-y-1">
        <h3 class="text-base font-bold text-zinc-900 dark:text-zinc-100 flex items-center gap-2">
          <Send class="w-4.5 h-4.5 text-indigo-500" />
          <span>IMA 知识库同步管道</span>
        </h3>
        <p class="text-xs text-zinc-400 leading-relaxed">
          把已本地归档的 Markdown 文章同步推送到腾讯 IMA 知识库。未完成归档的文章会自动依赖前置步骤。
        </p>
      </div>

      <div class="text-xs text-zinc-500 dark:text-zinc-400 font-mono bg-zinc-50 dark:bg-zinc-900/60 p-2.5 rounded-xl border border-zinc-200/60 dark:border-zinc-800/60">
        {{ pushStatusText }}
      </div>

      <div class="flex flex-wrap items-center gap-3 pt-1">
        <div class="flex items-center gap-2">
          <label for="pushDays" class="text-xs font-medium text-zinc-600 dark:text-zinc-400">时间范围</label>
          <select 
            id="pushDays" 
            v-model="pushDays" 
            @change="loadPushStatus"
            class="text-xs px-3 py-1.5 rounded-lg border border-zinc-200 dark:border-zinc-800 bg-zinc-50 dark:bg-zinc-900 text-zinc-800 dark:text-zinc-200 outline-none focus:border-emerald-500"
          >
            <option value="7">近 7 天</option>
            <option value="30">近 30 天</option>
            <option value="90">近 90 天</option>
            <option value="180">近 180 天</option>
            <option value="365">近 365 天</option>
          </select>
        </div>

        <div class="flex items-center gap-2">
          <label class="text-xs font-medium text-zinc-600 dark:text-zinc-400">数量上限</label>
          <input 
            type="number" 
            v-model.number="pushLimit" 
            min="1" 
            max="1000"
            class="text-xs w-20 px-3 py-1.5 rounded-lg border border-zinc-200 dark:border-zinc-800 bg-zinc-50 dark:bg-zinc-900 text-zinc-800 dark:text-zinc-200 outline-none focus:border-emerald-500 font-mono"
          />
        </div>

        <button 
          @click="startPushJob" 
          :disabled="isPushRunning"
          class="text-xs px-4 py-1.5 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-white font-medium disabled:opacity-50 transition-colors shadow-xs"
        >
          {{ isPushRunning ? '正在推送…' : '开始推送' }}
        </button>

        <button 
          v-if="pushBlockedCount > 0"
          @click="retryPushJob" 
          :disabled="isPushRunning"
          class="text-xs px-3 py-1.5 rounded-xl border border-zinc-200 dark:border-zinc-700 hover:bg-zinc-50 dark:hover:bg-zinc-800 text-zinc-700 dark:text-zinc-300 disabled:opacity-50 transition-colors"
        >
          重试未成功项 ({{ pushBlockedCount }})
        </button>

        <button 
          v-if="isPushRunning"
          @click="cancelPushJob" 
          class="text-xs px-3 py-1.5 rounded-xl bg-rose-50 text-rose-600 dark:bg-rose-950/40 dark:text-rose-400 hover:bg-rose-100 border border-rose-200 dark:border-rose-900/60 transition-colors"
        >
          取消任务
        </button>
      </div>

      <!-- Segmented Progress Bar -->
      <div v-if="isPushRunning || pushTotal > 0" class="space-y-1.5 pt-2 bg-zinc-50/70 dark:bg-zinc-900/40 p-3 rounded-xl border border-zinc-200/50 dark:border-zinc-800/50">
        <div class="h-2 w-full bg-zinc-200/70 dark:bg-zinc-800 rounded-full overflow-hidden flex">
          <div class="bg-indigo-500 transition-all duration-300" :style="{ width: `${pushSegOk}%` }"></div>
          <div class="bg-rose-500 transition-all duration-300" :style="{ width: `${pushSegBad}%` }"></div>
          <div class="bg-zinc-300 dark:bg-zinc-600 transition-all duration-300" :style="{ width: `${pushSegSkip}%` }"></div>
        </div>
        <div class="text-[11px] text-zinc-400 font-mono flex justify-between">
          <span>进度: {{ pushProcessed }} / {{ pushTotal }} 篇 ({{ pushPercent }}%)</span>
          <span>成功: {{ pushDone }} · 失败: {{ pushFailed }}</span>
        </div>
      </div>
    </div>

    <!-- Card 3: AI 智能摘要生成 -->
    <div class="bg-white dark:bg-[#121215] rounded-2xl border border-zinc-200/80 dark:border-zinc-800/80 p-6 shadow-xs space-y-4">
      <div class="space-y-1">
        <h3 class="text-base font-bold text-zinc-900 dark:text-zinc-100 flex items-center gap-2">
          <Sparkles class="w-4.5 h-4.5 text-purple-500" />
          <span>LLM 批量摘要提炼</span>
        </h3>
        <p class="text-xs text-zinc-400 leading-relaxed">
          调用配置的大语言模型为已归档文章并发提炼结构化摘要，自动打标并供宏观研报智能聚合使用。
        </p>
      </div>

      <div class="flex items-center gap-3 pt-1">
        <label class="text-xs font-medium text-zinc-600 dark:text-zinc-400">每次处理篇数</label>
        <input 
          type="number" 
          v-model.number="summaryLimit" 
          min="1" 
          max="100"
          class="text-xs w-20 px-3 py-1.5 rounded-lg border border-zinc-200 dark:border-zinc-800 bg-zinc-50 dark:bg-zinc-900 text-zinc-800 dark:text-zinc-200 outline-none focus:border-emerald-500 font-mono"
        />
        <button 
          @click="batchGenerateSummaries" 
          :disabled="isSummaryRunning"
          class="text-xs px-4 py-1.5 rounded-xl bg-purple-600 hover:bg-purple-500 text-white font-medium disabled:opacity-50 transition-colors shadow-xs"
        >
          {{ isSummaryRunning ? '提炼中…' : '批量生成摘要' }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue';
import { FileDown, Send, Sparkles } from 'lucide-vue-next';
import { api } from '@/api/client';
import { useToast } from '@/composables/useToast';

const toast = useToast();

// Export state
const exportDays = ref(30);
const exportLimit = ref(100);
const exportPendingCount = ref(0);
const exportExportedCount = ref(0);
const exportBlockedCount = ref(0);
const isExportRunning = ref(false);
const autoResume = ref(false);
const autoResumeCountdown = ref(0);

const exportTotal = ref(0);
const exportProcessed = ref(0);
const exportPercent = ref(0);
const exportDone = ref(0);
const exportFailed = ref(0);
const exportSkipped = ref(0);
const exportSegOk = ref(0);
const exportSegBad = ref(0);
const exportSegSkip = ref(0);
const currentExportArticleTitle = ref('');
const currentExportArticleAccount = ref('');

let currentExportJobId = null;
let exportTimer = null;
let autoResumeTimer = null;

// Push state
const pushDays = ref(30);
const pushLimit = ref(100);
const pushStatusText = ref('加载中…');
const isPushRunning = ref(false);
const pushBlockedCount = ref(0);
const pushTotal = ref(0);
const pushProcessed = ref(0);
const pushPercent = ref(0);
const pushDone = ref(0);
const pushFailed = ref(0);
const pushSegOk = ref(0);
const pushSegBad = ref(0);
const pushSegSkip = ref(0);

let currentPushJobId = null;
let pushTimer = null;

// Summaries
const summaryLimit = ref(20);
const isSummaryRunning = ref(false);

async function loadExportStatus() {
  try {
    const res = await api.getExportStatus(exportDays.value);
    exportPendingCount.value = res.pending || 0;
    exportExportedCount.value = res.exported || 0;
    exportBlockedCount.value = res.blocked || 0;

    // Attach to active running job if any
    if (res.activeJobID && !isExportRunning.value) {
      currentExportJobId = res.activeJobID;
      isExportRunning.value = true;
      pollExportJob(res.activeJobID);
    }
  } catch {
    // ignore
  }
}

async function loadPushStatus() {
  try {
    const res = await api.getPushStatus(pushDays.value);
    pushStatusText.value = `可推送文章: ${res.pending || 0} 篇 · 已推送: ${res.pushed || 0} 篇 · 失败: ${res.failed || 0} 篇`;
    pushBlockedCount.value = res.failed || 0;

    if (res.activeJobID && !isPushRunning.value) {
      currentPushJobId = res.activeJobID;
      isPushRunning.value = true;
      pollPushJob(res.activeJobID);
    }
  } catch {
    pushStatusText.value = '无法加载状态';
  }
}

function pollExportJob(jobId) {
  if (exportTimer) clearInterval(exportTimer);

  const check = async () => {
    try {
      const res = await api.getExportJob(jobId, true);
      const job = res.job;
      if (!job) return;

      exportTotal.value = job.total || 0;
      exportDone.value = job.succeeded || 0;
      exportFailed.value = job.failed || 0;
      exportSkipped.value = job.skipped || 0;
      exportProcessed.value = (job.succeeded || 0) + (job.failed || 0) + (job.skipped || 0);

      if (job.total > 0) {
        exportPercent.value = Math.min(100, Math.round((exportProcessed.value / job.total) * 100));
        exportSegOk.value = Math.round(((job.succeeded || 0) / job.total) * 100);
        exportSegBad.value = Math.round(((job.failed || 0) / job.total) * 100);
        exportSegSkip.value = Math.round(((job.skipped || 0) / job.total) * 100);
      } else {
        exportPercent.value = 0;
      }

      // Find current running item
      if (res.items && res.items.length > 0) {
        const runningItem = res.items.find(it => it.status === 'running') || res.items.find(it => it.status === 'pending');
        if (runningItem) {
          currentExportArticleTitle.value = runningItem.title;
          currentExportArticleAccount.value = runningItem.account;
        } else {
          currentExportArticleTitle.value = '';
          currentExportArticleAccount.value = '';
        }
      }

      // Check termination
      if (job.status === 'done' || job.status === 'failed' || job.status === 'canceled') {
        clearInterval(exportTimer);
        exportTimer = null;
        isExportRunning.value = false;
        currentExportArticleTitle.value = '';
        currentExportArticleAccount.value = '';

        await loadExportStatus();

        if (job.status === 'done') {
          toast.success(`归档批次完成：成功 ${job.succeeded || 0} 篇，失败 ${job.failed || 0} 篇`);
          if (autoResume.value && exportPendingCount.value > 0) {
            triggerAutoResumeNextBatch();
          }
        } else if (job.status === 'canceled') {
          toast.info('归档任务已被取消');
          cancelAutoResume();
        } else {
          toast.error(`归档任务结束: ${job.error || '部分条目失败'}`);
          cancelAutoResume();
        }
      }
    } catch {
      // transient network glitch
    }
  };

  check();
  exportTimer = setInterval(check, 1200);
}

function triggerAutoResumeNextBatch() {
  cancelAutoResume();
  autoResumeCountdown.value = 3;
  autoResumeTimer = setInterval(async () => {
    autoResumeCountdown.value--;
    if (autoResumeCountdown.value <= 0) {
      cancelAutoResume();
      if (autoResume.value && exportPendingCount.value > 0 && !isExportRunning.value) {
        await executeExportBatch(true);
      }
    }
  }, 1000);
}

function cancelAutoResume() {
  if (autoResumeTimer) {
    clearInterval(autoResumeTimer);
    autoResumeTimer = null;
  }
  autoResumeCountdown.value = 0;
}

async function startExportJob() {
  const isResume = exportExportedCount.value > 0 && exportPendingCount.value > 0;
  const prompt = isResume
    ? `确认继续归档近 ${exportDays.value} 天的文章吗？本次将处理下一批最多 ${exportLimit.value} 篇。\n已导出的 ${exportExportedCount.value} 篇将严格跳过（断点续传）。`
    : `确认开始归档近 ${exportDays.value} 天的文章吗？最多处理 ${exportLimit.value} 篇。`;

  if (!confirm(prompt)) return;
  cancelAutoResume();
  await executeExportBatch(false);
}

async function executeExportBatch(silent = false) {
  try {
    const res = await api.startExportJob(exportDays.value, exportLimit.value);
    const jobId = res.job?.id || res.job_id || res.id;
    if (!jobId) throw new Error('未获取到任务ID');
    currentExportJobId = jobId;
    isExportRunning.value = true;
    if (!silent) toast.success('归档任务已启动，正在后台抓取…');
    pollExportJob(jobId);
  } catch (err) {
    toast.error('启动归档失败: ' + err.message);
    isExportRunning.value = false;
    cancelAutoResume();
  }
}

async function retryExportJob() {
  if (!confirm(`确认重试当前未成功的 ${exportBlockedCount.value} 项归档吗？`)) return;
  cancelAutoResume();
  try {
    const res = await api.retryExportJob(currentExportJobId || 'latest');
    const jobId = res.job?.id || res.job_id || res.id;
    if (jobId) {
      currentExportJobId = jobId;
      isExportRunning.value = true;
      toast.success('已开始重试归档任务');
      pollExportJob(jobId);
    }
  } catch (err) {
    toast.error('重试失败: ' + err.message);
  }
}

async function cancelExportJob() {
  if (!confirm('确认取消当前正在运行的归档任务吗？已归档的内容将被保留。')) return;
  cancelAutoResume();
  try {
    await api.cancelExportJob(currentExportJobId || 'active');
    toast.info('已请求取消归档任务');
  } catch (err) {
    toast.error('取消失败: ' + err.message);
  }
}

function pollPushJob(jobId) {
  if (pushTimer) clearInterval(pushTimer);

  const check = async () => {
    try {
      const res = await api.getPushJob(jobId);
      const job = res.job;
      if (!job) return;

      pushTotal.value = job.total || 0;
      pushDone.value = job.succeeded || 0;
      pushFailed.value = job.failed || 0;
      pushProcessed.value = (job.succeeded || 0) + (job.failed || 0) + (job.skipped || 0);

      if (job.total > 0) {
        pushPercent.value = Math.min(100, Math.round((pushProcessed.value / job.total) * 100));
        pushSegOk.value = Math.round(((job.succeeded || 0) / job.total) * 100);
        pushSegBad.value = Math.round(((job.failed || 0) / job.total) * 100);
        pushSegSkip.value = Math.round(((job.skipped || 0) / job.total) * 100);
      }

      if (job.status === 'done' || job.status === 'failed' || job.status === 'canceled') {
        clearInterval(pushTimer);
        pushTimer = null;
        isPushRunning.value = false;
        await loadPushStatus();
        if (job.status === 'done') {
          toast.success(`推送任务完成：成功 ${job.succeeded || 0} 篇，失败 ${job.failed || 0} 篇`);
        } else if (job.status === 'canceled') {
          toast.info('推送任务已被取消');
        } else {
          toast.error(`推送任务结束: ${job.error || '失败'}`);
        }
      }
    } catch {
      // ignore
    }
  };

  check();
  pushTimer = setInterval(check, 1500);
}

async function startPushJob() {
  if (!confirm(`确认开始推送近 ${pushDays.value} 天的归档文章至 IMA 知识库吗？最多处理 ${pushLimit.value} 篇。`)) return;
  try {
    const res = await api.startPushJob(pushDays.value, pushLimit.value);
    const jobId = res.job?.id || res.job_id || res.id;
    currentPushJobId = jobId;
    isPushRunning.value = true;
    toast.success('推送任务已创建并在后台执行');
    if (jobId) pollPushJob(jobId);
  } catch (err) {
    toast.error('启动推送失败: ' + err.message);
  }
}

async function retryPushJob() {
  if (!confirm(`确认重试当前未成功的推送项吗？`)) return;
  try {
    const res = await api.retryPushJob(currentPushJobId || 'latest');
    const jobId = res.job?.id || res.job_id || res.id;
    if (jobId) {
      currentPushJobId = jobId;
      isPushRunning.value = true;
      toast.success('已开始重试推送任务');
      pollPushJob(jobId);
    }
  } catch (err) {
    toast.error('重试失败: ' + err.message);
  }
}

async function cancelPushJob() {
  if (!confirm('确认取消当前正在运行的推送任务吗？')) return;
  try {
    await api.cancelPushJob(currentPushJobId || 'active');
    toast.info('已请求取消推送任务');
  } catch (err) {
    toast.error('取消失败: ' + err.message);
  }
}

async function batchGenerateSummaries() {
  if (!confirm(`确认调用 LLM 为最多 ${summaryLimit.value} 篇已归档文章批量生成摘要吗？`)) return;
  isSummaryRunning.value = true;
  try {
    await api.batchSummarize(summaryLimit.value);
    toast.success('已触发批量摘要生成任务');
  } catch (err) {
    toast.error('生成摘要失败: ' + err.message);
  } finally {
    isSummaryRunning.value = false;
  }
}

onMounted(() => {
  loadExportStatus();
  loadPushStatus();
});

onUnmounted(() => {
  if (exportTimer) clearInterval(exportTimer);
  if (pushTimer) clearInterval(pushTimer);
  cancelAutoResume();
});
</script>
