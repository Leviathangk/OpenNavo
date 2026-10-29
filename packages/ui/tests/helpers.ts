import { defineComponent, h, ref } from 'vue';
import type { SiteLocale } from '@opennavo/shared';
import { ON_UI_LOCALE } from '../src/composables/locale';

/** mount global options for an explicitly selected locale. */
export function withLocale(locale: SiteLocale) {
  return { provide: { [ON_UI_LOCALE as symbol]: ref(locale) } };
}

/** Like RouterLink, root href derives from to; undefined fallthrough attributes would overwrite it. */
export const RouterLinkStub = defineComponent({
  props: { to: { type: String, required: true } },
  setup:
    (props, { slots }) =>
    () =>
      h('a', { href: `#${props.to}` }, slots.default?.())
});
