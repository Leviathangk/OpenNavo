<script setup lang="ts">
import type { Component } from 'vue';
import type { PackageKind } from '@opennavo/shared';
import OnAboutSection from './OnAboutSection.vue';
import OnAppIcon from './OnAppIcon.vue';
import type { OnImageVariant } from '../utils/image';
import OnCodeText from './OnCodeText.vue';
import OnInstallCommand from './OnInstallCommand.vue';
import OnKeyValueList from './OnKeyValueList.vue';
import type { OnKeyValueItem } from './OnKeyValueList.vue';
import OnScreenshotGallery from './OnScreenshotGallery.vue';
import type { OnScreenshot } from './OnScreenshotGallery.vue';

// Shared overview (08 §10.5, mockup 03, ADR-017): left screenshots → introduction → host sections (offline/latest notes) → caveats.
// Right install command → information → host monthly installs → similar apps; host supplies data/links.
export interface OnRelatedPackage {
  key: string;
  kind: PackageKind;
  token: string;
  name: string;
  src?: string | null;
  /** Stored WebP renditions of the icon (X3). */
  variants?: readonly OnImageVariant[] | null;
  accent?: string | null;
  description?: string | null;
  href: string;
}

export interface OnPackageOverviewProps {
  screenshots?: readonly OnScreenshot[];
  screenshotsLabel?: string;
  /** App name for the alternative text of screenshots without a caption. */
  screenshotsName?: string;
  /** Whether introduction exists, rendered through about slot. */
  hasAbout?: boolean;
  caveats?: string | null;
  caveatsTitle?: string;
  /** Install command; link apps have none and fill the `download` slot instead. */
  command?: string;
  info: readonly OnKeyValueItem[];
  related?: readonly OnRelatedPackage[];
  relatedTitle?: string;
  linkAs?: string | Component;
}

const props = withDefaults(defineProps<OnPackageOverviewProps>(), {
  screenshots: () => [],
  hasAbout: false,
  related: () => [],
  linkAs: 'a'
});

const emit = defineEmits<{ copied: [command: string] }>();

const linkAttrs = (href: string) => (props.linkAs === 'a' ? { href } : { to: href });

defineSlots<{
  /** Top of the main column, before screenshots, e.g. the web's facts table. */
  top?: () => unknown;
  /** Markdown introduction. */
  about?: () => unknown;
  /** After introduction/before caveats, e.g. offline/latest-release cards. */
  main?: () => unknown;
  /** Top of the side column in place of the install command, e.g. link apps' download panel. */
  download?: () => unknown;
  /** Shared package specification card, rendered in the side column. */
  spec?: () => unknown;
  /** After information, e.g. monthly installs. */
  aside?: () => unknown;
  /** End of the side column, after similar apps, e.g. the collections an app is in. */
  asideEnd?: () => unknown;
}>();
</script>

<template>
  <div class="grid grid-cols-1 gap-20px pt-4px lg:grid-cols-[minmax(0,1fr)_320px]">
    <div class="flex min-w-0 flex-col gap-16px">
      <slot name="top" />
      <OnScreenshotGallery
        v-if="screenshots.length"
        :items="screenshots"
        :label="screenshotsLabel"
        :name="screenshotsName"
      />
      <OnAboutSection v-if="hasAbout">
        <slot name="about" />
      </OnAboutSection>
      <slot name="main" />
      <section
        v-if="caveats"
        class="rounded-big border border-solid border-status-warning bg-surface-card px-18px py-14px"
      >
        <h2 class="m-0 text-13.5px font-600 text-status-warning">{{ caveatsTitle }}</h2>
        <pre class="m-0 mt-8px whitespace-pre-wrap font-mono text-12px leading-[1.6] text-ink-secondary"><OnCodeText
          :text="caveats"
        /></pre>
      </section>
    </div>
    <aside class="flex min-w-0 flex-col gap-14px">
      <slot name="download" />
      <section
        v-if="command"
        class="rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-14px"
      >
        <OnInstallCommand :command="command" @copied="emit('copied', $event)" />
      </section>
      <slot name="spec" />
      <section
        v-if="info.length"
        class="rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-4px"
      >
        <OnKeyValueList :items="info" />
      </section>
      <slot name="aside" />
      <!-- Similar apps (.sim): 32 icon, name, summary, row dividers; whole row clickable. -->
      <section v-if="related.length" class="mt-4px">
        <h2 class="m-0 text-headline text-ink-primary">{{ relatedTitle }}</h2>
        <ul class="m-0 mt-8px list-none p-0">
          <li
            v-for="pkg in related"
            :key="pkg.key"
            class="relative flex items-center gap-10px border-t border-t-solid border-line-subtle py-8px first:border-t-0"
          >
            <OnAppIcon
              :kind="pkg.kind"
              :token="pkg.token"
              :name="pkg.name"
              :src="pkg.src"
              :variants="pkg.variants"
              :accent="pkg.accent"
              :size="32"
            />
            <span class="min-w-0 flex-1">
              <component
                :is="linkAs"
                v-bind="linkAttrs(pkg.href)"
                class="on-stretched block truncate text-12.5px font-600 text-ink-primary no-underline outline-none focus-visible:underline"
              >
                {{ pkg.name }}
              </component>
              <span v-if="pkg.description" class="block truncate text-11px text-ink-tertiary">{{
                pkg.description
              }}</span>
            </span>
          </li>
        </ul>
      </section>
      <slot name="asideEnd" />
    </aside>
  </div>
</template>
