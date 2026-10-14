<script setup lang="ts">
import { ApiError, unwrap } from '@opennavo/api';
import type { PublicComponents } from '@opennavo/api';
import { isPackageKind, isValidToken } from '@opennavo/shared';
import { OnButton, OnCodeText, OnEmpty } from '@opennavo/ui';

type FeedbackType = PublicComponents['schemas']['FeedbackCreateRequest']['type'];

// Feedback (05 §4): type, optional related package from details/search, 5–2000-character body, optional contact; website is a honeypot.
const TYPES: readonly FeedbackType[] = [
  'wrong_info',
  'install_failed',
  'broken_link',
  'suggest_package',
  'translation',
  'other'
];

const { t } = useI18n();
const api = useApi();
const route = useRoute();
const localePath = useLocalePath();
const toasts = useToasts();
const NuxtLink = resolveComponent('NuxtLink');
const ids = { type: useId(), content: useId(), contact: useId(), hint: useId() };

const first = (value: unknown) => (Array.isArray(value) ? value[0] : value);
const queryType = first(route.query.type);
const queryKind = first(route.query.kind);
const queryToken = first(route.query.token);
const queryText = first(route.query.q);

const type = ref<FeedbackType>(
  typeof queryType === 'string' && TYPES.includes(queryType as FeedbackType)
    ? (queryType as FeedbackType)
    : 'wrong_info'
);
const pkg = ref(
  typeof queryKind === 'string' &&
    isPackageKind(queryKind) &&
    typeof queryToken === 'string' &&
    isValidToken(queryToken)
    ? { kind: queryKind, token: queryToken }
    : null
);
const content = ref(
  typeof queryText === 'string' && queryText ? t('feedback.suggestPrefill', { q: queryText.slice(0, 64) }) : ''
);
const contact = ref('');
const website = ref('');
const submitting = ref(false);
const done = ref(false);
const errorText = ref('');

const length = computed(() => Array.from(content.value.trim()).length);
const valid = computed(() => length.value >= 5 && length.value <= 2000 && contact.value.length <= 200);

async function submit() {
  if (!valid.value || submitting.value) return;
  submitting.value = true;
  errorText.value = '';
  try {
    await unwrap(
      api.POST('/feedback', {
        // Omit absent fields rather than null, matching contract examples.
        body: {
          type: type.value,
          ...(pkg.value ? { packageKind: pkg.value.kind, packageToken: pkg.value.token } : {}),
          content: content.value.trim(),
          ...(contact.value.trim() ? { contact: contact.value.trim() } : {}),
          platform: 'web',
          website: website.value
        }
      })
    );
    done.value = true;
  } catch (reason) {
    errorText.value =
      reason instanceof ApiError && reason.status === 429
        ? t('feedback.rateLimited')
        : reason instanceof ApiError
          ? reason.msg
          : t('feedback.failed');
    toasts.push({ tone: 'danger', title: errorText.value });
  } finally {
    submitting.value = false;
  }
}

usePageSeo({ title: () => t('feedback.metaTitle'), description: () => t('feedback.description') });
</script>

<template>
  <div class="mx-auto box-border flex max-w-720px flex-col gap-20px pt-14px">
    <PageHeader :title="t('feedback.title')" :description="t('feedback.description')" />
    <OnEmpty
      v-if="done"
      icon="lucide:check"
      :title="t('feedback.doneTitle')"
      :description="t('feedback.doneDescription')"
    >
      <template #actions>
        <OnButton :href="localePath('/')" :link-as="NuxtLink">{{ t('feedback.backHome') }}</OnButton>
      </template>
    </OnEmpty>
    <form v-else class="flex flex-col gap-16px" novalidate @submit.prevent="submit">
      <div class="flex flex-col gap-6px">
        <label :for="ids.type" class="text-13px font-500 text-ink-primary">{{ t('feedback.type') }}</label>
        <select
          :id="ids.type"
          v-model="type"
          class="h-36px max-w-320px rounded-default border border-solid border-line-default bg-component-search-bg px-10px font-sans text-13px text-ink-primary outline-none focus-visible:shadow-focus-ring"
        >
          <option v-for="item in TYPES" :key="item" :value="item">{{ t(`feedback.types.${item}`) }}</option>
        </select>
      </div>
      <p v-if="pkg" class="m-0 flex items-center gap-8px text-13px text-ink-secondary">
        {{ t('feedback.package') }}
        <code class="rounded-tiny bg-surface-inset px-6px py-2px font-mono text-12px text-ink-primary"
          ><OnCodeText :text="pkg.token"
        /></code>
        <button
          type="button"
          class="m-0 border-none bg-transparent p-0 font-sans text-12.5px text-ink-tertiary underline outline-none hover:text-ink-primary focus-visible:shadow-focus-ring"
          @click="pkg = null"
        >
          {{ t('feedback.removePackage') }}
        </button>
      </p>
      <div class="flex flex-col gap-6px">
        <label :for="ids.content" class="text-13px font-500 text-ink-primary">{{ t('feedback.content') }}</label>
        <textarea
          :id="ids.content"
          v-model="content"
          rows="7"
          maxlength="2000"
          required
          :aria-describedby="ids.hint"
          class="box-border resize-y rounded-default border border-solid border-line-default bg-component-search-bg px-12px py-10px font-sans text-13.5px leading-[1.6] text-ink-primary outline-none placeholder:text-ink-tertiary focus-visible:shadow-focus-ring"
          :placeholder="t('feedback.contentPlaceholder')"
        ></textarea>
        <span :id="ids.hint" class="text-12px text-ink-tertiary tabular-nums">{{
          t('feedback.contentHint', { count: length })
        }}</span>
      </div>
      <div class="flex flex-col gap-6px">
        <label :for="ids.contact" class="text-13px font-500 text-ink-primary">{{ t('feedback.contact') }}</label>
        <input
          :id="ids.contact"
          v-model="contact"
          type="text"
          maxlength="200"
          autocomplete="email"
          class="box-border h-36px max-w-420px rounded-default border border-solid border-line-default bg-component-search-bg px-12px font-sans text-13.5px text-ink-primary outline-none placeholder:text-ink-tertiary focus-visible:shadow-focus-ring"
          :placeholder="t('feedback.contactPlaceholder')"
        />
      </div>
      <!-- Honeypot: invisible/unfocusable to humans; server silently discards bot submissions that fill it. -->
      <div class="absolute left-[-9999px] top-auto h-1px w-1px overflow-hidden" aria-hidden="true">
        <label><input v-model="website" type="text" name="website" tabindex="-1" autocomplete="off" /></label>
      </div>
      <p v-if="errorText" class="m-0 text-13px text-status-danger" role="alert">{{ errorText }}</p>
      <div>
        <OnButton type="submit" variant="primary" :loading="submitting" :disabled="!valid">{{
          t('feedback.submit')
        }}</OnButton>
      </div>
    </form>
  </div>
</template>
