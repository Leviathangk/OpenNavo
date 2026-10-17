<script setup lang="ts">
import { computed } from 'vue';
import { RouterLink, useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { formatBytes } from '@opennavo/shared';
import { OnSidebar } from '@opennavo/ui';
import type { OnSidebarGroup, OnSidebarItem } from '@opennavo/ui';
import { useBrewfileStore } from '@/stores/brewfile';
import { useAppLocale } from '@/composables/useAppLocale';
import { refreshPage } from '@/composables/usePageRefresh';
import { useCleanupStore } from '@/stores/cleanup';
import { useSecurityStore } from '@/stores/security';
import { useLibraryStore, useLocalAppsStore, useUpdatesStore } from '@/stores';

// Use shared @opennavo/ui OnSidebar (ADR-017); provide desktop groups and local counts here.
const { t } = useI18n();
const route = useRoute();
const brewfile = useBrewfileStore();
const library = useLibraryStore();
const localApps = useLocalAppsStore();
// Installed counts every app on this Mac (uninstall decision 3); Homebrew's count covers the first read.
const installedCount = computed(() => (localApps.loaded ? localApps.items.length : library.items.length));
const updates = useUpdatesStore();
const cleanup = useCleanupStore();
const { appLocale } = useAppLocale();
// Clean Up Mac (cleanup decision 10): reclaimable size from the last scan; background items of deleted apps that still
// load show as an attention dot instead.
const cleanupItem = computed<Partial<OnSidebarItem>>(() => {
  const summary = cleanup.summary;
  if (summary?.scannedAt == null) return {};
  if (summary.activeBackgroundItems)
    return {
      dot: true,
      badgeLabel: t(
        'nav.cleanupBadge',
        { count: summary.activeBackgroundItems },
        { plural: summary.activeBackgroundItems }
      )
    };
  return summary.reclaimableBytes ? { meta: formatBytes(summary.reclaimableBytes, { locale: appLocale.value }) } : {};
});

// Security (store/security plan §6): a dot when threats or new startup items wait for review, like Clean Up Mac.
const security = useSecurityStore();
const securityItem = computed<Partial<OnSidebarItem>>(() =>
  security.attention
    ? {
        dot: true,
        badgeLabel: t('nav.securityBadge', { count: security.attention }, { plural: security.attention })
      }
    : {}
);

const item = (name: string, icon: string, label: string, extra: Partial<OnSidebarItem> = {}): OnSidebarItem => ({
  key: name,
  label,
  icon,
  href: `/${name}`,
  active: route.meta.nav === name,
  ...extra
});

const groups = computed<OnSidebarGroup[]>(() => [
  {
    key: 'browse',
    label: t('nav.browse'),
    items: [
      item('discover', 'lucide:compass', t('nav.discover')),
      item('categories', 'lucide:layout-grid', t('nav.categories')),
      item('rankings', 'lucide:chart-no-axes-column', t('nav.rankings')),
      item('recent', 'lucide:clock', t('nav.recent')),
      item('articles', 'lucide:newspaper', t('articles.title'))
    ]
  },
  {
    key: 'lists',
    label: t('nav.lists'),
    items: [
      item('collections', 'lucide:library', t('collections.title')),
      item('brewfile', 'lucide:file-text', t('brewfile.title'), {
        count: brewfile.items.length || undefined
      })
    ]
  },
  {
    key: 'mine',
    label: t('nav.mine'),
    items: [
      item('installed', 'lucide:package', t('nav.installed'), {
        count: installedCount.value || undefined
      }),
      item('updates', 'lucide:circle-arrow-down', t('nav.updates'), {
        badge: updates.actionable.length || undefined,
        badgeLabel: t('nav.updatesBadge', { count: updates.actionable.length }, { plural: updates.actionable.length })
      }),
      item('cleanup', 'lucide:eraser', t('nav.cleanup'), cleanupItem.value),
      item('monitor', 'lucide:activity', t('nav.monitor')),
      item('security', 'lucide:shield-check', t('nav.security'), securityItem.value)
    ]
  }
]);

const footer = computed(() => [item('settings', 'lucide:sliders-horizontal', t('nav.settings'))]);
const emit = defineEmits<{ reselect: [] }>();
function reselect(event: MouseEvent) {
  if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
  const link = event.target instanceof Element ? event.target.closest('a') : null;
  if (route.path === '/installed' && link?.getAttribute('href')?.endsWith('/installed')) {
    void localApps.enterInstalled().catch(() => undefined);
  }
  if (route.path === '/discover' && link?.getAttribute('href')?.endsWith('/discover')) {
    emit('reselect');
    void refreshPage();
  }
}
</script>

<template>
  <OnSidebar
    :groups="groups"
    :footer-items="footer"
    :label="t('app.name')"
    drag-region
    :link-as="RouterLink"
    @click="reselect"
  />
</template>
