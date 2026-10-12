<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes } from '@opennavo/shared';
import { OnButton, OnIcon } from '@opennavo/ui';
import type { SkippedItem } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useCleanupStore } from '@/stores/cleanup';
import { displayPath } from '@/utils/paths';

// Done (cleanup proposal §03 ⑨): what is free now, what waits in the Trash (undo / purge), what was skipped and why.
const { t } = useI18n();
const { appLocale } = useAppLocale();
const store = useCleanupStore();
const toasts = useToasts();
const { errorText } = usePackageState();

const format = (bytes: number) => formatBytes(bytes, { locale: appLocale.value });
const outcome = computed(() => store.outcome);
const failedTask = computed(() => (store.task && store.task.state !== 'succeeded' ? store.task : null));
const trashBytes = computed(() => store.trashedBytes);
const nowBytes = computed(() => store.freedNow);
const pending = computed(() => Boolean(outcome.value?.restorable && outcome.value.trashed && !store.restored));
const tiles = computed(() => {
  const value = outcome.value;
  const skipped = value?.skipped.length ?? 0;
  return [
    {
      key: 'freed',
      label: t('cleanup.result.freed'),
      value: format(nowBytes.value),
      hint: t('cleanup.result.freedHint')
    },
    {
      key: 'trashed',
      label: t('cleanup.result.trashed'),
      value: value?.trashed ? format(trashBytes.value) : '—',
      hint: store.restored
        ? t('cleanup.result.restored')
        : value?.trashed
          ? t('cleanup.result.trashedHint', { count: value.trashed }, { plural: value.trashed })
          : t('cleanup.result.trashedNone')
    },
    {
      key: 'skipped',
      label: t('cleanup.result.skipped'),
      value: t('cleanup.result.skippedCount', { count: skipped }, { plural: skipped }),
      hint: skipped ? t('cleanup.result.skippedHint') : t('cleanup.result.skippedNone'),
      warn: skipped > 0
    }
  ];
});
/** Skipped items keep their scan-time size, so it is clear what quitting the app would still free. */
const skippedSize = (itemId: string) => store.entries.find(entry => entry.item.id === itemId)?.item.sizeBytes ?? 0;
/** Fixed commands (brew cleanup, Docker prunes) have no path; the command names them. */
const skippedLabel = (item: SkippedItem) =>
  item.path ? displayPath(item.path) : (store.entries.find(entry => entry.item.id === item.itemId)?.item.command ?? '');
const free = computed(() =>
  store.freeBefore === null
    ? null
    : { before: format(store.freeBefore), after: format(store.freeBefore + nowBytes.value) }
);

async function undo() {
  try {
    const result = await store.restore();
    toasts.push({
      tone: result.missing.length ? 'warning' : 'success',
      title: result.missing.length
        ? t('uninstall.toasts.undoMissing', { count: result.missing.length }, { plural: result.missing.length })
        : t('cleanup.toasts.restored'),
      description: [
        t('cleanup.toasts.restoredBody', { count: result.restored }, { plural: result.restored }),
        ...result.missing
      ].join('\n')
    });
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

async function purge() {
  try {
    const freed = await store.purge();
    toasts.push({
      tone: 'success',
      title: t('cleanup.toasts.purged'),
      description: t('cleanup.toasts.purgedBody', { size: format(freed) })
    });
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}
</script>

<template>
  <section class="rounded-big border border-solid border-line-subtle bg-surface-card px-24px pb-20px pt-26px">
    <div class="flex flex-col items-center text-center">
      <span
        class="mb-12px grid h-52px w-52px place-items-center rounded-full"
        :class="
          failedTask ? 'bg-status-danger-subtle text-status-danger' : 'bg-status-success-subtle text-status-success'
        "
      >
        <OnIcon :name="failedTask ? 'triangle-alert' : 'check'" :size="26" />
      </span>
      <h2 class="m-0 text-19px font-600 text-ink-primary">
        {{ failedTask ? t('cleanup.result.failedTitle') : t('cleanup.result.title', { size: format(nowBytes) }) }}
      </h2>
      <p class="m-0 mt-6px text-13px text-ink-tertiary">
        {{
          failedTask?.error
            ? errorText(failedTask.error)
            : pending
              ? t('cleanup.result.trashPending', { size: format(trashBytes) })
              : t('cleanup.result.rebuilds')
        }}
      </p>
    </div>

    <dl class="m-0 my-16px grid grid-cols-3 gap-10px">
      <div
        v-for="tile in tiles"
        :key="tile.key"
        class="rounded-panel border border-solid border-line-subtle bg-surface-card px-14px py-12px"
      >
        <dt class="text-12px text-ink-tertiary">{{ tile.label }}</dt>
        <dd
          class="m-0 mt-4px text-20px font-600 tracking-[-0.015em]"
          :class="tile.warn ? 'text-status-warning' : 'text-ink-primary'"
        >
          {{ tile.value }}
        </dd>
        <dd class="m-0 mt-2px text-11.5px text-ink-tertiary">{{ tile.hint }}</dd>
      </div>
    </dl>
    <p v-if="free" class="m-0 flex items-center justify-center gap-8px text-13px text-ink-secondary">
      {{ t('cleanup.result.free') }}
      <b class="font-600 tabular-nums text-ink-primary">{{ free.before }}</b>
      <OnIcon name="arrow-right" :size="14" class="text-ink-tertiary" />
      <b class="font-600 tabular-nums text-status-success">{{ free.after }}</b>
    </p>
  </section>

  <section
    v-if="outcome?.skipped.length"
    class="mt-12px rounded-panel border border-solid border-[rgba(250,178,25,0.25)] bg-status-warning-subtle px-12px py-6px"
  >
    <h3 class="m-0 flex items-center gap-6px py-6px text-12.5px font-600 text-status-warning">
      <OnIcon name="triangle-alert" :size="14" />{{ t('cleanup.result.skippedTitle') }}
    </h3>
    <div
      v-for="item in outcome.skipped"
      :key="item.itemId"
      class="flex items-center gap-10px border-t border-t-solid border-[rgba(250,178,25,0.15)] py-8px"
    >
      <div class="min-w-0 flex-1">
        <div class="truncate font-mono text-11.5px text-ink-primary" :title="item.path || undefined">
          {{ skippedLabel(item) }}
        </div>
        <div class="text-12px text-ink-tertiary">
          {{ t(`uninstall.skipReasons.${item.reason}`) }}
        </div>
      </div>
      <span v-if="skippedSize(item.itemId)" class="shrink-0 text-12px tabular-nums text-ink-secondary">{{
        format(skippedSize(item.itemId))
      }}</span>
    </div>
  </section>

  <div class="mt-16px flex items-center gap-8px">
    <template v-if="pending">
      <OnButton variant="secondary" icon="undo-2" @click="undo">{{ t('cleanup.result.undo') }}</OnButton>
      <OnButton variant="ghost" @click="purge">{{ t('cleanup.result.purge') }}</OnButton>
    </template>
    <span class="flex-1"></span>
    <OnButton variant="primary" @click="store.finish()">{{ t('cleanup.result.done') }}</OnButton>
  </div>
</template>
