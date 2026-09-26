<template>
  <div class="flex flex-col lg:flex-row gap-6 items-start">
    <!-- Sidebar (Visible in Stream Mode) -->
    <aside 
      v-show="mode === 'stream'"
      class="w-full lg:w-72 flex-shrink-0 bg-white dark:bg-[#121215] rounded-2xl border border-zinc-200/80 dark:border-zinc-800/80 p-4 sm:p-5 shadow-xs space-y-4 lg:sticky lg:top-20"
    >
      <div class="flex items-center justify-between">
        <h3 class="text-xs font-bold text-zinc-400 uppercase tracking-wider">公众号筛选</h3>
        <router-link to="/biz/admin" class="text-xs font-medium text-emerald-600 dark:text-emerald-400 hover:underline">
          管理公众号
        </router-link>
      </div>

      <!-- Tag Filter Bar -->
      <div class="flex flex-wrap gap-1.5 pb-3 border-b border-zinc-100 dark:border-zinc-800/80">
        <button
          @click="selectTag(null)"
          class="px-3 py-1 rounded-full text-xs font-medium transition-all"
          :class="selectedTagId === null ? 'bg-emerald-600 text-white font-semibold shadow-xs' : 'bg-zinc-100 dark:bg-zinc-800 text-zinc-600 dark:text-zinc-400 hover:bg-zinc-200 dark:hover:bg-zinc-700'"
        >
          全部
        </button>
        <button
          v-for="t in tags"
          :key="t.id"
          @click="selectTag(t.id)"
          class="px-3 py-1 rounded-full text-xs font-medium transition-all"
          :style="selectedTagId === t.id ? { backgroundColor: t.color, color: '#fff' } : {}"
          :class="selectedTagId !== t.id ? 'bg-zinc-100 dark:bg-zinc-800 text-zinc-600 dark:text-zinc-400 hover:bg-zinc-200 dark:hover:bg-zinc-700' : 'font-semibold shadow-xs'"
        >
          {{ t.name }}
        </button>
      </div>

      <!-- Account List Section Header -->
      <div class="flex items-center justify-between text-xs text-zinc-400">
        <span class="font-medium text-zinc-500 dark:text-zinc-400">
          {{ selectedTag ? `「${selectedTag.name}」公众号 (${filteredAccounts.length})` : `公众号列表 (${filteredAccounts.length})` }}
        </span>
        <button 
          v-if="selectedGhid" 
          @click="selectedGhid = null" 
          class="text-emerald-600 dark:text-emerald-400 hover:underline text-xs"
        >
          取消单选
        </button>
      </div>

      <!-- Account List -->
      <div class="max-h-[calc(100vh-280px)] overflow-y-auto space-y-1 pr-1">
        <button
          v-for="a in filteredAccounts"
          :key="a.gh_id"
          @click="selectAccount(a.gh_id)"
          class="w-full text-left px-3 py-2.5 rounded-xl text-sm flex items-center justify-between gap-2 transition-colors group"
          :class="selectedGhid === a.gh_id ? 'bg-emerald-50 dark:bg-emerald-950/60 text-emerald-700 dark:text-emerald-300 font-semibold' : 'text-zinc-700 dark:text-zinc-300 hover:bg-zinc-50 dark:hover:bg-zinc-800/60'"
        >
          <span class="truncate">{{ a.name }}</span>
          <span class="text-xs font-mono px-2 py-0.5 rounded-full bg-zinc-100 dark:bg-zinc-800 text-zinc-400 group-hover:bg-zinc-200 dark:group-hover:bg-zinc-700">
            {{ a.article_count || 0 }}
          </span>
        </button>

        <div v-if="filteredAccounts.length === 0" class="py-8 text-center text-xs text-zinc-400 space-y-2">
          <p>该分类下暂无公众号</p>
          <router-link to="/biz/admin" class="text-emerald-600 dark:text-emerald-400 hover:underline">
            去管理页为公众号标记标签 →
          </router-link>
        </div>
      </div>
    </aside>

    <!-- Main Content Stream -->
    <main class="flex-1 min-w-0 space-y-5">
      <!-- Controls Bar -->
      <div class="bg-white dark:bg-[#121215] rounded-2xl border border-zinc-200/80 dark:border-zinc-800/80 p-3.5 sm:p-4 shadow-xs flex flex-wrap items-center justify-between gap-3">
        <!-- Mode Switcher -->
        <div class="inline-flex p-1 rounded-xl bg-zinc-100 dark:bg-zinc-900/90 border border-zinc-200/80 dark:border-zinc-800">
          <button
            @click="setMode('digest')"
            class="px-4 py-1.5 rounded-lg text-sm font-medium transition-all"
            :class="mode === 'digest' ? 'bg-white dark:bg-[#18181b] text-zinc-900 dark:text-zinc-100 shadow-xs font-semibold' : 'text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-300'"
          >
            精选看点
          </button>
          <button
            @click="setMode('stream')"
            class="px-4 py-1.5 rounded-lg text-sm font-medium transition-all"
            :class="mode === 'stream' ? 'bg-white dark:bg-[#18181b] text-zinc-900 dark:text-zinc-100 shadow-xs font-semibold' : 'text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-300'"
          >
            全部文章
          </button>
        </div>

        <!-- Search Input -->
        <div class="relative flex-1 min-w-[220px] max-w-sm">
          <Search class="w-4 h-4 absolute left-3 top-1/2 -translate-y-1/2 text-zinc-400" />
          <input
            type="text"
            v-model="searchQuery"
            placeholder="搜索文章标题或正文…"
            class="w-full pl-9 pr-8 py-2 rounded-xl text-sm bg-zinc-50 dark:bg-zinc-900/80 border border-zinc-200 dark:border-zinc-800 text-zinc-900 dark:text-zinc-100 placeholder-zinc-400 focus:border-emerald-500 outline-none shadow-xs"
          />
          <button 
            v-if="searchQuery" 
            @click="searchQuery = ''"
            class="absolute right-2.5 top-1/2 -translate-y-1/2 text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-200"
          >
            <X class="w-4 h-4" />
          </button>
        </div>

        <!-- Bookmarked Filter -->
        <button
          @click="onlyBookmarked = !onlyBookmarked"
          class="inline-flex items-center gap-2 px-3.5 py-2 rounded-xl border text-sm font-medium transition-all shadow-xs"
          :class="onlyBookmarked ? 'bg-amber-50 dark:bg-amber-950/60 border-amber-300 dark:border-amber-800 text-amber-600 dark:text-amber-400 font-semibold' : 'border-zinc-200 dark:border-zinc-800 bg-white dark:bg-[#121215] text-zinc-600 dark:text-zinc-400 hover:bg-zinc-50 dark:hover:bg-zinc-800/60'"
        >
          <Bookmark class="w-4 h-4" :class="{ 'fill-current': onlyBookmarked }" />
          <span>我的收藏</span>
        </button>
      </div>

      <!-- Active Search Query Indicator -->
      <div v-if="searchQuery" class="flex items-center justify-between px-4 py-2 bg-emerald-50/70 dark:bg-emerald-950/40 rounded-xl border border-emerald-200/60 dark:border-emerald-800/50 text-sm">
        <div class="flex items-center gap-2 text-emerald-800 dark:text-emerald-200 font-medium">
          <Search class="w-4 h-4 text-emerald-600" />
          <span>正在搜索关键词: <strong class="underline decoration-emerald-500 decoration-2">"{{ searchQuery }}"</strong></span>
        </div>
        <button 
          @click="searchQuery = ''" 
          class="text-xs text-emerald-700 dark:text-emerald-400 hover:underline font-semibold"
        >
          清除筛选
        </button>
      </div>

      <!-- Active Tag / Account Filter Indicator -->
      <div v-if="selectedFilterTitle" class="flex items-center justify-between px-4 py-2 bg-emerald-50/70 dark:bg-emerald-950/40 rounded-xl border border-emerald-200/60 dark:border-emerald-800/50 text-sm">
        <div class="flex items-center gap-2 text-emerald-800 dark:text-emerald-200 font-medium">
          <Tag class="w-4 h-4 text-emerald-600" />
          <span>当前筛选: <strong class="underline decoration-emerald-500 decoration-2">{{ selectedFilterTitle }}</strong></span>
        </div>
        <button 
          @click="clearFilters" 
          class="text-xs text-emerald-700 dark:text-emerald-400 hover:underline font-semibold"
        >
          清除分类筛选
        </button>
      </div>

      <!-- ============================================== -->
      <!-- Mode 1: 精选看点 (Digest Mode)                  -->
      <!-- ============================================== -->
      <div v-if="mode === 'digest'" class="space-y-6">
        <div v-if="loadingDigest" class="py-28 text-center text-sm text-zinc-400">
          <div class="w-8 h-8 border-2 border-emerald-500/20 border-t-emerald-500 rounded-full animate-spin mx-auto mb-3"></div>
          AI 正在聚合今日精选看点与宏观叙事…
        </div>

        <div v-else-if="!digestData || (!digestData.picks?.length && !digestData.themes?.length)" class="py-20 text-center text-sm text-zinc-400 bg-white dark:bg-[#121215] rounded-2xl border border-zinc-200/80 dark:border-zinc-800/80 p-8 space-y-4">
          <Sparkles class="w-10 h-10 text-zinc-300 dark:text-zinc-700 mx-auto" />
          <h3 class="text-base font-bold text-zinc-700 dark:text-zinc-300">暂无看点数据</h3>
          <p class="text-zinc-400 max-w-sm mx-auto">点击下方按钮即刻调用大模型生成今日智能看点与必读精选</p>
          <button
            @click="refreshDigest(true)"
            :disabled="loadingDigest"
            class="text-sm px-4 py-2.5 rounded-xl bg-emerald-600 text-white font-medium hover:bg-emerald-500 transition-colors shadow-xs"
          >
            立即生成今日看点
          </button>
        </div>

        <div v-else class="space-y-6">
          <!-- Top Narrative & Keyword Cloud Card -->
          <div class="bg-gradient-to-br from-white via-zinc-50/50 to-white dark:from-[#121215] dark:via-zinc-900/60 dark:to-[#121215] rounded-2xl border border-zinc-200/80 dark:border-zinc-800/80 p-6 sm:p-7 shadow-xs space-y-5">
            <div class="flex items-start justify-between gap-4">
              <div class="space-y-2">
                <div class="flex items-center gap-2.5">
                  <span class="px-3 py-1 rounded-lg text-xs font-mono font-semibold bg-emerald-50 dark:bg-emerald-950/60 text-emerald-700 dark:text-emerald-400 border border-emerald-200/60 dark:border-emerald-800/50">
                    {{ digestData.digestDate || '今日重点' }}
                  </span>
                  <span class="text-xs text-zinc-400 font-mono">
                    ✦ AI 宏观舆情脉搏
                  </span>
                </div>
                <h2 class="text-xl sm:text-2xl font-bold text-zinc-900 dark:text-zinc-100 leading-snug font-serif">
                  {{ digestData.headline || '今日公众号精选提炼' }}
                </h2>
              </div>

              <button
                @click="refreshDigest(true)"
                :disabled="loadingDigest"
                class="inline-flex items-center gap-2 px-3.5 py-2 rounded-xl border border-zinc-200 dark:border-zinc-800 hover:bg-zinc-50 dark:hover:bg-zinc-800 text-sm font-medium text-zinc-600 dark:text-zinc-300 transition-colors shadow-xs flex-shrink-0"
                title="重新调用 LLM 提炼最新看点"
              >
                <RefreshCw class="w-4 h-4 text-emerald-600 dark:text-emerald-400" :class="{ 'animate-spin': loadingDigest }" />
                <span>刷新看点</span>
              </button>
            </div>

            <!-- Keyword Cloud (Clickable Jumps!) -->
            <div v-if="digestData.keywords?.length" class="flex flex-wrap items-center gap-2 pt-3 border-t border-zinc-100 dark:border-zinc-800/60">
              <span class="text-xs text-zinc-400 font-medium mr-1 flex items-center gap-1.5">
                <Tag class="w-3.5 h-3.5 text-zinc-400" /> 点击热词筛选:
              </span>
              <button
                v-for="(kw, kidx) in digestData.keywords"
                :key="kidx"
                @click="filterByKeyword(kw)"
                class="px-3 py-1 rounded-full text-xs font-medium bg-zinc-100 hover:bg-emerald-50 dark:bg-zinc-800/80 dark:hover:bg-emerald-950/60 text-zinc-700 hover:text-emerald-700 dark:text-zinc-300 dark:hover:text-emerald-300 border border-zinc-200/60 hover:border-emerald-300 dark:border-zinc-700/60 dark:hover:border-emerald-800 transition-all cursor-pointer shadow-2xs"
                :title="`点击查看关于 '${kw}' 的全部文章`"
              >
                # {{ kw }}
              </button>
            </div>
          </div>

          <!-- Section: 今日必读精选 (Daily Top Picks) -->
          <div v-if="digestData.picks?.length" class="space-y-4">
            <div class="flex items-center justify-between">
              <h3 class="text-base font-bold text-zinc-900 dark:text-zinc-100 uppercase tracking-wider flex items-center gap-2">
                <span class="w-1.5 h-4 bg-emerald-500 rounded-xs"></span>
                <span>今日必读精选 ({{ digestData.picks.length }} 篇深度研选)</span>
              </h3>
            </div>

            <div class="grid gap-4 sm:grid-cols-2">
              <div
                v-for="(pick, pidx) in digestData.picks"
                :key="pidx"
                class="p-5 rounded-2xl bg-white dark:bg-[#121215] border border-zinc-200/80 dark:border-zinc-800/80 hover:border-emerald-500/40 dark:hover:border-emerald-500/40 shadow-xs hover:shadow-md flex flex-col justify-between transition-all group"
              >
                <div class="space-y-2.5">
                  <div class="flex items-center justify-between gap-2">
                    <span class="font-medium text-emerald-700 dark:text-emerald-400 bg-emerald-50 dark:bg-emerald-950/60 px-2.5 py-1 rounded-lg text-xs border border-emerald-200/50 dark:border-emerald-800/40">
                      {{ pick.ghName }}
                    </span>
                    <span v-if="pick.score" class="text-xs font-mono font-bold text-amber-700 dark:text-amber-400 bg-amber-50 dark:bg-amber-950/60 border border-amber-200/60 dark:border-amber-900/60 px-2.5 py-1 rounded-lg">
                      ★ {{ pick.score }}/10 推荐
                    </span>
                  </div>

                  <!-- Clickable Article Title: Direct Page Jump -->
                  <h4 
                    @click="navigateToArticle(pick.articleId, 'digest')"
                    class="text-base sm:text-lg font-bold text-zinc-900 dark:text-zinc-100 group-hover:text-emerald-600 dark:group-hover:text-emerald-400 transition-colors line-clamp-2 cursor-pointer leading-snug"
                    title="点击在新页面打开阅读完整文章"
                  >
                    {{ pick.title }}
                  </h4>

                  <p class="text-sm text-zinc-600 dark:text-zinc-400 line-clamp-3 leading-relaxed border-l-2 border-zinc-100 dark:border-zinc-800 pl-3">
                    {{ pick.reason || pick.summary }}
                  </p>
                </div>

                <!-- Single Article Action Buttons on Pick Card -->
                <div class="flex items-center justify-between pt-3.5 mt-3.5 border-t border-zinc-100 dark:border-zinc-800/60 text-xs">
                  <div class="flex items-center gap-1.5">
                    <button
                      @click="handleExportMD(pick.articleId)"
                      class="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-lg text-xs font-medium bg-zinc-50 dark:bg-zinc-900 text-zinc-700 dark:text-zinc-300 border border-zinc-200/60 dark:border-zinc-800 hover:border-emerald-500 hover:text-emerald-600 dark:hover:text-emerald-400 transition-colors shadow-2xs"
                      title="导出此篇为本地 Markdown"
                    >
                      <FileDown class="w-3.5 h-3.5" />
                      <span>导出 MD</span>
                    </button>

                    <button
                      @click="handlePushIMA(pick.articleId)"
                      class="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-lg text-xs font-medium bg-zinc-50 dark:bg-zinc-900 text-zinc-700 dark:text-zinc-300 border border-zinc-200/60 dark:border-zinc-800 hover:border-indigo-500 hover:text-indigo-600 dark:hover:text-indigo-400 transition-colors shadow-2xs"
                      title="推送到 IMA 知识库"
                    >
                      <Send class="w-3.5 h-3.5" />
                      <span>推送 IMA</span>
                    </button>
                  </div>

                  <!-- Jump Action & External Link -->
                  <div class="flex items-center gap-2">
                    <button
                      @click="navigateToArticle(pick.articleId, 'digest')"
                      class="text-xs sm:text-sm font-semibold text-emerald-600 dark:text-emerald-400 hover:text-emerald-700 dark:hover:text-emerald-300 inline-flex items-center gap-1 transition-colors"
                    >
                      <span>阅读全文</span>
                      <ArrowRight class="w-3.5 h-3.5" />
                    </button>
                    <a
                      :href="pick.url"
                      target="_blank"
                      rel="noopener noreferrer"
                      class="text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-200 p-1 rounded-md"
                      title="在微信中打开原文"
                    >
                      <ExternalLink class="w-4 h-4" />
                    </a>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- Section: 宏观洞察与热辣快评 (Hot Takes) -->
          <div v-if="digestData.hotTakes?.length" class="space-y-3.5">
            <h3 class="text-base font-bold text-zinc-900 dark:text-zinc-100 uppercase tracking-wider flex items-center gap-2">
              <Flame class="w-4 h-4 text-rose-500" />
              <span>核心洞见与热辣快评</span>
            </h3>

            <div class="grid gap-3">
              <div
                v-for="(take, tidx) in digestData.hotTakes"
                :key="tidx"
                class="p-4 sm:p-5 rounded-2xl bg-zinc-50/80 dark:bg-zinc-900/50 border border-zinc-200/60 dark:border-zinc-800/60 text-sm sm:text-base text-zinc-800 dark:text-zinc-200 leading-relaxed flex items-start gap-3.5 shadow-2xs"
              >
                <div class="w-6 h-6 rounded-lg bg-rose-100 dark:bg-rose-950/80 text-rose-700 dark:text-rose-400 flex items-center justify-center font-bold text-xs flex-shrink-0 mt-0.5 font-mono">
                  #{{ tidx + 1 }}
                </div>
                <div class="flex-1">{{ take }}</div>
              </div>
            </div>
          </div>

          <!-- Section: 本周聚合主题 (Clustered Themes - Clickable Jumps!) -->
          <div v-if="digestData.themes?.length" class="space-y-3.5">
            <h3 class="text-base font-bold text-zinc-900 dark:text-zinc-100 uppercase tracking-wider flex items-center gap-2">
              <Layers class="w-4 h-4 text-blue-500" />
              <span>本周聚焦主题 ({{ digestData.themes.length }} 个聚类，点击探索)</span>
            </h3>

            <div class="grid gap-4 sm:grid-cols-2">
              <div
                v-for="(theme, thidx) in digestData.themes"
                :key="thidx"
                @click="filterByKeyword(theme.title)"
                class="p-5 rounded-2xl bg-white dark:bg-[#121215] border border-zinc-200/80 dark:border-zinc-800/80 hover:border-blue-400/50 dark:hover:border-blue-500/40 shadow-xs hover:shadow-md flex flex-col justify-between transition-all cursor-pointer group"
              >
                <div class="space-y-2">
                  <div class="flex items-center justify-between gap-2">
                    <h4 class="text-sm sm:text-base font-bold text-zinc-900 dark:text-zinc-100 group-hover:text-blue-600 dark:group-hover:text-blue-400 transition-colors">
                      {{ theme.title }}
                    </h4>
                    <span class="text-xs font-mono text-blue-600 dark:text-blue-400 bg-blue-50 dark:bg-blue-950/60 px-2.5 py-0.5 rounded-full font-semibold flex-shrink-0">
                      {{ theme.count }} 篇关联
                    </span>
                  </div>
                  <p class="text-sm text-zinc-600 dark:text-zinc-400 leading-relaxed line-clamp-3">
                    {{ theme.summary }}
                  </p>
                </div>

                <div class="pt-3 mt-3 border-t border-zinc-100 dark:border-zinc-800/60 flex items-center justify-between text-xs text-blue-600 dark:text-blue-400 font-medium">
                  <span>查看该主题所有文章</span>
                  <ArrowRight class="w-3.5 h-3.5 group-hover:translate-x-1 transition-transform" />
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- ============================================== -->
      <!-- Mode 2: 全部文章流 (Stream Mode)               -->
      <!-- ============================================== -->
      <div v-else class="space-y-4">
        <div v-if="loadingArticles" class="py-24 text-center text-sm text-zinc-400">
          <div class="w-8 h-8 border-2 border-emerald-500/20 border-t-emerald-500 rounded-full animate-spin mx-auto mb-3"></div>
          正在加载文章流…
        </div>

        <div v-else-if="articles.length === 0" class="py-24 text-center text-sm text-zinc-400 bg-white dark:bg-[#121215] rounded-2xl border border-zinc-200/80 dark:border-zinc-800/80 space-y-3">
          <p class="font-medium text-zinc-600 dark:text-zinc-400">暂无匹配的文章记录</p>
          <button
            v-if="searchQuery"
            @click="searchQuery = ''"
            class="px-3.5 py-1.5 rounded-xl bg-zinc-100 dark:bg-zinc-800 text-xs font-medium text-zinc-700 dark:text-zinc-300 hover:bg-zinc-200 transition-colors"
          >
            清除搜索关键词
          </button>
        </div>

        <div v-else class="space-y-4">
          <article
            v-for="a in articles"
            :key="a.id"
            class="bg-white dark:bg-[#121215] rounded-2xl border border-zinc-200/80 dark:border-zinc-800/80 p-5 sm:p-6 shadow-xs hover:border-zinc-300 dark:hover:border-zinc-700 hover:shadow-md transition-all group"
          >
            <div class="flex items-start justify-between gap-4">
              <div class="space-y-2.5 flex-1 min-w-0">
                <div class="flex items-center gap-2.5">
                  <span class="text-xs font-medium px-2.5 py-0.5 rounded-md bg-zinc-100 dark:bg-zinc-800 text-zinc-700 dark:text-zinc-300">
                    {{ a.gh_name || a.gh_id }}
                  </span>
                  <span class="text-xs text-zinc-400 font-mono">{{ formatDate(a.publish_time) }}</span>
                </div>

                <!-- Clickable Article Title: Direct Page Jump -->
                <h2 
                  @click="navigateToArticle(a.id, 'stream')"
                  class="text-base sm:text-lg font-bold text-zinc-900 dark:text-zinc-100 group-hover:text-emerald-600 dark:group-hover:text-emerald-400 transition-colors line-clamp-2 leading-snug cursor-pointer"
                  title="点击查看正文与详情"
                >
                  {{ a.title || '无标题文章' }}
                </h2>

                <p v-if="a.description || a.digest" class="text-sm text-zinc-600 dark:text-zinc-400 line-clamp-2 leading-relaxed">
                  {{ a.description || a.digest }}
                </p>

                <!-- Article Meta & Single Actions Toolbar -->
                <div class="flex flex-wrap items-center justify-between gap-3 text-xs text-zinc-400 pt-3 border-t border-zinc-100 dark:border-zinc-800/60 mt-3">
                  <!-- Single Article Actions: MD / IMA / AI -->
                  <div class="flex items-center gap-2" @click.stop>
                    <!-- Single MD Export Button -->
                    <button
                      @click="handleExportMD(a)"
                      class="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-xs font-medium transition-all"
                      :class="a.is_exported ? 'bg-emerald-50 dark:bg-emerald-950/60 text-emerald-700 dark:text-emerald-400 border border-emerald-200 dark:border-emerald-800/60' : 'bg-zinc-100 dark:bg-zinc-800 text-zinc-700 dark:text-zinc-300 hover:bg-emerald-50 hover:text-emerald-600 dark:hover:bg-emerald-950/40 border border-zinc-200/50 dark:border-zinc-700/50'"
                      :title="a.is_exported ? '已导出 Markdown，点击可重新导出' : '单篇导出为本地 Markdown（含图片）'"
                    >
                      <FileDown class="w-3.5 h-3.5" />
                      <span>{{ a.is_exported ? '已导出' : '导出 MD' }}</span>
                    </button>

                    <!-- Single IMA Push Button -->
                    <button
                      @click="handlePushIMA(a)"
                      class="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-xs font-medium transition-all"
                      :class="a.is_pushed ? 'bg-indigo-50 dark:bg-indigo-950/60 text-indigo-700 dark:text-indigo-400 border border-indigo-200 dark:border-indigo-800/60' : 'bg-zinc-100 dark:bg-zinc-800 text-zinc-700 dark:text-zinc-300 hover:bg-indigo-50 hover:text-indigo-600 dark:hover:bg-indigo-950/40 border border-zinc-200/50 dark:border-zinc-700/50'"
                      :title="a.is_pushed ? '已推送到 IMA 知识库' : (!a.is_exported ? '需先导出 MD 才能推送到 IMA' : '单篇推送到 IMA 知识库')"
                    >
                      <Send class="w-3.5 h-3.5" />
                      <span>{{ a.is_pushed ? '已推送' : '推 IMA' }}</span>
                    </button>

                    <!-- Single AI Summary Button -->
                    <button
                      @click="handleSummarize(a)"
                      class="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-xs font-medium transition-all"
                      :class="a.is_summarized ? 'bg-purple-50 dark:bg-purple-950/60 text-purple-700 dark:text-purple-400 border border-purple-200 dark:border-purple-800/60' : 'bg-zinc-100 dark:bg-zinc-800 text-zinc-700 dark:text-zinc-300 hover:bg-purple-50 hover:text-purple-600 dark:hover:bg-purple-950/40 border border-zinc-200/50 dark:border-zinc-700/50'"
                      :title="a.is_summarized ? '已生成 AI 摘要' : '调用大模型为本篇生成结构化摘要'"
                    >
                      <Sparkles class="w-3.5 h-3.5" />
                      <span>{{ a.is_summarized ? '有摘要' : 'AI 摘要' }}</span>
                    </button>
                  </div>

                  <!-- Read Full Link -->
                  <div class="flex items-center gap-3">
                    <button
                      @click="navigateToArticle(a.id, 'stream')"
                      class="text-xs sm:text-sm font-semibold text-emerald-600 dark:text-emerald-400 hover:underline inline-flex items-center gap-1"
                    >
                      <span>阅读全文</span>
                      <ArrowRight class="w-3.5 h-3.5" />
                    </button>
                    <a
                      :href="a.url"
                      target="_blank"
                      rel="noopener noreferrer"
                      class="text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-200 p-1"
                      title="在微信中打开原文"
                    >
                      <ExternalLink class="w-3.5 h-3.5" />
                    </a>
                  </div>
                </div>
              </div>

              <!-- Bookmark button -->
              <button
                @click.stop="toggleBookmark(a)"
                class="p-2 text-zinc-300 dark:text-zinc-600 hover:text-amber-500 dark:hover:text-amber-400 transition-colors"
                :class="{ 'text-amber-500 dark:text-amber-400': a.is_bookmarked }"
                title="收藏"
              >
                <Bookmark class="w-4 h-4" :class="{ 'fill-current': a.is_bookmarked }" />
              </button>
            </div>
          </article>
        </div>
      </div>
    </main>

    <!-- ============================================== -->
    <!-- Article Reader Modal (Quick Preview Option)     -->
    <!-- ============================================== -->
    <div v-if="readingArticle" class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs p-4">
      <div class="bg-white dark:bg-[#121215] rounded-2xl border border-zinc-200 dark:border-zinc-800 max-w-3xl w-full max-h-[88vh] flex flex-col shadow-2xl animate-scale-in">
        <!-- Modal Header -->
        <div class="px-6 py-4 border-b border-zinc-200 dark:border-zinc-800 flex items-center justify-between gap-4">
          <div class="min-w-0">
            <h3 class="text-base sm:text-lg font-bold text-zinc-900 dark:text-zinc-100 truncate">
              {{ readingArticle.title }}
            </h3>
            <div class="text-xs text-zinc-400 mt-1 flex items-center gap-2">
              <span class="text-emerald-600 dark:text-emerald-400 font-medium">{{ readingArticle.gh_name }}</span>
              <span>·</span>
              <span class="font-mono">{{ formatDate(readingArticle.publish_time) }}</span>
            </div>
          </div>

          <div class="flex items-center gap-2 flex-shrink-0">
            <button
              @click="navigateToArticle(readingArticle.id)"
              class="text-xs px-3 py-1.5 rounded-lg bg-emerald-600 text-white font-medium hover:bg-emerald-500 flex items-center gap-1 transition-colors"
            >
              <span>独立页浏览</span>
              <ExternalLink class="w-3.5 h-3.5" />
            </button>
            <button @click="readingArticle = null" class="p-1.5 text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-200 rounded-lg">
              <X class="w-5 h-5" />
            </button>
          </div>
        </div>

        <!-- Modal Single Actions Bar -->
        <div class="px-6 py-3 bg-zinc-50/80 dark:bg-zinc-900/60 border-b border-zinc-200/80 dark:border-zinc-800/80 flex flex-wrap items-center justify-between gap-3 text-xs">
          <div class="flex items-center gap-2">
            <!-- Single Export in Modal -->
            <button
              @click="handleExportMD(readingArticle)"
              :disabled="actionLoading"
              class="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border text-xs font-medium transition-all shadow-xs"
              :class="readingArticle.is_exported ? 'bg-emerald-50 dark:bg-emerald-950/60 border-emerald-300 dark:border-emerald-800 text-emerald-700 dark:text-emerald-400' : 'bg-white dark:bg-[#18181b] border-zinc-200 dark:border-zinc-700 text-zinc-700 dark:text-zinc-300 hover:bg-zinc-50 dark:hover:bg-zinc-800'"
            >
              <FileDown class="w-3.5 h-3.5" />
              <span>{{ readingArticle.is_exported ? '已导出 Markdown' : '单篇导出 MD' }}</span>
            </button>

            <!-- Single IMA Push in Modal -->
            <button
              @click="handlePushIMA(readingArticle)"
              :disabled="actionLoading"
              class="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border text-xs font-medium transition-all shadow-xs"
              :class="readingArticle.is_pushed ? 'bg-indigo-50 dark:bg-indigo-950/60 border-indigo-300 dark:border-indigo-800 text-indigo-700 dark:text-indigo-400' : 'bg-white dark:bg-[#18181b] border-zinc-200 dark:border-zinc-700 text-zinc-700 dark:text-zinc-300 hover:bg-zinc-50 dark:hover:bg-zinc-800'"
            >
              <Send class="w-3.5 h-3.5" />
              <span>{{ readingArticle.is_pushed ? '已推送至 IMA' : '单篇推送到 IMA' }}</span>
            </button>

            <!-- Single AI Summary in Modal -->
            <button
              @click="handleSummarize(readingArticle)"
              :disabled="actionLoading"
              class="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border text-xs font-medium transition-all shadow-xs"
              :class="readingArticle.is_summarized ? 'bg-purple-50 dark:bg-purple-950/60 border-purple-300 dark:border-purple-800 text-purple-700 dark:text-purple-400' : 'bg-white dark:bg-[#18181b] border-zinc-200 dark:border-zinc-700 text-zinc-700 dark:text-zinc-300 hover:bg-zinc-50 dark:hover:bg-zinc-800'"
            >
              <Sparkles class="w-3.5 h-3.5" />
              <span>{{ readingArticle.is_summarized ? '已生成 AI 摘要' : '生成 AI 摘要' }}</span>
            </button>
          </div>

          <!-- Bookmark toggle -->
          <button
            @click="toggleBookmark(readingArticle)"
            class="inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-lg text-xs font-medium text-zinc-600 dark:text-zinc-400 hover:text-amber-500 transition-colors"
            :class="{ 'text-amber-500 dark:text-amber-400': readingArticle.is_bookmarked }"
          >
            <Bookmark class="w-4 h-4" :class="{ 'fill-current': readingArticle.is_bookmarked }" />
            <span>{{ readingArticle.is_bookmarked ? '已收藏' : '收藏' }}</span>
          </button>
        </div>

        <!-- Modal Body -->
        <div class="flex-1 overflow-y-auto px-6 py-5 prose prose-zinc dark:prose-invert max-w-none text-base leading-relaxed space-y-4">
          <!-- AI Digest Card if available -->
          <div v-if="readingArticle.digest" class="p-5 rounded-xl bg-purple-50/70 dark:bg-purple-950/40 border border-purple-200/70 dark:border-purple-900/50 text-sm text-purple-950 dark:text-purple-200 leading-relaxed space-y-2 not-prose">
            <div class="flex items-center gap-2 font-bold text-purple-700 dark:text-purple-300 uppercase tracking-wide text-xs">
              <Sparkles class="w-4 h-4" />
              <span>AI 提炼看点</span>
            </div>
            <div>{{ readingArticle.digest }}</div>
          </div>

          <div v-if="readingArticle.content" v-html="readingArticle.content"></div>
          <div v-else-if="readingArticle.description" class="p-5 rounded-xl bg-zinc-50 dark:bg-zinc-900 text-zinc-700 dark:text-zinc-300 not-prose">
            {{ readingArticle.description }}
          </div>
          <div v-else class="text-center py-12 text-zinc-400 not-prose">
            暂无正文离线预览，可点击右上角「在微信中打开」直接查看
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { 
  Search, 
  Bookmark, 
  Sparkles, 
  ExternalLink, 
  X, 
  FileDown, 
  Send, 
  RefreshCw, 
  Flame, 
  Layers, 
  Tag,
  ArrowRight
} from 'lucide-vue-next';
import { api } from '@/api/client';
import { useToast } from '@/composables/useToast';

const route = useRoute();
const router = useRouter();
const toast = useToast();

// Determine initial mode from path or query
const isDigestRoute = computed(() => {
  return route.path === '/biz/digest' || route.query.mode === 'digest';
});

const mode = ref(isDigestRoute.value ? 'digest' : 'stream');
const searchQuery = ref(route.query.q || '');
const onlyBookmarked = ref(false);
const selectedTagId = ref(route.query.tag ? Number(route.query.tag) : (route.query.tag_id ? Number(route.query.tag_id) : null));
const selectedGhid = ref(route.query.ghid || null);

watch(() => route.path, (newPath) => {
  if (newPath === '/biz/digest') {
    mode.value = 'digest';
  } else if (newPath === '/biz' && route.query.mode !== 'digest') {
    mode.value = 'stream';
  }
});

watch(() => route.query.mode, (newMode) => {
  if (newMode === 'digest' || newMode === 'stream') {
    mode.value = newMode;
  }
});

watch(() => route.query.q, (newQ) => {
  if (newQ !== undefined && newQ !== searchQuery.value) {
    searchQuery.value = newQ;
  }
});

watch(() => route.query.tag, (newTag) => {
  const val = newTag ? Number(newTag) : null;
  if (selectedTagId.value !== val) selectedTagId.value = val;
});

watch(() => route.query.ghid, (newGhid) => {
  const val = newGhid || null;
  if (selectedGhid.value !== val) selectedGhid.value = val;
});

function setMode(newMode) {
  mode.value = newMode;
  if (newMode === 'digest') {
    router.push({ path: '/biz/digest' });
  } else {
    router.push({ path: '/biz' });
  }
}

function filterByKeyword(kw) {
  searchQuery.value = kw;
  mode.value = 'stream';
  router.push({ path: '/biz', query: { q: kw } });
}

function navigateToArticle(id, from = 'digest') {
  router.push({
    path: `/biz/article/${id}`,
    query: { from }
  });
}

const accounts = ref([]);
const tags = ref([]);
const articles = ref([]);
const digestData = ref(null);
const loadingArticles = ref(false);
const loadingDigest = ref(false);
const actionLoading = ref(false);
const readingArticle = ref(null);

const filteredAccounts = computed(() => {
  if (!selectedTagId.value) {
    return accounts.value.filter(a => !a.is_hidden || a.is_watched);
  }
  return accounts.value.filter(a => (a.tags || []).some(t => t.id === selectedTagId.value));
});

const selectedTag = computed(() => {
  if (!selectedTagId.value) return null;
  return tags.value.find(t => t.id === selectedTagId.value);
});

const selectedAccount = computed(() => {
  if (!selectedGhid.value) return null;
  return accounts.value.find(a => a.gh_id === selectedGhid.value);
});

const selectedFilterTitle = computed(() => {
  if (selectedTag.value && selectedAccount.value) {
    return `分类「${selectedTag.value.name}」 · 公众号「${selectedAccount.value.name}」`;
  }
  if (selectedAccount.value) {
    return `公众号「${selectedAccount.value.name}」`;
  }
  if (selectedTag.value) {
    return `分类「${selectedTag.value.name}」 (${filteredAccounts.value.length} 个公众号)`;
  }
  return '';
});

function selectTag(tagId) {
  if (selectedTagId.value === tagId) {
    selectedTagId.value = null;
  } else {
    selectedTagId.value = tagId;
  }
  selectedGhid.value = null;
  const q = { ...route.query };
  if (selectedTagId.value) {
    q.tag = selectedTagId.value;
  } else {
    delete q.tag;
    delete q.tag_id;
  }
  delete q.ghid;
  router.replace({ query: q });
}

function selectAccount(ghid) {
  if (selectedGhid.value === ghid) {
    selectedGhid.value = null;
  } else {
    selectedGhid.value = ghid;
  }
  const q = { ...route.query };
  if (selectedGhid.value) {
    q.ghid = selectedGhid.value;
  } else {
    delete q.ghid;
  }
  router.replace({ query: q });
}

function clearFilters() {
  selectedTagId.value = null;
  selectedGhid.value = null;
  const q = { ...route.query };
  delete q.tag;
  delete q.tag_id;
  delete q.ghid;
  router.replace({ query: q });
}

async function loadInitial() {
  const [accRes, tagRes] = await Promise.all([api.getAccounts({ include_hidden: true }), api.getTags()]);
  const rawAccs = accRes.items || accRes.accounts || [];
  accounts.value = rawAccs.map(a => ({
    ...a,
    gh_id: a.ghID || a.gh_id,
    name: a.ghName || a.name || '未知公众号',
    article_count: a.articleCount ?? a.article_count ?? 0,
    is_hidden: a.hidden ?? a.is_hidden ?? false,
    is_watched: a.watched ?? a.is_watched ?? false,
    tags: a.tags || [],
  }));
  tags.value = tagRes.items || tagRes.tags || [];
  loadArticles();
}

async function loadArticles() {
  loadingArticles.value = true;
  try {
    let res;
    if (onlyBookmarked.value) {
      res = await api.getBookmarks(50);
    } else if (searchQuery.value.trim()) {
      res = await api.searchArticles(searchQuery.value.trim(), 50);
    } else {
      res = await api.getArticles({
        limit: 50,
        ghid: selectedGhid.value || undefined,
        tag_id: (!selectedGhid.value && selectedTagId.value) ? selectedTagId.value : undefined,
      });
    }

    const list = res.items || res.articles || [];
    articles.value = list.map(art => {
      const expStatus = art.exportStatus || art.export_status || '';
      const pushStatus = art.pushStatus || art.push_status || '';
      const isExported = expStatus === 'exported' || expStatus === 'summary_generated';
      const isPushed = pushStatus === 'pushed';
      const isSummarized = expStatus === 'summary_generated' || Boolean(art.digest || art.ai_summary);

      return {
        ...art,
        id: art.id,
        gh_id: art.ghID || art.gh_id,
        gh_name: art.ghName || art.gh_name,
        title: art.title,
        description: art.description || art.desc,
        url: art.url,
        publish_time: art.publishedAt || art.publish_time,
        published_at: art.publishedAt || art.published_at,
        is_bookmarked: art.bookmarked ?? art.is_bookmarked ?? false,
        bookmarked: art.bookmarked ?? art.is_bookmarked ?? false,
        is_read: art.isRead ?? art.is_read ?? false,
        is_exported: isExported,
        is_pushed: isPushed,
        is_summarized: isSummarized,
        export_status: expStatus,
        push_status: pushStatus,
      };
    });
  } catch (err) {
    toast.error('加载文章失败: ' + err.message);
  } finally {
    loadingArticles.value = false;
  }
}

function extractCleanHeadline(rawHeadline, fallback) {
  if (!rawHeadline) return fallback || '今日公众号精选提炼';
  const trimmed = String(rawHeadline).trim();
  if (trimmed.startsWith('{') || trimmed.includes('"headline":')) {
    const match = trimmed.match(/"headline"\s*:\s*"([^"]+)"/);
    if (match && match[1]) {
      return match[1];
    }
  }
  return trimmed;
}

async function loadDigest(fresh = false) {
  loadingDigest.value = true;
  try {
    const [feedRes, dailyRes] = await Promise.allSettled([
      api.getFeedDigest(7, fresh),
      api.getDailyDigest(fresh),
    ]);

    const feedData = feedRes.status === 'fulfilled' ? feedRes.value : null;
    const dailyData = dailyRes.status === 'fulfilled' ? dailyRes.value : null;

    const summaryObj = feedData?.summary || {};
    const structuredObj = summaryObj.structured || {};
    const digestObj = dailyData?.digest || {};

    const rawHeadline = digestObj.headline || summaryObj.headline || structuredObj.headline;
    const cleanHeadline = extractCleanHeadline(rawHeadline, summaryObj.headline || structuredObj.headline || '今日公众号精选提炼');

    digestData.value = {
      feed: summaryObj,
      daily: digestObj,
      headline: cleanHeadline,
      digestDate: digestObj.digestDate || '今日推荐',
      picks: digestObj.picks || [],
      keywords: structuredObj.keywords || [],
      hotTakes: structuredObj.hotTakes || [],
      themes: structuredObj.themes || [],
    };
  } catch (err) {
    toast.error('加载看点数据失败: ' + err.message);
    digestData.value = null;
  } finally {
    loadingDigest.value = false;
  }
}

function refreshDigest(fresh = true) {
  loadDigest(fresh);
}

// Single Article Action Handlers
async function handleExportMD(item) {
  const id = typeof item === 'object' ? item.id : item;
  actionLoading.value = true;
  try {
    toast.info('正在抓取并导出本地 Markdown…');
    await api.exportArticleMD(id);
    toast.success(`已导出为本地 Markdown！`);
    
    // Update local state
    if (typeof item === 'object') {
      item.is_exported = true;
      item.export_status = 'exported';
    }
    const matched = articles.value.find(a => a.id === id);
    if (matched) {
      matched.is_exported = true;
      matched.export_status = 'exported';
    }
    if (readingArticle.value && readingArticle.value.id === id) {
      readingArticle.value.is_exported = true;
      readingArticle.value.export_status = 'exported';
    }
  } catch (err) {
    toast.error('单篇导出失败: ' + err.message);
  } finally {
    actionLoading.value = false;
  }
}

async function handlePushIMA(item) {
  const id = typeof item === 'object' ? item.id : item;
  actionLoading.value = true;
  try {
    toast.info('正在同步推送到 IMA 知识库…');
    await api.pushArticleIMA(id);
    toast.success('已成功推送到 IMA 知识库！');
    
    if (typeof item === 'object') {
      item.is_pushed = true;
      item.push_status = 'pushed';
    }
    const matched = articles.value.find(a => a.id === id);
    if (matched) {
      matched.is_pushed = true;
      matched.push_status = 'pushed';
    }
    if (readingArticle.value && readingArticle.value.id === id) {
      readingArticle.value.is_pushed = true;
      readingArticle.value.push_status = 'pushed';
    }
  } catch (err) {
    toast.error('推送到 IMA 失败: ' + err.message);
  } finally {
    actionLoading.value = false;
  }
}

async function handleSummarize(item) {
  const id = typeof item === 'object' ? item.id : item;
  actionLoading.value = true;
  try {
    toast.info('大模型正在为本篇生成结构化摘要…');
    await api.summarizeArticle(id);
    toast.success('AI 摘要生成成功！');
    
    if (typeof item === 'object') {
      item.is_summarized = true;
    }
    const matched = articles.value.find(a => a.id === id);
    if (matched) {
      matched.is_summarized = true;
    }
    // Update reader if open
    if (readingArticle.value && readingArticle.value.id === id) {
      const updated = await api.getArticle(id);
      readingArticle.value.digest = updated.digest || updated.description;
      readingArticle.value.is_summarized = true;
    }
  } catch (err) {
    toast.error('AI 摘要生成失败: ' + err.message);
  } finally {
    actionLoading.value = false;
  }
}

watch(mode, (newMode) => {
  if (newMode === 'digest' && !digestData.value) {
    loadDigest();
  }
});

watch([selectedTagId, selectedGhid, onlyBookmarked], () => {
  loadArticles();
});

let searchTimer;
watch(searchQuery, () => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(loadArticles, 300);
});

async function openArticle(id) {
  try {
    const data = await api.getArticle(id);
    const expStatus = data.exportStatus || data.export_status || '';
    const pushStatus = data.pushStatus || data.push_status || '';

    readingArticle.value = {
      ...data,
      id: data.id,
      gh_name: data.ghName || data.gh_name,
      publish_time: data.publishedAt || data.publish_time,
      is_bookmarked: data.bookmarked ?? data.is_bookmarked ?? false,
      is_exported: expStatus === 'exported' || expStatus === 'summary_generated',
      is_pushed: pushStatus === 'pushed',
      is_summarized: expStatus === 'summary_generated' || Boolean(data.digest || data.ai_summary),
    };
  } catch (err) {
    toast.error('获取文章详情失败: ' + err.message);
  }
}

async function toggleBookmark(a) {
  const next = !a.is_bookmarked;
  try {
    await api.toggleBookmark(a.id, next);
    a.is_bookmarked = next;
    a.bookmarked = next;
    toast.success(next ? '已加入收藏' : '已取消收藏');
  } catch (err) {
    toast.error('收藏失败: ' + err.message);
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

onMounted(async () => {
  await router.isReady();
  if (route.query.tag) {
    selectedTagId.value = Number(route.query.tag);
  } else if (route.query.tag_id) {
    selectedTagId.value = Number(route.query.tag_id);
  }
  if (route.query.ghid) {
    selectedGhid.value = route.query.ghid;
  }
  loadInitial();
  loadDigest();
});
</script>
