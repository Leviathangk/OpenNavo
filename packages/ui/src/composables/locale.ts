// Default component locale: injected by apps (web i18n, desktop settings); English when no locale is provided.
import { computed, inject, ref } from 'vue';
import type { App, ComputedRef, InjectionKey, Ref } from 'vue';
import { sharedMessages } from '@opennavo/shared';
// Site locales: the website renders shared components in ten languages; apps use a subset.
import type { SiteLocale as Locale, SharedMessages } from '@opennavo/shared';

export const ON_UI_LOCALE: InjectionKey<Readonly<Ref<Locale>>> = Symbol('on-ui-locale');

/** Vue plugin: app.use(createOnUi({ locale })); locale may be a reactive reference. */
export function createOnUi({ locale }: { locale: Readonly<Ref<Locale>> }) {
  return {
    install(app: App) {
      app.provide(ON_UI_LOCALE, locale);
    }
  };
}

export function useUiLocale(): Readonly<Ref<Locale>> {
  return inject(ON_UI_LOCALE, ref<Locale>('en-US'));
}

export function useUiMessages(): ComputedRef<SharedMessages> {
  const locale = useUiLocale();
  return computed(() => sharedMessages[locale.value]);
}
