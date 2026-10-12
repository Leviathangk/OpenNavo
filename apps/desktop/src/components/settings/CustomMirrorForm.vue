<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnButton, OnCard } from '@opennavo/ui';
import type { MirrorInput } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';
import { customMirrorInput, customMirrorProbes, MIRROR_FIELDS, validateMirrorDraft } from '@/composables/customMirrors';
import type { CustomMirrorDraft, MirrorErrors } from '@/composables/customMirrors';

const props = defineProps<{ mirror?: MirrorInput; saving: boolean; saveError?: string }>();
const emit = defineEmits<{ save: [mirror: MirrorInput]; close: [] }>();
const { t } = useI18n();
const key = props.mirror?.key ?? `custom-${crypto.randomUUID()}`;
const draft = reactive<CustomMirrorDraft>({
  name: props.mirror?.name ?? '',
  apiDomain: props.mirror?.apiDomain ?? '',
  bottleDomain: props.mirror?.bottleDomain ?? '',
  brewGitRemote: props.mirror?.brewGitRemote ?? '',
  coreGitRemote: props.mirror?.coreGitRemote ?? ''
});
const errors = ref<MirrorErrors>({});
const checking = ref(false);
const checkState = ref<'untested' | 'success' | 'failure'>('untested');
const failedFields = ref<string[]>([]);
const form = ref<HTMLFormElement>();
const advanced = computed(() => Boolean(props.mirror?.brewGitRemote || props.mirror?.coreGitRemote));
let revision = 0;
watch(draft, () => {
  revision++;
  checkState.value = 'untested';
  failedFields.value = [];
  errors.value = {};
});
onMounted(() => form.value?.querySelector<HTMLInputElement>('input')?.focus());

async function validInput() {
  errors.value = validateMirrorDraft(draft);
  if (Object.keys(errors.value).length) {
    await nextTick();
    form.value?.querySelector<HTMLInputElement>('[aria-invalid="true"]')?.focus();
    return null;
  }
  return customMirrorInput(draft, key);
}

async function check() {
  const input = await validInput();
  if (!input || checking.value) return;
  const started = revision;
  checking.value = true;
  const targets = customMirrorProbes(input);
  try {
    const results = await unwrap(commands.mirrorProbe(targets));
    if (started !== revision) return;
    failedFields.value = targets
      .filter(target => !results.find(result => result.key === target.key)?.ok)
      .map(target => t(`settings.customMirrors.${target.name}`));
    checkState.value = failedFields.value.length ? 'failure' : 'success';
  } catch {
    if (started === revision) {
      checkState.value = 'failure';
      failedFields.value = targets.map(target => t(`settings.customMirrors.${target.name}`));
    }
  } finally {
    checking.value = false;
  }
}

async function submit() {
  const input = await validInput();
  if (input && !props.saving) emit('save', input);
}
const inputClass =
  'box-border h-36px w-full min-w-0 rounded-small border border-solid border-line-default bg-surface-control px-10px font-sans text-13px text-ink-primary outline-none focus-visible:shadow-focus-ring';
</script>

<template>
  <OnCard padding="md">
    <form
      ref="form"
      novalidate
      class="flex flex-col gap-16px"
      :aria-label="t(mirror ? 'settings.customMirrors.edit' : 'settings.customMirrors.add')"
      @submit.prevent="submit"
    >
      <header class="flex items-start justify-between gap-12px">
        <div>
          <h3 class="m-0 text-15px font-600 text-ink-primary">
            {{ t(mirror ? 'settings.customMirrors.edit' : 'settings.customMirrors.add') }}
          </h3>
          <p class="m-0 mt-4px text-12.5px text-ink-secondary">
            {{ t('settings.customMirrors.fallback') }}
          </p>
        </div>
        <OnButton
          size="sm"
          variant="ghost"
          icon="x"
          icon-only
          :aria-label="t('common.close')"
          :disabled="saving"
          @click="emit('close')"
        />
      </header>
      <div class="flex flex-col gap-6px">
        <label :for="`${key}-name`" class="text-13px text-ink-primary">{{ t('settings.customMirrors.name') }}</label>
        <input
          :id="`${key}-name`"
          v-model="draft.name"
          :class="inputClass"
          :disabled="saving"
          maxlength="120"
          autocomplete="off"
          :aria-invalid="Boolean(errors.name)"
          :aria-describedby="errors.name ? `${key}-name-error` : undefined"
        />
        <p v-if="errors.name" :id="`${key}-name-error`" class="m-0 text-12px text-status-danger" role="alert">
          {{ t(`settings.customMirrors.${errors.name}`) }}
        </p>
      </div>
      <div v-for="field in MIRROR_FIELDS.slice(0, 2)" :key="field" class="flex flex-col gap-6px">
        <label :for="`${key}-${field}`" class="text-13px text-ink-primary">{{
          t(`settings.customMirrors.${field}`)
        }}</label>
        <input
          :id="`${key}-${field}`"
          v-model="draft[field]"
          type="url"
          :class="inputClass"
          :disabled="saving"
          maxlength="4096"
          :placeholder="t('settings.customMirrors.officialFallback')"
          spellcheck="false"
          autocomplete="off"
          :aria-invalid="Boolean(errors[field])"
          :aria-describedby="errors[field] ? `${key}-${field}-error` : undefined"
        />
        <p v-if="errors[field]" :id="`${key}-${field}-error`" class="m-0 text-12px text-status-danger" role="alert">
          {{ t(`settings.customMirrors.${errors[field]}`) }}
        </p>
      </div>
      <details :open="advanced" class="rounded-default border border-solid border-line-subtle px-12px py-10px">
        <summary class="cursor-pointer text-13px text-ink-primary outline-none focus-visible:shadow-focus-ring">
          {{ t('settings.customMirrors.advanced') }}
        </summary>
        <div v-for="field in MIRROR_FIELDS.slice(2)" :key="field" class="mt-12px flex flex-col gap-6px">
          <label :for="`${key}-${field}`" class="text-13px text-ink-primary">{{
            t(`settings.customMirrors.${field}`)
          }}</label>
          <input
            :id="`${key}-${field}`"
            v-model="draft[field]"
            type="url"
            :class="inputClass"
            :disabled="saving"
            maxlength="4096"
            :placeholder="t('settings.customMirrors.officialFallback')"
            spellcheck="false"
            autocomplete="off"
            :aria-invalid="Boolean(errors[field])"
            :aria-describedby="errors[field] ? `${key}-${field}-error` : undefined"
          />
          <p v-if="errors[field]" :id="`${key}-${field}-error`" class="m-0 text-12px text-status-danger" role="alert">
            {{ t(`settings.customMirrors.${errors[field]}`) }}
          </p>
        </div>
      </details>
      <p v-if="errors.addresses" class="m-0 text-12px text-status-danger" role="alert">
        {{ t('settings.customMirrors.addressRequired') }}
      </p>
      <p v-if="saveError" class="m-0 text-12px text-status-danger" role="alert">{{ saveError }}</p>
      <p class="m-0 text-12px text-ink-secondary">{{ t('settings.customMirrors.probeHint') }}</p>
      <footer class="flex flex-wrap items-center justify-between gap-12px">
        <div class="flex flex-wrap items-center gap-8px">
          <OnButton size="sm" variant="ghost" icon="refresh-cw" :loading="checking" :disabled="saving" @click="check">{{
            t('settings.customMirrors.check')
          }}</OnButton>
          <span
            class="text-12px"
            :class="
              checkState === 'failure'
                ? 'text-status-danger'
                : checkState === 'success'
                  ? 'text-status-success'
                  : 'text-ink-secondary'
            "
            role="status"
          >
            {{
              checking
                ? t('mirrors.probing')
                : checkState === 'failure'
                  ? t('settings.customMirrors.checkFailed', { fields: failedFields.join('、') })
                  : t(`settings.customMirrors.${checkState}`)
            }}
          </span>
        </div>
        <div class="flex gap-8px">
          <OnButton size="sm" :disabled="saving" @click="emit('close')">{{ t('common.cancel') }}</OnButton>
          <OnButton size="sm" variant="accent" type="submit" :loading="saving" :disabled="checking">{{
            t(mirror ? 'settings.customMirrors.save' : 'settings.customMirrors.saveAndUse')
          }}</OnButton>
        </div>
      </footer>
    </form>
  </OnCard>
</template>
