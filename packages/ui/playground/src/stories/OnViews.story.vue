<script setup lang="ts">
import { computed, ref } from 'vue';
import type { PackageSummary } from '@opennavo/api';
import {
  OnAppCard,
  OnAppShell,
  OnCategoriesView,
  OnCollectionView,
  OnDiscoverView,
  OnGetButton,
  OnPackageHeader,
  OnPackageOverview,
  OnPackageStats,
  OnRailPanel,
  OnRailSection,
  OnRankingsView,
  OnSearchResultsView,
  OnSegmented,
  OnSidebar,
  OnToolbar
} from '../../../src';
import type { OnSidebarGroup } from '../../../src';
import { samplePackages } from '../data/packages';
import StoryRow from '../StoryRow.vue';
import StorySection from '../StorySection.vue';

// Shared shells/views (ADR-017) in desktop container-scroll/local-group/drag-region mode and web document-scroll
// with download card/list groups. ?story=OnViews&env=web&view=rankings opens a specific combination for screenshot comparison.
type Env = 'desktop' | 'web';
type View = 'discover' | 'categories' | 'rankings' | 'search' | 'collection' | 'detail';

const params = new URLSearchParams(location.search);
const env = ref<Env>(params.get('env') === 'web' ? 'web' : 'desktop');
const views: { value: View; label: string }[] = [
  { value: 'discover', label: 'Discover' },
  { value: 'categories', label: 'Categories' },
  { value: 'rankings', label: 'Rankings' },
  { value: 'search', label: 'Search' },
  { value: 'collection', label: 'Collections' },
  { value: 'detail', label: 'Details' }
];
const view = ref<View>(views.find(item => item.value === params.get('view'))?.value ?? 'discover');

const href = (pkg: PackageSummary) => (env.value === 'desktop' ? `#/package/cask/${pkg.token}` : `/apps/${pkg.token}`);

const groups = computed<OnSidebarGroup[]>(() => [
  {
    key: 'browse',
    label: 'Browse',
    items: [
      { key: 'discover', label: 'Discover', icon: 'lucide:compass', href: '#', active: view.value === 'discover' },
      {
        key: 'categories',
        label: 'Categories',
        icon: 'lucide:layout-grid',
        href: '#',
        active: view.value === 'categories'
      },
      {
        key: 'rankings',
        label: 'Rankings',
        icon: 'lucide:chart-no-axes-column',
        href: '#',
        active: view.value === 'rankings'
      }
    ]
  },
  env.value === 'desktop'
    ? {
        key: 'mine',
        label: 'My library',
        items: [
          { key: 'installed', label: 'Installed', icon: 'lucide:package', href: '#', count: 8 },
          { key: 'updates', label: 'Updates', icon: 'lucide:circle-arrow-down', href: '#', badge: 4 },
          { key: 'history', label: 'Update history', icon: 'lucide:rotate-ccw-clock', href: '#' }
        ]
      }
    : {
        key: 'lists',
        label: 'Brewfiles',
        items: [
          {
            key: 'collections',
            label: 'Collections',
            icon: 'lucide:library',
            href: '#',
            active: view.value === 'collection'
          },
          { key: 'brewfile', label: 'My Brewfiles', icon: 'lucide:file-text', href: '#', count: 3 }
        ]
      }
]);

const labels = {
  categories: 'Browse by category',
  popularApps: 'Popular apps',
  popularAppsHint: 'Homebrew installs in the last 30 days',
  viewRankings: 'View rankings',
  recentlyUpdated: 'Recently updated',
  recentlyUpdatedHint: 'New releases',
  collections: 'Collections',
  viewAll: 'View all'
};

const chips = [
  { value: 'all', label: 'All', href: '#' },
  { value: 'ai', label: 'AI tools', href: '#' },
  { value: 'dev', label: 'Development', href: '#' },
  { value: 'productivity', label: 'Productivity', href: '#' }
];

const collections = computed(() => [
  {
    key: 'new-mac',
    title: 'New Mac essentials',
    subtitle: 'Everyday essentials for browsing, media, and window management',
    count: samplePackages.length,
    icons: samplePackages.slice(0, 5).map(pkg => ({ kind: pkg.kind, token: pkg.token, name: pkg.displayName })),
    href: '#'
  }
]);

const entries = computed(() =>
  samplePackages.map((pkg, index) => ({
    rank: index + 1,
    change: index === 1 ? 2 : null,
    pkg,
    value: `${(pkg.installs30d / 1000).toFixed(1)}K`,
    href: href(pkg)
  }))
);

const period = ref<'30d' | '90d' | '365d'>('30d');
const detail = samplePackages[1] as PackageSummary;
</script>

<template>
  <StorySection title="Shared shell and page views" spec="ADR-017 · 05 §11.2 · M10-04">
    <StoryRow label="Environment / page">
      <OnSegmented
        v-model="env"
        size="sm"
        :options="[
          { value: 'desktop', label: 'Desktop' },
          { value: 'web', label: 'Web' }
        ]"
      />
      <OnSegmented v-model="view" size="sm" :options="views" />
    </StoryRow>
    <div class="relative h-820px overflow-hidden rounded-big border border-solid border-line-subtle">
      <OnAppShell
        :scroll="env === 'desktop' ? 'container' : 'document'"
        :has-rail="view === 'discover'"
        rail-docked
        class="!h-820px !min-h-0"
      >
        <template #sidebar>
          <OnSidebar
            :groups="groups"
            :footer-items="[
              { key: 'settings', label: env === 'desktop' ? 'Settings' : 'About', icon: 'lucide:info', href: '#' }
            ]"
            label="Main navigation"
            top-label="Mac desktop app"
            :drag-region="env === 'desktop'"
          >
            <template v-if="env === 'web'" #top>
              <div
                class="mx-2px rounded-default border border-solid border-line-subtle bg-component-search-bg px-9px py-8px"
              >
                <div class="text-12.5px font-600 text-ink-primary">OpenNavo for Mac</div>
                <div class="text-11px text-ink-tertiary">macOS 13+ · Free</div>
              </div>
            </template>
          </OnSidebar>
        </template>
        <template #toolbar>
          <OnToolbar search-label="Search apps" search-shortcut="⌘K" :drag-region="env === 'desktop'">
            <template v-if="env === 'web'" #end>
              <span class="text-13px text-ink-secondary">English</span>
            </template>
          </OnToolbar>
        </template>

        <OnDiscoverView
          v-if="view === 'discover'"
          :hero="{
            badge: 'Featured this week',
            title: 'Visual Studio Code',
            subtitle: 'Turn ideas into code.',
            description: 'Start developing with **Visual Studio Code**, from quick edits to project debugging.',
            icons: samplePackages.slice(1, 4).map(pkg => ({ kind: pkg.kind, token: pkg.token, name: pkg.displayName })),
            glowColor: '#2F8FEF'
          }"
          :chips="chips"
          :popular="samplePackages"
          :recent="samplePackages"
          :collections="collections"
          :labels="labels"
          rankings-href="#"
          recent-href="#"
          collections-href="#"
        >
          <template #card="{ pkg, stats }">
            <OnAppCard :pkg="pkg" state="get" :stats="stats" :href="href(pkg)" />
          </template>
        </OnDiscoverView>

        <OnCategoriesView
          v-else-if="view === 'categories'"
          title="Categories"
          subtitle="Browse Homebrew apps by purpose"
          :items="[
            {
              key: 'dev',
              name: 'Developer tools',
              icon: 'lucide:code-xml',
              count: '21 apps',
              children: ['Editors', 'Terminal'],
              href: '#'
            },
            { key: 'ai', name: 'AI tools', icon: 'lucide:sparkles', count: '12 apps', children: [], href: '#' },
            { key: 'browsers', name: 'Browsers', icon: 'lucide:globe', count: '9 apps', children: [], href: '#' }
          ]"
        />

        <OnRankingsView
          v-else-if="view === 'rankings'"
          v-model:period="period"
          title="Rankings"
          subtitle="Official Homebrew installation statistics, updated daily"
          :periods="[
            { value: '30d', label: '30 days' },
            { value: '90d', label: '90 days' },
            { value: '365d', label: 'One year' }
          ]"
          period-label="Statistics period"
          :entries="entries"
        >
          <template #action>
            <OnGetButton state="get" />
          </template>
        </OnRankingsView>

        <OnSearchResultsView
          v-else-if="view === 'search'"
          title="Search results for “code”"
          subtitle="6 results"
          :results="
            samplePackages.map(pkg => ({
              key: pkg.token,
              kind: pkg.kind,
              token: pkg.token,
              name: pkg.displayName,
              meta: env === 'desktop' ? pkg.token : pkg.version,
              description: pkg.summary,
              matched: pkg.token === 'visual-studio-code' ? 'Matched: VS Code' : null,
              href: href(pkg)
            }))
          "
        >
          <template #action>
            <OnGetButton state="get" />
          </template>
        </OnSearchResultsView>

        <OnCollectionView
          v-else-if="view === 'collection'"
          :breadcrumb="[{ label: 'Collections', href: '#' }, { label: 'New Mac essentials' }]"
          title="New Mac essentials"
          subtitle="Everyday essentials for browsing, media, and window management"
          :meta="env === 'web' ? '6 apps · Updated September 30' : undefined"
          :entries="
            samplePackages.map(pkg => ({
              key: pkg.token,
              kind: pkg.kind,
              token: pkg.token,
              name: pkg.displayName,
              description: pkg.summary,
              href: href(pkg)
            }))
          "
        >
          <template #action>
            <OnGetButton state="get" />
          </template>
        </OnCollectionView>

        <div v-else class="flex flex-col gap-14px pt-6px">
          <OnPackageHeader
            :kind="detail.kind"
            :token="detail.token"
            :name="detail.displayName"
            :subtitle="`Microsoft · ${detail.summary}`"
            :accent="detail.accentColor"
            :chips="[
              { key: 'kind', label: 'Cask' },
              { key: 'category', label: 'Developer tools' },
              { key: 'auto', label: 'Built-in updater', tone: 'info' },
              ...(env === 'desktop'
                ? [{ key: 'installed', label: 'Installed 1.139.1', tone: 'success' as const, dot: true }]
                : [])
            ]"
            meta="From homebrew/cask"
          >
            <template #actions>
              <OnGetButton :state="env === 'desktop' ? 'update' : 'get'" />
            </template>
          </OnPackageHeader>
          <OnPackageStats
            label="Data"
            :cells="[
              { key: 'd30', label: 'Installs in the last 30 days', value: '17,394', hint: 'Number 9 in apps' },
              { key: 'd365', label: 'Installs in the last year', value: '478K', hint: '365-day total' },
              { key: 'version', label: 'Latest version', value: '1.140.0', hint: '4 days ago · September 30' },
              { key: 'size', label: 'DownloadSize', value: '318', unit: 'MB', hint: 'arm64 · zip' },
              { key: 'cadence', label: 'Release cadence', value: 'Weekly', hint: '8 releases in the last 30 days' }
            ]"
          />
          <OnPackageOverview
            has-about
            command="brew install --cask visual-studio-code"
            :info="[
              { key: 'token', label: 'Token', value: 'visual-studio-code', mono: true },
              { key: 'auto', label: 'Automatic updates', value: 'Built in (auto_updates)' }
            ]"
            :related="
              samplePackages.slice(2, 5).map(pkg => ({
                key: pkg.token,
                kind: pkg.kind,
                token: pkg.token,
                name: pkg.displayName,
                description: pkg.summary,
                href: href(pkg)
              }))
            "
            related-title="Similar apps"
          >
            <template #about>
              <p class="m-0 text-14px leading-[1.75] text-ink-secondary">
                A free, open-source code editor from Microsoft with Git, a debugger, a terminal, and a rich extension
                ecosystem.
              </p>
            </template>
          </OnPackageOverview>
        </div>

        <template #rail>
          <OnRailPanel :responsive="env === 'web'">
            <OnRailSection
              :title="env === 'desktop' ? 'Overview' : 'Popular this week'"
              subtitle="Ranked by installs in the last 30 days"
            >
              <p class="m-0 text-12.5px text-ink-tertiary">
                {{
                  env === 'desktop'
                    ? 'Local overview 2×2 (installed / updates / disk / last checked)'
                    : 'Popular this week · Featured collections · Download desktop app'
                }}
              </p>
            </OnRailSection>
          </OnRailPanel>
        </template>
      </OnAppShell>
    </div>
  </StorySection>
</template>
