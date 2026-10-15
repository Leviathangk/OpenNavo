<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatList } from '@opennavo/shared';
import { OnButton, OnIcon } from '@opennavo/ui';
import RelaunchNotice from '@/components/permissions/RelaunchNotice.vue';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useCleanupStore } from '@/stores/cleanup';
import { usePermissionsStore } from '@/stores/permissions';
import { displayPath } from '@/utils/paths';

// Review notices (cleanup proposal §06 branches): stale report replaced, missing Full Disk Access, background items of
// deleted apps that still load. Caches of running apps are routine, so their size shows in the summary and cards instead.
type Tone = 'danger' | 'info';
interface Notice {
  key: string;
  tone: Tone;
  icon: string;
  text: string;
  access?: boolean;
}

const { t } = useI18n();
const { appLocale } = useAppLocale();
const store = useCleanupStore();
const permissions = usePermissionsStore();
const toasts = useToasts();
const { errorText } = usePackageState();

const notices = computed<Notice[]>(() => {
  const list: Notice[] = [];
  if (store.rescanned)
    list.push({
      key: 'rescanned',
      tone: 'info',
      icon: 'refresh-cw',
      text: t('cleanup.notices.rescanned')
    });
  const services = store.activeServices;
  if (services.length) {
    const names = [...new Set(services.map(entry => entry.group.title))];
    list.push({
      key: 'services',
      tone: 'danger',
      icon: 'triangle-alert',
      text: t(
        'cleanup.notices.services',
        { names: formatList(names, { locale: appLocale.value }), count: services.length },
        { plural: services.length }
      )
    });
  }
  const incomplete = store.report?.incomplete ?? [];
  if (incomplete.some(location => location.reason === 'full_disk_access'))
    list.push({
      key: 'access',
      tone: 'info',
      icon: 'lock',
      text: t('cleanup.notices.incomplete', {
        paths: formatList(
          incomplete.map(location => displayPath(location.path)),
          { locale: appLocale.value }
        )
      }),
      access: true
    });
  return list;
});

const TONE: Record<Tone, string> = {
  danger: 'bg-status-danger-subtle',
  info: 'bg-surface-card-alt'
};
const ICON: Record<Tone, string> = {
  danger: 'text-status-danger',
  info: 'text-status-info'
};

async function grant() {
  try {
    await permissions.guide('full_disk_access');
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}
</script>

<template>
  <div v-if="notices.length" class="mb-12px flex flex-col gap-8px">
    <div
      v-for="notice in notices"
      :key="notice.key"
      class="flex items-start gap-10px rounded-default px-12px py-10px text-12.5px leading-[1.55] text-ink-secondary"
      :class="TONE[notice.tone]"
      role="status"
    >
      <OnIcon :name="notice.icon" :size="15" class="mt-2px shrink-0" :class="ICON[notice.tone]" />
      <span class="min-w-0 flex-1">{{ notice.text }}</span>
      <OnButton
        v-if="notice.access && !permissions.fullDiskAccessPending"
        variant="secondary"
        size="xs"
        class="shrink-0 self-center"
        :loading="permissions.guiding === 'full_disk_access'"
        @click="grant"
      >
        {{
          permissions.guiding === 'full_disk_access' ? t('permission.prompt.waiting') : t('permission.prompt.enable')
        }}
      </OnButton>
    </div>
    <RelaunchNotice v-if="permissions.relaunchRequired && notices.some(notice => notice.access)" />
  </div>
</template>
