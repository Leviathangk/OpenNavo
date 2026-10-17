import { afterEach, describe, expect, it, vi } from 'vitest';
import { effectScope, nextTick, reactive, ref, type Ref } from 'vue';
import { useCanonicalQuery, useListPage } from '../app/composables/useListPage';
import type { ListQueryParam } from '../app/utils/catalogQuery';

const scopes: ReturnType<typeof effectScope>[] = [];
afterEach(() => {
  scopes.splice(0).forEach(scope => scope.stop());
  vi.unstubAllGlobals();
});

function stubNuxt(initialPage = '1') {
  const route = reactive({ path: '/apps', query: { page: initialPage } });
  // The router's current route, as app.vue sees it; useRoute() in a page is that page's own route.
  const router = { currentRoute: ref({ path: '/apps' }) };
  const states = new Map<string, Ref<unknown>>();
  const showError = vi.fn();
  vi.stubGlobal('useRoute', () => route);
  vi.stubGlobal('useRouter', () => router);
  vi.stubGlobal('useState', (key: string, init: () => unknown) => {
    if (!states.has(key)) states.set(key, ref(init()));
    return states.get(key);
  });
  vi.stubGlobal('createError', (options: object) => Object.assign(new Error('Page not found'), options));
  vi.stubGlobal('showError', showError);
  const scope = effectScope();
  scopes.push(scope);
  return { route, router, states, showError, scope };
}

function setup(initialPage = '1', options: { canonical?: Exclude<ListQueryParam, 'page'>[] } = {}) {
  const nuxt = stubNuxt(initialPage);
  const list = nuxt.scope.run(() => useListPage(options))!;
  const total = ref<number | undefined>(50);
  const size = ref(24);
  const status = ref('success');
  const register = () => nuxt.scope.run(() => list.assertExists(total, size, status));
  return { ...nuxt, total, size, status, register, list };
}

describe('canonical parameters of paginated lists', () => {
  it('keeps page and the list’s own bound parameters on its route only', () => {
    const state = setup('2', { canonical: ['size', 'disabled'] });
    const kept = state.scope.run(() => useCanonicalQuery())!;
    expect(kept.value).toEqual(['page', 'size', 'disabled']);
    // Client-side navigation to a page that is not a list: the record names another path.
    state.router.currentRoute.value = { path: '/apps/wechat' };
    expect(kept.value).toEqual([]);
    state.router.currentRoute.value = { path: '/apps' };
    expect(kept.value).toEqual(['page', 'size', 'disabled']);
  });

  it('keeps only page by default and follows the path when the list is reused', async () => {
    const state = setup('2');
    const kept = state.scope.run(() => useCanonicalQuery())!;
    expect(kept.value).toEqual(['page']);
    state.route.path = '/zh/apps';
    state.router.currentRoute.value = { path: '/zh/apps' };
    expect(kept.value).toEqual([]);
    await nextTick();
    expect(kept.value).toEqual(['page']);
  });

  it('keeps nothing where no list was set up, or where the page number is a 404', () => {
    const state = stubNuxt('abc');
    expect(() => state.scope.run(() => useListPage())).toThrow('Page not found');
    expect(state.scope.run(() => useCanonicalQuery())!.value).toEqual([]);
  });
});

describe('reactive list page validation', () => {
  it('throws during setup for malformed and initially out-of-range pages', () => {
    expect(() => setup('abc')).toThrow('Page not found');
    const state = setup('4');
    expect(state.register).toThrow('Page not found');
  });

  it.each(['abc', '0', '-1', '1.5', '01', ''])('shows a fatal 404 when the query changes to %s', async value => {
    const state = setup('2');
    state.register();
    state.route.query.page = value;
    await nextTick();
    expect(state.showError).toHaveBeenCalledWith(expect.objectContaining({ statusCode: 404, fatal: true }));
  });

  it('checks a new page after its response even when its total is unchanged', async () => {
    const state = setup('2');
    state.register();
    state.route.query.page = '4';
    state.status.value = 'pending';
    await nextTick();
    expect(state.showError).not.toHaveBeenCalled();
    state.status.value = 'success';
    await nextTick();
    expect(state.showError).toHaveBeenCalledWith(expect.objectContaining({ statusCode: 404 }));
  });

  it('rechecks cached results without a pending transition', async () => {
    const state = setup('2');
    state.register();
    state.route.query.page = '3';
    await nextTick();
    expect(state.showError).not.toHaveBeenCalled();
    state.route.query.page = '4';
    await nextTick();
    expect(state.showError).toHaveBeenCalledWith(expect.objectContaining({ statusCode: 404 }));
  });

  it('does not reject expanded filters using the previous response total', async () => {
    const state = setup('2');
    state.register();
    state.route.query.page = '4';
    state.status.value = 'pending';
    await nextTick();
    expect(state.showError).not.toHaveBeenCalled();
    state.total.value = 100;
    state.status.value = 'success';
    await nextTick();
    expect(state.showError).not.toHaveBeenCalled();
    // A later filter narrows the results without changing the page number.
    state.status.value = 'pending';
    await nextTick();
    state.total.value = 50;
    state.status.value = 'success';
    await nextTick();
    expect(state.showError).toHaveBeenCalledOnce();
  });

  it('reacts to page size and leaves request errors to the page', async () => {
    const state = setup('4');
    state.size.value = 12;
    state.register();
    state.size.value = 24;
    state.status.value = 'pending';
    await nextTick();
    state.status.value = 'error';
    await nextTick();
    expect(state.showError).not.toHaveBeenCalled();
    state.status.value = 'success';
    await nextTick();
    expect(state.showError).toHaveBeenCalledOnce();
  });

  it('accepts empty first pages and unknown totals, and disposes watchers with the page', async () => {
    const state = setup();
    state.total.value = 0;
    state.register();
    state.total.value = undefined;
    state.route.query.page = '4';
    await nextTick();
    expect(state.showError).not.toHaveBeenCalled();
    state.scope.stop();
    state.route.query.page = 'abc';
    await nextTick();
    expect(state.showError).not.toHaveBeenCalled();
  });
});
