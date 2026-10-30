import { IncomingMessage, ServerResponse } from 'node:http';
import { Socket } from 'node:net';
import { createEvent, createError, getResponseHeader } from 'h3';
import { describe, expect, it, vi } from 'vitest';
import { GEO_SAMPLES, GEO_NOW } from './geoFixtures';
import { packageMarkdown, renderPackageMarkdown } from '../server/utils/packageMarkdown';
import { slimPackage } from '../app/utils/packageFacts';
import { faqItems } from '../app/utils/packageFaq';
import { softwareAppNode } from '../app/utils/structuredData';
import markdownMiddleware from '../server/middleware/markdown-alternate';
import { siteMessage } from '../server/utils/siteCopy';
import type { PackageDetail } from '@opennavo/api';
function outputsForExpiry(detail: PackageDetail) {
  const page = slimPackage(detail);
  const t = (key: string, named?: Record<string, unknown>) =>
    siteMessage('en-US', key, named as Record<string, string | number>);
  return {
    faq: faqItems(page, t, 'en-US'),
    schema: softwareAppNode(page, 'https://opennavo.example/apps/geo-fixture-15', t),
    md: renderPackageMarkdown(detail, { siteUrl: 'https://opennavo.example', locale: 'en-US' })
  };
}

function eventFor(path: string) {
  const socket = new Socket();
  const req = new IncomingMessage(socket);
  req.url = path;
  req.method = 'GET';
  return { event: createEvent(req, new ServerResponse(req)), socket };
}
function mockApi(get: () => Promise<unknown>) {
  vi.stubGlobal('useRuntimeConfig', () => ({
    apiBaseInternal: 'https://api.example',
    public: { apiBase: 'https://api.example', siteUrl: 'https://opennavo.example' }
  }));
  vi.stubGlobal('$fetch', get);
}

describe('G5 public Markdown boundaries', () => {
  it('G5-06 redirects an old token to the retained entity, never rendering parallel old facts', async () => {
    const detail = GEO_SAMPLES[5]!.detail;
    mockApi(async () => ({ data: detail }));
    const { event, socket } = eventFor('/apps/geo-fixture-old.md');
    try {
      const body = await packageMarkdown(event, 'en-US', 'geo-fixture-old');
      expect(event.node.res.statusCode).toBe(301);
      expect(getResponseHeader(event, 'location')).toBe('/apps/geo-fixture-06.md');
      expect(body).toBeUndefined();
    } finally {
      socket.destroy();
      vi.unstubAllGlobals();
    }
  });
  it('G5-06 lets the Markdown middleware finish a redirect without writing headers after send', async () => {
    const detail = GEO_SAMPLES[5]!.detail;
    vi.stubGlobal('useRuntimeConfig', () => ({
      apiBaseInternal: 'https://api.example',
      public: { apiBase: 'https://api.example', siteUrl: 'https://opennavo.example', markdownAlternates: true }
    }));
    vi.stubGlobal('$fetch', async () => ({ data: detail }));
    const log = vi.spyOn(console, 'info').mockImplementation(() => {});
    const { event, socket } = eventFor('/apps/geo-fixture-old.md');
    try {
      await expect(markdownMiddleware(event)).resolves.toBeUndefined();
      expect(event.node.res.statusCode).toBe(301);
      expect(getResponseHeader(event, 'location')).toBe('/apps/geo-fixture-06.md');
    } finally {
      socket.destroy();
      log.mockRestore();
      vi.unstubAllGlobals();
    }
  });
  it.each([404, 503])(
    'G5-07 preserves upstream %s instead of inventing a removal or leaking old body',
    async statusCode => {
      mockApi(async () => {
        throw createError({ statusCode });
      });
      const { event, socket } = eventFor('/apps/geo-fixture-07.md');
      try {
        await expect(packageMarkdown(event, 'en-US', 'geo-fixture-07')).rejects.toMatchObject({ statusCode });
      } finally {
        socket.destroy();
        vi.unstubAllGlobals();
      }
    }
  );
  it('G5-07 declines a hidden public projection', async () => {
    mockApi(async () => ({ data: GEO_SAMPLES[6]!.detail }));
    const { event, socket } = eventFor('/apps/geo-fixture-07.md');
    try {
      await expect(packageMarkdown(event, 'en-US', 'geo-fixture-07')).rejects.toMatchObject({ statusCode: 404 });
    } finally {
      socket.destroy();
      vi.unstubAllGlobals();
    }
  });
  for (const variant of GEO_SAMPLES[29]!.variants)
    it(`G5-30 route ${variant.label}`, async () => {
      mockApi(async () => ({ data: variant.detail }));
      const { event, socket } = eventFor('/apps/geo-fixture-30.md');
      try {
        const response = packageMarkdown(event, variant.locale!, variant.detail.token);
        if (variant.label.startsWith('withheld')) await expect(response).rejects.toMatchObject({ statusCode: 404 });
        else expect(await response).toContain(variant.detail.summary);
      } finally {
        socket.destroy();
        vi.unstubAllGlobals();
      }
    });
  it('G5-15 uses newly normalized evidence on either side of the expiry boundary without extending it', async () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    try {
      vi.setSystemTime(Date.parse(GEO_NOW) - 1);
      const before = await outputsForExpiry(GEO_SAMPLES[14]!.detail);
      expect(before.schema).toHaveProperty('offers');
      expect(before.faq.some(item => item.key === 'price')).toBe(true);
      vi.setSystemTime(Date.parse(GEO_NOW));
      const after = await outputsForExpiry(GEO_SAMPLES[14]!.variants[0]!.detail);
      expect(after.schema).not.toHaveProperty('offers');
      expect(after.faq.some(item => item.key === 'price')).toBe(false);
      expect(after.md).toContain('2026-10-19');
    } finally {
      vi.useRealTimers();
    }
  });
});

describe('G5 Markdown discovery follows the rendered page language decision', () => {
  it('does not advertise a withheld alternate, and adds it when the same locale is ready', async () => {
    const { ref } = await import('vue');
    const route = ref({ path: '/de/apps/geo-fixture-30' });
    const hreflangs = ref(['en']);
    const settings = ref({ pages: { openLocales: ['en-US'] } });
    let head: (() => { link?: { href: string }[] }) | undefined;
    vi.stubGlobal('defineNuxtPlugin', (plugin: unknown) => plugin);
    vi.stubGlobal('useRuntimeConfig', () => ({
      public: { markdownAlternates: true, siteUrl: 'https://opennavo.example' }
    }));
    vi.stubGlobal('useRouter', () => ({ currentRoute: route }));
    vi.stubGlobal('useState', () => hreflangs);
    vi.stubGlobal('useSiteSettings', () => settings);
    vi.stubGlobal('useHead', (getter: typeof head) => {
      head = getter;
    });
    try {
      const { default: plugin } = await import('../app/plugins/markdown-alternate');
      await (plugin as unknown as () => void)();
      expect(head!()).toEqual({});
      hreflangs.value = ['en', 'de'];
      expect(head!()).toEqual({});
      settings.value.pages.openLocales.push('de-DE');
      expect(head!().link).toMatchObject([{ href: 'https://opennavo.example/de/apps/geo-fixture-30.md' }]);
      route.value.path = '/de/apps/geo-fixture-30/versions';
      expect(head!()).toEqual({});
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
