/* bizhub-report.js — 报告渲染模块
 *
 * 职责：把「后端报告数据」渲染成 DOM。涵盖三层：
 *   ① 取值器   getStructured / getHighlights / getThemes / getMustReads / getByAccount / hasStructured
 *   ② 渲染块   render*Block / renderStructuredBody / renderFallbackBody / renderMetaFooter
 *   ③ 成品卡片 reportCardHTML
 * 另有 3 个只会被本模块用到的辅助函数：scoreNumber / scoreStyle / articleLabel，
 * 以及主题折叠的 toggleThemeCard、列表用的 metaSnippet。
 *
 * 为什么独立成模块：
 *   报告的数据结构（structured / highlights / themes / mustReads / byAccount）
 *   是这套接口里最不稳定的一块 —— LLM 返回的字段大小写、拼写、缺字段都需要
 *   在取值器里做兼容。把它和「轻提示/日期格式化/分页」这类基础工具放在同一个
 *   文件里，会让改报告结构的人不得不读一份跟自己无关的文件。拆开后：
 *     · 改报告渲染 → 只动本文件
 *     · 改基础工具 → 只动 bizhub.js
 *
 * 对外契约：window.BizHub.Report.<name>，页面用 BizHub.Report.getStructured(...) 调用。
 *
 * 依赖方向（单向，不成环）：
 *   本模块 → bizhub.js（esc / formatDate / formatDateTime / rangeLabel / renderMarkdown）
 *   bizhub.js 不依赖本模块。
 *   这些都是运行时按名字查找的，所以加载顺序只要满足「bizhub.js → bizhub-report.js」
 *   即可；页面内联 <script> 里的 esc 在本模块函数被调用时已经存在。
 *
 * 内联事件注意：reportCardHTML 生成的折叠按钮用
 *   onclick="BizHub.Report.toggleThemeCard(this)"
 * 引用，必须带命名空间 —— 模块内的函数不在全局，写裸名会 ReferenceError。
 *
 * 由脚本从 bizhub.js 按函数边界原样搬移而来，函数体未做改动。
 */

(function (global) {
    if (!global.BizHub) global.BizHub = {};

    /* ---------- ① 取值器：对 LLM 返回的大小写/拼写差异做兼容 ---------- */

    function getStructured(s) {
        return (s && (s.structured || s.Structured)) || null;
    }

    function getHighlights(st) {
        if (!st) return [];
        const v = st.highLights || st.highlights || st.Highlights;
        if (Array.isArray(v)) return v.filter(x => typeof x === 'string' && x.trim()).slice(0, 5);
        if (typeof v === 'string' && v.trim()) return [v];
        return [];
    }

    function getThemes(st) {
        if (!st) return [];
        const v = st.themes || st.Themes;
        return Array.isArray(v) ? v.filter(t => t && (t.title || t.summary)) : [];
    }

    function getMustReads(st) {
        if (!st) return [];
        const v = st.mustReads || st.mustReads || st.MustReads;
        return Array.isArray(v) ? v.filter(m => m && (m.title || m.url)) : [];
    }

    function getByAccount(st) {
        if (!st) return [];
        const v = st.byAccount || st.ByAccount;
        return Array.isArray(v) ? v.filter(a => a && (a.ghName || a.ghID || a.digest)) : [];
    }

    function hasStructured(s) {
        const st = getStructured(s);
        if (!st) return false;
        if (getHighlights(st).length) return true;
        if (getThemes(st).length) return true;
        if (getMustReads(st).length) return true;
        if (getByAccount(st).length) return true;
        const summaryText = st.summary || st.Summary;
        if (typeof summaryText === 'string' && summaryText.trim()) return true;
        return false;
    }

    function scoreNumber(score) {
        const n = Number(score);
        if (!isFinite(n) || n <= 0) return 1;
        return Math.max(1, Math.min(10, Math.round(n)));
    }

    function scoreStyle(score) {
        const n = scoreNumber(score);
        const opacity = (0.28 + (n - 1) * 0.08).toFixed(2);
        return `background: var(--accent); opacity: ${opacity};`;
    }

    function articleLabel(a) {
        return a.ghName || a.ghID || a.accountName || '';
    }


    /* ---------- ② 渲染块：每块独立返回 HTML，无内容时返回空串 ---------- */

    function renderHighlightsBlock(st) {
        const items = getHighlights(st);
        if (!items.length) return '';
        return `<div class="highlights-block">
            <div class="summary-block-label">重点摘录</div>
            <div class="highlights-list">
                ${items.map(t => `<div class="highlight-pill">${esc(t)}</div>`).join('')}
            </div>
        </div>`;
    }

    function renderSummaryBlock(st) {
        const text = (st && (st.summary || st.Summary)) || '';
        if (!text || !text.trim()) return '';
        return `<div class="summary-block">
            <div class="summary-block-label">总评</div>
            <p class="summary-block-text">${esc(text)}</p>
        </div>`;
    }

    function renderThemesBlock(st) {
        const themes = getThemes(st);
        if (!themes.length) return '';
        const cards = themes.map((t, idx) => {
            const arts = Array.isArray(t.articles) ? t.articles.filter(a => a && (a.title || a.url)) : [];
            const artsHTML = arts.length
                ? `<div class="theme-articles">${arts.map(a => `<a class="theme-article-item" href="${esc(a.url || '#')}" target="_blank" rel="noopener">
                        <span class="theme-article-src">${esc(articleLabel(a) || '')}</span>
                        <span class="theme-article-title">${esc(a.title || '')}</span>
                        <span class="theme-article-ext">↗</span>
                    </a>`).join('')}</div>`
                : '';
            return `<div class="theme-card" data-theme="${idx}">
                <button type="button" class="theme-head" onclick="BizHub.Report.toggleThemeCard(this)" aria-expanded="false">
                    <span class="theme-head-title">${esc(t.title || `主题 ${idx + 1}`)}</span>
                    ${arts.length ? `<span class="theme-head-meta">${arts.length} 篇</span>` : ''}
                    <span class="theme-chevron">▶</span>
                </button>
                <div class="theme-body">
                    ${t.summary ? `<p class="theme-summary">${esc(t.summary)}</p>` : ''}
                    ${artsHTML}
                </div>
            </div>`;
        }).join('');
        return `<div class="themes-block">
            <div class="summary-block-label">主题</div>
            <div class="themes-list">${cards}</div>
        </div>`;
    }

    function renderMustReadsBlock(st) {
        const items = getMustReads(st);
        if (!items.length) return '';
        const rows = items.map(m => {
            const score = scoreNumber(m.score);
            const reason = m.reason || m.summary || '';
            const src = articleLabel(m) || '';
            return `<div class="must-read-item">
                <div class="must-read-score" style="${scoreStyle(score)}" title="评分 ${score}"></div>
                <div class="must-read-main">
                    <div class="must-read-title"><a href="${esc(m.url || '#')}" target="_blank" rel="noopener">${esc(m.title || '无标题')}</a></div>
                    <div class="must-read-meta">
                        ${src ? `<span class="must-read-src">${esc(src)}</span>` : ''}
                        ${reason ? `<span class="must-read-reason">${esc(reason)}</span>` : ''}
                        <span class="must-read-score-num">${score} 分</span>
                    </div>
                </div>
                <a class="must-read-open btn btn-sm btn-ghost" href="${esc(m.url || '#')}" target="_blank" rel="noopener">打开</a>
            </div>`;
        }).join('');
        return `<div class="must-reads-block">
            <div class="summary-block-label">重点阅读</div>
            <div class="must-read-list">${rows}</div>
        </div>`;
    }

    function renderByAccountBlock(st) {
        const items = getByAccount(st);
        if (!items.length) return '';
        const rows = items.map(a => `<div class="by-account-item">
            <span class="by-account-name">${esc(articleLabel(a) || '')}</span>
            <span class="by-account-digest">${esc(a.digest || a.summary || '')}</span>
        </div>`).join('');
        return `<details class="by-account-block">
            <summary>
                <span>按公众号小段 · ${items.length}</span>
                <span class="chevron">▶</span>
            </summary>
            <div class="by-account-list">${rows}</div>
        </details>`;
    }


    /* ---------- 组装：把各块拼成报告正文；全空则返回空串，交由调用方走回退分支 ---------- */

    function renderStructuredBody(st) {
        const parts = [
            renderSummaryBlock(st),
            renderHighlightsBlock(st),
            renderThemesBlock(st),
            renderMustReadsBlock(st),
            renderByAccountBlock(st),
        ].filter(Boolean);
        if (!parts.length) return '';
        return `<div class="report-body">${parts.join('')}</div>`;
    }

    function renderFallbackBody(content) {
        return `<div class="report-body"><div class="report-body-fallback">${renderMarkdown(content || '')}</div></div>`;
    }

    function renderMetaFooter(s) {
        const parts = [];
        const fetched = s.fetchedCount ?? s.FetchedCount;
        const model = s.model || s.Model;
        const tokIn = s.tokensIn ?? s.TokensIn;
        const tokOut = s.tokensOut ?? s.TokensOut;
        const instruction = s.instruction || s.Instruction;
        const created = formatDateTime(s.createdAt || s.created_at);
        const articleCount = s.articleCount ?? s.ArticleCount;

        if (typeof fetched === 'number') parts.push(`<span class="meta-val">${fetched}</span><span class="meta-key">篇正文</span>`);
        else if (articleCount) parts.push(`<span class="meta-val">${articleCount}</span><span class="meta-key">篇候选</span>`);
        if (model) parts.push(`<span class="meta-key">模型</span><span class="meta-val">${esc(model)}</span>`);
        if (typeof tokIn === 'number' || typeof tokOut === 'number') {
            const tin = typeof tokIn === 'number' ? tokIn.toLocaleString() : '-';
            const tout = typeof tokOut === 'number' ? tokOut.toLocaleString() : '-';
            parts.push(`<span class="meta-key">tokens</span><span class="meta-val">${tin} / ${tout}</span>`);
        }
        if (created) parts.push(`<span class="meta-val">${esc(created)}</span>`);
        if (instruction && instruction.trim()) {
            const trimmed = instruction.trim();
            const snippet = trimmed.length > 60 ? trimmed.slice(0, 60) + '…' : trimmed;
            parts.push(`<span class="meta-instruction" title="${esc(trimmed)}">${esc(snippet)}</span>`);
        }
        if (!parts.length) return '';
        const sep = '<span class="sep">·</span>';
        return `<div class="report-meta">${parts.join(sep)}</div>`;
    }


    /* ---------- ③ 成品卡片：列表里的一张报告卡 ---------- */

    function reportCardHTML(s) {
        const date = formatDate(s.createdAt || s.created_at) || '';
        const range = rangeLabel(s.days || s.range);
        const instruction = s.instruction || s.Instruction;
        const instrHTML = instruction && instruction.trim()
            ? `<div class="report-instruction">${esc(instruction)}</div>`
            : '';
        const st = getStructured(s);
        let bodyHTML;
        if (hasStructured(s)) {
            bodyHTML = renderStructuredBody(st);
        } else {
            bodyHTML = renderFallbackBody(s.content || s.Content || '');
        }
        const metaHTML = renderMetaFooter(s);
        return `<div class="report-card" data-id="${esc(s.id || '')}">
            <div class="report-head">
                <div class="report-head-meta">
                    <div class="report-date">${esc(date)}</div>
                    ${instrHTML}
                </div>
                <div class="report-tags">
                    <span class="report-range">${esc(range)}</span>
                </div>
            </div>
            ${bodyHTML}
            ${metaHTML}
        </div>`;
    }


    /* ---------- 交互 ---------- */

    function toggleThemeCard(btn) {
        const card = btn.closest('.theme-card');
        if (!card) return;
        const open = card.classList.toggle('open');
        btn.setAttribute('aria-expanded', open ? 'true' : 'false');
    }


    /* ---------- 列表用：报告元信息摘要 ---------- */

    function metaSnippet(s) {
        const parts = [];
        const fetched = s.fetchedCount ?? s.FetchedCount;
        const articleCount = s.articleCount ?? s.ArticleCount;
        const tokIn = s.tokensIn ?? s.TokensIn;
        const tokOut = s.tokensOut ?? s.TokensOut;
        const model = s.model || s.Model;
        if (typeof fetched === 'number') parts.push(`抓取 ${fetched}`);
        else if (articleCount) parts.push(`${articleCount} 篇`);
        if (model) parts.push(esc(model));
        if (typeof tokIn === 'number' || typeof tokOut === 'number') {
            const tin = typeof tokIn === 'number' ? tokIn.toLocaleString() : '-';
            const tout = typeof tokOut === 'number' ? tokOut.toLocaleString() : '-';
            parts.push(`tokens ${tin}/${tout}`);
        }
        return parts;
    }

    global.BizHub.Report = {
        getStructured: getStructured,
        getHighlights: getHighlights,
        getThemes: getThemes,
        getMustReads: getMustReads,
        getByAccount: getByAccount,
        hasStructured: hasStructured,
        scoreNumber: scoreNumber,
        scoreStyle: scoreStyle,
        articleLabel: articleLabel,
        renderHighlightsBlock: renderHighlightsBlock,
        renderSummaryBlock: renderSummaryBlock,
        renderThemesBlock: renderThemesBlock,
        renderMustReadsBlock: renderMustReadsBlock,
        renderByAccountBlock: renderByAccountBlock,
        renderStructuredBody: renderStructuredBody,
        renderFallbackBody: renderFallbackBody,
        renderMetaFooter: renderMetaFooter,
        reportCardHTML: reportCardHTML,
        toggleThemeCard: toggleThemeCard,
        metaSnippet: metaSnippet
    };
})(window);
