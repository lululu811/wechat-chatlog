<template>
  <div class="space-y-6">
    <!-- Sub-tabs Navigation -->
    <div class="flex items-center gap-1.5 p-1 bg-zinc-100 dark:bg-zinc-900/90 border border-zinc-200/80 dark:border-zinc-800 rounded-xl w-fit">
      <button
        @click="activeSection = 'accounts'"
        class="inline-flex items-center gap-2 px-4 py-2 rounded-lg text-sm font-medium transition-all"
        :class="activeSection === 'accounts' ? 'bg-white dark:bg-[#18181b] text-zinc-900 dark:text-zinc-100 shadow-xs font-semibold' : 'text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-300'"
      >
        <span>公众号治理</span>
        <span class="px-2 py-0.5 rounded-md text-xs font-mono" :class="activeSection === 'accounts' ? 'bg-zinc-100 dark:bg-zinc-800 text-zinc-700 dark:text-zinc-300 font-semibold' : 'text-zinc-400'">
          {{ accounts.length }}
        </span>
      </button>

      <button
        @click="activeSection = 'tags'"
        class="inline-flex items-center gap-2 px-4 py-2 rounded-lg text-sm font-medium transition-all"
        :class="activeSection === 'tags' ? 'bg-white dark:bg-[#18181b] text-zinc-900 dark:text-zinc-100 shadow-xs font-semibold' : 'text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-300'"
      >
        <span>标签库</span>
        <span class="px-2 py-0.5 rounded-md text-xs font-mono" :class="activeSection === 'tags' ? 'bg-zinc-100 dark:bg-zinc-800 text-zinc-700 dark:text-zinc-300 font-semibold' : 'text-zinc-400'">
          {{ tags.length }}
        </span>
      </button>

      <button
        @click="activeSection = 'tasks'"
        class="inline-flex items-center gap-2 px-4 py-2 rounded-lg text-sm font-medium transition-all"
        :class="activeSection === 'tasks' ? 'bg-white dark:bg-[#18181b] text-zinc-900 dark:text-zinc-100 shadow-xs font-semibold' : 'text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-300'"
      >
        <span>管道调度与归档</span>
      </button>
    </div>

    <!-- Section 1: Accounts -->
    <div v-show="activeSection === 'accounts'" class="space-y-5">
      <!-- Pipeline Funnel Strip -->
      <PipelineStrip ref="pipelineRef" @open-failures="showFailuresDrawer = true" />

      <!-- Controls & Filter Bar -->
      <div class="flex flex-wrap items-center gap-2.5">
        <!-- Search Box -->
        <div class="relative flex-1 min-w-[220px] max-w-sm">
          <Search class="w-4 h-4 absolute left-3 top-1/2 -translate-y-1/2 text-zinc-400" />
          <input
            type="text"
            v-model="searchQuery"
            placeholder="搜索公众号名称或 ID…"
            class="w-full pl-9 pr-4 py-2 rounded-xl text-sm bg-white dark:bg-[#121215] border border-zinc-200 dark:border-zinc-800 text-zinc-900 dark:text-zinc-100 placeholder-zinc-400 focus:outline-none focus:border-emerald-500 dark:focus:border-emerald-500 shadow-xs transition-colors"
          />
        </div>

        <!-- Visibility Segmented Pills -->
        <div class="inline-flex p-1 rounded-xl bg-zinc-100 dark:bg-zinc-900/90 border border-zinc-200/80 dark:border-zinc-800">
          <button
            @click="visFilter = 'all'"
            class="px-3 py-1.5 rounded-lg text-sm font-medium transition-all"
            :class="visFilter === 'all' ? 'bg-white dark:bg-[#18181b] text-zinc-900 dark:text-zinc-100 shadow-xs font-semibold' : 'text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-300'"
          >
            全部
          </button>
          <button
            @click="visFilter = 'visible'"
            class="px-3 py-1.5 rounded-lg text-sm font-medium transition-all"
            :class="visFilter === 'visible' ? 'bg-white dark:bg-[#18181b] text-zinc-900 dark:text-zinc-100 shadow-xs font-semibold' : 'text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-300'"
          >
            显示中
          </button>
          <button
            @click="visFilter = 'hidden'"
            class="px-3 py-1.5 rounded-lg text-sm font-medium transition-all"
            :class="visFilter === 'hidden' ? 'bg-white dark:bg-[#18181b] text-zinc-900 dark:text-zinc-100 shadow-xs font-semibold' : 'text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-300'"
          >
            已隐藏
          </button>
        </div>

        <!-- Filter Chips -->
        <button
          @click="onlyWatched = !onlyWatched"
          class="inline-flex items-center gap-1.5 px-3.5 py-2 rounded-xl border text-sm font-medium transition-all shadow-xs"
          :class="onlyWatched ? 'bg-amber-50 dark:bg-amber-950/40 border-amber-300 dark:border-amber-800/80 text-amber-600 dark:text-amber-400 font-semibold' : 'border-zinc-200 dark:border-zinc-800 bg-white dark:bg-[#121215] text-zinc-600 dark:text-zinc-400 hover:bg-zinc-50 dark:hover:bg-zinc-800/50'"
        >
          <Star class="w-4 h-4" :class="{ 'fill-current': onlyWatched }" />
          <span>重点关注</span>
        </button>

        <button
          @click="onlyUncategorized = !onlyUncategorized"
          class="inline-flex items-center gap-1.5 px-3.5 py-2 rounded-xl border text-sm font-medium transition-all shadow-xs"
          :class="onlyUncategorized ? 'bg-amber-500 border-amber-500 text-white font-semibold' : 'border-amber-200/80 dark:border-amber-900/60 bg-amber-50/50 dark:bg-amber-950/30 text-amber-700 dark:text-amber-400 hover:bg-amber-100/50'"
        >
          <AlertCircle class="w-4 h-4" />
          <span>待分类 ({{ unclassifiedCount }})</span>
        </button>

        <!-- Tag Filter Dropdown -->
        <select
          v-model="selectedTagFilter"
          class="px-3.5 py-2 rounded-xl text-sm bg-white dark:bg-[#121215] border border-zinc-200 dark:border-zinc-800 text-zinc-700 dark:text-zinc-300 outline-none shadow-xs cursor-pointer focus:border-emerald-500"
        >
          <option value="">全部标签</option>
          <option v-for="t in tags" :key="t.id" :value="t.id">{{ t.name }}</option>
        </select>

        <!-- Sort Select -->
        <select
          v-model="sortBy"
          class="ml-auto px-3.5 py-2 rounded-xl text-sm bg-white dark:bg-[#121215] border border-zinc-200 dark:border-zinc-800 text-zinc-700 dark:text-zinc-300 outline-none shadow-xs cursor-pointer font-medium focus:border-emerald-500"
        >
          <option value="articles-desc">发文数倒序</option>
          <option value="time-desc">最新更新倒序</option>
          <option value="name-asc">名称排序</option>
        </select>
      </div>

      <!-- Batch Selection Dock -->
      <div 
        v-if="selectedGhids.length > 0"
        class="sticky top-16 z-30 p-3 rounded-2xl bg-white/95 dark:bg-[#18181b]/95 border border-zinc-200 dark:border-zinc-800 shadow-xl backdrop-blur-md flex items-center justify-between gap-4 animate-fade-in"
      >
        <div class="flex items-center gap-2 text-sm text-zinc-700 dark:text-zinc-300 pl-2">
          <span>已选 <strong class="text-emerald-600 dark:text-emerald-400 font-bold font-mono">{{ selectedGhids.length }}</strong> 个公众号</span>
        </div>

        <div class="flex items-center gap-2">
          <button @click="batchSetVisibility(true)" class="px-3 py-1.5 rounded-lg border border-zinc-200 dark:border-zinc-700 text-xs sm:text-sm font-medium text-zinc-700 dark:text-zinc-300 hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors">
            批量隐藏
          </button>
          <button @click="batchSetVisibility(false)" class="px-3 py-1.5 rounded-lg border border-zinc-200 dark:border-zinc-700 text-xs sm:text-sm font-medium text-zinc-700 dark:text-zinc-300 hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors">
            批量显示
          </button>
          <button @click="batchSetWatch(true)" class="px-3 py-1.5 rounded-lg border border-zinc-200 dark:border-zinc-700 text-xs sm:text-sm font-medium text-zinc-700 dark:text-zinc-300 hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors">
            批量关注
          </button>
          <button @click="batchSetWatch(false)" class="px-3 py-1.5 rounded-lg border border-zinc-200 dark:border-zinc-700 text-xs sm:text-sm font-medium text-zinc-700 dark:text-zinc-300 hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors">
            取消关注
          </button>
          <button @click="showAssignModal = true" class="px-3.5 py-1.5 rounded-lg bg-emerald-600 text-white text-xs sm:text-sm font-medium hover:bg-emerald-500 transition-colors shadow-xs">
            追加标签
          </button>
          <button @click="selectedGhids = []" class="px-2.5 py-1.5 text-xs sm:text-sm text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-200 transition-colors">
            清除选择
          </button>
        </div>
      </div>

      <!-- Main Accounts Table Container -->
      <div class="bg-white dark:bg-[#121215] rounded-2xl border border-zinc-200/80 dark:border-zinc-800/80 shadow-xs overflow-hidden">
        <div class="overflow-x-auto">
          <table class="w-full text-left text-sm">
            <thead class="bg-zinc-50/80 dark:bg-zinc-900/60 text-zinc-500 dark:text-zinc-400 font-semibold uppercase tracking-wider border-b border-zinc-200/80 dark:border-zinc-800/80 text-xs">
              <tr>
                <th class="py-3 pl-4 pr-2 w-10">
                  <input
                    type="checkbox"
                    :checked="isPageAllSelected"
                    @change="toggleSelectPage"
                    class="rounded border-zinc-300 dark:border-zinc-700 text-emerald-600 focus:ring-emerald-500 cursor-pointer"
                  />
                </th>
                <th class="py-3 px-4 font-semibold text-zinc-500 dark:text-zinc-400">公众号</th>
                <th class="py-3 px-4 font-semibold text-zinc-500 dark:text-zinc-400">标签分类</th>
                <th class="py-3 px-4 font-semibold text-zinc-500 dark:text-zinc-400">文章数</th>
                <th class="py-3 px-4 font-semibold text-zinc-500 dark:text-zinc-400">最新更新</th>
                <th class="py-3 px-4 font-semibold text-zinc-500 dark:text-zinc-400 w-16 text-center">关注</th>
                <th class="py-3 pr-5 pl-4 font-semibold text-zinc-500 dark:text-zinc-400 w-16 text-center">显示</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-zinc-100 dark:divide-zinc-800/60 font-medium">
              <tr v-if="filteredAccounts.length === 0">
                <td colspan="7" class="py-12 text-center text-zinc-400">
                  未匹配到任何公众号
                </td>
              </tr>
              <tr
                v-for="a in paginatedAccounts"
                :key="a.gh_id"
                @click="inspectAccount(a)"
                class="hover:bg-zinc-50/80 dark:hover:bg-zinc-900/50 cursor-pointer transition-colors group"
                :class="{ 'opacity-50': a.is_hidden }"
              >
                <!-- Checkbox -->
                <td class="py-3.5 pl-4 pr-2" @click.stop>
                  <input
                    type="checkbox"
                    :value="a.gh_id"
                    v-model="selectedGhids"
                    class="rounded border-zinc-300 dark:border-zinc-700 text-emerald-600 focus:ring-emerald-500 cursor-pointer"
                  />
                </td>

                <!-- Account info -->
                <td class="py-3.5 px-4">
                  <div class="flex items-center gap-3">
                    <div class="w-8 h-8 rounded-lg bg-emerald-50 dark:bg-emerald-950/60 text-emerald-700 dark:text-emerald-400 flex items-center justify-center font-bold text-xs border border-emerald-200/50 dark:border-emerald-800/40 flex-shrink-0">
                      {{ (a.name || '公').slice(0, 1) }}
                    </div>
                    <div class="min-w-0">
                      <div class="flex items-center gap-1.5">
                        <span class="font-semibold text-zinc-900 dark:text-zinc-100 truncate text-[13px]">{{ a.name }}</span>
                        <span class="opacity-0 group-hover:opacity-100 text-[10px] text-emerald-600 dark:text-emerald-400 bg-emerald-50 dark:bg-emerald-950/60 px-1.5 py-0.5 rounded transition-opacity">
                          画像
                        </span>
                      </div>
                      <div class="text-[11px] font-mono text-zinc-400 truncate">{{ a.gh_id }}</div>
                    </div>
                  </div>
                </td>

                <!-- Tags -->
                <td class="py-3.5 px-4">
                  <div class="flex flex-wrap gap-1.5">
                    <span
                      v-for="t in a.tags"
                      :key="t.id"
                      class="px-2 py-0.5 rounded-full text-[10px] font-medium text-white shadow-xs"
                      :style="{ backgroundColor: t.color }"
                    >
                      {{ t.name }}
                    </span>
                    <span v-if="!a.tags || a.tags.length === 0" class="text-[11px] text-zinc-400 dark:text-zinc-600">
                      未分类
                    </span>
                  </div>
                </td>

                <!-- Article Count -->
                <td class="py-3.5 px-4 font-mono font-semibold text-zinc-700 dark:text-zinc-300">
                  {{ a.article_count || 0 }}
                </td>

                <!-- Last Update Time -->
                <td class="py-3.5 px-4 font-mono text-zinc-400 dark:text-zinc-400 text-[11px]">
                  {{ formatDate(a.last_publish_time || a.updated_at) }}
                </td>

                <!-- Star watch -->
                <td class="py-3.5 px-4 text-center" @click.stop>
                  <button
                    @click="toggleWatch(a)"
                    class="p-1.5 text-zinc-300 dark:text-zinc-600 hover:text-amber-500 dark:hover:text-amber-400 transition-colors"
                    :class="{ 'text-amber-500 dark:text-amber-400': a.is_watched }"
                  >
                    <Star class="w-4 h-4" :class="{ 'fill-current': a.is_watched }" />
                  </button>
                </td>

                <!-- Hide switch -->
                <td class="py-3.5 pr-5 pl-4 text-center" @click.stop>
                  <button
                    @click="toggleVisibility(a)"
                    class="relative inline-flex h-5 w-9 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none"
                    :class="!a.is_hidden ? 'bg-emerald-600' : 'bg-zinc-300 dark:bg-zinc-700'"
                  >
                    <span
                      class="pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow-xs ring-0 transition duration-200 ease-in-out"
                      :class="!a.is_hidden ? 'translate-x-4' : 'translate-x-0'"
                    ></span>
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- Pagination Controls Footer -->
        <div class="px-4 py-3 bg-zinc-50/60 dark:bg-zinc-900/40 border-t border-zinc-200/80 dark:border-zinc-800/80 flex flex-wrap items-center justify-between gap-4 text-xs text-zinc-500 dark:text-zinc-400">
          <div class="flex items-center gap-3">
            <span>
              显示第 <strong class="font-mono text-zinc-700 dark:text-zinc-300">{{ ((currentPage - 1) * pageSize) + 1 }}</strong> - 
              <strong class="font-mono text-zinc-700 dark:text-zinc-300">{{ Math.min(currentPage * pageSize, filteredAccounts.length) }}</strong> 条，
              共 <strong class="font-mono text-zinc-700 dark:text-zinc-300">{{ filteredAccounts.length }}</strong> 个公众号
            </span>

            <select
              v-model.number="pageSize"
              class="px-2 py-1 rounded-lg bg-white dark:bg-[#121215] border border-zinc-200 dark:border-zinc-800 text-zinc-700 dark:text-zinc-300 text-xs font-mono"
            >
              <option :value="25">25 条/页</option>
              <option :value="50">50 条/页</option>
              <option :value="100">100 条/页</option>
            </select>
          </div>

          <div class="flex items-center gap-1.5">
            <button
              @click="currentPage = Math.max(1, currentPage - 1)"
              :disabled="currentPage <= 1"
              class="px-3 py-1.5 rounded-lg border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-[#121215] font-medium disabled:opacity-40 hover:bg-zinc-50 dark:hover:bg-zinc-800/60 transition-colors shadow-xs"
            >
              上一页
            </button>
            <span class="px-2 font-mono text-xs">
              {{ currentPage }} / {{ totalPages }}
            </span>
            <button
              @click="currentPage = Math.min(totalPages, currentPage + 1)"
              :disabled="currentPage >= totalPages"
              class="px-3 py-1.5 rounded-lg border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-[#121215] font-medium disabled:opacity-40 hover:bg-zinc-50 dark:hover:bg-zinc-800/60 transition-colors shadow-xs"
            >
              下一页
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Section 2: Tags -->
    <div v-show="activeSection === 'tags'">
      <TagManager :tags="tags" :tag-counts="tagUsageCounts" @tags-updated="loadAllData" />
    </div>

    <!-- Section 3: Batch Tasks -->
    <div v-show="activeSection === 'tasks'">
      <BatchTasks />
    </div>

    <!-- Account Inspector Drawer -->
    <AccountDrawer 
      :account="inspectingAccount"
      :all-tags="tags"
      @close="inspectingAccount = null"
      @account-updated="loadAccounts"
    />

    <!-- Pipeline Failures Drawer -->
    <PipelineFailuresDrawer
      :is-open="showFailuresDrawer"
      :status-data="pipelineRef?.status"
      @close="showFailuresDrawer = false"
      @pipeline-updated="pipelineRef?.loadStatus"
    />

    <!-- Assign Tags Modal -->
    <div v-if="showAssignModal" class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-xs p-4">
      <div class="bg-white dark:bg-[#121215] rounded-2xl border border-zinc-200 dark:border-zinc-800 p-6 max-w-md w-full shadow-2xl space-y-4 animate-scale-in">
        <h3 class="text-base font-bold text-zinc-900 dark:text-zinc-100">
          为 {{ selectedGhids.length }} 个公众号追加标签
        </h3>
        
        <div class="space-y-1.5 max-h-60 overflow-y-auto divide-y divide-zinc-100 dark:divide-zinc-800/60 pr-1">
          <label 
            v-for="t in tags" 
            :key="t.id"
            class="flex items-center gap-3 py-2 cursor-pointer hover:bg-zinc-50 dark:hover:bg-zinc-900/40 px-2 rounded-lg transition-colors"
          >
            <input type="checkbox" :value="t.id" v-model="assignModalTagIds" class="rounded border-zinc-300 dark:border-zinc-700 text-emerald-600 focus:ring-emerald-500" />
            <span class="w-3 h-3 rounded-full flex-shrink-0" :style="{ backgroundColor: t.color }"></span>
            <span class="text-xs font-semibold text-zinc-800 dark:text-zinc-200">{{ t.name }}</span>
          </label>
        </div>

        <div class="flex items-center justify-end gap-2 pt-3 border-t border-zinc-100 dark:border-zinc-800">
          <button @click="showAssignModal = false" class="text-xs px-3 py-2 rounded-lg text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-200 transition-colors">取消</button>
          <button @click="confirmAssignTags" class="text-xs px-4 py-2 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white font-semibold transition-colors shadow-xs">确认追加</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch, onMounted } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { Search, Star, AlertCircle } from 'lucide-vue-next';
import { api } from '@/api/client';
import { useToast } from '@/composables/useToast';
import PipelineStrip from '@/components/PipelineStrip.vue';
import AccountDrawer from '@/components/AccountDrawer.vue';
import PipelineFailuresDrawer from '@/components/PipelineFailuresDrawer.vue';
import TagManager from '@/components/TagManager.vue';
import BatchTasks from '@/components/BatchTasks.vue';

const route = useRoute();
const router = useRouter();
const toast = useToast();

const initialSection = route.query.section && ['accounts', 'tags', 'tasks'].includes(route.query.section) 
  ? route.query.section 
  : 'accounts';
const activeSection = ref(initialSection);

watch(() => route.query.section, (val) => {
  if (val && ['accounts', 'tags', 'tasks'].includes(val) && val !== activeSection.value) {
    activeSection.value = val;
  }
});

watch(activeSection, (val) => {
  if (route.query.section !== val) {
    router.replace({ query: { ...route.query, section: val } });
  }
});

const pipelineRef = ref(null);

const accounts = ref([]);
const tags = ref([]);
const searchQuery = ref('');
const visFilter = ref('all');
const onlyWatched = ref(false);
const onlyUncategorized = ref(false);
const selectedTagFilter = ref('');
const sortBy = ref('articles-desc');

const currentPage = ref(1);
const pageSize = ref(50);

const selectedGhids = ref([]);
const inspectingAccount = ref(null);
const showFailuresDrawer = ref(false);
const showAssignModal = ref(false);
const assignModalTagIds = ref([]);

const unclassifiedCount = computed(() => {
  return accounts.value.filter(a => !a.tags || a.tags.length === 0).length;
});

const tagUsageCounts = computed(() => {
  const counts = {};
  accounts.value.forEach(a => {
    (a.tags || []).forEach(t => {
      counts[t.id] = (counts[t.id] || 0) + 1;
    });
  });
  return counts;
});

const filteredAccounts = computed(() => {
  let list = accounts.value.slice();

  // Search filter
  const q = searchQuery.value.trim().toLowerCase();
  if (q) {
    list = list.filter(a => 
      (a.name && a.name.toLowerCase().includes(q)) || 
      (a.gh_id && a.gh_id.toLowerCase().includes(q))
    );
  }

  // Visibility filter
  if (visFilter.value === 'visible') list = list.filter(a => !a.is_hidden);
  if (visFilter.value === 'hidden') list = list.filter(a => a.is_hidden);

  // Watched filter
  if (onlyWatched.value) list = list.filter(a => a.is_watched);

  // Uncategorized filter
  if (onlyUncategorized.value) list = list.filter(a => !a.tags || a.tags.length === 0);

  // Tag filter
  if (selectedTagFilter.value) {
    const tid = Number(selectedTagFilter.value);
    list = list.filter(a => (a.tags || []).some(t => t.id === tid));
  }

  // Sort
  if (sortBy.value === 'articles-desc') {
    list.sort((a, b) => (b.article_count || 0) - (a.article_count || 0));
  } else if (sortBy.value === 'time-desc') {
    list.sort((a, b) => (b.last_publish_time || b.updated_at || 0) - (a.last_publish_time || a.updated_at || 0));
  } else if (sortBy.value === 'name-asc') {
    list.sort((a, b) => (a.name || '').localeCompare(b.name || ''));
  }

  return list;
});

const totalPages = computed(() => {
  return Math.max(1, Math.ceil(filteredAccounts.value.length / pageSize.value));
});

const paginatedAccounts = computed(() => {
  const start = (currentPage.value - 1) * pageSize.value;
  return filteredAccounts.value.slice(start, start + pageSize.value);
});

// Reset pagination on filter changes
watch([searchQuery, visFilter, onlyWatched, onlyUncategorized, selectedTagFilter, sortBy, pageSize], () => {
  currentPage.value = 1;
});

const isPageAllSelected = computed(() => {
  const page = paginatedAccounts.value;
  return page.length > 0 && page.every(a => selectedGhids.value.includes(a.gh_id));
});

function toggleSelectPage() {
  const pageIds = paginatedAccounts.value.map(a => a.gh_id);
  if (isPageAllSelected.value) {
    selectedGhids.value = selectedGhids.value.filter(id => !pageIds.includes(id));
  } else {
    const set = new Set([...selectedGhids.value, ...pageIds]);
    selectedGhids.value = Array.from(set);
  }
}

async function loadAccounts() {
  try {
    const res = await api.getAdminAccounts();
    const list = res.items || res.accounts || [];
    accounts.value = list.map(a => ({
      ...a,
      gh_id: a.ghID || a.gh_id,
      name: a.ghName || a.name || '未知公众号',
      article_count: a.articleCount ?? a.article_count ?? 0,
      updated_at: a.updatedAt || a.updated_at,
      last_publish_time: a.lastPublishTime || a.last_publish_time || a.updatedAt || a.updated_at,
      is_hidden: a.hidden ?? a.is_hidden ?? false,
      is_watched: a.watched ?? a.is_watched ?? false,
      tags: a.tags || [],
    }));
  } catch (err) {
    toast.error('加载公众号失败: ' + err.message);
  }
}

async function loadTags() {
  try {
    const res = await api.getTags();
    tags.value = res.items || res.tags || [];
  } catch (err) {
    toast.error('加载标签失败: ' + err.message);
  }
}

async function loadAllData() {
  await Promise.all([loadAccounts(), loadTags()]);
  if (pipelineRef.value) pipelineRef.value.loadStatus();
}

function inspectAccount(a) {
  inspectingAccount.value = a;
}

async function toggleWatch(a) {
  const next = !a.is_watched;
  try {
    await api.setAccountWatch([a.gh_id], next);
    a.is_watched = next;
    toast.success(next ? '已加入重点关注' : '已取消关注');
  } catch (err) {
    toast.error('操作失败: ' + err.message);
  }
}

async function toggleVisibility(a) {
  const nextHidden = !a.is_hidden;
  try {
    await api.setAccountVisibility([a.gh_id], nextHidden);
    a.is_hidden = nextHidden;
    toast.success(nextHidden ? '已隐藏公众号' : '已恢复展示');
  } catch (err) {
    toast.error('操作失败: ' + err.message);
  }
}

// Bulk operations with confirm()
async function batchSetVisibility(hidden) {
  if (!confirm(`确认${hidden ? '隐藏' : '显示'}选中的 ${selectedGhids.value.length} 个公众号吗？`)) return;
  try {
    await api.setAccountVisibility(selectedGhids.value, hidden);
    toast.success(`已批量${hidden ? '隐藏' : '显示'}`);
    selectedGhids.value = [];
    loadAccounts();
  } catch (err) {
    toast.error('操作失败: ' + err.message);
  }
}

async function batchSetWatch(watched) {
  if (!confirm(`确认${watched ? '关注' : '取消关注'}选中的 ${selectedGhids.value.length} 个公众号吗？`)) return;
  try {
    await api.setAccountWatch(selectedGhids.value, watched);
    toast.success(`已批量${watched ? '关注' : '取消关注'}`);
    selectedGhids.value = [];
    loadAccounts();
  } catch (err) {
    toast.error('操作失败: ' + err.message);
  }
}

async function confirmAssignTags() {
  if (assignModalTagIds.value.length === 0) {
    toast.error('请选择要追加的标签');
    return;
  }
  if (!confirm(`确认给选中的 ${selectedGhids.value.length} 个公众号追加标签吗？`)) return;
  try {
    await api.setAccountTags(selectedGhids.value, assignModalTagIds.value, 'add');
    toast.success('标签追加成功');
    showAssignModal.value = false;
    assignModalTagIds.value = [];
    selectedGhids.value = [];
    loadAccounts();
  } catch (err) {
    toast.error('追加标签失败: ' + err.message);
  }
}

function formatDate(ts) {
  if (!ts) return '—';
  if (typeof ts === 'number') {
    const d = new Date(ts * 1000);
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
  }
  return String(ts).split('T')[0] || String(ts).slice(0, 10);
}

onMounted(() => {
  loadAllData();
  window.addEventListener('bizhub-sync-finished', loadAllData);
});
</script>
