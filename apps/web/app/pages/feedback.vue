<script setup lang="ts">
import { ApiError, unwrap } from '@opennavo/api';
import { isPackageKind, isValidToken } from '@opennavo/shared';
import { OnButton, OnCodeText, OnEmpty, OnIcon } from '@opennavo/ui';
import {
  byteLength,
  canSend,
  charLength,
  FEEDBACK_LIMITS,
  feedbackBody,
  feedbackProblems,
  showsProblemFields
} from '~/utils/feedbackForm';
import type { FeedbackType } from '~/utils/feedbackForm';

// Feedback (05 §4, F7): type, optional related package from details/search, 5–2000-character body, optional
// contact; website is a honeypot. Problem reports (install failed, other) add the app's version, the macOS
// version, the symptom and an optional diagnostic log; any report can carry up to three https links. The
// limits are the API's own (utils/feedbackForm.ts). Submissions are private: never published, only a lead
// for the known issues Hermes checks against official sources.
const TYPES: readonly FeedbackType[] = [
  'wrong_info',
  'install_failed',
  'broken_link',
  'suggest_package',
  'translation',
  'other'
];

const { t, locale } = useI18n();
const api = useApi();
const route = useRoute();
const localePath = useLocalePath();
const toasts = useToasts();
const NuxtLink = resolveComponent('NuxtLink');
const ids = {
  type: useId(),
  content: useId(),
  contact: useId(),
  hint: useId(),
  packageVersion: useId(),
  macosVersion: useId(),
  symptom: useId(),
  symptomHint: useId(),
  log: useId(),
  logHint: useId(),
  links: useId(),
  linksHint: useId(),
  privacy: useId()
};

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
const packageVersion = ref('');
const macosVersion = ref('');
const symptom = ref('');
const diagnosticLog = ref('');
const links = ref<string[]>(['']);
const submitting = ref(false);
const done = ref(false);
const errorText = ref('');

const problem = computed(() => showsProblemFields(type.value));
const draft = computed(() => ({
  type: type.value,
  pkg: pkg.value,
  content: content.value,
  contact: contact.value,
  website: website.value,
  packageVersion: packageVersion.value,
  macosVersion: macosVersion.value,
  symptom: symptom.value,
  diagnosticLog: diagnosticLog.value,
  links: links.value
}));
const problems = computed(() => feedbackProblems(draft.value));
const valid = computed(() => canSend(problems.value));

const length = computed(() => charLength(content.value.trim()));
const symptomLength = computed(() => charLength(symptom.value.trim()));
// Kilobytes for the log's size hint, rounded up so the limit reads 16 KB exactly.
const logKilobytes = computed(() => Math.ceil(byteLength(diagnosticLog.value.trim()) / 1024));

function addLink() {
  if (links.value.length < FEEDBACK_LIMITS.links) links.value = [...links.value, ''];
}
function removeLink(index: number) {
  const next = links.value.filter((_, position) => position !== index);
  links.value = next.length ? next : [''];
}
function setLink(index: number, value: string) {
  links.value = links.value.map((link, position) => (position === index ? value : link));
}

async function submit() {
  if (!valid.value || submitting.value) return;
  submitting.value = true;
  errorText.value = '';
  try {
    await unwrap(api.POST('/feedback', { body: feedbackBody(draft.value) }));
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

const inputClass =
  'box-border h-36px rounded-default border border-solid border-line-default bg-component-search-bg px-12px font-sans text-13.5px text-ink-primary outline-none placeholder:text-ink-tertiary focus-visible:shadow-focus-ring';

usePageSeo({ title: () => t('feedback.metaTitle'), description: () => t('feedback.description') });
// Share image (WP13): the site's default card, as on the home page.
defineOgImage('OgDefaultSatori', {
  locale: toAppLocale(locale.value),
  variant: 'home',
  title: t('og.homeTitle'),
  description: t('og.homeDescription'),
  ...useOgFrame()
});
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
    <form v-else class="flex flex-col gap-16px" novalidate :aria-describedby="ids.privacy" @submit.prevent="submit">
      <p
        :id="ids.privacy"
        class="m-0 flex items-start gap-8px rounded-big bg-status-info-subtle px-14px py-10px text-13px leading-[1.6] text-ink-secondary"
      >
        <OnIcon name="lock" :size="15" class="mt-3px shrink-0 text-status-info" />
        <span>{{ t('feedback.private') }}</span>
      </p>
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
      <template v-if="problem">
        <div class="grid gap-16px sm:grid-cols-2">
          <div class="flex flex-col gap-6px">
            <label :for="ids.packageVersion" class="text-13px font-500 text-ink-primary">{{
              t('feedback.packageVersion')
            }}</label>
            <input
              :id="ids.packageVersion"
              v-model="packageVersion"
              type="text"
              maxlength="100"
              autocomplete="off"
              :class="inputClass"
              :placeholder="t('feedback.packageVersionPlaceholder')"
            />
          </div>
          <div class="flex flex-col gap-6px">
            <label :for="ids.macosVersion" class="text-13px font-500 text-ink-primary">{{
              t('feedback.macosVersion')
            }}</label>
            <input
              :id="ids.macosVersion"
              v-model="macosVersion"
              type="text"
              maxlength="100"
              autocomplete="off"
              :class="inputClass"
              :placeholder="t('feedback.macosVersionPlaceholder')"
            />
          </div>
        </div>
        <div class="flex flex-col gap-6px">
          <label :for="ids.symptom" class="text-13px font-500 text-ink-primary">{{ t('feedback.symptom') }}</label>
          <input
            :id="ids.symptom"
            v-model="symptom"
            type="text"
            maxlength="500"
            autocomplete="off"
            :aria-describedby="ids.symptomHint"
            :aria-invalid="problems.fields.symptom ? 'true' : undefined"
            :class="inputClass"
            :placeholder="t('feedback.symptomPlaceholder')"
          />
          <span :id="ids.symptomHint" class="text-12px text-ink-tertiary tabular-nums">{{
            t('feedback.symptomHint', { count: symptomLength })
          }}</span>
        </div>
      </template>
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
      <div v-if="problem" class="flex flex-col gap-6px">
        <label :for="ids.log" class="text-13px font-500 text-ink-primary">{{ t('feedback.diagnosticLog') }}</label>
        <textarea
          :id="ids.log"
          v-model="diagnosticLog"
          rows="5"
          spellcheck="false"
          :aria-describedby="ids.logHint"
          :aria-invalid="problems.fields.diagnosticLog ? 'true' : undefined"
          class="box-border resize-y rounded-default border border-solid border-line-default bg-component-search-bg px-12px py-10px font-mono text-12px leading-[1.6] text-ink-primary outline-none placeholder:font-sans placeholder:text-ink-tertiary focus-visible:shadow-focus-ring"
          :placeholder="t('feedback.diagnosticLogPlaceholder')"
        ></textarea>
        <span :id="ids.logHint" class="text-12px leading-[1.6] text-ink-tertiary">
          {{ t('feedback.diagnosticLogHint') }}
          <span class="tabular-nums">{{ ` ${t('feedback.diagnosticLogSize', { size: logKilobytes })}` }}</span>
        </span>
        <span v-if="problems.fields.diagnosticLog" class="text-12px text-status-danger" role="alert">{{
          t('feedback.errors.logSize')
        }}</span>
      </div>
      <fieldset class="m-0 flex flex-col gap-6px border-none p-0" :aria-describedby="ids.linksHint">
        <legend class="mb-6px p-0 text-13px font-500 text-ink-primary">{{ t('feedback.links') }}</legend>
        <div v-for="(link, index) in links" :key="index" class="flex items-center gap-8px">
          <input
            :value="link"
            type="url"
            inputmode="url"
            maxlength="2048"
            autocomplete="off"
            spellcheck="false"
            :aria-label="t('feedback.linkLabel', { number: index + 1 })"
            :aria-invalid="problems.links[index] === false ? 'true' : undefined"
            :class="[inputClass, 'min-w-0 flex-1']"
            :placeholder="t('feedback.linkPlaceholder')"
            @input="event => setLink(index, (event.target as HTMLInputElement).value)"
          />
          <OnButton
            v-if="links.length > 1 || link"
            variant="ghost"
            size="sm"
            icon="x"
            icon-only
            :aria-label="t('feedback.removeLink', { number: index + 1 })"
            @click="removeLink(index)"
          />
        </div>
        <div>
          <OnButton
            v-if="links.length < FEEDBACK_LIMITS.links"
            variant="ghost"
            size="sm"
            icon="plus"
            @click="addLink"
            >{{ t('feedback.addLink') }}</OnButton
          >
        </div>
        <span :id="ids.linksHint" class="text-12px leading-[1.6] text-ink-tertiary">{{ t('feedback.linksHint') }}</span>
        <span v-if="problems.links.includes(false)" class="text-12px text-status-danger" role="alert">{{
          t('feedback.errors.link')
        }}</span>
        <span v-else-if="problems.tooManyLinks" class="text-12px text-status-danger" role="alert">{{
          t('feedback.errors.tooManyLinks')
        }}</span>
      </fieldset>
      <div class="flex flex-col gap-6px">
        <label :for="ids.contact" class="text-13px font-500 text-ink-primary">{{ t('feedback.contact') }}</label>
        <input
          :id="ids.contact"
          v-model="contact"
          type="text"
          maxlength="200"
          autocomplete="email"
          :class="[inputClass, 'max-w-420px']"
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
