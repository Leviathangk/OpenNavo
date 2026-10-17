<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import type { PublicComponents } from '@opennavo/api';
import { OnCommandPalette, useUiMessages } from '@opennavo/ui';
import type { OnCommandGroup, OnCommandItem } from '@opennavo/ui';

type SuggestItem = PublicComponents['schemas']['SuggestItem'];

// Web Command-K (05 §7): Apps and tools / Navigation groups only; suggestions from suggest API.
const { t } = useI18n();
const api = useApi();
const localePath = useLocalePath();
const messages = useUiMessages();
const { copy } = useCopyCommand();
const { open } = useCommandPalette();

const query = ref('');
const suggestions = ref<SuggestItem[]>([]);
const loading = ref(false);
let timer: ReturnType<typeof setTimeout> | undefined;
let sequence = 0;

// Request after 150 ms of idle typing; discard stale earlier responses.
watch(query, value => {
  clearTimeout(timer);
  const q = value.trim();
  if (!q) {
    suggestions.value = [];
    loading.value = false;
    return;
  }
  loading.value = true;
  timer = setTimeout(async () => {
    const current = ++sequence;
    try {
      const items = await unwrap(api.GET('/search/suggest', { params: { query: { q: q.slice(0, 64), limit: 8 } } }));
      if (current === sequence) suggestions.value = items;
    } catch {
      if (current === sequence) suggestions.value = [];
    } finally {
      if (current === sequence) loading.value = false;
    }
  }, 150);
});

watch(open, value => {
  if (!value) query.value = '';
});

const PAGES = [
  { key: 'discover', path: '/discover', icon: 'compass' },
  { key: 'categories', path: '/categories', icon: 'layout-grid' },
  { key: 'rankings', path: '/rankings', icon: 'chart-no-axes-column' },
  { key: 'recent', path: '/recent', icon: 'clock' },
  { key: 'collections', path: '/collections', icon: 'boxes' },
  { key: 'download', path: '/download', icon: 'download' },
  { key: 'brewfile', path: '/brewfile', icon: 'file-text' }
] as const;

const groups = computed<OnCommandGroup[]>(() => {
  const q = query.value.trim();
  const lower = q.toLowerCase();
  const packages: OnCommandItem[] = suggestions.value.flatMap(item =>
    // Casks and link apps share /apps (store/security plan §4); command-line tools have no web page.
    item.type === 'package' && (item.kind === 'cask' || item.kind === 'link') && item.token
      ? [
          {
            key: `package:${item.kind}:${item.token}`,
            label: item.name,
            description: item.summary ?? undefined,
            app: { kind: item.kind, token: item.token, name: item.name, src: item.iconUrl }
          }
        ]
      : []
  );
  const jumps: OnCommandItem[] = [
    ...(q ? [{ key: `search:${q}`, label: t('palette.searchAll', { q }), icon: 'search' }] : []),
    ...suggestions.value
      .filter(item => item.type === 'category' && item.slug)
      .map(item => ({
        key: `category:${item.slug}`,
        label: t('palette.category', { name: item.name }),
        icon: 'layout-grid'
      })),
    // Match page names or English keys; rank can find Rankings even in the Chinese UI.
    ...PAGES.filter(
      page => !lower || page.key.includes(lower) || t(`palette.pages.${page.key}`).toLowerCase().includes(lower)
    ).map(page => ({ key: `page:${page.path}`, label: t(`palette.pages.${page.key}`), icon: page.icon }))
  ];
  return [
    { key: 'packages', label: t('palette.packages'), items: packages },
    { key: 'jump', label: t('palette.jump'), items: jumps }
  ];
});

function targetOf(item: OnCommandItem): string | null {
  const [type, ...rest] = item.key.split(':');
  if (type === 'package') return localePath(`/apps/${rest.slice(1).join(':')}`);
  if (type === 'category') return localePath(`/categories/${rest.join(':')}`);
  if (type === 'page') return localePath(rest.join(':'));
  if (type === 'search') return localePath(`/search?q=${encodeURIComponent(rest.join(':'))}`);
  return null;
}

function onSelect(item: OnCommandItem) {
  const target = targetOf(item);
  open.value = false;
  if (target) void navigateTo(target);
}

function onCopy(item: OnCommandItem) {
  // App Store rows exist only in the desktop palette and have no brew command.
  if (!item.app || item.app.kind === 'mas') return;
  void copy(item.app.kind, item.app.token);
}

// Command-K / Ctrl-K opens the palette, including from inputs.
function onKeydown(event: KeyboardEvent) {
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
    event.preventDefault();
    open.value = !open.value;
  }
}

onMounted(() => document.addEventListener('keydown', onKeydown));
onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKeydown);
  clearTimeout(timer);
});

const hints = computed(() => [
  messages.value.palette.hintSelect,
  messages.value.palette.hintOpen,
  messages.value.palette.hintCopy
]);
</script>

<template>
  <OnCommandPalette
    v-model:open="open"
    v-model:query="query"
    :groups="groups"
    :loading="loading"
    :hints="hints"
    @select="onSelect"
    @copy="onCopy"
  />
</template>
