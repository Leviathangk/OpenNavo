<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnAppIcon, OnButton, OnIcon, OnSegmented } from '@opennavo/ui';
import type { FirewallAction, FirewallReplyScope, FirewallRuleUntil } from '@/ipc/bindings';
import { IpcError } from '@/ipc/client';
import { appIcon, isBundle, signerText } from '@/components/firewall/meta';
import { usePackageState } from '@/composables/usePackageState';
import { useTrayWindowSize } from '@/composables/useTrayWindowSize';
import { useFirewallStore } from '@/stores/firewall';

// A connection waiting for an answer (firewall plan §4.4), in Rust's small always-on-top window. The oldest one shows;
// the answer becomes a rule for the whole app or just this destination, forever or until the app quits. There is no
// default button: a stray Return while typing elsewhere must not decide for the user. Held connections are let through
// when nobody answers in time (TCP 2 minutes, UDP 5 seconds); the question stays, and a late answer still becomes a rule.
const { t } = useI18n();
const { errorText } = usePackageState();
const store = useFirewallStore();
const content = ref<HTMLElement | null>(null);
useTrayWindowSize(content);

const alert = computed(() => store.alerts[0] ?? null);
const others = computed(() => Math.max(0, store.alerts.length - 1));
const scope = ref<FirewallReplyScope>('app');
const until = ref<FirewallRuleUntil>('always');
const failure = ref<string | null>(null);

// Each new connection starts from the broadest, lasting choice.
watch(
  () => alert.value?.id,
  () => {
    scope.value = 'app';
    until.value = 'always';
    failure.value = null;
  }
);

const destination = computed(() => {
  const value = alert.value;
  if (!value) return '';
  return `${value.remoteHost ?? value.remoteAddress}:${value.port}`;
});
const scopes = computed(() => [
  { value: 'app' as const, label: t('firewall.alert.scopes.app') },
  { value: 'destination' as const, label: t('firewall.alert.scopes.destination') }
]);
const untils = computed(() => [
  { value: 'always' as const, label: t('firewall.alert.untils.always') },
  { value: 'app_quits' as const, label: t('firewall.alert.untils.app_quits') }
]);
const warning = computed(() => {
  const signer = alert.value?.app.signer;
  return signer === 'unsigned' || signer === 'adhoc' || signer === 'invalid' ? signer : null;
});
/** Programs run from a terminal or script: who started them and with what says more than the name. */
const chain = computed(() => {
  const value = alert.value;
  if (!value?.ancestors.length) return null;
  return [value.app.name, ...value.ancestors.map(process => process.name)].join(' ← ');
});
const args = computed(() => {
  const value = alert.value;
  return value && value.args.length > 1 ? value.args.join(' ') : null;
});

// A second-by-second clock only while a deadline is on screen.
const now = ref(Date.now());
let ticker: ReturnType<typeof setInterval> | undefined;
watch(
  () => alert.value?.deadline ?? null,
  deadline => {
    clearInterval(ticker);
    ticker = undefined;
    now.value = Date.now();
    if (deadline !== null) ticker = setInterval(() => (now.value = Date.now()), 1000);
  },
  { immediate: true }
);
onBeforeUnmount(() => clearInterval(ticker));
/** "1:58": time left before the held connections are let through. */
const remaining = computed(() => {
  const deadline = alert.value?.deadline;
  if (deadline == null) return null;
  const seconds = Math.max(0, Math.ceil((deadline - now.value) / 1000));
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
});

async function answer(action: FirewallAction) {
  const current = alert.value;
  if (!current) return;
  failure.value = null;
  try {
    await store.reply({ alertId: current.id, action, scope: scope.value, until: until.value });
  } catch (error) {
    // Answered elsewhere (or released when OpenNavo restarted): the list Rust sends next is the truth.
    if (error instanceof IpcError && error.code === 'E_FW_UNKNOWN_ALERT') {
      await store.loadAlerts().catch(() => undefined);
      return;
    }
    failure.value = errorText(error);
  }
}
</script>

<template>
  <div ref="content" class="box-border w-440px bg-surface-card p-16px font-sans" data-firewall-alert>
    <template v-if="alert">
      <header class="flex items-start gap-12px">
        <OnAppIcon
          v-if="isBundle(alert.app)"
          kind="cask"
          :token="alert.app.bundleId ?? alert.app.key"
          :name="alert.app.name"
          :src="appIcon(alert.app)"
          :size="40"
        />
        <OnAppIcon v-else kind="formula" :token="alert.app.name" :name="alert.app.name" :size="40" />
        <div class="min-w-0 flex-1">
          <h1 class="m-0 text-14px font-600 leading-[1.4] text-ink-primary">
            {{ t('firewall.alert.title', { name: alert.app.name }) }}
          </h1>
          <p class="m-0 mt-2px truncate text-12px text-ink-tertiary">{{ signerText(alert.app, t) }}</p>
        </div>
      </header>

      <p
        v-if="warning"
        class="m-0 mt-10px flex items-start gap-6px rounded-default px-10px py-7px text-12px"
        :class="
          warning === 'invalid'
            ? 'bg-status-danger-subtle text-status-danger'
            : 'bg-status-warning-subtle text-ink-secondary'
        "
        role="status"
      >
        <OnIcon name="triangle-alert" :size="13" class="mt-2px shrink-0" />{{ t(`firewall.alert.warning.${warning}`) }}
      </p>

      <dl class="m-0 mt-12px grid grid-cols-[88px_minmax(0,1fr)] gap-x-10px gap-y-6px text-12.5px">
        <dt class="text-ink-tertiary">{{ t('firewall.alert.to') }}</dt>
        <dd class="m-0 min-w-0">
          <span class="block truncate font-mono text-ink-primary" :title="alert.remoteHost ?? alert.remoteAddress">{{
            alert.remoteHost ?? alert.remoteAddress
          }}</span>
          <span class="block truncate text-11.5px text-ink-tertiary">
            <template v-if="alert.remoteHost">{{ alert.remoteAddress }} · </template
            >{{ t('firewall.alert.port', { port: alert.port }) }} · {{ alert.protocol.toUpperCase() }}
          </span>
        </dd>
        <dt class="text-ink-tertiary">{{ t('firewall.alert.path') }}</dt>
        <dd class="m-0 min-w-0 select-text break-all font-mono text-11.5px text-ink-secondary">
          {{ alert.app.bundlePath ?? alert.app.path }}
        </dd>
        <template v-if="chain">
          <dt class="text-ink-tertiary">{{ t('firewall.alert.parents') }}</dt>
          <dd class="m-0 min-w-0 break-all text-11.5px text-ink-secondary">{{ chain }}</dd>
        </template>
        <template v-if="args">
          <dt class="text-ink-tertiary">{{ t('firewall.alert.args') }}</dt>
          <dd
            class="m-0 max-h-60px min-w-0 select-text overflow-y-auto break-all font-mono text-11.5px text-ink-secondary"
          >
            {{ args }}
          </dd>
        </template>
      </dl>

      <div class="mt-14px flex flex-col gap-8px border-t border-t-solid border-line-subtle pt-12px">
        <div class="flex items-center gap-10px">
          <span class="w-88px shrink-0 text-12.5px text-ink-tertiary">{{ t('firewall.alert.scope') }}</span>
          <OnSegmented v-model="scope" :options="scopes" size="sm" :aria-label="t('firewall.alert.scope')" />
        </div>
        <p v-if="scope === 'destination'" class="m-0 truncate pl-98px font-mono text-11.5px text-ink-tertiary">
          {{ destination }}
        </p>
        <div class="flex items-center gap-10px">
          <span class="w-88px shrink-0 text-12.5px text-ink-tertiary">{{ t('firewall.alert.until') }}</span>
          <OnSegmented v-model="until" :options="untils" size="sm" :aria-label="t('firewall.alert.until')" />
        </div>
      </div>

      <p
        v-if="alert.released"
        class="m-0 mt-10px flex items-start gap-6px text-11.5px leading-[1.5] text-ink-tertiary"
        data-alert-released
      >
        <OnIcon name="info" :size="12" class="mt-2px shrink-0" />{{ t('firewall.alert.released') }}
      </p>
      <p
        v-else-if="remaining"
        class="m-0 mt-10px flex items-center gap-6px text-11.5px text-ink-tertiary tabular-nums"
        data-alert-deadline
      >
        <OnIcon name="clock" :size="12" class="shrink-0" />{{ t('firewall.alert.deadline', { time: remaining }) }}
      </p>
      <p v-if="failure" class="m-0 mt-10px text-12px text-status-danger" role="alert">{{ failure }}</p>

      <footer class="mt-14px flex items-center gap-8px">
        <span class="min-w-0 flex-1 text-11.5px leading-[1.4] text-ink-tertiary">
          <template v-if="alert.waiting">{{
            t('firewall.alert.waiting', { count: alert.waiting }, { plural: alert.waiting })
          }}</template>
          <template v-else-if="others">{{ t('firewall.alert.queue', { count: others }, { plural: others }) }}</template>
        </span>
        <OnButton icon="ban" :loading="store.busy.has(alert.id)" @click="answer('block')">{{
          t('firewall.alert.block')
        }}</OnButton>
        <OnButton variant="primary" icon="check" :disabled="store.busy.has(alert.id)" @click="answer('allow')">{{
          t('firewall.alert.allow')
        }}</OnButton>
      </footer>
    </template>
    <p v-else class="m-0 py-8px text-center text-12.5px text-ink-tertiary">{{ t('firewall.alert.none') }}</p>
  </div>
</template>
