<script setup lang="ts">
import { LOCALE_METADATA, LOCALES } from '@opennavo/shared';
import type { SiteLocale } from '@opennavo/shared';
import { canonicalPageUrl } from '~/utils/catalogQuery';
import { languageNavLocales, settledPageData } from '~/utils/languageNav';
import { hreflangsFor } from '~~/shared/utils/hreflang';

// Crawlable language links (C4, plan 4.7): one real link per language to this page's equivalent, for the
// languages the page allows (page:hreflangs; null means all), so they match its hreflang alternates. The
// language menu stays for interaction; a link chosen here is remembered the same way.
// The footer renders before an async page has finished its setup on the server, so the server waits for the
// page's data first and renders the set the page decided, the same set the client hydrates from the payload.
if (import.meta.server) {
  // Nuxt's registry of running async data, one object for the request that entries join and leave.
  const { _asyncDataPromises: running } = useNuxtApp();
  onServerPrefetch(() => settledPageData(() => Object.values(running)));
}
const { t, locale } = useI18n();
const switchLocalePath = useSwitchLocalePath();
const hreflangs = useState<string[] | null>('page:hreflangs', () => null);
// Pages without their own decision list the languages open for indexing, as app.vue's hreflang does (L1).
const settings = useSiteSettings();
const allowed = computed(() => hreflangs.value ?? hreflangsFor(settings.value?.pages.openLocales ?? [...LOCALES]));
const current = computed(() => toAppLocale(locale.value));
const chosen = useState('onv-language-choice', () => false);

const items = computed(() =>
  languageNavLocales(allowed.value).flatMap(code => {
    const path = switchLocalePath(toRouteLocale(code));
    if (!path) return [];
    const { name, hreflang } = LOCALE_METADATA[code];
    return [{ code, name, hreflang, href: canonicalPageUrl(path) }];
  })
);

function remember(code: SiteLocale) {
  if (code === current.value) return;
  saveLanguagePreference(LANGUAGE_CHOICE_KEY, code);
  chosen.value = true;
}
</script>

<template>
  <nav v-if="items.length" :aria-label="t('footer.languages')">
    <ul class="m-0 flex list-none flex-wrap gap-x-14px gap-y-6px p-0">
      <li v-for="item in items" :key="item.code">
        <NuxtLink
          :to="item.href"
          :hreflang="item.hreflang"
          :lang="item.hreflang"
          :aria-current="item.code === current ? 'page' : undefined"
          class="rounded-tiny text-12px no-underline outline-none transition-colors duration-fast ease-standard hover:text-ink-primary focus-visible:shadow-focus-ring"
          :class="item.code === current ? 'text-ink-secondary' : 'text-ink-tertiary'"
          @click="remember(item.code)"
          >{{ item.name }}</NuxtLink
        >
      </li>
    </ul>
  </nav>
</template>
