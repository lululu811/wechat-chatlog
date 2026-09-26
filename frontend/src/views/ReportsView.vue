<template>
  <div class="space-y-6">
    <!-- Reports Generator Card -->
    <div class="bg-white dark:bg-[#121215] rounded-2xl border border-zinc-200/80 dark:border-zinc-800/80 p-6 sm:p-7 shadow-xs space-y-4">
      <div class="flex items-center justify-between">
        <div>
          <h2 class="text-lg sm:text-xl font-bold text-zinc-900 dark:text-zinc-100 flex items-center gap-2">
            <Sparkles class="w-5 h-5 text-emerald-500" />
            <span>智能汇总与宏观研报</span>
          </h2>
          <p class="text-sm text-zinc-500 dark:text-zinc-400 mt-1">
            基于已归档的微信公众号文章深度提炼，自动聚合跨公号主题热点与核心洞见
          </p>
        </div>

        <button
          @click="showCreateForm = !showCreateForm"
          class="text-sm px-4 py-2 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white font-medium transition-colors shadow-xs"
        >
          {{ showCreateForm ? '收起生成面板' : '生成新研报' }}
        </button>
      </div>

      <!-- Creation Form Drawer -->
      <div v-show="showCreateForm" class="pt-4 border-t border-zinc-100 dark:border-zinc-800/80 space-y-4 animate-fade-in">
        <div class="flex flex-wrap items-center gap-3">
          <label class="text-sm font-medium text-zinc-700 dark:text-zinc-300">时间窗口</label>
          <select 
            v-model.number="createDays" 
            class="text-sm px-3.5 py-2 rounded-xl border border-zinc-200 dark:border-zinc-800 bg-zinc-50 dark:bg-zinc-900 text-zinc-800 dark:text-zinc-200 outline-none focus:border-emerald-500"
          >
            <option :value="1">今日 (近 24 小时)</option>
            <option :value="3">近 3 天</option>
            <option :value="7">近 7 天</option>
            <option :value="30">近 30 天</option>
          </select>
        </div>

        <div>
          <textarea
            v-model="customInstruction"
            placeholder="自定义提炼指令（可选，例如：重点关注中美科技博弈、算力与大模型落地动态…）"
            rows="2"
            class="w-full text-sm p-3.5 rounded-xl border border-zinc-200 dark:border-zinc-800 bg-zinc-50 dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 placeholder-zinc-400 outline-none focus:border-emerald-500 focus:ring-1 focus:ring-emerald-500 transition-all"
          ></textarea>
        </div>

        <div class="flex justify-end gap-2.5">
          <button
            @click="showCreateForm = false"
            class="text-sm px-4 py-2 text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-200 transition-colors"
          >
            取消
          </button>
          <button
            @click="handleGenerate"
            :disabled="isGenerating"
            class="text-sm px-5 py-2 rounded-xl bg-zinc-900 hover:bg-zinc-800 dark:bg-emerald-600 dark:hover:bg-emerald-500 text-white font-medium disabled:opacity-50 transition-colors shadow-xs flex items-center gap-2"
          >
            <Sparkles v-if="!isGenerating" class="w-4 h-4" />
            <span v-else class="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin"></span>
            <span>{{ isGenerating ? 'AI 汇总生成中…' : '立即开始提炼' }}</span>
          </button>
        </div>
      </div>
    </div>

    <!-- Reports Stream / List -->
    <div v-if="loadingReports" class="py-24 text-center text-sm text-zinc-400">
      <div class="w-8 h-8 border-2 border-emerald-500/20 border-t-emerald-500 rounded-full animate-spin mx-auto mb-3"></div>
      正在加载智能研报与详情…
    </div>

    <div v-else-if="reports.length === 0" class="py-24 text-center text-sm text-zinc-400 bg-white dark:bg-[#121215] rounded-2xl border border-zinc-200/80 dark:border-zinc-800/80">
      <Sparkles class="w-10 h-10 text-zinc-300 dark:text-zinc-700 mx-auto mb-3" />
      <p class="font-medium text-zinc-600 dark:text-zinc-400">暂无历史研报</p>
      <p class="text-zinc-400 mt-1">点击右上角「生成新研报」即可基于近期文章智能聚合洞见</p>
    </div>

    <div v-else class="space-y-6">
      <div
        v-for="r in reports"
        :key="r.id"
        class="bg-white dark:bg-[#121215] rounded-2xl border border-zinc-200/80 dark:border-zinc-800/80 p-6 sm:p-7 shadow-xs space-y-6"
      >
        <!-- Report Header -->
        <div class="flex items-start justify-between gap-4 border-b border-zinc-100 dark:border-zinc-800/80 pb-4">
          <div>
            <div class="flex items-center gap-2">
              <span class="px-2.5 py-0.5 rounded-md text-xs font-mono font-medium bg-emerald-50 dark:bg-emerald-950/60 text-emerald-700 dark:text-emerald-400 border border-emerald-200/60 dark:border-emerald-800/50">
                近 {{ r.days }} 天
              </span>
              <span v-if="r.article_count" class="text-xs font-mono text-zinc-400">
                涵盖 {{ r.article_count }} 篇文章
              </span>
            </div>

            <h3 class="text-lg sm:text-xl font-bold text-zinc-900 dark:text-zinc-100 mt-2 font-serif">
              {{ r.title || '公众号智能总结' }}
            </h3>

            <div class="flex flex-wrap items-center gap-2.5 text-xs text-zinc-400 mt-1.5 font-mono">
              <span>生成于 {{ formatDate(r.created_at) }}</span>
              <span v-if="r.instruction" class="text-emerald-600 dark:text-emerald-400 bg-emerald-50/80 dark:bg-emerald-950/40 px-2 py-0.5 rounded text-xs font-sans">
                ✎ 重点指令：{{ r.instruction }}
              </span>
            </div>
          </div>

          <div class="flex items-center gap-2 flex-shrink-0">
            <button
              @click="copyReport(r)"
              class="inline-flex items-center gap-1.5 px-3.5 py-1.5 rounded-xl border border-zinc-200 dark:border-zinc-800 hover:bg-zinc-50 dark:hover:bg-zinc-800/60 text-sm font-medium text-zinc-600 dark:text-zinc-300 transition-colors shadow-xs"
            >
              <Copy class="w-4 h-4" />
              <span>复制报告</span>
            </button>
          </div>
        </div>

        <!-- Structured Report Content (AI Highlights & Themes) -->
        <div v-if="r.parsedData" class="space-y-6">
          <!-- Highlights Section -->
          <div v-if="r.parsedData.highlights && r.parsedData.highlights.length > 0" class="space-y-3">
            <div class="flex items-center gap-2 text-sm font-bold text-zinc-900 dark:text-zinc-100 uppercase tracking-wider">
              <span class="w-1.5 h-4 bg-emerald-500 rounded-xs"></span>
              <span>核心要点速递</span>
            </div>

            <div class="grid gap-2.5">
              <div
                v-for="(hl, idx) in r.parsedData.highlights"
                :key="idx"
                class="flex items-start gap-3 p-4 rounded-xl bg-zinc-50/70 dark:bg-zinc-900/50 border border-zinc-200/60 dark:border-zinc-800/60 text-sm sm:text-base text-zinc-800 dark:text-zinc-200 leading-relaxed shadow-2xs"
              >
                <div class="w-6 h-6 rounded-lg bg-emerald-100 dark:bg-emerald-950/80 text-emerald-700 dark:text-emerald-300 flex items-center justify-center font-bold text-xs flex-shrink-0 mt-0.5 font-mono">
                  {{ idx + 1 }}
                </div>
                <div class="flex-1">{{ hl }}</div>
              </div>
            </div>
          </div>

          <!-- Themes Section -->
          <div v-if="r.parsedData.themes && r.parsedData.themes.length > 0" class="space-y-4">
            <div class="flex items-center gap-2 text-sm font-bold text-zinc-900 dark:text-zinc-100 uppercase tracking-wider">
              <span class="w-1.5 h-4 bg-blue-500 rounded-xs"></span>
              <span>聚合主题与深度观察</span>
            </div>

            <div class="grid gap-4">
              <div
                v-for="(theme, tidx) in r.parsedData.themes"
                :key="tidx"
                class="p-5 rounded-2xl border border-zinc-200/80 dark:border-zinc-800/80 bg-white dark:bg-[#151518] shadow-xs space-y-3"
              >
                <div class="flex items-center justify-between gap-3">
                  <h4 class="text-base font-bold text-zinc-900 dark:text-zinc-100 flex items-center gap-2">
                    <span class="text-blue-500 font-mono text-sm">#{{ tidx + 1 }}</span>
                    <span>{{ theme.title }}</span>
                  </h4>
                </div>

                <p class="text-sm text-zinc-600 dark:text-zinc-300 leading-relaxed">
                  {{ theme.summary }}
                </p>

                <!-- Referenced Articles -->
                <div v-if="theme.articles && theme.articles.length > 0" class="pt-3 border-t border-zinc-100 dark:border-zinc-800/60 space-y-2">
                  <div class="text-xs font-medium text-zinc-400">参考文章来源：</div>
                  <div class="flex flex-wrap gap-2">
                    <a
                      v-for="(art, aidx) in theme.articles"
                      :key="aidx"
                      :href="art.url"
                      target="_blank"
                      rel="noopener noreferrer"
                      class="inline-flex items-center gap-1.5 px-3 py-1 rounded-lg bg-zinc-50 dark:bg-zinc-900 border border-zinc-200/60 dark:border-zinc-800/60 text-xs text-zinc-700 dark:text-zinc-300 hover:text-emerald-600 dark:hover:text-emerald-400 hover:border-emerald-300 dark:hover:border-emerald-800 transition-colors group"
                    >
                      <span class="font-medium text-zinc-900 dark:text-zinc-200 group-hover:text-emerald-600 dark:group-hover:text-emerald-400">
                        {{ art.ghName || '公众号' }}
                      </span>
                      <span class="text-zinc-400">·</span>
                      <span class="truncate max-w-[280px]">{{ art.title }}</span>
                      <ExternalLink class="w-3.5 h-3.5 text-zinc-400 group-hover:text-emerald-500 flex-shrink-0" />
                    </a>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- Must Reads Section -->
          <div v-if="r.parsedData.mustReads && r.parsedData.mustReads.length > 0" class="space-y-3">
            <div class="flex items-center gap-2 text-sm font-bold text-zinc-900 dark:text-zinc-100 uppercase tracking-wider">
              <span class="w-1.5 h-4 bg-amber-500 rounded-xs"></span>
              <span>精选必读长文</span>
            </div>

            <div class="grid gap-2.5">
              <a
                v-for="(mr, midx) in r.parsedData.mustReads"
                :key="midx"
                :href="mr.url"
                target="_blank"
                rel="noopener noreferrer"
                class="flex items-center justify-between gap-3 p-4 rounded-xl bg-amber-50/40 dark:bg-amber-950/20 border border-amber-200/50 dark:border-amber-900/40 hover:border-amber-300 dark:hover:border-amber-700 transition-colors group"
              >
                <div class="min-w-0">
                  <div class="text-sm font-semibold text-zinc-900 dark:text-zinc-100 group-hover:text-amber-600 dark:group-hover:text-amber-400 transition-colors truncate">
                    {{ mr.title }}
                  </div>
                  <div class="text-xs text-zinc-400 mt-1">
                    {{ mr.ghName }} · {{ mr.reason || '深度解析' }}
                  </div>
                </div>
                <ExternalLink class="w-4 h-4 text-zinc-400 group-hover:text-amber-500 flex-shrink-0" />
              </a>
            </div>
          </div>
        </div>

        <!-- Markdown Fallback Content -->
        <div
          v-else-if="r.content"
          class="prose prose-zinc dark:prose-invert max-w-none text-base leading-relaxed"
          v-html="renderMarkdown(r.content)"
        ></div>

        <div v-else class="py-6 text-center text-xs text-zinc-400">
          正文生成中或暂无内容
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue';
import { Sparkles, Copy, ExternalLink } from 'lucide-vue-next';
import { api } from '@/api/client';
import { useToast } from '@/composables/useToast';

const toast = useToast();

const reports = ref([]);
const loadingReports = ref(false);
const showCreateForm = ref(false);
const createDays = ref(7);
const customInstruction = ref('');
const isGenerating = ref(false);

function parseReportContent(raw) {
  if (!raw) return null;
  let cleaned = raw.trim();
  if (cleaned.startsWith('```json')) {
    cleaned = cleaned.slice(7);
  } else if (cleaned.startsWith('```')) {
    cleaned = cleaned.slice(3);
  }
  if (cleaned.endsWith('```')) {
    cleaned = cleaned.slice(0, -3);
  }
  cleaned = cleaned.trim();
  try {
    const obj = JSON.parse(cleaned);
    if (obj && (obj.highlights || obj.themes || obj.mustReads)) {
      return obj;
    }
  } catch (e) {
    // not valid JSON, treat as raw markdown
  }
  return null;
}

async function loadReports() {
  loadingReports.value = true;
  try {
    const res = await api.getReports();
    const list = res.items || res.reports || [];
    
    // Fetch full report detail in parallel
    const fullReports = await Promise.all(list.map(async r => {
      try {
        const detail = await api.getReport(r.id);
        const content = detail.content || r.content || '';
        return {
          ...r,
          ...detail,
          title: r.instruction ? `专项研报：${r.instruction}` : `近 ${r.days} 天公众号智能总结`,
          created_at: r.createdAt || r.created_at,
          article_count: r.articleCount ?? r.article_count ?? 0,
          content,
          parsedData: parseReportContent(content),
        };
      } catch (err) {
        return {
          ...r,
          title: r.instruction ? `专项研报：${r.instruction}` : `近 ${r.days} 天公众号智能总结`,
          created_at: r.createdAt || r.created_at,
          article_count: r.articleCount ?? r.article_count ?? 0,
          content: r.content || '',
          parsedData: parseReportContent(r.content || ''),
        };
      }
    }));

    reports.value = fullReports;
  } catch (err) {
    toast.error('加载报告失败: ' + err.message);
  } finally {
    loadingReports.value = false;
  }
}

async function handleGenerate() {
  isGenerating.value = true;
  try {
    await api.createReport({
      days: Number(createDays.value),
      instruction: customInstruction.value.trim() || undefined,
    });
    toast.success('报告生成成功');
    showCreateForm.value = false;
    customInstruction.value = '';
    await loadReports();
  } catch (err) {
    toast.error('生成失败: ' + err.message);
  } finally {
    isGenerating.value = false;
  }
}

function formatDate(ts) {
  if (!ts) return '';
  if (typeof ts === 'number') {
    const d = new Date(ts * 1000);
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
  }
  return String(ts).split('T')[0] || String(ts).slice(0, 10);
}

function copyReport(r) {
  let text = `${r.title}\n时间：${formatDate(r.created_at)}\n\n`;
  if (r.parsedData) {
    if (r.parsedData.highlights?.length) {
      text += '【核心要点】\n';
      r.parsedData.highlights.forEach((h, i) => {
        text += `${i + 1}. ${h}\n`;
      });
      text += '\n';
    }
    if (r.parsedData.themes?.length) {
      text += '【聚合主题】\n';
      r.parsedData.themes.forEach(t => {
        text += `\n# ${t.title}\n${t.summary}\n`;
        if (t.articles?.length) {
          t.articles.forEach(a => {
            text += `- [${a.ghName}] ${a.title} ${a.url}\n`;
          });
        }
      });
    }
  } else {
    text += r.content;
  }

  navigator.clipboard.writeText(text).then(() => {
    toast.success('已复制到剪贴板');
  }).catch(() => {
    toast.error('复制失败');
  });
}

function renderMarkdown(md = '') {
  if (!md) return '';
  return md
    .replace(/^### (.*$)/gim, '<h4 class="font-bold text-sm mt-3 mb-1 text-zinc-900 dark:text-zinc-100">$1</h4>')
    .replace(/^## (.*$)/gim, '<h3 class="font-bold text-base mt-4 mb-2 text-zinc-900 dark:text-zinc-100">$1</h3>')
    .replace(/^# (.*$)/gim, '<h2 class="font-bold text-lg mt-4 mb-2 text-zinc-900 dark:text-zinc-100">$1</h2>')
    .replace(/\*\*(.*?)\*\*/gim, '<strong class="font-semibold text-zinc-900 dark:text-zinc-100">$1</strong>')
    .replace(/\*(.*?)\*/gim, '<em class="text-zinc-600 dark:text-zinc-400">$1</em>')
    .replace(/\n\n/gim, '<br><br>')
    .replace(/\n/gim, '<br>');
}

onMounted(() => {
  loadReports();
});
</script>
