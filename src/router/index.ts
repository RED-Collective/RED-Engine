import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

import DefaultLayout from '../layouts/DefaultLayout.vue'
import AdminLayout from '../layouts/AdminLayout.vue'
import HomePage from '../pages/HomePage.vue'
import NodesPage from '../pages/NodesPage.vue'
import TagsPage from '../pages/TagsPage.vue'
import SectionOrArticlePage from '../pages/SectionOrArticlePage.vue'

import AdminDashboardPage from '../pages/admin/AdminDashboardPage.vue'
import AdminPeersPage from '../pages/admin/AdminPeersPage.vue'
import AdminContributorsPage from '../pages/admin/AdminContributorsPage.vue'
import AdminSyncPage from '../pages/admin/AdminSyncPage.vue'

const routes: RouteRecordRaw[] = [
  {
    path: '/-/admin',
    component: AdminLayout,
    children: [
      { path: '', redirect: '/-/admin/dashboard' },
      { path: 'dashboard', name: 'admin-dashboard', component: AdminDashboardPage },
      { path: 'peers', name: 'admin-peers', component: AdminPeersPage },
      { path: 'contributors', name: 'admin-contributors', component: AdminContributorsPage },
      { path: 'sync', name: 'admin-sync', component: AdminSyncPage },
    ],
  },
  {
    path: '/',
    component: DefaultLayout,
    children: [
      { path: '', name: 'home', component: HomePage },
      { path: '-/nodes', name: 'nodes', component: NodesPage },
      { path: 'tags', name: 'tags', component: TagsPage },
      { path: 'tags/:tag', name: 'tag', component: TagsPage },
      // Catch-all for every section/article path. Must come last.
      {
        path: ':pathMatch(.*)*',
        name: 'section-or-article',
        component: SectionOrArticlePage,
      },
    ],
  },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
  scrollBehavior(to, _from, saved) {
    if (saved) return saved
    if (to.hash) return { el: to.hash, behavior: 'smooth' }
    return { top: 0 }
  },
})

export default router
