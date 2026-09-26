<template>
  <div v-if="account">
    <!-- Backdrop -->
    <div 
      class="fixed inset-0 z-50 bg-black/40 backdrop-blur-xs transition-opacity"
      @click="$emit('close')"
    ></div>

    <!-- Slide-over Drawer -->
    <div 
      class="fixed inset-y-0 right-0 z-50 w-full max-w-lg bg-white dark:bg-[#121215] shadow-2xl border-l border-zinc-200 dark:border-zinc-800 flex flex-col transform transition-transform duration-300 ease-out animate-slide-in"
    >
      <!-- Drawer Header -->
      <div class="px-6 py-5 border-b border-zinc-200/80 dark:border-zinc-800 flex items-start justify-between gap-4">
        <div class="flex items-center gap-4 min-w-0">
          <div class="w-12 h-12 rounded-xl bg-emerald-50 dark:bg-emerald-950/80 text-emerald-600 dark:text-emerald-400 flex items-center justify-center font-bold text-lg border border-emerald-200/60 dark:border-emerald-800/60 flex-shrink-0">
            {{ (account.name || '公').slice(0, 1) }}
          </div>
          <div class="min-w-0">
            <h2 class="text-base font-bold text-zinc-900 dark:text-zinc-100 truncate">
              {{ account.name }}
            </h2>
            <div class="flex items-center gap-2 mt-1">
              <span class="text-xs font-mono text-zinc-400 truncate">{{ account.gh_id }}</span>
              <span class="text-[11px] px-2 py-0.5 rounded-full font-medium" :class="account.is_hidden ? 'bg-zinc-100 text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400' : 'bg-emerald-50 text-emerald-600 dark:bg-emerald-950/60 dark:text-emerald-400'">
                {{ account.is_hidden ? '已隐藏' : '展示中' }}
              </span>
            </div>
          </div>
        </div>

        <button 
          @click="$emit('close')"
          class="p-2 text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-200 rounded-lg hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors"
        >
          <X class="w-5 h-5" />
        </button>
      </div>

      <!-- Drawer Body -->
      <div class="flex-1 overflow-y-auto px-6 py-5 space-y-6">
        <!-- Quick Actions Row -->
        <div class="flex items-center gap-3">
          <button
            @click="toggleWatch"
            class="flex-1 inline-flex items-center justify-center gap-2 px-3.5 py-2 rounded-xl border text-xs font-semibold transition-all shadow-xs"
            :class="account.is_watched ? 'bg-amber-50 dark:bg-amber-950/40 border-amber-300 dark:border-amber-800 text-amber-600 dark:text-amber-400' : 'border-zinc-200 dark:border-zinc-800 text-zinc-600 dark:text-zinc-400 hover:bg-zinc-50 dark:hover:bg-zinc-800'"
          >
            <Star class="w-4 h-4" :class="{ 'fill-current': account.is_watched }" />
            <span>{{ account.is_watched ? '已设为重点关注' : '加入重点关注' }}</span>
          </button>

          <router-link
            :to="`/biz?account=${account.gh_id}`"
            class="inline-flex items-center justify-center gap-1.5 px-3.5 py-2 rounded-xl border border-zinc-200 dark:border-zinc-800 text-xs font-semibold text-zinc-700 dark:text-zinc-300 hover:bg-zinc-50 dark:hover:bg-zinc-800 transition-colors shadow-xs"
          >
            <ExternalLink class="w-4 h-4" />
            <span>查看发文</span>
          </router-link>
        </div>

        <!-- Tag assignment section -->
        <div class="space-y-3">
          <div class="flex items-center justify-between">
            <h3 class="text-xs font-bold text-zinc-400 uppercase tracking-wider">分类标签</h3>
            <span class="text-[11px] text-zinc-400">点击标签即可即时关联</span>
          </div>

          <div class="flex flex-wrap gap-2">
            <button
              v-for="t in allTags"
              :key="t.id"
              @click="toggleTag(t.id)"
              class="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full text-xs font-medium border transition-all"
              :style="isTagActive(t.id) ? { backgroundColor: t.color, borderColor: t.color, color: '#fff' } : {}"
              :class="!isTagActive(t.id) ? 'border-zinc-200 dark:border-zinc-800 text-zinc-600 dark:text-zinc-400 hover:border-zinc-400 dark:hover:border-zinc-600 bg-white dark:bg-zinc-800/60' : 'shadow-xs'"
            >
              <span v-if="!isTagActive(t.id)" class="w-2 h-2 rounded-full" :style="{ backgroundColor: t.color }"></span>
              <Check v-else class="w-3.5 h-3.5 stroke-[2.5]" />
              <span>{{ t.name }}</span>
            </button>
          </div>
        </div>

        <!-- Recent Articles -->
        <div class="space-y-3">
          <div class="flex items-center justify-between">
            <h3 class="text-xs font-bold text-zinc-400 uppercase tracking-wider">最近发文预览 (最新 5 篇)</h3>
            <span class="text-[11px] text-zinc-400 font-mono">共 {{ account.article_count || 0 }} 篇</span>
          </div>

          <div v-if="loadingArticles" class="py-10 text-center text-xs text-zinc-400">
            加载文章中…
          </div>

          <div v-else-if="articles.length === 0" class="py-10 text-center text-xs text-zinc-400">
            暂无发文记录
          </div>

          <div v-else class="space-y-2.5">
            <div
              v-for="a in articles"
              :key="a.id"
              class="p-3.5 rounded-xl bg-zinc-50/70 dark:bg-zinc-900/50 border border-zinc-200/60 dark:border-zinc-800/80 hover:border-zinc-300 dark:hover:border-zinc-700 transition-all space-y-2"
            >
              <div class="text-sm font-medium text-zinc-900 dark:text-zinc-100 line-clamp-2 leading-snug">
                {{ a.title || '无标题文章' }}
              </div>

              <div class="flex items-center justify-between text-xs text-zinc-400 pt-1">
                <span class="font-mono text-[11px]">{{ formatDate(a.publish_time) }}</span>
                
                <div class="flex items-center gap-1.5">
                  <!-- Single MD Export button -->
                  <button
                    @click="exportMD(a)"
                    class="px-2 py-0.5 rounded text-[11px] font-mono font-medium transition-colors"
                    :class="a.is_exported ? 'bg-emerald-50 dark:bg-emerald-950/70 text-emerald-600 dark:text-emerald-400 border border-emerald-200 dark:border-emerald-800' : 'bg-zinc-200 dark:bg-zinc-800 text-zinc-700 dark:text-zinc-300 hover:bg-emerald-600 hover:text-white'"
                    :title="a.is_exported ? '已导出 Markdown，点击可重新导出' : '单篇导出为本地 Markdown'"
                  >
                    {{ a.is_exported ? '✓ MD' : '导出 MD' }}
                  </button>

                  <!-- Single IMA Push button -->
                  <button
                    @click="pushIMA(a)"
                    class="px-2 py-0.5 rounded text-[11px] font-mono font-medium transition-colors"
                    :class="a.is_pushed ? 'bg-indigo-50 dark:bg-indigo-950/70 text-indigo-600 dark:text-indigo-400 border border-indigo-200 dark:border-indigo-800' : 'bg-zinc-200 dark:bg-zinc-800 text-zinc-700 dark:text-zinc-300 hover:bg-indigo-600 hover:text-white'"
                    :title="a.is_pushed ? '已推送到 IMA' : (!a.is_exported ? '需先导出 MD' : '单篇推送到 IMA')"
                  >
                    {{ a.is_pushed ? '✓ IMA' : '推 IMA' }}
                  </button>

                  <!-- Single AI Summary button -->
                  <button
                    @click="summarize(a)"
                    class="px-2 py-0.5 rounded text-[11px] font-mono font-medium transition-colors"
                    :class="a.is_summarized ? 'bg-purple-50 dark:bg-purple-950/70 text-purple-600 dark:text-purple-400 border border-purple-200 dark:border-purple-800' : 'bg-zinc-200 dark:bg-zinc-800 text-zinc-700 dark:text-zinc-300 hover:bg-purple-600 hover:text-white'"
                    :title="a.is_summarized ? '已生成 AI 摘要' : '单篇生成 AI 摘要'"
                  >
                    {{ a.is_summarized ? '✓ AI' : 'AI 摘要' }}
                  </button>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, watch } from 'vue';
import { X, Star, ExternalLink, Check } from 'lucide-vue-next';
import { api } from '@/api/client';
import { useToast } from '@/composables/useToast';

const props = defineProps({
  account: Object,
  allTags: Array,
});

const emit = defineEmits(['close', 'account-updated']);
const toast = useToast();

const articles = ref([]);
const loadingArticles = ref(false);

watch(() => props.account, (newAcc) => {
  if (newAcc) {
    const ghid = newAcc.ghID || newAcc.gh_id;
    if (ghid) loadArticles(ghid);
  } else {
    articles.value = [];
  }
}, { immediate: true });

async function loadArticles(ghid) {
  loadingArticles.value = true;
  try {
    const res = await api.getArticles({ ghid, limit: 5 });
    const list = res.items || res.articles || [];
    articles.value = list.map(a => {
      const expStatus = a.exportStatus || a.export_status || '';
      const pushStatus = a.pushStatus || a.push_status || '';
      return {
        ...a,
        id: a.id,
        title: a.title,
        publish_time: a.publishedAt || a.publish_time,
        is_exported: expStatus === 'exported' || expStatus === 'summary_generated',
        is_pushed: pushStatus === 'pushed',
        is_summarized: expStatus === 'summary_generated' || Boolean(a.digest || a.ai_summary),
      };
    });
  } catch {
    articles.value = [];
  } finally {
    loadingArticles.value = false;
  }
}

function isTagActive(tagId) {
  if (!props.account || !props.account.tags) return false;
  return props.account.tags.some(t => t.id === tagId);
}

async function toggleTag(tagId) {
  if (!props.account) return;
  const ghid = props.account.ghID || props.account.gh_id;
  const currentTagIds = (props.account.tags || []).map(t => t.id);
  let newTagIds = [];
  if (currentTagIds.includes(tagId)) {
    newTagIds = currentTagIds.filter(id => id !== tagId);
  } else {
    newTagIds = [...currentTagIds, tagId];
  }

  try {
    await api.setAccountTags([ghid], newTagIds, 'replace');
    toast.success('已更新分类标签');
    emit('account-updated');
  } catch (err) {
    toast.error('更新标签失败: ' + err.message);
  }
}

async function toggleWatch() {
  if (!props.account) return;
  const ghid = props.account.ghID || props.account.gh_id;
  const nextWatch = !(props.account.watched ?? props.account.is_watched);
  try {
    await api.setAccountWatch([ghid], nextWatch);
    toast.success(nextWatch ? '已加入重点关注' : '已取消关注');
    emit('account-updated');
  } catch (err) {
    toast.error('操作失败: ' + err.message);
  }
}

async function exportMD(item) {
  const id = typeof item === 'object' ? item.id : item;
  try {
    toast.info('正在导出 Markdown…');
    await api.exportArticleMD(id);
    toast.success('已导出为本地 Markdown');
    if (typeof item === 'object') item.is_exported = true;
    const ghid = props.account?.ghID || props.account?.gh_id;
    if (ghid) loadArticles(ghid);
  } catch (err) {
    toast.error('导出失败: ' + err.message);
  }
}

async function pushIMA(item) {
  const id = typeof item === 'object' ? item.id : item;
  try {
    toast.info('正在推送到 IMA…');
    await api.pushArticleIMA(id);
    toast.success('已推送到 IMA 知识库');
    if (typeof item === 'object') item.is_pushed = true;
    const ghid = props.account?.ghID || props.account?.gh_id;
    if (ghid) loadArticles(ghid);
  } catch (err) {
    toast.error('推送失败: ' + err.message);
  }
}

async function summarize(item) {
  const id = typeof item === 'object' ? item.id : item;
  try {
    toast.info('正在生成 AI 摘要…');
    await api.summarizeArticle(id);
    toast.success('已生成 AI 摘要');
    if (typeof item === 'object') item.is_summarized = true;
    const ghid = props.account?.ghID || props.account?.gh_id;
    if (ghid) loadArticles(ghid);
  } catch (err) {
    toast.error('摘要生成失败: ' + err.message);
  }
}

function formatDate(ts) {
  if (!ts) return '';
  if (typeof ts === 'number') {
    const d = new Date(ts * 1000);
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
  }
  return String(ts).split('T')[0] || String(ts).slice(0, 10);
}
</script>
