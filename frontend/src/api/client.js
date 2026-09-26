// Centralized API Client for Chatlog Bizhub

export async function request(path, options = {}) {
  const url = path.startsWith('/api') ? path : `/api/v1/biz${path}`;
  const res = await fetch(url, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      Accept: 'application/json',
      ...options.headers,
    },
  });

  const contentType = res.headers.get('content-type') || '';
  let data = null;
  if (contentType.includes('application/json')) {
    data = await res.json();
  } else {
    data = await res.text();
  }

  if (!res.ok) {
    const errorMsg = data && data.error ? data.error : (typeof data === 'string' ? data : `HTTP ${res.status}`);
    throw new Error(errorMsg);
  }

  return data;
}

function buildQuery(params = {}) {
  const clean = {};
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== '') {
      clean[k] = v;
    }
  }
  const qs = new URLSearchParams(clean).toString();
  return qs ? `?${qs}` : '';
}

export const api = {
  // Sync
  triggerSync: (ghid = '') => request('/sync', { method: 'POST', body: JSON.stringify({ ghid }) }),
  getSyncStatus: () => request('/status'),

  // Accounts
  getAccounts: (params = {}) => request('/accounts' + buildQuery(params)),
  getAdminAccounts: (params = {}) => request('/admin/accounts' + buildQuery(params)),
  setAccountWatch: (ghids, watched) => 
    request('/admin/accounts/watch', {
      method: 'POST',
      body: JSON.stringify({ ghIDs: ghids, watched }),
    }),
  setAccountVisibility: (ghids, hidden) => 
    request('/admin/accounts/visibility', {
      method: 'POST',
      body: JSON.stringify({ ghIDs: ghids, hidden }),
    }),
  setAccountTags: (ghids, tagIds, mode = 'replace') => 
    request('/admin/accounts/tags', {
      method: 'POST',
      body: JSON.stringify({ ghIDs: ghids, tagIDs: tagIds, mode }),
    }),

  // Tags
  getTags: () => request('/tags'),
  createTag: (name, color) => 
    request('/tags', {
      method: 'POST',
      body: JSON.stringify({ name, color }),
    }),
  updateTag: (id, name, color) => 
    request(`/tags/${id}`, {
      method: 'PATCH',
      body: JSON.stringify({ name, color }),
    }),
  deleteTag: (id) => 
    request(`/tags/${id}`, { method: 'DELETE' }),

  // Pipeline
  getPipelineStatus: () => request('/admin/pipeline/status'),
  triggerPipeline: () => request('/admin/pipeline/trigger', { method: 'POST' }),
  retryPipelineItem: (articleId) => request(`/admin/pipeline/retry/${articleId}`, { method: 'POST' }),

  // Export / Archive
  getExportStatus: (days = 30) => request(`/admin/export/status?days=${days}`),
  getExportJob: (jobId, withItems = false) => request(`/admin/export/jobs/${jobId}${withItems ? '?items=1' : ''}`),
  listExportJobs: (limit = 10) => request(`/admin/export/jobs?limit=${limit}`),
  startExportJob: (days = 30, limit = 100, ghids = []) => 
    request('/admin/export/jobs', {
      method: 'POST',
      body: JSON.stringify({ days, limit, ghIDs: ghids }),
    }),
  retryExportJob: (jobId = 'latest') => 
    request(`/admin/export/jobs/${jobId}/retry`, { method: 'POST' }),
  cancelExportJob: (jobId = 'active') => 
    request(`/admin/export/jobs/${jobId}/cancel`, { method: 'POST' }),

  // Push
  getPushStatus: (days = 30) => request(`/admin/push/status?days=${days}`),
  getPushJob: (jobId, withItems = false) => request(`/admin/push/jobs/${jobId}${withItems ? '?items=true' : ''}`),
  listPushJobs: (limit = 10) => request(`/admin/push/jobs?limit=${limit}`),
  startPushJob: (days = 30, limit = 100, ghids = []) => 
    request('/admin/push/jobs', {
      method: 'POST',
      body: JSON.stringify({ days, limit, ghIDs: ghids }),
    }),
  retryPushJob: (jobId = 'latest') => 
    request(`/admin/push/jobs/${jobId}/retry`, { method: 'POST' }),
  cancelPushJob: (jobId = 'active') => 
    request(`/admin/push/jobs/${jobId}/cancel`, { method: 'POST' }),

  // Summaries
  batchSummarize: (limit = 20) => 
    request('/admin/batch/summary', {
      method: 'POST',
      body: JSON.stringify({ limit }),
    }),

  // Articles & Reading
  getArticles: (params = {}) => request('/articles' + buildQuery(params)),
  searchArticles: (keyword, limit = 50, offset = 0) => 
    request('/search' + buildQuery({ keyword, limit, offset })),
  getBookmarks: (limit = 50, offset = 0) => 
    request('/bookmarks' + buildQuery({ limit, offset })),
  getFeed: (days = 7, limit = 50, offset = 0) => 
    request('/feed' + buildQuery({ days, limit, offset })),
  getArticle: (id) => request(`/articles/${id}`),
  exportArticleMD: (id) => request(`/articles/${id}/export`, { method: 'POST' }),
  pushArticleIMA: (id) => request(`/articles/${id}/push`, { method: 'POST' }),
  summarizeArticle: (id) => request(`/articles/${id}/summary`, { method: 'POST' }),
  toggleBookmark: (id, bookmarked) => 
    request(`/articles/${id}/bookmark`, {
      method: 'POST',
      body: JSON.stringify({ bookmarked }),
    }),
  markArticleRead: (id, isRead) =>
    request(`/articles/${id}/read`, {
      method: 'POST',
      body: JSON.stringify({ isRead }),
    }),

  // Reports & Digest
  getReports: (params = {}) => request('/reports' + buildQuery(params)),
  getReport: (id) => request(`/reports/${id}`),
  createReport: (data) => request('/reports', { method: 'POST', body: JSON.stringify(data) }),
  getFeedDigest: (window = 7, fresh = false) => request(`/feed/digest?window=${window}&fresh=${fresh}`),
  getDailyDigest: (fresh = false) => request(`/daily-digest?fresh=${fresh}`),
};
