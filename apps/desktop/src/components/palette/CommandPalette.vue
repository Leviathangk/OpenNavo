<script setup lang="ts">
import { computed, onScopeDispose, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { formatVersion, formatVersionChange, installCommand } from '@opennavo/shared';
import { OnCommandPalette } from '@opennavo/ui';
import type { OnCommandGroup, OnCommandItem } from '@opennavo/ui';
import type { Kind } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';
import type { LocalSearchHit } from '@/ipc/client';
import { useAppLocale } from '@/composables/useAppLocale';
import { searchApps } from '@/composables/searchApps';
import { useMasSearch } from '@/composables/useMasSearch';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useCatalogStore, useLibraryStore, useSettingsStore, useUpdatesStore } from '@/stores';
import { useLinksStore } from '@/stores/links';
import { useMasStore } from '@/stores/mas';
import { useNamesStore } from '@/stores/names';

// Command-K palette (mockup 07, M3-11): local search (06 §7.1), first-result actions, and navigation.
// Typing lists the actions first, with "view all results" on top, so Enter goes to the full results; the apps follow
// (Command-Enter installs/updates the one chosen, Command-C copies its brew command). Up to three App Store results
// come after the catalog (store/security plan §5); they arrive after the search debounce and never move the rows above.
const open = defineModel<boolean>('open', { required: true });

const { t } = useI18n();
const { pick, appLocale } = useAppLocale();
const router = useRouter();
const library = useLibraryStore();
const updates = useUpdatesStore();
const names = useNamesStore();
const catalog = useCatalogStore();
const toasts = useToasts();
const { stateOf, act } = usePackageState();

const query = ref('');
const hits = ref<LocalSearchHit[]>([]);
const loading = ref(false);
let sequence = 0;
onScopeDispose(() => {
  sequence += 1;
});
const mas = useMasStore();
const links = useLinksStore();
const settings = useSettingsStore();
const masSearch = useMasSearch(
  query,
  computed(() => open.value && (settings.value?.appStoreSearch ?? true)),
  3
);

watch(open, value => {
  if (value) {
    query.value = '';
    hits.value = [];
    // Installed App Store apps label their rows; a failure only drops that label.
    void mas.loadInstalled().catch(() => undefined);
  }
});

watch([query, appLocale, () => catalog.revision], async ([value]) => {
  const q = value.trim();
  sequence += 1;
  const current = sequence;
  if (!q) {
    hits.value = [];
    return;
  }
  loading.value = true;
  try {
    const page = await searchApps(q.slice(0, 64), 8);
    if (current !== sequence) return;
    names.remember(page.items.map(hit => hit.item));
    links.remember(page.items.map(hit => hit.item));
    hits.value = page.items;
  } finally {
    if (current === sequence) loading.value = false;
  }
});

type Payload =
  | { type: 'package'; kind: Kind; token: string; tap: string | null }
  | { type: 'mas'; adamId: number }
  | { type: 'update'; kind: Kind; token: string }
  | { type: 'reveal'; kind: Kind; token: string }
  | { type: 'search'; q: string }
  | { type: 'route'; to: string };

const payloads = new Map<string, Payload>();

function packageItem(hit: LocalSearchHit): OnCommandItem {
  const { item } = hit;
  const installed = library.find(item.kind, item.token);
  const outdated = updates.find(item.kind, item.token);
  const description = installed
    ? t('palette.installedVersion', { version: formatVersion(installed.installedVersion) })
    : pick(item.summary, '', item.sourceLocale);
  const current = stateOf(item.kind, item.token, item.disabled);
  const key = `package:${item.kind}/${item.token}`;
  payloads.set(key, { type: 'package', kind: item.kind, token: item.token, tap: item.tap ?? null });
  return {
    key,
    label: pick(item.displayName, item.name, item.sourceLocale),
    description,
    app: {
      kind: item.kind,
      token: item.token,
      name: pick(item.displayName, item.name, item.sourceLocale),
      src: item.iconUrl,
      accent: item.accentColor
    },
    badge: outdated && !outdated.pinned ? t('palette.updatable') : undefined,
    badgeTone: 'accent',
    // Without Homebrew, Command-Enter opens Welcome; use the button's label for the notice.
    hint:
      current.state === 'get'
        ? `⌘↵ ${current.label ?? t('palette.install')}`
        : current.state === 'download'
          ? `⌘↵ ${t('package.link.download')}`
          : `↵ ${t('palette.open')}`
  };
}

const groups = computed<OnCommandGroup[]>(() => {
  payloads.clear();
  const q = query.value.trim();
  if (!q) {
    const pages = [
      'discover',
      'categories',
      'rankings',
      'recent',
      'installed',
      'updates',
      'security',
      'settings'
    ] as const;
    return [
      {
        key: 'navigation',
        label: t('palette.navigation'),
        items: pages.map(page => {
          const key = `route:${page}`;
          payloads.set(key, { type: 'route', to: `/${page}` });
          return { key, label: t('palette.goTo', { page: t(`nav.${page}`) }), icon: 'arrow-right' };
        })
      }
    ];
  }
  const apps = hits.value.map(packageItem);
  // The first row of the first group is what Enter does: all the results for what was typed. It does not wait for hits.
  const searchKey = `search:${q}`;
  payloads.set(searchKey, { type: 'search', q });
  const actions: OnCommandItem[] = [{ key: searchKey, label: t('palette.viewAll', { q }), icon: 'search' }];
  const first = hits.value[0]?.item;
  if (first) {
    const name = pick(first.displayName, first.name, first.sourceLocale);
    const outdated = updates.find(first.kind, first.token);
    const installed = library.find(first.kind, first.token);
    if (outdated && !outdated.pinned) {
      const key = `update:${first.kind}/${first.token}`;
      payloads.set(key, { type: 'update', kind: first.kind, token: first.token });
      actions.push({
        key,
        label: t('palette.update', { name }),
        description: formatVersionChange(outdated.installedVersion, outdated.currentVersion),
        icon: 'circle-arrow-down',
        iconTone: 'accent'
      });
    }
    const app = installed?.appPaths[0]?.split('/').pop();
    if (first.kind === 'cask' && app) {
      const key = `reveal:${first.kind}/${first.token}`;
      payloads.set(key, { type: 'reveal', kind: first.kind, token: first.token });
      actions.push({ key, label: t('palette.reveal', { name: app }), icon: 'folder' });
    }
  }
  const store = (masSearch.results.value ?? []).slice(0, 3).map(app => {
    const key = `mas:${app.adamId}`;
    payloads.set(key, { type: 'mas', adamId: app.adamId });
    const installed = mas.installedFor(app.adamId);
    return {
      key,
      label: app.name,
      description: installed
        ? t('palette.installedVersion', { version: installed.version })
        : [app.seller, app.price > 0 ? app.formattedPrice : t('appStore.free')].join(' · '),
      app: { kind: 'mas' as const, token: String(app.adamId), name: app.name, src: app.iconUrl },
      badge: t('appStore.badge'),
      badgeTone: 'info' as const,
      hint: `↵ ${t('palette.open')}`
    };
  });
  return [
    { key: 'actions', label: t('palette.actions'), items: actions },
    { key: 'apps', label: t('palette.apps'), items: apps },
    ...(store.length ? [{ key: 'appstore', label: t('appStore.title'), items: store }] : [])
  ];
});

async function select(item: OnCommandItem, options: { meta: boolean }) {
  const payload = payloads.get(item.key);
  if (!payload) return;
  open.value = false;
  switch (payload.type) {
    case 'package': {
      const state = stateOf(payload.kind, payload.token).state;
      if (options.meta && (state === 'get' || state === 'update' || state === 'download'))
        await act(payload.kind, payload.token, state);
      else await router.push(`/package/${payload.kind}/${payload.token}`);
      break;
    }
    case 'mas':
      await router.push({ name: 'appstore', params: { id: String(payload.adamId) } });
      break;
    case 'update':
      await act(payload.kind, payload.token, 'update');
      break;
    case 'reveal':
      await unwrap(commands.appReveal({ kind: payload.kind, token: payload.token })).catch(() => undefined);
      break;
    case 'search':
      await router.push({ name: 'search', query: { q: payload.q } });
      break;
    case 'route':
      await router.push(payload.to);
      break;
  }
}

async function copy(item: OnCommandItem) {
  const payload = payloads.get(item.key);
  // Link apps have no brew command (store/security plan §4); their row copies nothing.
  if (!payload || payload.type !== 'package' || payload.kind === 'link') return;
  const command = installCommand(payload.kind, payload.token, payload.tap);
  await navigator.clipboard.writeText(command);
  toasts.push({ tone: 'success', title: t('palette.copied', { command }) });
}
</script>

<template>
  <OnCommandPalette
    v-model:open="open"
    v-model:query="query"
    :groups="groups"
    :loading="loading"
    :placeholder="t('palette.placeholder')"
    @select="select"
    @copy="copy"
  />
</template>
