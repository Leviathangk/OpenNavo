<script setup lang="ts">
import { joinSentences, leadSentences } from '~/utils/appAnswer';
import type { PackagePage } from '~/utils/packageFacts';

// Lead (T2): what the app is and who makes it, and for a link app where its files come from. Prices,
// chips and requirements are in the facts table right after it, each with its source.
const props = defineProps<{ pkg: PackagePage }>();

const { t, locale } = useI18n();
const text = computed(() => joinSentences(leadSentences(props.pkg, t), toAppLocale(locale.value)));
</script>

<template>
  <p class="m-0 text-13.5px leading-[1.6] text-ink-secondary" data-testid="app-answer">{{ text }}</p>
</template>
