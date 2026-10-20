import { afterEach, describe, expect, it, vi } from 'vitest';
import { effectScope, nextTick, reactive, ref, type Ref } from 'vue';
import { useKnownQueryValue, useListPage } from '../app/composables/useListPage';

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
  const head = vi.fn();
  vi.stubGlobal('useHead', head);
  const scope = effectScope();
  scopes.push(scope);
  return { route, router, states, showError, head, scope };
}

function setup(initialPage = '1') {
  const nuxt = stubNuxt(initialPage);
  const list = nuxt.scope.run(() => useListPage())!;
  const total = ref<number | undefined>(50);
  const size = ref(24);
  const status = ref('success');
  const register = () => nuxt.scope.run(() => list.assertExists(total, size, status));
  return { ...nuxt, total, size, status, register, list };
}

describe('empty lists and unknown filter values (S4)', () => {
  it('keeps an empty list out of the index once its total is known', () => {
    const state = setup('1');
    state.total.value = 0;
    state.register();
    const head = state.head.mock.calls.at(-1)?.[0] as () => { meta?: { name: string; content: string }[] };
    expect(head().meta).toEqual([expect.objectContaining({ name: 'robots', content: 'noindex, follow' })]);
    state.total.value = 3;
    expect(head().meta).toBeUndefined();
  });

  it('answers an unknown sort or filter value with a 404, and accepts it while the known values are unknown', async () => {
    const state = stubNuxt('1');
    (state.route.query as Record<string, unknown>).sort = 'bogus';
    expect(() => state.scope.run(() => useKnownQueryValue('sort', ['popular', 'name']))).toThrow('Page not found');
    (state.route.query as Record<string, unknown>).sort = 'name';
    state.scope.run(() => useKnownQueryValue('sort', ['popular', 'name']));
    const known = ref<string[] | null>(null);
    (state.route.query as Record<string, unknown>).category = 'browsers';
    state.scope.run(() => useKnownQueryValue('category', known));
    known.value = ['developer-tools'];
    await nextTick();
    expect(state.showError).toHaveBeenCalledWith(expect.objectContaining({ statusCode: 404, fatal: true }));
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
