// Real page blocks rendered by Vue, inside Vitest only (no Nuxt build or shared .output).
import { computed, createSSRApp, h, ref, type Component } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { vi } from 'vitest';
import { LOCALE_METADATA, type SiteLocale } from '@opennavo/shared';
// The live specification card the overview renders; imported from source because @opennavo/ui is mocked below.
import OnPackageSpecCard from '../../../packages/ui/src/components/OnPackageSpecCard.vue';
import { siteMessage } from '../server/utils/siteCopy';
import { capabilities, slimPackage } from '../app/utils/packageFacts';
import type { PackagePage } from '../app/utils/packageFacts';
import { packageSpec } from '../app/utils/packageSpec';
import type { PackageDetail } from '@opennavo/api';
import Lead from '../app/components/package/PackageLead.vue';
import Price from '../app/components/package/PackagePrice.vue';
import Requirements from '../app/components/package/PackageRequirements.vue';
import Uninstall from '../app/components/package/PackageUninstall.vue';
import Safety from '../app/components/package/PackageSafety.vue';
import Alternatives from '../app/components/package/PackageAlternatives.vue';
import Faq from '../app/components/package/PackageFaq.vue';
import Install from '../app/components/package/PackageInstall.vue';
import InstallDetails from '../app/components/package/PackageInstallDetailsContent.vue';
import Sources from '../app/components/package/PackageSources.vue';
import { GEO_NOW } from './geoFixtures';

// The page hands its locale and clock to the blocks; the harness publishes both for the mocked @opennavo/ui
// composables and for the specification card built here.
const page = vi.hoisted(() => ({ locale: 'en-US' as SiteLocale, now: 0 }));

// Only presentational atoms are replaced; selectors, i18n, Vue loops/conditions and page templates run.
// The repository helpers and the key/value list stay real, because the specification card renders them.
vi.mock('@opennavo/ui', async () => {
  const { h: vnode } = await import('vue');
  const { architectures, displayUrl } = await import('../../../packages/ui/src/utils/package');
  const { parseInline } = await import('../../../packages/ui/src/utils/version');
  const {
    formatRepoStars,
    repoAgeDays,
    repoMaintenanceStatus,
    repoSignalDays,
    toPackageRepoSignals,
    toPackageStarBadge
  } = await import('../../../packages/ui/src/utils/repoSignals');
  const { sharedMessages } = await import('@opennavo/shared');
  const { default: OnKeyValueList } = await import('../../../packages/ui/src/components/OnKeyValueList.vue');
  const slot = {
    setup: (_: unknown, ctx: { slots: { default?: () => unknown } }) => () =>
      vnode('span', ctx.slots.default?.() as never)
  };
  return {
    architectures,
    displayUrl,
    parseInline,
    formatRepoStars,
    repoAgeDays,
    repoMaintenanceStatus,
    repoSignalDays,
    toPackageRepoSignals,
    toPackageStarBadge,
    OnKeyValueList,
    useUiMessages: () => computed(() => sharedMessages[page.locale]),
    OnChip: slot,
    OnButton: slot,
    OnIcon: slot,
    OnAppIcon: slot,
    OnCodeText: { props: ['text'], setup: (props: { text: string }) => () => vnode('span', props.text) },
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
// The overview's specification card stands in for the retired quick-facts block: it carries the same version,
// download source and Homebrew rows, built by the same packageSpec model the page uses.
const Specification = {
  props: { pkg: { type: Object, required: true } },
  setup: (props: { pkg: unknown }) => () =>
    h(OnPackageSpecCard, {
      title: translator(page.locale)('package.spec.title'),
      rows: packageSpec(props.pkg as PackagePage, translator(page.locale), page.locale, page.now).rows
    })
};
export async function renderGeo(detail: PackageDetail, locale: SiteLocale) {
  page.locale = locale;
  page.now = Date.parse(GEO_NOW);
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
      quick: Specification,
      price: Price,
      requirements: Requirements,
      uninstall: can.uninstall ? Uninstall : null,
      safety: can.safety ? Safety : null,
      alternatives: Alternatives,
      faq: Faq,
      install: can.homebrew ? Install : null,
      // The overview renders this same body, so the details assertions describe a page that ships.
      details: can.homebrew ? InstallDetails : null,
      sources: Sources
    };
    const out: Record<string, string> = {};
    for (const [key, component] of Object.entries(blocks)) {
      if (!component) {
        out[key] = '';
        continue;
      }
      const app = createSSRApp({ render: () => h(component, { pkg, homebrew: can.homebrew }) });
      app.component('PackageCommand', {
        props: ['command', 'copyLabel'],
        setup: props => () =>
          h('div', [h('code', props.command), h('button', { 'aria-label': props.copyLabel }, 'Copy')])
      });
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
