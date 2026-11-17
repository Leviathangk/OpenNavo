<script setup lang="ts">
import { OnIcon } from '@opennavo/ui';
import { TOPIC_LINKS } from '~/utils/securityTopics';

// Cross-links between /security, its topic pages (F8) and the background items encyclopedia (F1), so each
// topic is one click from the others. The current page is left out.
const props = withDefaults(
  defineProps<{ title: string; current?: (typeof TOPIC_LINKS)[number]['key']; align?: 'center' | 'left' }>(),
  { align: 'center' }
);

const { t } = useI18n();
const localePath = useLocalePath();
const links = computed(() => TOPIC_LINKS.filter(link => link.key !== props.current));
</script>

<template>
  <section
    class="mx-auto box-border w-full px-24px"
    :class="
      props.align === 'left'
        ? 'grid max-w-1040px gap-16px lg:grid-cols-[260px_minmax(0,1fr)] lg:gap-x-48px'
        : 'max-w-1240px'
    "
    aria-labelledby="security-topic-links"
    data-reveal
  >
    <h2
      id="security-topic-links"
      class="m-0 text-title1 text-ink-primary"
      :class="props.align === 'left' ? 'text-left' : 'text-center md:text-section'"
    >
      {{ title }}
    </h2>
    <ul
      class="m-0 grid list-none gap-12px p-0"
      :class="props.align === 'left' ? 'md:grid-cols-3' : 'mt-32px md:grid-cols-2 lg:grid-cols-4'"
    >
      <li v-for="link in links" :key="link.key" class="flex">
        <NuxtLink
          :to="localePath(link.path)"
          class="flex w-full flex-col gap-10px rounded-big border border-solid border-line-subtle bg-surface-card px-20px pb-20px pt-18px text-inherit no-underline transition-colors duration-fast ease-standard hover:border-line-strong focus-visible:shadow-focus-ring"
        >
          <span
            class="grid h-36px w-36px place-items-center rounded-default bg-status-success-subtle text-status-success"
            aria-hidden="true"
          >
            <OnIcon :name="link.icon" :size="18" />
          </span>
          <span class="flex items-center gap-6px text-15px font-600 text-ink-primary">
            {{ t(`securityTopics.links.${link.key}.title`) }}
            <OnIcon name="arrow-right" :size="14" class="text-ink-tertiary" />
          </span>
          <span class="text-13.5px leading-[1.65] text-ink-secondary">{{
            t(`securityTopics.links.${link.key}.body`)
          }}</span>
        </NuxtLink>
      </li>
    </ul>
  </section>
</template>
