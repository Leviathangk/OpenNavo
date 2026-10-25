<script setup lang="ts">
import { computed, reactive } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnButton, OnCard, OnCheckbox, OnIcon } from '@opennavo/ui';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useFirewallStore } from '@/stores/firewall';
import { firewallPhase, SETUP_STEPS, stepState } from './meta';

// Before the firewall runs (firewall plan §5): what it does and the two choices that keep it quiet, then macOS's own
// steps — install the system extension, allow it in System Settings, allow network filtering — as they happen. After a
// removal the same card says so: macOS deletes the stopped extension only at the next restart.
const { t, te } = useI18n();
const toasts = useToasts();
const { errorText } = usePackageState();
const store = useFirewallStore();

const status = computed(() => store.status);
const phase = computed(() => firewallPhase(status.value));
// Both start on: only programs nobody vouched for ask, which keeps the first days quiet.
const OPTIONS = ['allowApple', 'allowInstalled'] as const;
const choices = reactive({ allowApple: true, allowInstalled: true });

const failure = computed(() => {
  const error = status.value?.error;
  if (!error) return null;
  const key = `errors.${error.code}`;
  return te(key) ? t(key) : (error.detail ?? error.code);
});

const POINTS = ['ask', 'rules', 'blocklists'] as const;
const POINT_ICONS = { ask: 'circle-alert', rules: 'list-filter', blocklists: 'shield' } as const;

const cleanupPending = computed(
  () =>
    status.value?.error?.code === 'E_FW_REMOVAL' &&
    ['not_installed', 'removal_pending'].includes(status.value.extension)
);

async function enable() {
  try {
    if (cleanupPending.value) {
      await store.uninstall();
    } else {
      await store.enable({ ...choices });
    }
  } catch (error) {
    toasts.push({
      tone: 'danger',
      title: cleanupPending.value ? failure.value! : t('firewall.setup.failedTitle'),
      description: errorText(error)
    });
  }
}

async function openApproval() {
  try {
    await store.openApproval();
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}
</script>

<template>
  <OnCard
    v-if="phase === 'unavailable' && status"
    padding="lg"
    as="section"
    class="flex flex-col gap-8px"
    aria-labelledby="firewall-unavailable"
  >
    <h2 id="firewall-unavailable" class="m-0 flex items-center gap-8px text-15px font-600 text-ink-primary">
      <OnIcon name="info" :size="17" />{{ t(`firewall.unavailable.${status.availability}.title`) }}
    </h2>
    <p class="m-0 text-13px leading-[1.6] text-ink-secondary">
      {{ t(`firewall.unavailable.${status.availability}.body`) }}
    </p>
  </OnCard>

  <OnCard
    v-else-if="phase === 'setup'"
    padding="lg"
    as="section"
    class="flex flex-col gap-14px"
    aria-labelledby="firewall-setup"
  >
    <div class="flex flex-col gap-6px">
      <h2 id="firewall-setup" class="m-0 flex items-center gap-8px text-16px font-600 text-ink-primary">
        <OnIcon name="brick-wall-fire" :size="18" />{{ t('firewall.setup.title') }}
      </h2>
      <p class="m-0 text-13px leading-[1.6] text-ink-secondary">{{ t('firewall.setup.lead') }}</p>
    </div>
    <p
      v-if="status?.extension === 'removal_pending'"
      class="m-0 rounded-big bg-status-info-subtle px-12px py-8px text-12.5px text-ink-secondary"
      role="status"
      data-firewall-removed
    >
      {{ t('firewall.setup.removed') }}
    </p>
    <ul class="m-0 grid list-none grid-cols-1 gap-10px p-0 md:grid-cols-3">
      <li
        v-for="point in POINTS"
        :key="point"
        class="flex flex-col gap-4px rounded-big bg-surface-inset px-14px py-12px"
      >
        <span class="flex items-center gap-6px text-13px font-600 text-ink-primary">
          <OnIcon :name="POINT_ICONS[point]" :size="14" />{{ t(`firewall.setup.points.${point}.title`) }}
        </span>
        <span class="text-12.5px leading-[1.55] text-ink-secondary">{{
          t(`firewall.setup.points.${point}.body`)
        }}</span>
      </li>
    </ul>
    <div class="flex flex-col gap-10px">
      <OnCheckbox v-for="option in OPTIONS" :key="option" v-model="choices[option]">
        <span class="flex flex-col gap-2px">
          <span class="text-13px text-ink-primary">{{ t(`firewall.options.${option}`) }}</span>
          <span class="text-12px text-ink-tertiary">{{ t(`firewall.options.${option}Hint`) }}</span>
        </span>
      </OnCheckbox>
    </div>
    <p
      v-if="failure"
      class="m-0 rounded-big bg-status-danger-subtle px-12px py-8px text-12.5px text-status-danger"
      role="alert"
    >
      {{ cleanupPending ? failure : t('firewall.setup.failed', { reason: failure }) }}
    </p>
    <div class="flex flex-wrap items-center gap-12px">
      <OnButton variant="primary" icon="brick-wall-fire" :loading="store.switching" @click="enable">
        {{ cleanupPending ? t('common.retry') : failure ? t('firewall.setup.retry') : t('firewall.setup.enable') }}
      </OnButton>
      <span class="min-w-0 flex-1 text-12px leading-[1.55] text-ink-tertiary">{{ t('firewall.setup.steps') }}</span>
    </div>
    <p class="m-0 flex items-center gap-6px text-12px text-ink-tertiary">
      <OnIcon name="lock" :size="13" />{{ t('firewall.setup.privacy') }}
    </p>
  </OnCard>

  <OnCard
    v-else-if="phase === 'progress' && status"
    padding="lg"
    as="section"
    class="flex flex-col gap-12px"
    aria-labelledby="firewall-progress"
  >
    <h2 id="firewall-progress" class="m-0 flex items-center gap-8px text-15px font-600 text-ink-primary">
      <OnIcon v-if="status.extension === 'needs_reboot'" name="refresh-cw" :size="17" />
      <OnIcon v-else name="loader-circle" :size="17" class="animate-spin" />
      {{ status.extension === 'uninstalling' ? t('firewall.progress.uninstalling') : t('firewall.progress.title') }}
    </h2>
    <ol v-if="status.extension !== 'uninstalling'" class="m-0 flex list-none flex-col gap-10px p-0">
      <li v-for="(step, index) in SETUP_STEPS" :key="step" class="flex items-start gap-10px" :data-step="step">
        <span
          class="mt-1px flex h-20px w-20px shrink-0 items-center justify-center rounded-full text-11px font-600"
          :class="
            stepState(status, step) === 'done'
              ? 'bg-status-success text-ink-inverse'
              : stepState(status, step) === 'active'
                ? 'bg-brand-salmon text-ink-inverse'
                : 'bg-surface-chip text-ink-tertiary'
          "
        >
          <OnIcon v-if="stepState(status, step) === 'done'" name="check" :size="12" />
          <template v-else>{{ index + 1 }}</template>
        </span>
        <span class="flex min-w-0 flex-1 flex-col gap-4px">
          <span
            class="text-13px"
            :class="stepState(status, step) === 'todo' ? 'text-ink-tertiary' : 'font-600 text-ink-primary'"
            :aria-current="stepState(status, step) === 'active' ? 'step' : undefined"
            >{{ t(`firewall.progress.${step}`) }}</span
          >
          <template v-if="stepState(status, step) === 'active'">
            <span class="text-12.5px leading-[1.55] text-ink-secondary">{{ t(`firewall.progress.${step}Hint`) }}</span>
            <OnButton
              v-if="step === 'approve'"
              class="self-start"
              variant="primary"
              size="sm"
              icon="arrow-up-right"
              @click="openApproval"
            >
              {{ t('firewall.progress.openSettings') }}
            </OnButton>
          </template>
        </span>
      </li>
    </ol>
    <p
      v-if="status.extension === 'needs_reboot'"
      class="m-0 rounded-big bg-status-info-subtle px-12px py-8px text-12.5px text-ink-secondary"
      role="status"
    >
      {{ t('firewall.progress.reboot') }}
    </p>
  </OnCard>
</template>
