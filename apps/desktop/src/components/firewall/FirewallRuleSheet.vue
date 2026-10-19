<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnAppIcon, OnButton, OnIcon, OnSegmented, OnSheet } from '@opennavo/ui';
import type { FirewallAction, FirewallApp, FirewallRule } from '@/ipc/bindings';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useFirewallStore } from '@/stores/firewall';
import { pickProgram } from '@/utils/files';
import { appIcon, developerText, isBundle, validPort, validRemote } from './meta';

// Add or edit a rule (firewall plan §4.3): which program (or every program), allow or block, and optionally one
// destination and port. Rust checks the address again; a saved rule is always the user's and never expires.
const props = defineProps<{ open: boolean; rule: FirewallRule | null }>();
const emit = defineEmits<{ close: [] }>();

const { t } = useI18n();
const toasts = useToasts();
const { errorText } = usePackageState();
const store = useFirewallStore();

const app = ref<FirewallApp | null>(null);
/** "all" applies to every program; "app" needs one chosen. */
const target = ref<'app' | 'all'>('app');
const action = ref<FirewallAction>('block');
const remoteMode = ref<'any' | 'some'>('any');
const remote = ref('');
const portMode = ref<'any' | 'some'>('any');
const port = ref('');
const choosing = ref(false);
const saving = ref(false);
const touched = ref(false);

watch(
  () => [props.open, props.rule] as const,
  ([open, rule]) => {
    if (!open) return;
    app.value = rule?.app ?? null;
    target.value = rule && !rule.app ? 'all' : 'app';
    action.value = rule?.action ?? 'block';
    remoteMode.value = rule?.remote ? 'some' : 'any';
    remote.value = rule?.remote ?? '';
    portMode.value = rule?.port ? 'some' : 'any';
    port.value = rule?.port ? String(rule.port) : '';
    touched.value = false;
  },
  { immediate: true }
);

const editing = computed(() => props.rule !== null);
const remoteError = computed(() => remoteMode.value === 'some' && !validRemote(remote.value));
const portError = computed(() => portMode.value === 'some' && !validPort(Number(port.value)));
// A rule for every program must name a destination, or it would allow or block the whole Mac.
const needsDestination = computed(
  () => target.value === 'all' && remoteMode.value === 'any' && portMode.value === 'any'
);
const ready = computed(
  () =>
    (target.value === 'all' || app.value !== null) && !remoteError.value && !portError.value && !needsDestination.value
);

const targets = computed(() => [
  { value: 'app' as const, label: t('firewall.sheet.oneApp') },
  { value: 'all' as const, label: t('firewall.sheet.allApps') }
]);
const actions = computed(() => [
  { value: 'allow' as const, label: t('firewall.rules.action.allow') },
  { value: 'block' as const, label: t('firewall.rules.action.block') }
]);
const remoteModes = computed(() => [
  { value: 'any' as const, label: t('firewall.rules.anyRemote') },
  { value: 'some' as const, label: t('firewall.sheet.someRemote') }
]);
const portModes = computed(() => [
  { value: 'any' as const, label: t('firewall.rules.anyPort') },
  { value: 'some' as const, label: t('firewall.sheet.somePort') }
]);

async function choose() {
  choosing.value = true;
  try {
    const path = await pickProgram();
    if (path) app.value = await store.identifyApp(path);
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  } finally {
    choosing.value = false;
  }
}

async function save() {
  touched.value = true;
  if (!ready.value) return;
  saving.value = true;
  try {
    const known = target.value === 'app' ? app.value : null;
    await store.saveRule({
      id: props.rule?.id ?? null,
      // A program already known to the extension keeps its identity; a newly chosen one goes by its path.
      appKey: known && editing.value ? known.key : null,
      appPath: known && !editing.value ? known.path : null,
      action: action.value,
      remote: remoteMode.value === 'some' ? remote.value.trim().toLowerCase() : null,
      port: portMode.value === 'some' ? Number(port.value) : null
    });
    toasts.push({ tone: 'success', title: t(editing.value ? 'firewall.sheet.saved' : 'firewall.sheet.added') });
    emit('close');
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  } finally {
    saving.value = false;
  }
}

const field =
  'box-border h-30px rounded-small border border-solid bg-surface-control px-8px font-mono text-12.5px text-ink-primary outline-none focus-visible:shadow-focus-ring';
</script>

<template>
  <OnSheet
    :open="open"
    :title="editing ? t('firewall.sheet.editTitle') : t('firewall.sheet.addTitle')"
    @close="emit('close')"
  >
    <form class="flex flex-col gap-16px" novalidate @submit.prevent="save">
      <fieldset class="m-0 flex flex-col gap-8px border-none p-0">
        <legend class="mb-8px p-0 text-12.5px font-600 text-ink-primary">{{ t('firewall.sheet.program') }}</legend>
        <OnSegmented v-if="!editing" v-model="target" class="self-start" :options="targets" size="sm" />
        <template v-if="target === 'app'">
          <div v-if="app" class="flex items-center gap-10px rounded-default bg-surface-inset px-10px py-8px">
            <OnAppIcon
              v-if="isBundle(app)"
              kind="cask"
              :token="app.bundleId ?? app.key"
              :name="app.name"
              :src="appIcon(app)"
              :size="28"
            />
            <OnAppIcon v-else kind="formula" :token="app.name" :name="app.name" :size="28" />
            <span class="min-w-0 flex-1">
              <span class="block truncate text-13px font-500 text-ink-primary">{{ app.name }}</span>
              <span class="block truncate text-11px text-ink-tertiary" :title="app.path">{{
                developerText(app, t)
              }}</span>
            </span>
            <OnButton v-if="!editing" variant="ghost" size="xs" :loading="choosing" @click="choose">{{
              t('firewall.sheet.change')
            }}</OnButton>
          </div>
          <OnButton v-else class="self-start" size="sm" icon="folder" :loading="choosing" @click="choose">{{
            t('firewall.sheet.chooseApp')
          }}</OnButton>
          <p v-if="touched && !app" class="m-0 text-12px text-status-danger" role="alert">
            {{ t('firewall.sheet.needApp') }}
          </p>
        </template>
        <p v-else class="m-0 text-12px text-ink-tertiary">{{ t('firewall.sheet.allAppsHint') }}</p>
      </fieldset>

      <fieldset class="m-0 flex flex-col gap-8px border-none p-0">
        <legend class="mb-8px p-0 text-12.5px font-600 text-ink-primary">{{ t('firewall.sheet.action') }}</legend>
        <OnSegmented v-model="action" class="self-start" :options="actions" size="sm" />
      </fieldset>

      <fieldset class="m-0 flex flex-col gap-8px border-none p-0">
        <legend class="mb-8px p-0 text-12.5px font-600 text-ink-primary">{{ t('firewall.sheet.remote') }}</legend>
        <OnSegmented v-model="remoteMode" class="self-start" :options="remoteModes" size="sm" />
        <template v-if="remoteMode === 'some'">
          <input
            v-model="remote"
            type="text"
            spellcheck="false"
            autocapitalize="off"
            :aria-label="t('firewall.sheet.remote')"
            :aria-invalid="touched && remoteError"
            :placeholder="t('firewall.sheet.remotePlaceholder')"
            class="w-full"
            :class="[field, touched && remoteError ? 'border-status-danger' : 'border-line-default']"
          />
          <p class="m-0 text-11.5px text-ink-tertiary">{{ t('firewall.sheet.remoteHint') }}</p>
        </template>
      </fieldset>

      <fieldset class="m-0 flex flex-col gap-8px border-none p-0">
        <legend class="mb-8px p-0 text-12.5px font-600 text-ink-primary">{{ t('firewall.sheet.port') }}</legend>
        <OnSegmented v-model="portMode" class="self-start" :options="portModes" size="sm" />
        <input
          v-if="portMode === 'some'"
          v-model="port"
          type="text"
          inputmode="numeric"
          :aria-label="t('firewall.sheet.port')"
          :aria-invalid="touched && portError"
          placeholder="443"
          class="w-120px"
          :class="[field, touched && portError ? 'border-status-danger' : 'border-line-default']"
        />
      </fieldset>

      <p v-if="touched && (remoteError || portError)" class="m-0 text-12px text-status-danger" role="alert">
        {{ t('firewall.sheet.invalid') }}
      </p>
      <p v-if="needsDestination" class="m-0 flex items-start gap-6px text-12px text-ink-tertiary">
        <OnIcon name="info" :size="13" class="mt-2px shrink-0" />{{ t('firewall.sheet.needDestination') }}
      </p>
      <p class="m-0 text-11.5px leading-[1.55] text-ink-tertiary">{{ t('firewall.sheet.order') }}</p>
    </form>
    <template #footer>
      <OnButton variant="ghost" @click="emit('close')">{{ t('common.cancel') }}</OnButton>
      <OnButton variant="primary" :loading="saving" :disabled="touched && !ready" @click="save">{{
        t('firewall.sheet.save')
      }}</OnButton>
    </template>
  </OnSheet>
</template>
