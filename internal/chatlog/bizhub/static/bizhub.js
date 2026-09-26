/* bizhub.js — 公众号模块公共脚本（基础层）
 *
 * 来源：由 templates/{biz,feed,summary,admin}.html 内联 <script> 中
 *       【跨页面逐字节一致】的函数与常量收敛而成。
 *
 * 内容边界
 *   本文件只放「跟业务数据结构无关」的基础工具：轻提示、日期格式化、范围标签、
 *   轻量 Markdown、空态、指令面板开合、历史面板开合、无限滚动观察、同步按钮状态。
 *   跟报告数据结构（structured / highlights / themes / mustReads / byAccount）
 *   绑定的渲染逻辑在另一个文件：static/bizhub-report.js。
 *   两层都是独立的静态资源，页面按顺序引入：
 *
 *       <script src="/biz/static/bizhub.js"></script>
 *       <script src="/biz/static/bizhub-report.js"></script>   <!-- 用到报告的页面才需要 -->
 *       <script> ...页面专属逻辑... </script>
 *
 * 维护约定
 *   1. 只放「跨页面共享」的函数；页面专属逻辑留在各自模板的 <script> 里。
 *   2. 加载顺序：本文件在模板内联 <script> 之前引入。为了让页面里后声明的同名
 *      function 能正常覆盖（JavaScript 允许重复的函数声明），本文件只使用
 *      function 声明与顶层 const，不额外包裹 IIFE。
 *   3. 部分函数会调用页面私有函数（由页面自行提供）：
 *        toggleHistory() -> loadHistory() / historyLoaded
 *        triggerSync()   -> pollSyncStatus()
 *        watchLoadMore() -> loadMoreObserver
 *      这些名字必须在引用它的页面里存在，否则会抛 ReferenceError。
 *      同理，renderMarkdown() / emptyStateHTML() 依赖页面提供的 esc()。
 */

/* ---------- 共享常量 ---------- */

const EMPTY_SVG = `<svg viewBox="0 0 48 48" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
    <path d="M10 8h20l8 8v24a2 2 0 0 1-2 2H10a2 2 0 0 1-2-2V10a2 2 0 0 1 2-2z"/>
    <path d="M30 8v8h8"/>
    <path d="M16 24h16M16 30h16M16 36h9"/>
</svg>`;

const SYNC_DOTS = '<span class="sync-dots"><span>.</span><span>.</span><span>.</span></span>';


/* ---------- 鉴权与网络 ---------- */

/* 后端 authMiddleware 只认两种凭据：`?token=` 或 `Authorization: Bearer`。
 * 页面本身也是靠 `?token=` 打开的（/biz?token=xxx），所以这里从当前 URL 取，
 * 并存进 sessionStorage —— 这样用户点了不带 token 的内部链接后还能继续用。
 *
 * 为什么需要这一层：浏览器不会自动把页面 URL 上的 query 带到子请求上，
 * 页面内所有 fetch 都不带 token，配了 token 的部署下会全部 401（页面永远空）。 */
function authToken() {
    let fromQuery = '';
    try {
        fromQuery = new URLSearchParams(location.search).get('token') || '';
    } catch (e) { /* 老浏览器无 URLSearchParams，退化为 sessionStorage */ }
    if (fromQuery) {
        try { sessionStorage.setItem('chatlog-token', fromQuery); } catch (e) { /* 隐私模式 */ }
        return fromQuery;
    }
    try { return sessionStorage.getItem('chatlog-token') || ''; } catch (e) { return ''; }
}

/* 所有业务接口调用都走这里，不要直接 fetch —— 否则会漏掉 token。 */
function apiFetch(url, options) {
    const token = authToken();
    if (token && !/[?&]token=/.test(url)) {
        url += (url.indexOf('?') >= 0 ? '&' : '?') + 'token=' + encodeURIComponent(token);
    }
    return fetch(url, options);
}

/* 让页面里的同源链接也带上 token。只处理站内绝对路径（/ 开头），
 * 外链（公众号原文、Google Fonts）一律不动。 */
function propagateAuthToken() {
    const token = authToken();
    if (!token) return;
    document.querySelectorAll('a[href^="/"]').forEach(a => {
        const href = a.getAttribute('href') || '';
        a.setAttribute('href', pageHref(href));
    });
}

/* 给站内路径拼上 token，供【动态生成】的链接使用。
 *
 * propagateAuthToken 只在页面加载时扫一遍 DOM，而抽屉、浮层、列表里的链接是
 * 之后用 innerHTML 插进来的 —— 那些必须自己在生成时调这个函数，
 * 否则点过去会因为没带 token 被拦下。 */
function pageHref(path) {
    const token = authToken();
    if (!token || /[?&]token=/.test(path)) return path;
    return path + (path.indexOf('?') >= 0 ? '&' : '?') + 'token=' + encodeURIComponent(token);
}

propagateAuthToken();


/* ---------- 工具函数 ---------- */

function toast(msg, type) {
    const c = document.getElementById('toastContainer');
    const el = document.createElement('div');
    el.className = 'toast ' + (type || 'success');
    el.textContent = msg;
    c.appendChild(el);
    setTimeout(() => {
        el.classList.add('hide');
        setTimeout(() => el.remove(), 250);
    }, 2500);
}

function formatDate(ts) {
    if (!ts) return '';
    const d = new Date(Number(ts) > 1e12 ? Number(ts) : Number(ts) * 1000);
    if (isNaN(d.getTime())) return '';
    const y = d.getFullYear();
    const m = String(d.getMonth() + 1).padStart(2, '0');
    const day = String(d.getDate()).padStart(2, '0');
    return `${y}-${m}-${day}`;
}

function formatDateTime(ts) {
    const date = formatDate(ts);
    if (!date) return '';
    const d = new Date(Number(ts) > 1e12 ? Number(ts) : Number(ts) * 1000);
    const hh = String(d.getHours()).padStart(2, '0');
    const mm = String(d.getMinutes()).padStart(2, '0');
    return `${date} ${hh}:${mm}`;
}

function rangeLabel(days) {
    const map = { 1: '今天', 3: '近3天', 7: '近一周' };
    return map[days] || (days ? `近${days}天` : '汇总');
}

function renderMarkdown(md) {
    const inline = s => esc(s).replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
    const lines = String(md || '').split(/\r?\n/);
    let html = '';
    let inList = false;
    const closeList = () => { if (inList) { html += '</ul>'; inList = false; } };
    for (const raw of lines) {
        const t = raw.trim();
        if (!t) { closeList(); continue; }
        let m;
        if ((m = t.match(/^###\s+(.*)/))) { closeList(); html += `<h3>${inline(m[1])}</h3>`; }
        else if ((m = t.match(/^##\s+(.*)/))) { closeList(); html += `<h2>${inline(m[1])}</h2>`; }
        else if ((m = t.match(/^#\s+(.*)/))) { closeList(); html += `<h1>${inline(m[1])}</h1>`; }
        else if (/^(-{3,}|\*{3,})$/.test(t)) { closeList(); html += '<hr>'; }
        else if ((m = t.match(/^[-*•]\s+(.*)/))) {
            if (!inList) { html += '<ul>'; inList = true; }
            html += `<li>${inline(m[1])}</li>`;
        } else {
            closeList();
            html += `<p>${inline(t)}</p>`;
        }
    }
    closeList();
    return html;
}


function toggleInstruction() {
    const pane = document.getElementById('instructionPane');
    const btn = document.getElementById('toggleInstructionBtn');
    const isHidden = pane.hasAttribute('hidden');
    if (isHidden) {
        pane.removeAttribute('hidden');
        btn.classList.add('btn-accent');
        btn.classList.remove('btn-ghost');
        btn.setAttribute('aria-expanded', 'true');
        setTimeout(() => {
            const ta = document.getElementById('instructionInput');
            if (ta) ta.focus();
        }, 30);
    } else {
        pane.setAttribute('hidden', '');
        btn.classList.remove('btn-accent');
        btn.classList.add('btn-ghost');
        btn.setAttribute('aria-expanded', 'false');
    }
}

function toggleHistory() {
    const wrap = document.getElementById('historyWrap');
    wrap.classList.toggle('open');
    if (wrap.classList.contains('open') && !historyLoaded) loadHistory();
}


function emptyStateHTML(msg) {
    return `<div class="empty-card"><div class="empty-state">${EMPTY_SVG}<p>${esc(msg)}</p></div></div>`;
}

async function exportArticleMD(articleId, btn) {
    if (!btn) return;
    const originalText = btn.textContent;
    btn.disabled = true;
    btn.textContent = '导出中...';
    try {
        const resp = await apiFetch(`/api/v1/biz/articles/${articleId}/export`, { method: 'POST' });
        const data = await resp.json();
        if (!resp.ok) {
            toast(data.error || '导出失败', 'error');
            btn.textContent = originalText;
            btn.disabled = false;
            return;
        }
        btn.textContent = '✓ 已导出';
        btn.classList.add('exported');
        toast(`已导出: ${data.account || ''}/${data.title || ''}`, 'success');
    } catch (e) {
        toast('导出请求失败: ' + e.message, 'error');
        btn.textContent = originalText;
        btn.disabled = false;
    }
}

// pushArticleToIMA 单篇推送。链式前置：未 export 成功的不能推。
//
// 文案与按钮状态全部交给后端发回的 JSON —— 401/409/500 都显示后端的 error 字段，
// 因为只有后端知道为什么不能推：是未配置 IMA（400）还是文章没归档（409），
// 还是网络/凭证错（500）。
async function pushArticleToIMA(articleId, btn) {
    if (!btn) return;
    if (btn.disabled) {
        toast('请先执行 MD 归档', 'hint');
        return;
    }
    const originalText = btn.textContent;
    btn.disabled = true;
    btn.textContent = '推送中...';
    try {
        const resp = await apiFetch(`/api/v1/biz/articles/${articleId}/push`, { method: 'POST' });
        const data = await resp.json();
        if (!resp.ok) {
            // 409 = 未归档（链式硬约束）；400 = 未配置 IMA；其他 = 推送失败
            const hint = resp.status === 409
                ? '（提示：推送前需要先归档这篇文章）'
                : (resp.status === 400 ? '（提示：请在 chatlog-server.json 配置 ima_push_skill_dir / ima_push_kb_id）' : '');
            toast(`${data.error || '推送失败'} ${hint}`, 'error');
            btn.textContent = originalText;
            btn.disabled = false;
            return;
        }
        btn.textContent = '✓ IMA';
        btn.classList.add('pushed');
        toast(`已推送: ${data.account || ''}/${data.title || ''} (media: ${data.mediaID || ''})`, 'success');
    } catch (e) {
        toast('推送请求失败: ' + e.message, 'error');
        btn.textContent = originalText;
        btn.disabled = false;
    }
}

async function generateArticleSummary(articleId, btn) {
    if (!btn) return;
    const originalText = btn.textContent;
    btn.disabled = true;
    btn.textContent = '生成中...';
    try {
        const resp = await apiFetch(`/api/v1/biz/articles/${articleId}/summary`, { method: 'POST' });
        const data = await resp.json();
        if (!resp.ok) {
            toast(data.error || '摘要生成失败', 'error');
            btn.textContent = originalText;
            btn.disabled = false;
            return;
        }
        btn.textContent = '✓ 已生成';
        btn.classList.add('generated');
        toast('摘要已生成', 'success');
    } catch (e) {
        toast('摘要生成请求失败: ' + e.message, 'error');
        btn.textContent = originalText;
        btn.disabled = false;
    }
}

function watchLoadMore() {
    const el = document.getElementById('loadMore');
    if (el) { loadMoreObserver.unobserve(el); loadMoreObserver.observe(el); }
}

function setSyncing(btn, data) {
    if (!btn) return;
    const total = data && data.accountCount ? data.accountCount : 0;
    const done = data && data.lastSync && data.lastSync.accounts ? data.lastSync.accounts.length : 0;
    btn.innerHTML = done > 0 && total > 0 ? `同步中 ${done}/${total}` : `同步中${SYNC_DOTS}`;
}

async function pollSyncStatus() {
    const btn = document.getElementById('syncBtn');
    const origText = (btn && (btn.dataset.originalText || btn.textContent)) || '同步';
    if (btn) {
        btn.dataset.originalText = origText;
        btn.disabled = true;
    }

    const check = async () => {
        try {
            const resp = await apiFetch('/api/v1/biz/status');
            const data = await resp.json();
            if (data.syncing) {
                if (btn) setSyncing(btn, data);
                setTimeout(check, 1000);
            } else {
                if (btn) {
                    btn.disabled = false;
                    btn.textContent = origText;
                }
                let newCount = 0;
                if (data.lastSync && data.lastSync.accounts) {
                    newCount = data.lastSync.accounts.reduce((sum, a) => sum + (a.newCount || 0), 0);
                }
                if (newCount > 0) {
                    toast(`同步完成：新增 ${newCount} 篇新文章`);
                } else {
                    toast(`同步完成：暂无新文章（共 ${data.articleCount || 0} 篇）`);
                }
                if (typeof window.onSyncFinished === 'function') {
                    window.onSyncFinished(data);
                }
            }
        } catch (err) {
            if (btn) {
                btn.disabled = false;
                btn.textContent = origText;
            }
            toast('获取同步状态失败', 'error');
        }
    };
    setTimeout(check, 600);
}

async function triggerSync() {
    const btn = document.getElementById('syncBtn');
    if (btn) {
        if (!btn.dataset.originalText) btn.dataset.originalText = btn.textContent;
        btn.disabled = true;
        btn.innerHTML = `同步中${SYNC_DOTS}`;
    }
    toast('开始同步微信数据库…');
    try {
        const resp = await apiFetch('/api/v1/biz/sync', { method: 'POST' });
        const data = await resp.json();
        if (data.error) {
            toast(data.error, 'error');
            if (btn) {
                btn.disabled = false;
                btn.textContent = btn.dataset.originalText || '同步';
            }
        } else {
            pollSyncStatus();
        }
    } catch (err) {
        toast('同步请求失败: ' + err.message, 'error');
        if (btn) {
            btn.disabled = false;
            btn.textContent = btn.dataset.originalText || '同步';
        }
    }
}

/* ---------- 主题切换：白天 / 夜晚 / 跟随系统 ---------- */
/*
 * 三态而不是两态：只做「白天 ⇄ 夜晚」的话，第一次点下去就再也没有
 * 「跟随系统」这个选项了 —— 而它恰恰是默认行为，用户一旦点过就回不去。
 * 所以顺序是 跟随系统 → 白天 → 夜晚 → 跟随系统。
 *
 * 存的是偏好（auto/light/dark），不是最终底色。两者必须分开：
 * 直接存底色的话，系统主题变了、或者用户想重新跟随系统时，就无从判断了。
 *
 * 首帧防闪烁不在这里 —— 那段必须内联在 <head> 且早于样式表，
 * 见 templates/layout.html 的 doc-head。
 */

const THEME_STORAGE_KEY = 'bizhub-theme';
const THEME_ORDER = ['auto', 'light', 'dark'];

const THEME_ICONS = {
    auto: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M12 3a9 9 0 0 0 0 18z" fill="currentColor" stroke="none"/></svg>',
    light: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>',
    dark: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5z"/></svg>'
};

const THEME_LABELS = { auto: '跟随系统', light: '白天', dark: '夜晚' };

// 读偏好。隐私模式下 localStorage 会抛，拿不到就当「跟随系统」。
function readThemePref() {
    try {
        const v = localStorage.getItem(THEME_STORAGE_KEY);
        return THEME_LABELS[v] ? v : 'auto';
    } catch (e) {
        return 'auto';
    }
}

function saveThemePref(pref) {
    try { localStorage.setItem(THEME_STORAGE_KEY, pref); } catch (e) { /* 存不了就只在本页生效 */ }
}

// 偏好 → 实际底色。只有 auto 才需要问系统。
function resolveTheme(pref) {
    if (pref === 'light' || pref === 'dark') return pref;
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

// 打底色 + 同步按钮外观。图标和文字说的是「当前偏好」，
// 不是「当前底色」—— 跟随系统时底色是黑的，但用户的选择确实是「跟随系统」。
function applyTheme(pref) {
    const resolved = resolveTheme(pref);
    document.documentElement.dataset.theme = resolved;

    const btn = document.getElementById('themeToggle');
    if (!btn) return;

    btn.querySelector('.theme-icon').innerHTML = THEME_ICONS[pref] || THEME_ICONS.auto;
    btn.querySelector('.theme-label').textContent = THEME_LABELS[pref] || THEME_LABELS.auto;

    const hint = '主题：' + (THEME_LABELS[pref] || THEME_LABELS.auto) + '（点击切换）';
    btn.title = hint;
    btn.setAttribute('aria-label', hint);
}

// 切换瞬间才挂过渡类。常驻的话首屏加载也会走一遍颜色动画，
// 看着像页面没渲染完；reduced-motion 下直接不做。
function withThemeTransition(fn) {
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) { fn(); return; }
    const root = document.documentElement;
    root.classList.add('theme-transition');
    fn();
    window.setTimeout(function () { root.classList.remove('theme-transition'); }, 220);
}

function cycleTheme() {
    const cur = readThemePref();
    const next = THEME_ORDER[(THEME_ORDER.indexOf(cur) + 1) % THEME_ORDER.length];
    saveThemePref(next);
    withThemeTransition(function () { applyTheme(next); });
}

function initThemeToggle() {
    const btn = document.getElementById('themeToggle');
    if (!btn) return;

    applyTheme(readThemePref());
    btn.addEventListener('click', cycleTheme);

    // 系统主题变了只在「跟随系统」下实时响应。
    // 用户显式选了白天/夜晚还跟着系统改，等于把他的选择又抹掉了。
    const mq = window.matchMedia('(prefers-color-scheme: dark)');
    const onChange = function () {
        if (readThemePref() === 'auto') withThemeTransition(function () { applyTheme('auto'); });
    };
    if (mq.addEventListener) mq.addEventListener('change', onChange);
    else if (mq.addListener) mq.addListener(onChange); // Safari 13 及以下
}

if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initThemeToggle);
} else {
    initThemeToggle();
}
