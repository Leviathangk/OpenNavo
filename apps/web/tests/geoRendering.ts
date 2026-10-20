// Real page blocks rendered by Vue, inside Vitest only (no Nuxt build or shared .output).
import { computed, createSSRApp, h, ref, type Component } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { vi } from 'vitest';
import { LOCALE_METADATA, type SiteLocale } from '@opennavo/shared';
import { siteMessage } from '../server/utils/siteCopy';
import { capabilities, slimPackage } from '../app/utils/packageFacts';
import type { PackageDetail } from '@opennavo/api';
import Lead from '../app/components/package/PackageLead.vue';
import Price from '../app/components/package/PackagePrice.vue';
import Requirements from '../app/components/package/PackageRequirements.vue';
import Uninstall from '../app/components/package/PackageUninstall.vue';
import Safety from '../app/components/package/PackageSafety.vue';
import Alternatives from '../app/components/package/PackageAlternatives.vue';
import QuickFacts from '../app/components/package/PackageQuickFacts.vue';
import Faq from '../app/components/package/PackageFaq.vue';
import Install from '../app/components/package/PackageInstall.vue';
import InstallFacts from '../app/components/package/PackageInstallFacts.vue';
import Sources from '../app/components/package/PackageSources.vue';

// Only presentational atoms are replaced; selectors, i18n, Vue loops/conditions and page templates run.
vi.mock('@opennavo/ui', async () => {
  const { h: vnode } = await import('vue');
  const { displayUrl } = await import('../../../packages/ui/src/utils/package');
  const { parseInline } = await import('../../../packages/ui/src/utils/version');
  const slot = {
    setup: (_: unknown, ctx: { slots: { default?: () => unknown } }) => () =>
      vnode('span', ctx.slots.default?.() as never)
  };
  return {
    displayUrl,
    parseInline,
    OnChip: slot,
    OnIcon: slot,
    OnAppIcon: slot,
    OnCodeText: { props: ['text'], setup: (props: { text: string }) => () => vnode('span', props.text) },
    OnInstallCommand: { props: ['command'], setup: (props: { command: string }) => () => vnode('code', props.command) },
    OnCaskArtifacts: {
      props: ['entries'],
      setup: (props: { entries: unknown }) => () => vnode('pre', JSON.stringify(props.entries))
    }
  };
});
export const translator = (locale: SiteLocale) => (key: string, named?: Record<string, unknown>, plural?: number) =>
  siteMessage(locale, key, named as Record<string, string | number>, plural);
export const plainHtml = (html: string) =>
  html
    .replace(/<[^>]*>/g, '')
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&#39;|&apos;/g, "'")
    .replace(/&quot;/g, '"')
    .replace(/\s+/g, ' ')
    .trim();
export async function renderGeo(detail: PackageDetail, locale: SiteLocale) {
  const pkg = slimPackage(detail);
  const can = capabilities(pkg);
  const t = translator(locale);
  vi.stubGlobal('computed', computed);
  vi.stubGlobal('useI18n', () => ({
    locale: ref(locale),
    t: (key: string, named?: Record<string, unknown>, options?: { plural: number }) => t(key, named, options?.plural)
  }));
  vi.stubGlobal('toAppLocale', (value: string) => value);
  vi.stubGlobal('useLocalePath', () => (path: string) => `${LOCALE_METADATA[locale].prefix}${path}`);
  vi.stubGlobal('useToasts', () => ({ push: () => {} }));
  try {
    const blocks: Record<string, Component | null> = {
      lead: Lead,
      quick: QuickFacts,
      price: Price,
      requirements: Requirements,
      uninstall: can.uninstall ? Uninstall : null,
      safety: can.safety ? Safety : null,
      alternatives: Alternatives,
      faq: Faq,
      install: can.homebrew ? Install : null,
      details: can.homebrew ? InstallFacts : null,
      sources: Sources
    };
    const out: Record<string, string> = {};
    for (const [key, component] of Object.entries(blocks)) {
      if (!component) {
        out[key] = '';
        continue;
      }
      const app = createSSRApp({ render: () => h(component, { pkg, homebrew: can.homebrew }) });
      app.component('NuxtLink', {
        props: ['to'],
        setup:
          (props, { slots }) =>
          () =>
            h('a', { href: props.to }, slots.default?.())
      });
      out[key] = await renderToString(app);
    }
    return out;
  } finally {
    vi.unstubAllGlobals();
  }
}
