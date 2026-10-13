<script setup lang="ts">
import { LOCALE_METADATA, type Locale } from '@opennavo/shared';

const { locale, t, setLocale, loadLocaleMessages } = useI18n();
const suggestion = ref<Locale | null>(null);
const chosen = useState('onv-language-choice', () => false);
const busy = ref(false);
// Shared so the analytics consent banner waits until this prompt is gone.
const showing = useState('onv-language-suggestion', () => false);
watchEffect(() => {
  showing.value = Boolean(suggestion.value && !chosen.value);
});
const suggestedRoute = computed(() => (suggestion.value ? toRouteLocale(suggestion.value) : locale.value));
const suggestedName = computed(() => (suggestion.value ? LOCALE_METADATA[suggestion.value].name : ''));

onMounted(async () => {
  const target = suggestedLanguage(
    navigator.languages,
    toAppLocale(locale.value),
    readLanguagePreference(LANGUAGE_CHOICE_KEY),
    readLanguagePreference(LANGUAGE_DISMISSED_KEY)
  );
  if (target) {
    await loadLocaleMessages(toRouteLocale(target));
    suggestion.value = target;
  }
});

async function accept() {
  if (!suggestion.value || busy.value) return;
  busy.value = true;
  try {
    await setLocale(toRouteLocale(suggestion.value));
    saveLanguagePreference(LANGUAGE_CHOICE_KEY, suggestion.value);
    chosen.value = true;
  } finally {
    busy.value = false;
  }
}

function dismiss() {
  saveLanguagePreference(LANGUAGE_DISMISSED_KEY, '1');
  suggestion.value = null;
}
</script>

<template>
  <section
    v-if="suggestion && !chosen"
    :lang="suggestion"
    role="status"
    aria-live="polite"
    data-testid="language-suggestion"
    class="fixed bottom-16px left-16px right-16px z-50 mx-auto flex max-w-560px flex-wrap items-center gap-12px rounded-default border border-line-default bg-surface-raised p-16px shadow-popover"
  >
    <p class="min-w-0 flex-1 basis-200px text-14px text-ink-primary">
      {{ t('languageSuggestion.message', { language: suggestedName }, { locale: suggestedRoute }) }}
    </p>
    <div class="flex flex-wrap gap-12px">
      <button
        type="button"
        :disabled="busy"
        class="rounded-default text-14px text-brand-coral focus-visible:shadow-focus-ring"
        @click="accept"
      >
        {{ t('languageSuggestion.switch', { language: suggestedName }, { locale: suggestedRoute }) }}
      </button>
      <button
        type="button"
        class="rounded-default text-14px text-ink-secondary focus-visible:shadow-focus-ring"
        @click="dismiss"
      >
        {{ t('languageSuggestion.dismiss', {}, { locale: suggestedRoute }) }}
      </button>
    </div>
  </section>
</template>
