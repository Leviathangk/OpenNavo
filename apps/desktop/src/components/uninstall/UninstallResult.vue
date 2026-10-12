<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes } from '@opennavo/shared';
import { OnButton, OnIcon } from '@opennavo/ui';
import type { LocalApp } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageIdentity } from '@/composables/usePackageIdentity';
import type { UninstallResult } from '@/stores/uninstall';
import { displayPath } from '@/utils/paths';

// Done (proposal §03 scenario 10): what was freed, what moved to the Trash, what was skipped and why.
// Moving to the Trash frees nothing until it is emptied, so the space reads "can be freed".
const props = defineProps<{ result: UninstallResult; title: string; brew: boolean; apps: readonly LocalApp[] }>();
const emit = defineEmits<{ retry: [appIds: string[]] }>();

const { t, te } = useI18n();
const { appLocale } = useAppLocale();
const { taskName } = usePackageIdentity();

// Task errors arrive as plain AppError objects; show the localized message when one exists. An app that did not quit
// most likely waits on a "save changes?" sheet, which the update wording would not explain.
function reason(code: string, message: string) {
  if (code === 'E_APP_RUNNING') return t('uninstall.result.quitFailed');
  return te(`errors.${code}`) ? t(`errors.${code}`) : message;
}

/** Apps of failed tasks that can be uninstalled again (each gets a fresh scan). */
const retryable = computed(() =>
  props.result.failed.flatMap(task => {
    const target = task.target;
    const app =
      target?.kind === 'app'
        ? props.apps.find(item => item.id === target.token)
        : props.apps.find(item => item.cask === target?.token);
    return app && task.state === 'failed' ? [app.id] : [];
  })
);

const trash = computed(() => props.result.moved > 0 || props.result.restorable);
const count = computed(() => props.result.moved + props.result.deleted);
const tiles = computed(() => [
  {
    key: 'freed',
    label: trash.value ? t('uninstall.result.freed') : t('uninstall.result.freedNow'),
    value: formatBytes(props.result.freedBytes, { locale: appLocale.value }),
    hint: trash.value ? t('uninstall.result.freedHint') : t('uninstall.result.freedNowHint')
  },
  {
    key: 'count',
    label: trash.value ? t('uninstall.result.moved') : t('uninstall.result.deleted'),
    value: t('uninstall.itemCount', { count: count.value }, { plural: count.value }),
    hint: t('uninstall.result.countHint')
  },
  {
    key: 'skipped',
    label: t('uninstall.result.skipped'),
    value: t('uninstall.itemCount', { count: props.result.skipped.length }, { plural: props.result.skipped.length }),
    hint: props.result.skipped.length ? t('uninstall.result.skippedSee') : t('uninstall.result.skippedNone'),
    warn: props.result.skipped.length > 0
  }
]);
</script>

<template>
  <div class="pb-4px">
    <div class="flex flex-col items-center px-12px pb-2px pt-4px text-center">
      <span
        class="mb-12px grid h-52px w-52px place-items-center rounded-full"
        :class="
          result.failed.length
            ? 'bg-status-warning-subtle text-status-warning'
            : 'bg-status-success-subtle text-status-success'
        "
      >
        <OnIcon :name="result.failed.length ? 'triangle-alert' : 'check'" :size="26" />
      </span>
      <h3 class="m-0 text-19px font-600 text-ink-primary">{{ title }}</h3>
      <p class="m-0 mt-6px text-13px text-ink-tertiary">
        {{ trash ? t('uninstall.result.heroTrash') : t('uninstall.result.heroDelete') }}
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

    <section
      v-if="result.failed.length"
      class="mb-12px rounded-panel border border-solid border-status-danger bg-status-danger-subtle px-12px py-8px"
    >
      <p v-for="task in result.failed" :key="task.id" class="m-0 py-4px text-12.5px text-ink-secondary">
        {{
          t('uninstall.result.failed', {
            name: taskName(task),
            reason: task.error ? reason(task.error.code, task.error.message) : ''
          })
        }}
      </p>
      <OnButton
        v-if="retryable.length"
        class="mb-4px mt-4px"
        variant="secondary"
        size="xs"
        icon="refresh-cw"
        @click="emit('retry', retryable)"
        >{{ t('uninstall.result.retry') }}</OnButton
      >
    </section>

    <section
      v-if="result.skipped.length"
      class="rounded-panel border border-solid border-[rgba(250,178,25,0.25)] bg-status-warning-subtle px-12px py-6px"
    >
      <h4 class="m-0 flex items-center gap-6px py-6px text-12.5px font-600 text-status-warning">
        <OnIcon name="triangle-alert" :size="14" />{{ t('uninstall.result.skippedTitle') }}
      </h4>
      <div
        v-for="item in result.skipped"
        :key="item.path"
        class="border-t border-t-solid border-[rgba(250,178,25,0.15)] py-8px"
      >
        <div class="truncate font-mono text-11.5px text-ink-primary" :title="item.path">
          {{ displayPath(item.path) }}
        </div>
        <div class="text-12px text-ink-tertiary">
          {{ t(`uninstall.skipReasons.${item.reason}`) }}
        </div>
      </div>
      <p class="m-0 border-t border-t-solid border-[rgba(250,178,25,0.15)] py-8px text-12px text-ink-tertiary">
        {{ t('uninstall.result.skippedHint') }}
      </p>
    </section>

    <p v-if="brew && result.restorable" class="m-0 mt-12px text-12px leading-[1.55] text-ink-tertiary">
      {{ t('uninstall.result.brewNote') }}
    </p>
  </div>
</template>
