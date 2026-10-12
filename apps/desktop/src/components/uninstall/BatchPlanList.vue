<script setup lang="ts">
import { ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes } from '@opennavo/shared';
import { OnAppIcon, OnCheckbox, OnIcon } from '@opennavo/ui';
import type { AppPlan, PlanItem } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageIdentity } from '@/composables/usePackageIdentity';
import { useLocalAppsStore } from '@/stores/localApps';
import { useUninstallStore } from '@/stores/uninstall';
import PlanGroups from './PlanGroups.vue';

// Batch review (proposal §03 scenario 11): one row per app with its total; expand to adjust items,
// untick an app to leave it out of this run.
defineProps<{ apps: readonly AppPlan[] }>();
const emit = defineEmits<{ reveal: [item: PlanItem] }>();

const { t } = useI18n();
const { appLocale } = useAppLocale();
const store = useUninstallStore();
const localApps = useLocalAppsStore();
const { icon, localApp } = usePackageIdentity();
const nameOf = (app: AppPlan) => {
  const local = localApps.find(app.appId);
  return local ? localApp(local).name : app.name;
};

const expanded = ref(new Set<string>());
function toggle(appId: string) {
  const next = new Set(expanded.value);
  if (next.has(appId)) next.delete(appId);
  else next.add(appId);
  expanded.value = next;
}

const isIncluded = (app: AppPlan) => !store.excluded.includes(app.appId);
const chosen = (app: AppPlan) => app.items.filter(item => store.isChecked(item));
const size = (app: AppPlan) =>
  formatBytes(
    chosen(app).reduce((sum, item) => sum + (item.sizeBytes ?? 0), 0),
    { locale: appLocale.value }
  );
function meta(app: AppPlan) {
  const related = chosen(app).filter(item => item.category !== 'app').length;
  const parts = [
    t(`library.sources.${app.source}`),
    t('uninstall.batch.meta', { count: related }, { plural: related })
  ];
  if (app.running) parts.push(t('library.running'));
  if (chosen(app).some(item => item.needsAdmin)) parts.push(t('uninstall.batch.admin'));
  return parts.join(' · ');
}
const iconOf = (app: AppPlan) => {
  const local = localApps.find(app.appId);
  return local?.cask ? icon('cask', local.cask, app.name) : icon('app', app.appId, app.name);
};
</script>

<template>
  <div class="flex flex-col gap-8px">
    <section
      v-for="app in apps"
      :key="app.appId"
      class="overflow-hidden rounded-panel border border-solid border-line-subtle bg-surface-card"
    >
      <div class="flex items-center gap-10px px-12px py-10px" :class="isIncluded(app) ? '' : 'opacity-55'">
        <OnCheckbox
          :model-value="isIncluded(app)"
          :aria-label="isIncluded(app) ? t('uninstall.batch.exclude') : t('uninstall.batch.include')"
          @update:model-value="store.toggleApp(app.appId)"
        />
        <OnAppIcon v-bind="iconOf(app)" :size="30" />
        <span class="min-w-0 flex-1">
          <span class="block truncate text-13px font-600 text-ink-primary">{{ nameOf(app) }}</span>
          <span class="block truncate text-12px text-ink-tertiary">{{ meta(app) }}</span>
        </span>
        <span class="shrink-0 text-12.5px tabular-nums text-ink-secondary">{{
          isIncluded(app) ? size(app) : t('uninstall.batch.skipped')
        }}</span>
        <button
          type="button"
          class="m-0 flex h-26px w-26px shrink-0 cursor-pointer items-center justify-center rounded-small border-none bg-transparent p-0 text-ink-tertiary outline-none hover:text-ink-primary focus-visible:shadow-focus-ring disabled:cursor-not-allowed"
          :aria-expanded="expanded.has(app.appId) && isIncluded(app)"
          :aria-label="nameOf(app)"
          :disabled="!isIncluded(app)"
          @click="toggle(app.appId)"
        >
          <OnIcon
            name="chevron-down"
            :size="14"
            class="transition-transform duration-fast"
            :class="expanded.has(app.appId) && isIncluded(app) ? '' : '-rotate-90'"
          />
        </button>
      </div>
      <div v-if="expanded.has(app.appId) && isIncluded(app)" class="px-10px pb-10px">
        <PlanGroups :app="app" @reveal="item => emit('reveal', item)" />
      </div>
    </section>
  </div>
</template>
