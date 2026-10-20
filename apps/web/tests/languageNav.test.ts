import { describe, expect, it } from 'vitest';
import { createSSRApp, defineComponent, h, onServerPrefetch, ref } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { SITE_LOCALES } from '@opennavo/shared';
import { LANGUAGE_ORDER, languageNavLocales, settledPageData } from '../app/utils/languageNav';
import { hreflangsFor } from '../shared/utils/hreflang';

/** A stand-in for Nuxt's pending async data: entries leave the registry when they settle. */
function registry() {
  const pending: Record<string, Promise<unknown> | undefined> = {};
  const start = (key: string, ms: number) => {
    const promise = new Promise(resolve => setTimeout(resolve, ms)).then(() => {
      delete pending[key];
    });
    pending[key] = promise;
    return promise;
  };
  return { pending: () => Object.values(pending), start };
}

describe('Footer language links (C4)', () => {
  it('Lists every site language when the page does not limit its alternates', () => {
    expect(languageNavLocales(null)).toEqual(LANGUAGE_ORDER);
    expect([...LANGUAGE_ORDER].sort()).toEqual([...SITE_LOCALES].sort());
  });

  it('Lists only the languages whose hreflang the page keeps, in menu order', () => {
    // page:hreflangs holds hreflang values (with aliases, catch-alls and x-default), as usePageDecision sets them.
    expect(languageNavLocales(hreflangsFor(['ja-JP', 'en-US', 'zh-Hant']))).toEqual(['en-US', 'zh-Hant', 'ja-JP']);
    expect(languageNavLocales(hreflangsFor(['zh-CN']))).toEqual(['zh-CN']);
    expect(languageNavLocales([])).toEqual([]);
  });

  it('Waits for page data that starts only after earlier data has loaded', async () => {
    const { pending, start } = registry();
    const order: string[] = [];
    void (async () => {
      await start('detail', 5);
      await start('releases', 5);
      order.push('page decided');
    })();
    await settledPageData(pending);
    order.push('footer');
    expect(order).toEqual(['page decided', 'footer']);
  });

  it('Lets the server render the footer with what an async page decided', async () => {
    const { pending, start } = registry();
    const hreflangs = ref<string[] | null>(null);
    const Page = defineComponent({
      async setup() {
        await start('package', 5);
        hreflangs.value = hreflangsFor(['en-US', 'ja-JP']);
        return () => h('main');
      }
    });
    const Footer = defineComponent({
      setup() {
        onServerPrefetch(() => settledPageData(pending));
        return () => h('nav', languageNavLocales(hreflangs.value).join(' '));
      }
    });
    const html = await renderToString(createSSRApp({ render: () => h('div', [h(Page), h(Footer)]) }));
    expect(html).toBe('<div><main></main><nav>en-US ja-JP</nav></div>');
  });
});
