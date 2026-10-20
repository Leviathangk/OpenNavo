import { afterEach, expect, it, vi } from 'vitest';
import { effectScope, ref } from 'vue';
import type { PublicComponents } from '@opennavo/api';
import { usePageDecision } from '../app/composables/usePageDecision';

type PageDecision = PublicComponents['schemas']['PageDecision'];

function pageDecision(overrides: Partial<PageDecision>): PageDecision {
  return {
    canonicalPath: '',
    hreflang: [],
    robots: 'index, follow',
    inSitemap: false,
    announce: false,
    reason: 'test',
    ...overrides
  };
}

afterEach(() => vi.unstubAllGlobals());

it('old page unmount cannot clear the new page canonical or alternates', () => {
  const state = new Map<string, ReturnType<typeof ref>>();
  const unmounts: (() => void)[] = [];
  vi.stubGlobal('useState', (key: string, init: () => unknown) => {
    if (!state.has(key)) state.set(key, ref(init()));
    return state.get(key);
  });
  vi.stubGlobal('useSeoMeta', vi.fn());
  vi.stubGlobal('onUnmounted', (fn: () => void) => unmounts.push(fn));
  const scope = effectScope();
  scope.run(() => {
    usePageDecision(
      pageDecision({
        canonicalPath: '/apps/old/troubleshooting',
        hreflang: ['en-US'],
        robots: 'noindex, follow'
      })
    );
    usePageDecision(
      pageDecision({
        canonicalPath: '/apps/new',
        hreflang: ['en-US', 'ja-JP'],
        robots: 'index, follow'
      })
    );
  });
  const alternates = state.get('page:hreflangs')!.value;
  unmounts[0]!();
  expect(state.get('page:canonicalPath')!.value).toBe('/apps/new');
  expect(state.get('page:hreflangs')!.value).toBe(alternates);
  unmounts[1]!();
  expect(state.get('page:canonicalPath')!.value).toBeNull();
  expect(state.get('page:hreflangs')!.value).toBeNull();
  scope.stop();
});

it.each(['list', 'article', 'same-canonical'])('cleans only owned fields on navigation to %s', target => {
  const state = new Map<string, ReturnType<typeof ref>>();
  const unmounts: (() => void)[] = [];
  vi.stubGlobal('useState', (key: string, init: () => unknown) => {
    if (!state.has(key)) state.set(key, ref(init()));
    return state.get(key);
  });
  vi.stubGlobal('useSeoMeta', vi.fn());
  vi.stubGlobal('onUnmounted', (fn: () => void) => unmounts.push(fn));
  const scope = effectScope();
  scope.run(() => {
    // Layout may have no decision when entering the versions tab directly.
    usePageDecision(undefined);
    usePageDecision(
      pageDecision({
        canonicalPath: '/apps/old/versions',
        hreflang: ['en-US'],
        robots: 'index, follow'
      })
    );
    if (target === 'same-canonical')
      usePageDecision(
        pageDecision({
          canonicalPath: '/apps/old/versions',
          hreflang: ['ja-JP'],
          robots: 'index, follow'
        })
      );
  });
  if (target === 'article') state.get('page:hreflangs')!.value = ['ja'];
  const nextAlternates = state.get('page:hreflangs')!.value;
  unmounts[0]!();
  unmounts[1]!();
  expect(state.get('page:canonicalPath')!.value).toBe(target === 'same-canonical' ? '/apps/old/versions' : null);
  expect(state.get('page:hreflangs')!.value).toEqual(target === 'list' ? null : nextAlternates);
  scope.stop();
});
