import { createRouter, createWebHistory } from 'vue-router';
import FeedView from '@/views/FeedView.vue';
import ReportsView from '@/views/ReportsView.vue';
import AdminView from '@/views/AdminView.vue';
import ArticleDetailView from '@/views/ArticleDetailView.vue';

const routes = [
  { path: '/', redirect: '/biz' },
  { path: '/biz', name: 'Feed', component: FeedView },
  { path: '/biz/digest', name: 'Digest', component: FeedView },
  { path: '/biz/article/:id', name: 'ArticleDetail', component: ArticleDetailView },
  { path: '/biz/reports', name: 'Reports', component: ReportsView },
  { path: '/biz/reports/:id', name: 'ReportDetail', component: ReportsView },
  { path: '/biz/admin', name: 'Admin', component: AdminView },
];

const router = createRouter({
  history: createWebHistory(),
  routes,
});

export default router;
