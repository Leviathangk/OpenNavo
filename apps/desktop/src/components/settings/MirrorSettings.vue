<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnButton, OnCard, OnMenu, OnModal } from '@opennavo/ui';
import type { MirrorInput } from '@/ipc/bindings';
import CustomMirrorForm from './CustomMirrorForm.vue';
import MirrorList from '@/components/mirrors/MirrorList.vue';
import MirrorNotices from '@/components/mirrors/MirrorNotices.vue';
import { toMirrorChoice, useMirrorOptions } from '@/composables/useMirrorOptions';
import type { MirrorOption } from '@/composables/useMirrorOptions';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useSettingsStore } from '@/stores';
import { CUSTOM_MIRROR_LIMIT } from '@/composables/customMirrors';

// Download sources (mockup 07, 06 §6.2): backend mirrors, probe then select; save full MirrorChoice URLs immediately.
// Copy terminal environment variables affects only the user's terminal, never shell configuration files.
const { t } = useI18n();
const settings = useSettingsStore();
const toasts = useToasts();
const { errorText } = usePackageState();
const { options, failed, probes, probing, fastest, suggestion, find, load, probe, unreachable } = useMirrorOptions(
  () => true,
  () => settings.value?.customMirrors ?? []
);
const formOpen = ref(false);
const editing = ref<MirrorInput>();
const saving = ref(false);
const saveError = ref('');
const removing = ref<MirrorOption>();
const removeError = ref('');
const removeOpen = computed({
  get: () => Boolean(removing.value),
  set: value => {
    if (!value) removing.value = undefined;
  }
});
const atLimit = computed(() => (settings.value?.customMirrors.length ?? 0) >= CUSTOM_MIRROR_LIMIT);

function add() {
  editing.value = undefined;
  saveError.value = '';
  formOpen.value = true;
}

function selectAction(option: MirrorOption, action: string) {
  if (action === 'edit') {
    editing.value = settings.value?.customMirrors.find(item => item.key === option.key);
    saveError.value = '';
    formOpen.value = true;
  } else if (action === 'delete') {
    removeError.value = '';
    removing.value = option;
  }
}

async function saveCustom(input: MirrorInput) {
  saving.value = true;
  saveError.value = '';
  try {
    await settings.saveCustomMirror(input, !editing.value);
    formOpen.value = false;
    toasts.push({ tone: 'success', title: t('settings.saved') });
    void probe();
  } catch (error) {
    saveError.value = errorText(error);
  } finally {
    saving.value = false;
  }
}

async function remove() {
  const option = removing.value;
  if (!option || saving.value) return;
  saving.value = true;
  try {
    await settings.removeCustomMirror(option.key);
    if (editing.value?.key === option.key) formOpen.value = false;
    removing.value = undefined;
    toasts.push({ tone: 'success', title: t('settings.saved') });
    void probe();
  } catch (error) {
    removeError.value = errorText(error);
  } finally {
    saving.value = false;
  }
}

onMounted(async () => {
  await load();
  await probe();
});

async function retry() {
  await load();
  await probe();
}

const current = computed(() => settings.value?.mirror.key);

async function choose(option: MirrorOption) {
  try {
    await settings.update('mirror', toMirrorChoice(option));
    toasts.push({ tone: 'success', title: t('settings.saved') });
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

function useSuggested() {
  const option = suggestion.value && find(suggestion.value.option.key);
  if (option) void choose(option);
}

interface EnvLine {
  command: 'export' | 'unset';
  name: string;
  value?: string;
}

// One environment variable per line, shared by display and clipboard. Unset official-source variables individually.
// Do not combine into a long command: neither wrapping nor scrolling would read well.
const envLines = computed<EnvLine[]>(() => {
  const mirror = settings.value?.mirror;
  if (!mirror) return [];
  const pairs: [string, string | null][] = [
    ['HOMEBREW_API_DOMAIN', mirror.apiDomain],
    ['HOMEBREW_BOTTLE_DOMAIN', mirror.bottleDomain],
    ['HOMEBREW_BREW_GIT_REMOTE', mirror.brewGitRemote],
    ['HOMEBREW_CORE_GIT_REMOTE', mirror.coreGitRemote]
  ];
  if (mirror.key === 'official' || pairs.every(([, value]) => !value))
    return pairs.map(([name]) => ({ command: 'unset', name }));
  return pairs.flatMap(([name, value]) => (value ? [{ command: 'export', name, value }] : []));
});

// Single-quote URLs and escape embedded quotes to prevent truncation or expansion when pasted into a terminal.
const quote = (value: string) => `'${value.replaceAll("'", "'\\''")}'`;

const env = computed(() =>
  envLines.value
    .map(({ command, name, value }) => `${command} ${name}${value === undefined ? '' : `=${quote(value)}`}`)
    .join('\n')
);

async function copyEnv() {
  await navigator.clipboard.writeText(env.value);
  toasts.push({ tone: 'success', title: t('settings.mirrors.envCopied') });
}
</script>

<template>
  <div class="flex flex-col gap-16px">
    <OnCard padding="none">
      <header
        class="flex flex-wrap items-start justify-between gap-12px border-b border-b-solid border-line-subtle px-18px py-14px"
      >
        <div>
          <h2 class="m-0 text-15px font-600 text-ink-primary">{{ t('settings.mirrors.title') }}</h2>
          <p class="m-0 mt-4px text-12.5px text-ink-tertiary">
            {{ t('settings.mirrors.description') }}
          </p>
        </div>
        <div class="flex flex-wrap gap-8px">
          <OnButton
            variant="secondary"
            size="sm"
            icon="refresh-cw"
            :loading="probing"
            :disabled="saving"
            @click="probe"
          >
            {{ probing ? t('mirrors.probing') : t('mirrors.probe') }}
          </OnButton>
          <OnButton size="sm" icon="plus" :disabled="saving || atLimit || formOpen" @click="add">{{
            t('settings.customMirrors.add')
          }}</OnButton>
        </div>
      </header>
      <MirrorList
        :options="options"
        :probes="probes"
        :probing="probing"
        :selected="current"
        :fastest="fastest"
        :disabled="saving"
        :label="t('settings.mirrors.title')"
        @select="choose"
      >
        <template #actions="{ option }">
          <OnMenu
            v-if="option.local"
            size="sm"
            :label="t('settings.customMirrors.actions', { name: option.name })"
            :items="[
              { key: 'edit', label: t('settings.customMirrors.edit'), disabled: saving },
              {
                key: 'delete',
                label: t('settings.customMirrors.delete'),
                icon: 'trash',
                danger: true,
                disabled: saving
              }
            ]"
            @select="action => selectAction(option, action)"
          />
        </template>
      </MirrorList>
      <MirrorNotices
        class="border-t border-t-solid border-line-subtle px-18px py-12px"
        :config-failed="failed"
        :suggestion="current === 'official' ? suggestion : undefined"
        :selected-unreachable="Boolean(current) && unreachable(current ?? '')"
        @retry="retry"
        @use-suggested="useSuggested"
      />
    </OnCard>
    <p class="m-0 text-12.5px text-ink-secondary">
      {{ t('settings.customMirrors.localHint')
      }}<span v-if="atLimit"> {{ t('settings.customMirrors.limit', { count: CUSTOM_MIRROR_LIMIT }) }}</span>
    </p>
    <CustomMirrorForm
      v-if="formOpen"
      :key="editing?.key ?? 'new'"
      :mirror="editing"
      :saving="saving"
      :save-error="saveError"
      @save="saveCustom"
      @close="formOpen = false"
    />
    <OnCard padding="md" class="flex flex-col gap-10px">
      <p class="m-0 text-12.5px text-ink-tertiary">{{ t('settings.mirrors.envHint') }}</p>
      <!-- Wrap long URLs after = first, then by character if necessary; never show a scrollbar. -->
      <pre
        class="m-0 whitespace-pre-wrap break-words rounded-default bg-surface-inset px-14px py-12px font-mono text-12px leading-[1.7] text-ink-secondary"
      ><code><template v-for="(line, index) in envLines" :key="line.name">{{ index ? '\n' : '' }}{{ line.command }} <span class="text-ink-primary">{{ line.name }}</span><template v-if="line.value !== undefined">=<wbr />{{ quote(line.value) }}</template></template></code></pre>
      <div class="flex justify-end">
        <OnButton variant="secondary" size="sm" icon="copy" @click="copyEnv">{{
          t('settings.mirrors.copyEnv')
        }}</OnButton>
      </div>
    </OnCard>
    <OnModal
      v-model:open="removeOpen"
      :title="t('settings.customMirrors.delete')"
      :dismissible="!saving"
      :closable="!saving"
      role="alertdialog"
    >
      <p class="m-0">{{ t('settings.customMirrors.deleteConfirm', { name: removing?.name }) }}</p>
      <p v-if="current === removing?.key" class="mb-0 mt-8px">
        {{ t('settings.customMirrors.deleteActive') }}
      </p>
      <p v-if="removeError" class="mb-0 mt-8px text-status-danger" role="alert">
        {{ removeError }}
      </p>
      <template #footer>
        <OnButton :disabled="saving" @click="removeOpen = false">{{ t('common.cancel') }}</OnButton>
        <OnButton variant="danger" :loading="saving" @click="remove">{{ t('settings.customMirrors.delete') }}</OnButton>
      </template>
    </OnModal>
  </div>
</template>
