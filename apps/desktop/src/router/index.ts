import { createRouter, createWebHashHistory } from 'vue-router';
import type { RouteRecordRaw } from 'vue-router';

declare module 'vue-router' {
  interface RouteMeta {
    /** Highlighted sidebar navigation item; group detail/search subpages under their top-level destination. */
    nav?:
      | 'discover'
      | 'categories'
      | 'collections'
      | 'articles'
      | 'rankings'
      | 'recent'
      | 'brewfile'
      | 'installed'
      | 'updates'
      | 'cleanup'
      | 'monitor'
      | 'security'
      | 'firewall'
      | 'settings';
    /** Render without the app shell (Welcome, menu-bar popover). */
    bare?: boolean;
  }
}

// Page-specific right statistics rails (08 §9.1: Discover, Installed, Updates), supplied by named rail views.
const DiscoverRail = () => import('@/components/rails/DiscoverRail.vue');
const LibraryRail = () => import('@/components/rails/LibraryRail.vue');
const UpdatesRail = () => import('@/components/rails/UpdatesRail.vue');
const CleanupRail = () => import('@/components/rails/CleanupRail.vue');
const MonitorRail = () => import('@/components/rails/MonitorRail.vue');
const SecurityRail = () => import('@/components/rails/SecurityRail.vue');
const FirewallRail = () => import('@/components/rails/FirewallRail.vue');

export const routes: RouteRecordRaw[] = [
  { path: '/', redirect: '/discover' },
  {
    path: '/discover',
    name: 'discover',
    components: {
      default: () => import('@/pages/DiscoverPage.vue'),
      rail: DiscoverRail
    },
    meta: { nav: 'discover' }
  },
  {
    path: '/categories',
    name: 'categories',
    component: () => import('@/pages/CategoriesPage.vue'),
    meta: { nav: 'categories' }
  },
  {
    path: '/categories/:slug',
    name: 'category',
    component: () => import('@/pages/CategoryPage.vue'),
    props: true,
    meta: { nav: 'categories' }
  },
  {
    path: '/rankings',
    name: 'rankings',
    component: () => import('@/pages/RankingsPage.vue'),
    meta: { nav: 'rankings' }
  },
  {
    path: '/recent',
    name: 'recent',
    component: () => import('@/pages/RecentPage.vue'),
    meta: { nav: 'recent' }
  },
  {
    path: '/search',
    name: 'search',
    component: () => import('@/pages/SearchPage.vue'),
    meta: { nav: 'discover' }
  },
  {
    // Mac App Store app (store/security plan §5): numeric App Store ID; the data comes straight from Apple.
    path: '/appstore/:id(\\d+)',
    name: 'appstore',
    component: () => import('@/pages/AppStorePage.vue'),
    props: true,
    meta: { nav: 'discover' }
  },
  {
    // Casks and link apps (store/security plan §4); command-line tool URLs fall through to Discover (ADR-018).
    path: '/package/:kind(cask|link)/:token',
    component: () => import('@/pages/package/PackageLayout.vue'),
    props: true,
    meta: { nav: 'discover' },
    children: [
      {
        path: '',
        name: 'package',
        component: () => import('@/pages/package/PackageOverview.vue')
      },
      {
        path: 'versions',
        name: 'package-versions',
        component: () => import('@/pages/package/PackageVersions.vue')
      },
      {
        path: 'dependencies',
        name: 'package-dependencies',
        component: () => import('@/pages/package/PackageDependencies.vue')
      },
      {
        path: 'details',
        name: 'package-details',
        component: () => import('@/pages/package/PackageDetails.vue')
      }
    ]
  },
  {
    path: '/collections',
    name: 'collections',
    component: () => import('@/pages/CollectionsPage.vue'),
    meta: { nav: 'collections' }
  },
  {
    path: '/collections/:slug',
    name: 'collection',
    component: () => import('@/pages/CollectionPage.vue'),
    props: true,
    meta: { nav: 'collections' }
  },
  {
    path: '/articles',
    name: 'articles',
    component: () => import('@/pages/ArticlesPage.vue'),
    meta: { nav: 'articles' }
  },
  {
    path: '/articles/:slug',
    name: 'article',
    component: () => import('@/pages/ArticlePage.vue'),
    props: true,
    meta: { nav: 'articles' }
  },
  {
    path: '/brewfile',
    name: 'brewfile',
    component: () => import('@/pages/BrewfilePage.vue'),
    meta: { nav: 'brewfile' }
  },
  {
    path: '/installed',
    name: 'installed',
    components: {
      default: () => import('@/pages/InstalledPage.vue'),
      rail: LibraryRail
    },
    meta: { nav: 'installed' }
  },
  {
    // Uninstall preview links (F1, opennavo://uninstall/{token}?preview=1): the Installed page with that app's
    // info sheet open. Uninstalling still takes the sheet's button and the dialog's review and confirmation.
    path: '/uninstall/:token',
    name: 'uninstall-preview',
    components: {
      default: () => import('@/pages/InstalledPage.vue'),
      rail: LibraryRail
    },
    meta: { nav: 'installed' }
  },
  {
    path: '/updates',
    name: 'updates',
    components: {
      default: () => import('@/pages/UpdatesPage.vue'),
      rail: UpdatesRail
    },
    meta: { nav: 'updates' }
  },
  {
    path: '/cleanup',
    name: 'cleanup',
    components: {
      default: () => import('@/pages/CleanupPage.vue'),
      rail: CleanupRail
    },
    meta: { nav: 'cleanup' }
  },
  {
    path: '/monitor',
    name: 'monitor',
    components: {
      default: () => import('@/pages/MonitorPage.vue'),
      rail: MonitorRail
    },
    meta: { nav: 'monitor' }
  },
  {
    // Security (store/security plan §6): tabs live in the route so notifications can link to one.
    path: '/security/:tab(scan|startup|protection|quarantine)?',
    name: 'security',
    components: {
      default: () => import('@/pages/SecurityPage.vue'),
      rail: SecurityRail
    },
    meta: { nav: 'security' }
  },
  {
    // App firewall (plan M7 / S4): its own Tools entry below Scan for Viruses; tabs live in the route like Security.
    path: '/firewall/:tab(rules|activity|blocklists|settings)?',
    name: 'firewall',
    components: {
      default: () => import('@/pages/FirewallPage.vue'),
      rail: FirewallRail
    },
    meta: { nav: 'firewall' }
  },
  {
    path: '/history',
    name: 'history',
    redirect: '/updates'
  },
  {
    path: '/settings/:section(general|mirrors|homebrew|appstore|cleanup|monitor|security|privacy|about)?',
    name: 'settings',
    component: () => import('@/pages/SettingsPage.vue'),
    meta: { nav: 'settings' }
  },
  {
    path: '/welcome',
    name: 'welcome',
    component: () => import('@/pages/WelcomePage.vue'),
    meta: { bare: true }
  },
  // Permission guidance overlay (06 §12.7), displayed below System Settings.
  {
    path: '/permission-guide/:pane(app_management|full_disk_access)',
    name: 'permission-guide',
    component: () => import('@/pages/PermissionGuidePage.vue'),
    meta: { bare: true }
  },
  // Monitoring menu bar popover (monitor proposal §12): one window shared by every monitoring item.
  {
    path: '/tray-monitor',
    name: 'tray-monitor',
    component: () => import('@/pages/TrayMonitorPage.vue'),
    meta: { bare: true }
  },
  // A connection waiting for an answer (firewall plan §4.4): Rust's small always-on-top window.
  {
    path: '/firewall-alert',
    name: 'firewall-alert',
    component: () => import('@/pages/FirewallAlertPage.vue'),
    meta: { bare: true }
  },
  // Menu-bar popover (06 §4.2), without sidebar or toolbar.
  {
    path: '/tray',
    name: 'tray',
    component: () => import('@/pages/TrayPage.vue'),
    meta: { bare: true }
  },
  { path: '/:pathMatch(.*)*', redirect: '/discover' }
];

// No server routing on desktop; use hash history (06 §2).
// Pages scroll in AppShell's <main>, not the window; AppShell resets scroll on navigation.
export const router = createRouter({ history: createWebHashHistory(), routes });
