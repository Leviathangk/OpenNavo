<script setup lang="ts">
import { OnButton } from '@opennavo/ui';
import type { OnButtonVariant } from '@opennavo/ui';
import { trackEvent } from '~/utils/analytics';
import { launchDeepLink } from '~/utils/deepLink';

// "Open in OpenNavo" for a preview deep link (F1): the client only finds and shows the item or app on this
// Mac, and nothing changes until the user confirms there. The link is a plain anchor, so it works without
// JavaScript; the hint says it is a preview and points to the download for people without the app.
const props = withDefaults(defineProps<{ href: string; label: string; hint: string; variant?: OnButtonVariant }>(), {
  variant: 'primary'
});

const { t } = useI18n();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');

function open(event: MouseEvent) {
  event.preventDefault();
  trackEvent('open_in_app');
  launchDeepLink(props.href);
}
</script>

<template>
  <div class="flex flex-col gap-8px">
    <div class="flex flex-wrap items-center gap-x-14px gap-y-8px">
      <OnButton :variant="variant" size="sm" icon="arrow-up-right" :href="href" @click="open">{{ label }}</OnButton>
      <NuxtLink
        :to="localePath('/download')"
        class="text-12.5px text-ink-secondary underline-offset-3 hover:text-ink-primary hover:underline"
        >{{ t('backgroundItems.openInApp.download') }}</NuxtLink
      >
    </div>
    <p class="m-0 text-12.5px leading-[1.6] text-ink-tertiary">{{ hint }}</p>
  </div>
</template>
