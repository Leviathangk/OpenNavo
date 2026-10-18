<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useId } from 'vue';
import type { Component } from 'vue';
import OnButton from './OnButton.vue';
import OnIcon from './OnIcon.vue';
import OnLogo from './OnLogo.vue';
import { useUiMessages } from '../composables/locale';

export interface OnTopNavItem {
  label: string;
  href: string;
  /** Current page, derived by the host; components never read routes. */
  active?: boolean;
}

export interface OnTopNavProps {
  items: readonly OnTopNavItem[];
  homeHref?: string;
  /** Client download-page URL. */
  downloadHref: string;
  downloadLabel?: string;
  /** Search-field label; clicking opens the palette. */
  searchLabel?: string;
  /** Right-side shortcut hint, e.g. Command-K. */
  searchShortcut?: string;
  /** Main navigation accessible name. */
  label?: string;
  /** Link component such as NuxtLink; native a by default. */
  linkAs?: string | Component;
}

const props = withDefaults(defineProps<OnTopNavProps>(), { homeHref: '/', linkAs: 'a' });

const emit = defineEmits<{
  /** Search click: host opens the palette. */
  search: [];
}>();

defineSlots<{
  /** Between search and download, e.g. locale selector. */
  extra?: () => unknown;
}>();

const messages = useUiMessages();
const panelId = useId();

const scrolled = ref(false);
const menuOpen = ref(false);
const menuButton = ref<HTMLButtonElement>();

const linkAttrs = (href: string) => (props.linkAs === 'a' ? { href } : { to: href });

function onScroll() {
  scrolled.value = window.scrollY > 0;
}

function closeMenu(restoreFocus = false) {
  if (!menuOpen.value) return;
  menuOpen.value = false;
  if (restoreFocus) menuButton.value?.focus();
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && menuOpen.value) closeMenu(true);
}

// Pages may already be scrolled on refresh/anchor navigation; compute once on mount.
onMounted(() => {
  onScroll();
  window.addEventListener('scroll', onScroll, { passive: true });
  document.addEventListener('keydown', onKeydown);
});

onBeforeUnmount(() => {
  window.removeEventListener('scroll', onScroll);
  document.removeEventListener('keydown', onKeydown);
});

const navLink = (active?: boolean) => [
  'whitespace-nowrap rounded-tiny text-15px no-underline outline-none transition-colors duration-fast ease-standard focus-visible:shadow-focus-ring',
  active ? 'text-ink-primary' : 'text-ink-secondary hover:text-ink-primary'
];
</script>

<template>
  <header
    class="sticky top-0 z-sticky box-border border-b border-b-solid border-line-subtle font-sans transition-colors duration-fast ease-standard"
    :class="scrolled ? 'on-top-nav-scrolled' : 'bg-surface-page'"
  >
    <div class="mx-auto box-border flex h-64px max-w-1240px items-center gap-24px px-24px">
      <component
        :is="linkAs"
        v-bind="linkAttrs(homeHref)"
        class="flex shrink-0 items-center rounded-tiny no-underline outline-none focus-visible:shadow-focus-ring"
        :aria-label="messages.topNav.home"
      >
        <OnLogo wordmark />
      </component>

      <nav
        :aria-label="label ?? messages.topNav.label"
        class="hidden min-w-0 flex-1 items-center justify-center gap-28px lg:flex"
      >
        <component
          :is="linkAs"
          v-for="item in items"
          :key="item.href"
          v-bind="linkAttrs(item.href)"
          :class="navLink(item.active)"
          :aria-current="item.active ? 'page' : undefined"
        >
          {{ item.label }}
        </component>
      </nav>

      <div class="ml-auto flex shrink-0 items-center gap-12px lg:ml-0">
        <button
          type="button"
          class="m-0 box-border hidden h-32px w-240px items-center gap-9px rounded-default border border-solid border-line-subtle bg-component-search-bg px-11px py-0 font-sans text-12.5px text-ink-tertiary outline-none transition-colors duration-fast ease-standard hover:border-line-strong focus-visible:shadow-focus-ring md:flex lg:hidden xl:flex"
          aria-haspopup="dialog"
          @click="emit('search')"
        >
          <OnIcon name="search" :size="16" />
          <span class="min-w-0 flex-1 truncate text-left">{{ searchLabel ?? messages.topNav.search }}</span>
          <kbd
            v-if="searchShortcut"
            class="inline-flex h-18px items-center rounded-tiny bg-surface-raised px-5px font-sans text-11px text-ink-tertiary"
            aria-hidden="true"
          >
            {{ searchShortcut }}
          </kbd>
        </button>
        <!-- Between 1024 and 1279 px the full nav needs the room, so search shrinks to its icon there too. -->
        <OnButton
          class="md:hidden lg:inline-flex xl:hidden"
          variant="ghost"
          size="sm"
          icon="search"
          icon-only
          :aria-label="searchLabel ?? messages.topNav.search"
          @click="emit('search')"
        />
        <slot name="extra" />
        <!-- Control visibility with a wrapper; hidden directly on the button conflicts with inline-flex. -->
        <span class="hidden sm:inline-flex">
          <OnButton variant="primary" size="sm" shape="round" :href="downloadHref" :link-as="linkAs">
            {{ downloadLabel ?? messages.topNav.download }}
          </OnButton>
        </span>
        <button
          ref="menuButton"
          type="button"
          class="m-0 box-border inline-flex h-32px w-32px items-center justify-center rounded-default border-none bg-transparent p-0 text-ink-secondary outline-none transition-colors duration-fast ease-standard hover:text-ink-primary focus-visible:shadow-focus-ring lg:hidden"
          :aria-expanded="menuOpen ? 'true' : 'false'"
          :aria-controls="panelId"
          :aria-label="menuOpen ? messages.topNav.closeMenu : messages.topNav.openMenu"
          @click="menuOpen = !menuOpen"
        >
          <OnIcon :name="menuOpen ? 'x' : 'menu'" :size="18" />
        </button>
      </div>
    </div>

    <div
      v-show="menuOpen"
      :id="panelId"
      class="box-border border-t border-t-solid border-line-subtle bg-surface-page px-24px pb-16px pt-8px lg:hidden"
    >
      <nav :aria-label="label ?? messages.topNav.label" class="flex flex-col">
        <component
          :is="linkAs"
          v-for="item in items"
          :key="item.href"
          v-bind="linkAttrs(item.href)"
          class="flex h-44px items-center"
          :class="navLink(item.active)"
          :aria-current="item.active ? 'page' : undefined"
          @click="closeMenu()"
        >
          {{ item.label }}
        </component>
      </nav>
      <OnButton
        class="mt-8px sm:hidden"
        variant="primary"
        size="sm"
        shape="round"
        :href="downloadHref"
        :link-as="linkAs"
        @click="closeMenu()"
      >
        {{ downloadLabel ?? messages.topNav.download }}
      </OnButton>
    </div>
  </header>
</template>

<style>
.on-top-nav-scrolled {
  background-color: var(--on-component-top-nav-scrolled);
  backdrop-filter: blur(var(--on-effect-backdrop-blur));
  -webkit-backdrop-filter: blur(var(--on-effect-backdrop-blur));
}
</style>
