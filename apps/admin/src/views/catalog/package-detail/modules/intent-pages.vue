<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { fetchPackageIntentPages, updatePackageIntentPages } from '@/service/api';
import type { DataOf } from '@/typings/api/opennavo';
import { $t } from '@/locales';
import { CONTENT_LOCALES, localeName } from '@/utils/content-locale';
import { formatDateTime, isDryRun, isRevisionConflict, writeOwnerLabel } from '@/utils/opennavo';

defineOptions({ name: 'IntentPages' });

// Alternatives and uninstall pages open per language, here or by agents through MCP. The server decides
// whether the content passes its threshold and whether the page ends up indexed; this list shows its verdict
// and saves only the switches that changed. Traditional Chinese follows the Simplified Chinese switch, and
// link apps have no uninstall page, so those rows are read-only.
const props = defineProps<{ packageId: number; editable: boolean }>();

type View = DataOf<'getPackageIntentPages'>;
type Item = View['items'][number];

const KINDS = ['alternatives', 'uninstall'] as const;
const view = ref<View>();
const enabled = ref<Record<string, boolean>>({});
const loading = ref(false);
const saving = ref(false);
const loadFailed = ref(false);
const saveError = ref<'reloaded' | 'saveFailed' | null>(null);
// Switches whose saved value someone else changed, found when a save lost a revision conflict.
const changedRows = ref<string[]>([]);

const text = (key: string) => $t(`page.catalog.packageDetail.index.intent.${key}` as App.I18n.I18nKey);
const keyOf = (item: Pick<Item, 'pageKind' | 'locale'>) => `${item.pageKind}:${item.locale}`;
// A derived row shows the Simplified Chinese switch it follows, including an unsaved change to it.
const switchKey = (item: Item) => (item.derived ? keyOf({ pageKind: item.pageKind, locale: 'zh-CN' }) : keyOf(item));
const locked = (item: Item) => item.derived || item.reason === 'unsupported_page';
const groups = computed(() =>
  KINDS.map(kind => ({
    kind,
    items: (view.value?.items ?? [])
      .filter(item => item.pageKind === kind)
      .sort((a, b) => CONTENT_LOCALES.indexOf(a.locale) - CONTENT_LOCALES.indexOf(b.locale))
  })).filter(group => group.items.length > 0)
);
const changes = computed(() =>
  (view.value?.items ?? [])
    .filter(item => !locked(item) && enabled.value[keyOf(item)] !== item.enabled)
    .map(item => ({ pageKind: item.pageKind, locale: item.locale, enabled: Boolean(enabled.value[keyOf(item)]) }))
);
// An open page that still stays out of the index is the case worth a second look.
const indexTag = (item: Item) => (item.indexable ? 'success' : item.enabled ? 'warning' : 'default');
const enabledInfo = (item: Item) =>
  item.enabled && item.enabledBy
    ? $t('page.shared.updatedBy', { name: writeOwnerLabel(item.enabledBy), time: formatDateTime(item.enabledAt) })
    : '';

function apply(value: View) {
  view.value = value;
  enabled.value = Object.fromEntries(value.items.map(item => [keyOf(item), item.enabled]));
}

async function load() {
  loading.value = true;
  loadFailed.value = false;
  try {
    const { data, error } = await fetchPackageIntentPages(props.packageId);
    if (error) loadFailed.value = true;
    else {
      apply(data);
      changedRows.value = [];
      saveError.value = null;
    }
  } finally {
    loading.value = false;
  }
}

/**
 * A lost conflict moves the revision on, so no unsaved switch may outlive the value it was set against:
 * every switch whose saved value changed takes the new value and is flagged, the others keep the editor's
 * unsaved changes for the next save.
 */
async function reloadAfterConflict() {
  const before = new Map((view.value?.items ?? []).map(item => [keyOf(item), item.enabled]));
  const { data, error } = await fetchPackageIntentPages(props.packageId);
  if (error) {
    loadFailed.value = true;
    saveError.value = 'saveFailed';
    return;
  }
  const moved = data.items.filter(item => !item.derived && before.get(keyOf(item)) !== item.enabled).map(keyOf);
  const drafts = enabled.value;
  view.value = data;
  enabled.value = Object.fromEntries(
    data.items.map(item => {
      const key = keyOf(item);
      return [key, locked(item) || moved.includes(key) || !(key in drafts) ? item.enabled : drafts[key]];
    })
  );
  changedRows.value = moved;
  saveError.value = 'reloaded';
}

async function save() {
  const current = view.value;
  if (!current || !changes.value.length || saving.value) return;
  saving.value = true;
  saveError.value = null;
  try {
    const { data, error, response } = await updatePackageIntentPages(
      props.packageId,
      { items: changes.value },
      current.revision
    );
    if (!error && !isDryRun(data)) {
      apply(data);
      changedRows.value = [];
      window.$message?.success($t('page.shared.saved'));
    } else if (isRevisionConflict(response)) await reloadAfterConflict();
    else saveError.value = 'saveFailed';
  } finally {
    saving.value = false;
  }
}

watch(() => props.packageId, load, { immediate: true });
</script>

<template>
  <div data-testid="intent-pages">
    <NText depth="3" class="block">{{ text('hint') }}</NText>
    <NText depth="3" class="mb-12px block">{{ text('thresholds') }}</NText>
    <NAlert v-if="loadFailed" type="error" class="mb-12px">
      {{ text('loadFailed') }}
      <NButton size="small" :loading="loading" @click="load">{{ $t('common.refresh') }}</NButton>
    </NAlert>
    <NSkeleton v-if="loading && !view" text :repeat="3" />
    <template v-if="view">
      <NAlert v-if="!view.globalEnabled" type="warning" class="mb-12px" data-testid="intent-global-off">
        {{ text('globalOff') }}
      </NAlert>
      <NEmpty v-if="!groups.length" :description="text('empty')" class="py-16px" />
      <div v-for="group in groups" :key="group.kind" class="mb-16px">
        <NText strong class="mb-8px block">{{ text(`kinds.${group.kind}`) }}</NText>
        <NTable size="small" :single-line="false">
          <thead>
            <tr>
              <th>{{ text('locale') }}</th>
              <th>{{ text('enabled') }}</th>
              <th>{{ text('content') }}</th>
              <th>{{ text('indexable') }}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in group.items" :key="item.locale" :data-testid="`intent-${keyOf(item)}`">
              <td>{{ localeName(item.locale) }}</td>
              <td>
                <NSwitch
                  v-model:value="enabled[switchKey(item)]"
                  size="small"
                  :disabled="!editable || saving || locked(item)"
                  :aria-label="`${text(`kinds.${group.kind}`)} · ${localeName(item.locale)}`"
                />
                <NText v-if="changedRows.includes(keyOf(item))" type="warning" class="block text-12px">
                  {{ text('changedRow') }}
                </NText>
                <NText v-if="enabledInfo(item)" depth="3" class="block text-12px">{{ enabledInfo(item) }}</NText>
              </td>
              <td>
                <NTag size="small" :bordered="false" :type="item.contentEligible ? 'success' : 'default'">
                  {{ text(item.contentEligible ? 'eligible' : 'ineligible') }}
                </NTag>
              </td>
              <td>
                <NTag size="small" :bordered="false" :type="indexTag(item)">
                  {{ text(item.indexable ? 'indexed' : 'notIndexed') }}
                </NTag>
                <NText v-if="!item.indexable && item.reason" depth="3" class="block text-12px font-mono">
                  {{ item.reason }}
                </NText>
              </td>
              <td class="text-right">
                <a v-if="item.url" :href="item.url" target="_blank" rel="noopener noreferrer" class="text-primary">
                  {{ $t('page.shared.viewOnSite') }}
                </a>
              </td>
            </tr>
          </tbody>
        </NTable>
      </div>
      <NAlert
        v-if="saveError"
        :type="saveError === 'reloaded' ? 'warning' : 'error'"
        class="mb-12px"
        data-testid="intent-save-error"
      >
        {{ text(saveError) }}
      </NAlert>
      <NButton
        v-if="editable && groups.length"
        type="primary"
        :loading="saving"
        :disabled="!changes.length"
        @click="save"
      >
        {{ text('save') }}
      </NButton>
    </template>
  </div>
</template>
