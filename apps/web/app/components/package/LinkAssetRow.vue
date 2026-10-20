<script setup lang="ts">
import { formatBytes, shownChip } from '@opennavo/shared';
import { OnButton } from '@opennavo/ui';
import { trackEvent } from '~/utils/analytics';
import type { ReleaseAsset } from '~/utils/linkApp';

// One release file of a link app: its name, size and chip, and a button to the developer's own address.
const props = defineProps<{ asset: ReleaseAsset }>();

const { t, locale } = useI18n();
const meta = computed(() => {
  const chip = shownChip(props.asset);
  return [
    formatBytes(props.asset.size, { locale: toAppLocale(locale.value) }),
    chip ? t(`package.link.arch.${chip}`) : null
  ]
    .filter(Boolean)
    .join(' · ');
});
</script>

<template>
  <li class="flex items-center gap-8px border-t border-t-solid border-line-subtle py-8px first:border-t-0">
    <div class="min-w-0 flex-1">
      <p class="m-0 truncate font-mono text-12px text-ink-primary" :title="asset.name">{{ asset.name }}</p>
      <p class="m-0 text-11.5px text-ink-tertiary">{{ meta }}</p>
    </div>
    <OnButton
      size="xs"
      icon="arrow-up-right"
      :href="asset.url"
      target="_blank"
      rel="noopener noreferrer"
      @click="trackEvent('get_app')"
    >
      {{ t('package.link.download') }}
    </OnButton>
  </li>
</template>
