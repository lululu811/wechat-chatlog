<template>
  <div class="max-w-4xl mx-auto space-y-5 pb-20">
    <!-- Top Action & Navigation Bar -->
    <div class="bg-white/90 dark:bg-[#121215]/90 backdrop-blur-md rounded-2xl border border-zinc-200/80 dark:border-zinc-800/80 p-3 sm:p-4 shadow-xs flex flex-wrap items-center justify-between gap-3 sticky top-20 z-30 transition-all">
      <!-- Back Button -->
      <button
        @click="goBack"
        class="inline-flex items-center gap-2 px-3.5 py-1.5 rounded-xl border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-zinc-900 text-sm font-medium text-zinc-700 dark:text-zinc-300 hover:border-emerald-500 hover:text-emerald-600 dark:hover:text-emerald-400 shadow-xs transition-all cursor-pointer"
      >
        <ArrowLeft class="w-4 h-4" />
        <span>{{ backLabel }}</span>
      </button>

      <!-- Action Buttons on Right -->
      <div class="flex flex-wrap items-center gap-2">
        <!-- Bookmark Button -->
        <button
          v-if="article"
          @click="handleToggleBookmark"
          class="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-xl border text-xs sm:text-sm font-medium transition-all shadow-xs cursor-pointer"
          :class="article.is_bookmarked ? 'bg-amber-50 dark:bg-amber-950/60 border-amber-300 dark:border-amber-800 text-amber-600 dark:text-amber-400 font-semibold' : 'border-zinc-200 dark:border-zinc-800 bg-white dark:bg-zinc-900 text-zinc-600 dark:text-zinc-400 hover:text-amber-500'"
        >
          <Bookmark class="w-4 h-4" :class="{ 'fill-current': article.is_bookmarked }" />
          <span>{{ article.is_bookmarked ? '已收藏' : '收藏' }}</span>
        </button>

        <!-- Single Export MD -->
        <button
          v-if="article"
          @click="handleExportMD"
          :disabled="actionLoading"
          class="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-xl border text-xs sm:text-sm font-medium transition-all shadow-xs cursor-pointer disabled:opacity-50"
          :class="article.is_exported ? 'bg-emerald-50 dark:bg-emerald-950/60 border-emerald-300 dark:border-emerald-800 text-emerald-700 dark:text-emerald-400' : 'bg-white dark:bg-zinc-900 border-zinc-200 dark:border-zinc-800 text-zinc-700 dark:text-zinc-300 hover:bg-emerald-50 dark:hover:bg-emerald-950/40 hover:text-emerald-600'"
          title="导出单篇为本地 Markdown（包含高清图片）"
        >
          <FileDown class="w-4 h-4" />
          <span>{{ article.is_exported ? '重新导出 MD' : '导出 MD' }}</span>
        </button>

        <!-- Single Push IMA -->
        <button
          v-if="article"
          @click="handlePushIMA"
          :disabled="actionLoading"
          class="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-xl border text-xs sm:text-sm font-medium transition-all shadow-xs cursor-pointer disabled:opacity-50"
          :class="article.is_pushed ? 'bg-indigo-50 dark:bg-indigo-950/60 border-indigo-300 dark:border-indigo-800 text-indigo-700 dark:text-indigo-400' : 'bg-white dark:bg-zinc-900 border-zinc-200 dark:border-zinc-800 text-zinc-700 dark:text-zinc-300 hover:bg-indigo-50 dark:hover:bg-indigo-950/40 hover:text-indigo-600'"
          title="推送单篇至 IMA 知识库"
        >
          <Send class="w-4 h-4" />
          <span>{{ article.is_pushed ? '已同步 IMA' : '推 IMA' }}</span>
        </button>

        <!-- Single AI Summary -->
        <button
          v-if="article"
          @click="handleSummarize"
          :disabled="actionLoading"
          class="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-xl border text-xs sm:text-sm font-medium transition-all shadow-xs cursor-pointer disabled:opacity-50"
          :class="article.is_summarized ? 'bg-purple-50 dark:bg-purple-950/60 border-purple-300 dark:border-purple-800 text-purple-700 dark:text-purple-400' : 'bg-white dark:bg-zinc-900 border-zinc-200 dark:border-zinc-800 text-zinc-700 dark:text-zinc-300 hover:bg-purple-50 dark:hover:bg-purple-950/40 hover:text-purple-600'"
          title="调用大模型提炼核心要点"
        >
          <Sparkles class="w-4 h-4" />
          <span>{{ article.is_summarized ? '重新提炼 AI' : 'AI 摘要' }}</span>
        </button>

        <!-- External Link to WeChat -->
        <a
          v-if="article?.url"
          :href="article.url"
          target="_blank"
          rel="noopener noreferrer"
          class="inline-flex items-center gap-1 px-3 py-1.5 rounded-xl border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-zinc-900 text-xs sm:text-sm font-medium text-zinc-600 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 hover:border-zinc-300 dark:hover:border-zinc-700 transition-all shadow-xs"
          title="在微信客户端或浏览器中打开原文"
        >
          <span>微信原文</span>
          <ExternalLink class="w-3.5 h-3.5" />
        </a>
      </div>
    </div>

    <!-- Loading State -->
    <div v-if="loading" class="py-32 text-center text-zinc-400 space-y-3 bg-white dark:bg-[#121215] rounded-3xl border border-zinc-200/80 dark:border-zinc-800/80 p-12">
      <div class="w-8 h-8 border-2 border-emerald-500/20 border-t-emerald-500 rounded-full animate-spin mx-auto"></div>
      <p class="text-sm font-medium text-zinc-600 dark:text-zinc-400">正在加载文章详情与正文…</p>
    </div>

    <!-- Error State -->
    <div v-else-if="error" class="p-12 text-center bg-white dark:bg-[#121215] rounded-3xl border border-zinc-200 dark:border-zinc-800 space-y-4">
      <AlertCircle class="w-10 h-10 text-rose-500 mx-auto" />
      <h3 class="text-base font-bold text-zinc-900 dark:text-zinc-100">加载文章失败</h3>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ error }}</p>
      <button
        @click="loadArticle"
        class="px-5 py-2.5 rounded-xl bg-emerald-600 text-white text-sm font-medium hover:bg-emerald-500 transition-colors shadow-xs"
      >
        重试
      </button>
    </div>

    <!-- Unified Master Reading Canvas -->
    <article v-else-if="article" class="bg-white dark:bg-[#121215] rounded-3xl border border-zinc-200/80 dark:border-zinc-800/80 p-6 sm:p-12 md:p-14 shadow-xs space-y-8">
      <!-- Article Header -->
      <header class="space-y-4 pb-6 border-b border-zinc-100 dark:border-zinc-800/80">
        <!-- Metadata Row -->
        <div class="flex flex-wrap items-center justify-between gap-3 text-sm">
          <div class="flex flex-wrap items-center gap-2.5">
            <span class="inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-xs font-semibold bg-emerald-50 dark:bg-emerald-950/80 text-emerald-800 dark:text-emerald-300 border border-emerald-200/70 dark:border-emerald-800/60">
              <span class="w-1.5 h-1.5 rounded-full bg-emerald-500"></span>
              {{ article.gh_name || article.gh_id || '微信公众号' }}
            </span>
            <span v-if="article.author" class="text-xs text-zinc-500 dark:text-zinc-400 font-medium">
              作者: {{ article.author }}
            </span>
            <span class="text-xs text-zinc-400 font-mono">
              发布于 {{ formatFullDate(article.publish_time) }}
            </span>
          </div>

          <!-- Status Indicators -->
          <div class="flex items-center gap-2">
            <span
              v-if="article.is_exported"
              class="inline-flex items-center gap-1 px-2.5 py-0.5 rounded-md text-xs font-medium bg-emerald-50 dark:bg-emerald-950/60 text-emerald-700 dark:text-emerald-400 border border-emerald-200/60 dark:border-emerald-800/50"
            >
              <Check class="w-3.5 h-3.5" />
              <span>本地已归档</span>
            </span>
            <span
              v-if="article.is_pushed"
              class="inline-flex items-center gap-1 px-2.5 py-0.5 rounded-md text-xs font-medium bg-indigo-50 dark:bg-indigo-950/60 text-indigo-700 dark:text-indigo-400 border border-indigo-200/60 dark:border-indigo-800/50"
            >
              <Send class="w-3.5 h-3.5" />
              <span>IMA 已入库</span>
            </span>
          </div>
        </div>

        <!-- Article Title -->
        <h1 class="text-2xl sm:text-3xl md:text-4xl font-bold font-serif text-zinc-900 dark:text-zinc-100 leading-tight tracking-tight pt-1">
          {{ article.title || '无标题文章' }}
        </h1>
      </header>

      <!-- Section: AI Executive Briefing Callout (When available) -->
      <section 
        v-if="hasSummaryContent"
        class="p-6 sm:p-7 rounded-2xl bg-gradient-to-br from-purple-50/70 via-indigo-50/30 to-white dark:from-purple-950/30 dark:via-indigo-950/20 dark:to-[#16161a] border border-purple-200/80 dark:border-purple-800/60 shadow-xs space-y-4.5"
      >
        <!-- AI Card Header -->
        <div class="flex items-center justify-between gap-3">
          <div class="flex items-center gap-2">
            <div class="w-7 h-7 rounded-lg bg-purple-600 text-white flex items-center justify-center shadow-xs">
              <Sparkles class="w-4 h-4" />
            </div>
            <h3 class="text-base font-bold text-purple-950 dark:text-purple-200">
              AI 深度提炼与核心要点
            </h3>
          </div>
          <div class="flex items-center gap-2">
            <span v-if="article.mustRead" class="text-xs font-mono font-bold text-amber-700 dark:text-amber-400 bg-amber-50 dark:bg-amber-950/60 px-2.5 py-1 rounded-lg border border-amber-200/60 dark:border-amber-900/60">
              ★ 推荐指数 {{ article.mustRead }}/10
            </span>
            <span class="text-xs font-medium text-purple-700 dark:text-purple-300 bg-purple-100/70 dark:bg-purple-900/50 px-2.5 py-1 rounded-lg">
              核心内参
            </span>
          </div>
        </div>

        <!-- Executive Summary Paragraph -->
        <p v-if="cleanDigestText" class="text-sm sm:text-base text-purple-950 dark:text-purple-200 leading-relaxed font-sans font-normal border-l-3 border-purple-400 dark:border-purple-600 pl-3.5">
          {{ cleanDigestText }}
        </p>

        <!-- Highlights Takeaways List -->
        <div v-if="article.highlights?.length" class="space-y-2.5 pt-2">
          <div class="text-xs font-bold text-purple-900 dark:text-purple-300 uppercase tracking-wider flex items-center gap-1.5">
            <span>关键论点速览 (Key Takeaways)</span>
          </div>
          <div class="grid gap-2.5">
            <div
              v-for="(hl, hidx) in article.highlights"
              :key="hidx"
              class="p-3.5 rounded-xl bg-white/90 dark:bg-zinc-900/80 border border-purple-100 dark:border-purple-900/40 text-sm sm:text-base text-zinc-800 dark:text-zinc-200 leading-relaxed flex items-start gap-3 shadow-2xs"
            >
              <span class="w-5 h-5 rounded-full bg-purple-100 dark:bg-purple-900/80 text-purple-700 dark:text-purple-300 font-mono font-bold text-xs flex items-center justify-center flex-shrink-0 mt-0.5">
                {{ hidx + 1 }}
              </span>
              <span class="flex-1 min-w-0">{{ hl }}</span>
            </div>
          </div>
        </div>

        <!-- Themes and Keywords Tags -->
        <div v-if="article.keywords?.length || article.themes?.length" class="flex flex-wrap items-center gap-1.5 pt-2 border-t border-purple-100/80 dark:border-purple-900/30">
          <span
            v-for="(th, tidx) in (article.themes || [])"
            :key="'th-' + tidx"
            class="px-2.5 py-0.5 rounded-md text-xs font-medium bg-purple-100/60 dark:bg-purple-900/40 text-purple-700 dark:text-purple-300"
          >
            主题: {{ th }}
          </span>
          <span
            v-for="(kw, kidx) in (article.keywords || [])"
            :key="'kw-' + kidx"
            class="px-2.5 py-0.5 rounded-md text-xs font-medium bg-zinc-100 dark:bg-zinc-800 text-zinc-600 dark:text-zinc-300"
          >
            # {{ kw }}
          </span>
        </div>
      </section>

      <!-- No AI Summary Banner Prompting One-Click Generation -->
      <section 
        v-else 
        class="p-5 rounded-2xl bg-zinc-50 dark:bg-zinc-900/40 border border-dashed border-zinc-200 dark:border-zinc-800 flex flex-wrap items-center justify-between gap-4"
      >
        <div class="flex items-center gap-3">
          <Sparkles class="w-5 h-5 text-zinc-400" />
          <div>
            <p class="text-sm font-medium text-zinc-800 dark:text-zinc-200">本篇尚未生成大模型摘要</p>
            <p class="text-xs text-zinc-400 mt-0.5">点击右侧按钮调用大模型一键提炼核心要点与论据</p>
          </div>
        </div>
        <button
          @click="handleSummarize"
          :disabled="actionLoading"
          class="px-4 py-2 rounded-xl bg-purple-600 hover:bg-purple-500 text-white text-xs sm:text-sm font-medium transition-colors shadow-xs flex-shrink-0 cursor-pointer disabled:opacity-50"
        >
          立即生成摘要
        </button>
      </section>

      <!-- Article Body Reader -->
      <section class="space-y-6 pt-2">
        <!-- Render Rich HTML / Markdown if content exists -->
        <div 
          v-if="article.content" 
          v-html="renderMarkdown(article.content, article.id)"
          class="article-markdown text-zinc-800 dark:text-zinc-200 text-base sm:text-[17px] leading-8 sm:leading-9 tracking-normal"
        ></div>

        <!-- Fallback: Description & prompt to read original -->
        <div v-else class="space-y-6 py-6">
          <div v-if="article.description" class="p-6 rounded-2xl bg-zinc-50 dark:bg-zinc-900/60 text-base text-zinc-700 dark:text-zinc-300 leading-relaxed border border-zinc-200/60 dark:border-zinc-800">
            <p class="font-bold text-zinc-900 dark:text-zinc-100 mb-2 flex items-center gap-2">
              <span class="w-1.5 h-4 bg-emerald-500 rounded-sm"></span>
              <span>文章导读 / 摘要：</span>
            </p>
            <p>{{ article.description }}</p>
          </div>

          <div class="text-center py-14 px-6 rounded-2xl border border-dashed border-zinc-200 dark:border-zinc-800 space-y-4">
            <ExternalLink class="w-9 h-9 text-zinc-300 dark:text-zinc-600 mx-auto" />
            <h4 class="text-base font-bold text-zinc-800 dark:text-zinc-200">
              微信公众号正文离线预览未缓存完整排版
            </h4>
            <p class="text-xs text-zinc-400 max-w-md mx-auto">
              您可以点击上方「导出 MD」按钮抓取完整排版及图片，或直接在微信中查看原文
            </p>
            <a
              :href="article.url"
              target="_blank"
              rel="noopener noreferrer"
              class="inline-flex items-center gap-2 px-5 py-2.5 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white text-sm font-semibold transition-colors shadow-xs mt-2"
            >
              <span>在微信中阅读原文全文</span>
              <ExternalLink class="w-4 h-4" />
            </a>
          </div>
        </div>
      </section>

      <!-- Article Footer -->
      <footer class="pt-8 border-t border-zinc-100 dark:border-zinc-800/80 flex flex-wrap items-center justify-between gap-4 text-xs text-zinc-400">
        <div class="flex items-center gap-2">
          <span>❖ 本文阅读完毕</span>
          <span class="text-zinc-300 dark:text-zinc-700">|</span>
          <span class="font-mono">ID: {{ article.id }}</span>
        </div>

        <div class="flex items-center gap-3">
          <button
            @click="scrollToTop"
            class="text-xs text-zinc-500 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100 underline decoration-zinc-300"
          >
            ↑ 回到顶部
          </button>
          <button
            @click="goBack"
            class="text-xs text-emerald-600 dark:text-emerald-400 hover:underline font-semibold"
          >
            ← {{ backLabel }}
          </button>
        </div>
      </footer>
    </article>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { 
  ArrowLeft, ExternalLink, Bookmark, Sparkles, 
  FileDown, Send, Check, AlertCircle 
} from 'lucide-vue-next';
import { marked } from 'marked';
import { api } from '@/api/client';
import { useToast } from '@/composables/useToast';

const route = useRoute();
const router = useRouter();
const toast = useToast();

const articleId = computed(() => route.params.id);
const article = ref(null);
const loading = ref(true);
const error = ref(null);
const actionLoading = ref(false);

const fromSource = computed(() => route.query.from || '');
const backLabel = computed(() => {
  if (fromSource.value === 'digest') return '返回精选看点';
  return '返回文章流';
});

function goBack() {
  if (fromSource.value === 'digest') {
    router.push({ path: '/biz/digest' });
  } else {
    router.push({ path: '/biz' });
  }
}

function scrollToTop() {
  window.scrollTo({ top: 0, behavior: 'smooth' });
}

// Clean and validate digest text
const cleanDigestText = computed(() => {
  if (!article.value) return '';
  const d = article.value.digest || article.value.ai_summary || '';
  const trimmed = d.trim();
  if (trimmed === '|-' || trimmed === '|' || trimmed === '-' || trimmed.startsWith('|-')) {
    return '';
  }
  return trimmed;
});

const hasSummaryContent = computed(() => {
  if (!article.value) return false;
  return Boolean(cleanDigestText.value || (article.value.highlights && article.value.highlights.length > 0));
});

async function loadArticle() {
  loading.value = true;
  error.value = null;
  try {
    const res = await api.getArticle(articleId.value);
    const expStatus = res.exportStatus || res.export_status || '';
    const pushStatus = res.pushStatus || res.push_status || '';
    const isExported = expStatus === 'exported' || expStatus === 'summary_generated';
    const isPushed = pushStatus === 'pushed';
    const isSummarized = expStatus === 'summary_generated' || Boolean(res.digest || res.ai_summary || (res.highlights && res.highlights.length > 0));

    article.value = {
      ...res,
      id: res.id,
      gh_id: res.ghID || res.gh_id,
      gh_name: res.ghName || res.gh_name,
      title: res.title,
      description: res.description || res.desc,
      url: res.url,
      publish_time: res.publishedAt || res.publish_time,
      is_bookmarked: res.bookmarked ?? res.is_bookmarked ?? false,
      is_read: res.isRead ?? res.is_read ?? false,
      is_exported: isExported,
      is_pushed: isPushed,
      is_summarized: isSummarized,
      export_status: expStatus,
      push_status: pushStatus,
      highlights: res.highlights || [],
      themes: res.themes || [],
      keywords: res.keywords || [],
      mustRead: res.mustRead || 0,
      content: res.content || '',
      digest: res.digest || '',
    };
  } catch (err) {
    error.value = err.message || '加载文章详情失败';
  } finally {
    loading.value = false;
  }
}

async function handleExportMD() {
  if (!article.value) return;
  actionLoading.value = true;
  try {
    toast.info('正在抓取排版并导出本地 Markdown…');
    await api.exportArticleMD(article.value.id);
    toast.success('已导出为本地 Markdown！');
    await loadArticle();
  } catch (err) {
    toast.error('导出失败: ' + err.message);
  } finally {
    actionLoading.value = false;
  }
}

async function handlePushIMA() {
  if (!article.value) return;
  actionLoading.value = true;
  try {
    toast.info('正在将文章推送到 IMA 知识库…');
    await api.pushArticleIMA(article.value.id);
    toast.success('已成功推送到 IMA 知识库！');
    await loadArticle();
  } catch (err) {
    toast.error('推送到 IMA 失败: ' + err.message);
  } finally {
    actionLoading.value = false;
  }
}

async function handleSummarize() {
  if (!article.value) return;
  actionLoading.value = true;
  try {
    toast.info('正在调用大模型提炼核心要点…');
    const res = await api.generateArticleSummary(article.value.id);
    toast.success('已生成 AI 深度要点摘要！');
    if (res.summary) {
      article.value.digest = res.summary;
      article.value.is_summarized = true;
    }
    await loadArticle();
  } catch (err) {
    toast.error('生成摘要失败: ' + err.message);
  } finally {
    actionLoading.value = false;
  }
}

async function handleToggleBookmark() {
  if (!article.value) return;
  const next = !article.value.is_bookmarked;
  try {
    await api.toggleBookmark(article.value.id, next);
    article.value.is_bookmarked = next;
    toast.success(next ? '已加入我的收藏' : '已取消收藏');
  } catch (err) {
    toast.error('更新收藏失败: ' + err.message);
  }
}

function formatFullDate(ts) {
  if (!ts) return '';
  if (typeof ts === 'number') {
    const d = new Date(ts * 1000);
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
  }
  return String(ts).replace('T', ' ').slice(0, 16);
}

// Clean and prepare raw markdown before marked compilation
function cleanRawMarkdown(raw = '') {
  let text = raw.trim();

  // 1. 去除开头的 # 标题（因为大标题已在顶部以高阶排版渲染）
  text = text.replace(/^#\s+[^\n]+\n*/, '');

  // 2. 去除开头的重复元数据引用块 (> 公众号: ... > 发布时间: ... > 原文链接: ...)
  text = text.replace(/^(?:>[^\n]*\n*)+/, '');

  // 3. 去除紧接着的分隔线 --- 或 ***
  text = text.replace(/^(?:---|---\s*|\*\*\*)\n*/, '');

  // 4. 去除公号正文前的推广宣传二维码广告图与联络信息块
  text = text.replace(/^(?:!\[[^\]]*\]\([^\)]+\)\s*)+(?=电话|手机|微信|来源|作者)/, '');
  text = text.replace(/^电话\s*\|[^\n]+\n*/m, '');
  text = text.replace(/^微信\s*\|[^\n]+\n*/m, '');

  // 5. 去除微信不可展示的占位符图标 (.other)
  text = text.replace(/^[ \t]*!\[.*?\]\([^\)]*\.other\)[ \t]*\n*/gm, '');

  return text.trim();
}

function renderMarkdown(content = '', id = '') {
  if (!content) return '';

  // If already full HTML
  if (content.includes('<!DOCTYPE html>') || (content.includes('<html') && content.includes('</html>'))) {
    return content;
  }

  const cleaned = cleanRawMarkdown(content);

  // Configure marked renderer
  const renderer = new marked.Renderer();

  // Custom Image rendering: rewrite local relative paths and hide broken formats cleanly
  renderer.image = function(token) {
    let src = token.href || '';
    if (src.startsWith('images/') || src.startsWith('./images/')) {
      const cleanPath = src.replace(/^\.\//, '');
      src = `/api/v1/biz/articles/${id}/media/${cleanPath}`;
    }
    if (src.endsWith('.other')) {
      return '';
    }

    const cleanAlt = (token.text || '').trim();
    const hasMeaningfulCaption = cleanAlt && cleanAlt !== '图片' && cleanAlt !== 'Image' && cleanAlt !== 'img';

    return `
      <figure class="my-7 text-center">
        <img 
          src="${src}" 
          alt="${cleanAlt}" 
          loading="lazy"
          class="rounded-2xl max-w-full mx-auto shadow-sm border border-zinc-200/70 dark:border-zinc-800" 
          onerror="this.closest('figure')?.remove ? this.closest('figure').remove() : this.style.display='none'"
        />
        ${hasMeaningfulCaption ? `<figcaption class="text-xs text-zinc-400 mt-2.5 text-center">${cleanAlt}</figcaption>` : ''}
      </figure>
    `;
  };

  // Custom Heading rendering
  renderer.heading = function(token) {
    const inlineHtml = this.parser.parseInline(token.tokens);
    if (token.depth === 1 || token.depth === 2) {
      return `<h2 class="text-xl sm:text-2xl font-bold font-serif text-zinc-900 dark:text-zinc-100 mt-10 mb-4 pb-2.5 border-b border-zinc-100 dark:border-zinc-800/80">${inlineHtml}</h2>`;
    }
    if (token.depth === 3) {
      return `<h3 class="text-lg sm:text-xl font-bold text-zinc-900 dark:text-zinc-100 mt-7 mb-3">${inlineHtml}</h3>`;
    }
    return `<h4 class="text-base sm:text-lg font-bold text-zinc-900 dark:text-zinc-100 mt-5 mb-2">${inlineHtml}</h4>`;
  };

  // Custom Blockquote rendering
  renderer.blockquote = function(token) {
    const bodyHtml = this.parser.parse(token.tokens);
    return `<blockquote class="border-l-4 border-emerald-500 pl-4 py-2.5 my-5 text-zinc-700 dark:text-zinc-300 italic bg-zinc-50/70 dark:bg-zinc-900/40 rounded-r-xl">${bodyHtml}</blockquote>`;
  };

  // Custom Paragraph rendering
  renderer.paragraph = function(token) {
    const inlineHtml = this.parser.parseInline(token.tokens);
    const trimmed = inlineHtml.trim();
    if (!trimmed || trimmed === '图片' || trimmed === 'Image') return '';
    if (trimmed.startsWith('<figure') || trimmed.startsWith('<div class="overflow-x-auto')) {
      return trimmed;
    }

    // 作者与来源元数据微样式
    if (trimmed.startsWith('来源：') || trimmed.startsWith('作者：') || trimmed.startsWith('来源:') || trimmed.startsWith('作者:')) {
      return `<p class="my-2.5 text-sm font-medium text-zinc-500 dark:text-zinc-400 flex items-center gap-1.5"><span class="w-1.5 h-1.5 rounded-full bg-zinc-400"></span>${inlineHtml}</p>`;
    }

    // 智能识别微信无 ## 标头的段落小标题（字数短、无句号等句尾标点）
    const isHeadingCandidate = trimmed.length > 3 && trimmed.length < 32 && 
      !/[。，；！？!?,:：]$/.test(trimmed) && 
      !trimmed.includes('<img') && 
      !trimmed.includes('<a') &&
      !trimmed.includes('电话') &&
      !trimmed.includes('手机');

    if (isHeadingCandidate) {
      return `<h3 class="text-xl sm:text-2xl font-bold font-serif text-zinc-900 dark:text-zinc-100 mt-10 mb-4 pb-2 border-b border-zinc-100 dark:border-zinc-800/80 flex items-center gap-2.5"><span class="w-1.5 h-5 bg-emerald-500 rounded-sm inline-block"></span><span>${inlineHtml}</span></h3>`;
    }

    return `<p class="my-5 text-base sm:text-[17px] text-zinc-800 dark:text-zinc-200 leading-8 sm:leading-9 font-normal">${inlineHtml}</p>`;
  };

  // Custom Table rendering
  renderer.table = function({ header, rows }) {
    let headerHtml = '';
    if (header) {
      headerHtml = `<thead><tr>${header.map(c => `<th class="p-3 bg-zinc-50 dark:bg-zinc-900/80 border-b border-zinc-200 dark:border-zinc-800 text-xs font-semibold text-zinc-900 dark:text-zinc-100">${c.text}</th>`).join('')}</tr></thead>`;
    }
    let bodyHtml = '';
    if (rows && rows.length) {
      bodyHtml = `<tbody>${rows.map(r => `<tr class="hover:bg-zinc-50/50 dark:hover:bg-zinc-900/40 transition-colors">${r.map(c => `<td class="p-3 border-b border-zinc-100 dark:border-zinc-800/60 text-xs sm:text-sm text-zinc-700 dark:text-zinc-300">${c.text}</td>`).join('')}</tr>`).join('')}</tbody>`;
    }
    return `
      <div class="overflow-x-auto my-7 border border-zinc-200/80 dark:border-zinc-800 rounded-2xl shadow-2xs">
        <table class="w-full text-left border-collapse">
          ${headerHtml}
          ${bodyHtml}
        </table>
      </div>
    `;
  };

  marked.use({ renderer });
  return marked.parse(cleaned);
}

onMounted(() => {
  loadArticle();
});
</script>

<style scoped>
:deep(.article-markdown) a {
  color: #059669;
  text-decoration: underline;
  text-underline-offset: 3px;
}
:deep(.article-markdown) a:hover {
  color: #047857;
}
:deep(.article-markdown) strong {
  font-weight: 600;
  color: inherit;
}
:deep(.article-markdown) ul {
  list-style-type: disc;
  padding-left: 1.5rem;
  margin: 1rem 0;
}
:deep(.article-markdown) ol {
  list-style-type: decimal;
  padding-left: 1.5rem;
  margin: 1rem 0;
}
:deep(.article-markdown) li {
  margin: 0.35rem 0;
}
:deep(.article-markdown) hr {
  border-color: rgba(228, 228, 231, 0.6);
  margin: 2rem 0;
}
</style>
