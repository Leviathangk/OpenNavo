// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { createSSRApp, defineComponent, h, ref } from 'vue';
import type { Ref } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { OnPackageSpecCard } from '@opennavo/ui';
import { usePackageSpecNow } from '../app/composables/usePackageSpecNow';
import { packageSpec } from '../app/utils/packageSpec';
import type { PackagePage } from '../app/utils/packageFacts';
import production from './fixtures/packageFaq.production.json';

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

it('hydrates cached repository ages and maintenance badges from the server clock', async () => {
  vi.useFakeTimers({ toFake: ['Date'] });
  vi.setSystemTime(new Date('2026-10-30T00:00:00Z'));
  let state: Record<string, Ref<number>> = {};
  vi.stubGlobal('useState', (key: string, init: () => number) => (state[key] ??= ref(init())));
  const pkg = production.packages.iterm2 as unknown as PackagePage;
  pkg.repository = { url: pkg.repoUrl!, pushedAt: '2025-10-30T00:00:00Z', stars: 42 };
  const t = (key: string, named: Record<string, unknown> = {}) =>
    key === 'package.repo.updated' ? `Updated ${named.days} days ago` : key;
  const Card = defineComponent(() => {
    const now = usePackageSpecNow();
    return () => h(OnPackageSpecCard, { rows: packageSpec(pkg, t, 'en-US', now.value).rows });
  });
  const html = await renderToString(createSSRApp(Card));
  expect(html).toContain('Updated 365 days ago');
  expect(html).not.toContain('package.repo.unmaintained');
  const payload = Object.fromEntries(Object.entries(state).map(([key, value]) => [key, value.value]));
  vi.setSystemTime(new Date('2026-11-01T00:00:00Z'));
  state = Object.fromEntries(Object.entries(payload).map(([key, value]) => [key, ref(value)]));
  const warn = vi.spyOn(console, 'warn');
  const error = vi.spyOn(console, 'error');
  const container = document.createElement('div');
  container.innerHTML = html;
  const app = createSSRApp(Card);
  try {
    app.mount(container);
    expect(container.textContent).toContain('Updated 365 days ago');
    expect(container.textContent).not.toContain('package.repo.unmaintained');
    expect([...warn.mock.calls, ...error.mock.calls].flat().join('\n')).not.toMatch(/hydration|mismatch/i);
  } finally {
    app.unmount();
  }
});
