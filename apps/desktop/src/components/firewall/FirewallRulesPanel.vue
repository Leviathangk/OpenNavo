<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnButton, OnConfirm, OnEmpty, OnSearchField, OnSegmented, OnSkeleton } from '@opennavo/ui';
import type { FirewallRule } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useFirewallStore } from '@/stores/firewall';
import FirewallRuleGroupRow from './FirewallRuleGroupRow.vue';
import FirewallRuleSheet from './FirewallRuleSheet.vue';
import { groupRules } from './meta';

// Rules (firewall plan §4.3): every answer becomes a rule here, by program; Apple programs and apps installed before
// the firewall get their allow rules the first time they connect. Search by app, developer or address.
const { t } = useI18n();
const toasts = useToasts();
const { errorText } = usePackageState();
const { appLocale } = useAppLocale();
const store = useFirewallStore();

type Filter = 'all' | 'allow' | 'block' | 'user' | 'auto';
const filter = ref<Filter>('all');
const query = ref('');
const loadError = ref<unknown>(null);
const editing = ref<FirewallRule | null>(null);
const sheetOpen = ref(false);
const deleting = ref<FirewallRule | null>(null);

// Rule changes, and the extension answering again after a failed read, reload the list while it is on screen.
onUnmounted(store.follow('rules'));
onMounted(async () => {
  try {
    await store.loadRules();
  } catch (error) {
    loadError.value = error;
  }
});

const filters = computed(() =>
  (['all', 'allow', 'block', 'user', 'auto'] as const).map(value => ({
    value,
    label: t(`firewall.rules.filters.${value}`)
  }))
);

const matches = (rule: FirewallRule) => {
  if (filter.value === 'allow' || filter.value === 'block') return rule.action === filter.value;
  if (filter.value === 'user') return rule.source === 'user';
  if (filter.value === 'auto') return rule.source !== 'user';
  return true;
};
const search = (rule: FirewallRule) => {
  const text = query.value.trim().toLowerCase();
  if (!text) return true;
  const app = rule.app;
  return [app?.name, app?.teamName, app?.bundleId, app?.path, rule.remote]
    .filter((value): value is string => Boolean(value))
    .some(value => value.toLowerCase().includes(text));
};
const groups = computed(() =>
  groupRules(
    store.rules.filter(rule => matches(rule) && search(rule)),
    new Intl.Collator(appLocale.value)
  )
);

function add() {
  editing.value = null;
  sheetOpen.value = true;
}

async function act(rule: FirewallRule, action: 'allow' | 'block' | 'edit' | 'delete') {
  if (action === 'edit') {
    editing.value = rule;
    sheetOpen.value = true;
    return;
  }
  if (action === 'delete') {
    deleting.value = rule;
    return;
  }
  try {
    // Switching keeps the program and destination; the rule becomes the user's own and never expires.
    await store.saveRule({
      id: rule.id,
      appKey: rule.app?.key ?? null,
      appPath: null,
      action,
      remote: rule.remote,
      port: rule.port
    });
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

async function confirmDelete() {
  const rule = deleting.value;
  if (!rule) return;
  try {
    if (!(await store.deleteRules([rule.id]))) return;
    deleting.value = null;
    toasts.push({ tone: 'success', title: t('firewall.rules.deleted') });
  } catch (error) {
    deleting.value = null;
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}
</script>

<template>
  <div class="flex flex-col gap-12px">
    <div class="flex flex-wrap items-center gap-10px">
      <OnSearchField
        v-model="query"
        class="w-240px"
        :placeholder="t('firewall.rules.search')"
        :aria-label="t('firewall.rules.search')"
      />
      <OnSegmented v-model="filter" :options="filters" size="sm" :aria-label="t('firewall.rules.filterLabel')" />
      <OnButton class="ml-auto" variant="secondary" size="sm" icon="plus" @click="add">{{
        t('firewall.rules.add')
      }}</OnButton>
    </div>

    <div v-if="!store.rulesLoaded && !loadError" class="flex flex-col gap-8px" aria-hidden="true">
      <OnSkeleton v-for="index in 5" :key="index" height="52px" radius="default" />
    </div>
    <p v-else-if="loadError && !store.rulesLoaded" class="m-0 text-13px text-status-danger" role="alert">
      {{ errorText(loadError) }}
    </p>
    <ul v-else-if="groups.length" class="m-0 list-none rounded-big border border-solid border-line-subtle p-0">
      <FirewallRuleGroupRow v-for="group in groups" :key="group.key" :group="group" :busy="store.busy" @act="act" />
    </ul>
    <OnEmpty v-else-if="store.rules.length" :title="t('firewall.rules.noMatch')" icon="lucide:search" />
    <OnEmpty
      v-else
      :title="t('firewall.rules.empty')"
      :description="t('firewall.rules.emptyBody')"
      icon="lucide:list-filter"
    />

    <FirewallRuleSheet :open="sheetOpen" :rule="editing" @close="sheetOpen = false" />
    <OnConfirm
      :open="deleting !== null"
      :title="t('firewall.rules.deleteTitle')"
      :description="
        deleting
          ? t('firewall.rules.deleteBody', { name: deleting.app?.name ?? t('firewall.rules.allApps') })
          : undefined
      "
      :confirm-label="t('firewall.rules.menu.delete')"
      tone="danger"
      :loading="deleting !== null && store.busy.has(deleting.id)"
      @confirm="confirmDelete"
      @cancel="deleting = null"
    />
  </div>
</template>
