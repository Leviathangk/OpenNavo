<script setup lang="ts">
import type { PackageDetail } from '@opennavo/api';
import { joinSentences, leadSentences } from '~/utils/appAnswer';

// WP9 lead: what the app is, then verified price and minimum macOS, in one or two sentences.
// The header already shows the developer and summary, so they are not repeated here.
const props = defineProps<{ pkg: PackageDetail }>();

const { t, locale } = useI18n();
const text = computed(() => joinSentences(leadSentences(props.pkg, t), toAppLocale(locale.value)));
</script>

<template>
  <p class="m-0 text-13.5px leading-[1.6] text-ink-secondary" data-testid="app-answer">{{ text }}</p>
</template>
